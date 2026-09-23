package charging

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/monthlyusage"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/provision"
	"playersense.dev/backend/internal/valkey"
)

// Accruer counts new players from the event bus and queues what they cost.
type Accruer struct {
	DB    *pgdb.DB
	Cache *valkey.Client
	Bus   eventbus.Bus
	Log   *slog.Logger

	// Key is the HMAC key behind every digest. Changing it mid-month makes every player of
	// that month new again, so it is a deployment secret that does not rotate casually.
	Key []byte

	// MaxInsert bounds one INSERT's row count, so a delivery of a hundred thousand new
	// players is several statements rather than one with a hundred thousand parameters.
	MaxInsert int

	// Parallelism is how many account-months one delivery accrues at once. Keep it well under the
	// database pool: every one holds a connection for its transaction.
	Parallelism int

	// Pricing serves each organization's account, allowance and rate cards from memory and
	// Valkey (ADR 0041). Nil builds one over DB and Cache on first use.
	Pricing *PricingCache
	once    sync.Once

	Stats Stats
}

// Stats is what the charger has done, for the health endpoint and for tests.
type Stats struct {
	Messages     atomic.Int64
	Skipped      atomic.Int64 // no persistent player: kids_strict, or no install id
	KnownByCache atomic.Int64 // answered by Valkey, never reached PostgreSQL
	Counted      atomic.Int64 // new this month, inserted into billable_player
	Queued       atomic.Int64 // usage_charges rows opened
	Extended     atomic.Int64 // deliveries that extended an open row instead
	Unbilled     atomic.Int64 // players of an organization with no account and no default plan
	DeadLettered atomic.Int64
	CacheErrors  atomic.Int64
}

// seenTTL is how long a counted player's Valkey marker lives: past the month it belongs to, plus a
// margin for a clock or a delivery that is late. The key names the month, so it cannot be mistaken
// for next month's player on the way out.
const seenMargin = 72 * time.Hour

// group is one account-month's candidate players from one delivery.
type group struct {
	orgSeq  int
	period  time.Time
	latest  time.Time // the latest arrival, which chooses the rate card
	digests map[string][]byte
}

// Handle is the bus handler for the billing-writer group.
//
// It returns nil only when every player in the delivery has been counted and every charge queued
// durably, or parked on the dead-letter topic because no retry could ever price it — the same
// contract every consumer here keeps (Appendix C.2). A failure part way through returns an error
// and the whole delivery comes again; the account-months that already committed find their players
// already counted and do nothing, so a retry is always safe.
func (a *Accruer) Handle(ctx context.Context, msgs []*eventbus.Message) error {
	groups, poison := a.group(msgs)

	for _, msg := range poison {
		if err := a.Bus.DeadLetter(ctx, msg.msg, eventbus.DeadLetterReason{
			Group: eventbus.GroupBillingWriter, Reason: msg.reason,
			FailedAt: time.Now().UTC(), Attempts: 1,
		}); err != nil {
			// Not durable, so not acknowledged: a stalled queue is better than a batch
			// that was neither charged nor parked.
			return fmt.Errorf("dead-letter %s: %w", msg.msg.DedupKey, err)
		}
		a.Stats.DeadLettered.Add(1)
		a.log().Error("a batch could not be priced and was dead-lettered",
			"dedup_key", msg.msg.DedupKey, "org_id", msg.msg.OrgID, "reason", msg.reason)
	}

	// Sorted, so a redelivery starts its account-months in the same order.
	keys := slices.SortedFunc(maps.Keys(groups), func(a, b groupKey) int {
		if c := cmp.Compare(a.orgSeq, b.orgSeq); c != 0 {
			return c
		}
		return a.period.Compare(b.period)
	})
	// Account-months in parallel, a bounded number at a time. Each takes exactly one lock — its
	// own month's row — so they cannot deadlock one another, and a delivery holding two hundred
	// organizations is two hundred independent short transactions that have no reason to wait
	// in line. Bounded, so one delivery cannot take every connection in the pool.
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
		sem  = make(chan struct{}, a.parallelism())
	)
	for _, key := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := a.accrue(ctx, groups[key]); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("organization %d, %s: %w",
					key.orgSeq, key.period.Format("2006-01"), err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	a.Stats.Messages.Add(int64(len(msgs)))
	return errors.Join(errs...)
}

func (a *Accruer) parallelism() int {
	if a.Parallelism > 0 {
		return a.Parallelism
	}
	return 8
}

type groupKey struct {
	orgSeq int
	period time.Time
}

type poisoned struct {
	msg    *eventbus.Message
	reason string
}

// group reads each message's player and gathers them by account-month.
//
// Only the install id is decoded. The payload is the whole normalized batch and this consumer
// needs one field of it.
func (a *Accruer) group(msgs []*eventbus.Message) (map[groupKey]*group, []poisoned) {
	groups := map[groupKey]*group{}
	var poison []poisoned

	for _, msg := range msgs {
		// §98: a kids_strict project has no persistent player — the gateway replaces the
		// install id per batch — so there is nobody to count. Counting each batch as a
		// player would bill a children's game per session. Not billed by tracked players;
		// see ADR 0039 for what that leaves open.
		if msg.PolicyTier == "kids_strict" || msg.EventCount == 0 {
			a.Stats.Skipped.Add(1)
			continue
		}
		var batch struct {
			InstallID string `json:"install_id"`
		}
		if err := json.Unmarshal(msg.Payload, &batch); err != nil {
			poison = append(poison, poisoned{msg, "the payload is not a batch: " + err.Error()})
			continue
		}
		if batch.InstallID == "" || msg.OrgSeq == 0 || msg.ProjectID == "" || msg.EnvironmentID == "" {
			poison = append(poison, poisoned{msg, "the message names no organization, project, environment or installation"})
			continue
		}

		period := PeriodOf(msg.PublishedAt)
		key := groupKey{orgSeq: int(msg.OrgSeq), period: period}
		g := groups[key]
		if g == nil {
			g = &group{orgSeq: key.orgSeq, period: period, digests: map[string][]byte{}}
			groups[key] = g
		}
		digest := Digest(a.Key, period, msg.ProjectID, msg.EnvironmentID, batch.InstallID)
		g.digests[string(digest)] = digest
		if msg.PublishedAt.After(g.latest) {
			g.latest = msg.PublishedAt
		}
	}
	return groups, poison
}

// accrue counts one account-month's players and queues their charge, in one transaction.
func (a *Accruer) accrue(ctx context.Context, g *group) error {
	candidates := a.unseen(ctx, g)
	if len(candidates) == 0 {
		return nil
	}

	// Before the transaction, so no lock is held while memory, Valkey or — on a miss — the
	// database answers. Nil: the organization cannot be billed.
	pricing, err := a.pricingFor(ctx, g.orgSeq)
	if err != nil {
		return err
	}
	if pricing == nil {
		// No account and no default plan to give it one. Nothing is counted, so that when
		// an account exists these players are still new this month rather than silently
		// free — and it is loud, because it is a configuration problem that loses money.
		a.Stats.Unbilled.Add(int64(len(candidates)))
		a.log().Warn("an organization has no billing account and no default plan exists; "+
			"its players are not being billed", "org_seq", g.orgSeq, "players", len(candidates))
		return nil
	}

	tx, err := a.DB.Client.Tx(ctx)
	if err != nil {
		return err
	}
	counted, err := a.count(ctx, tx, g, pricing, candidates)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Only now, after PostgreSQL holds them. Marking first would let a crash between the two
	// leave Valkey saying "counted" about a player PostgreSQL never counted — an uncharged
	// player, forever. Marking after means the worst a crash can do is a redundant lookup.
	if counted {
		a.mark(ctx, g, candidates)
	}
	return nil
}

// unseen drops the players Valkey already knows were counted.
//
// Valkey unavailable is not an error here: every candidate goes to PostgreSQL, which answers
// correctly and only more slowly.
func (a *Accruer) unseen(ctx context.Context, g *group) [][]byte {
	all := slices.Collect(maps.Values(g.digests))
	slices.SortFunc(all, bytes.Compare)
	if a.Cache == nil {
		return all
	}
	keys := make([]string, len(all))
	for i, digest := range all {
		keys[i] = a.seenKey(g, digest)
	}
	present, err := a.Cache.Present(ctx, keys)
	if err != nil {
		a.Stats.CacheErrors.Add(1)
		return all
	}
	out := all[:0:0]
	for i, digest := range all {
		if present[i] {
			a.Stats.KnownByCache.Add(1)
			continue
		}
		out = append(out, digest)
	}
	return out
}

func (a *Accruer) mark(ctx context.Context, g *group, digests [][]byte) {
	if a.Cache == nil {
		return
	}
	keys := make([]string, len(digests))
	for i, digest := range digests {
		keys[i] = a.seenKey(g, digest)
	}
	ttl := time.Until(g.period.AddDate(0, 1, 0)) + seenMargin
	if err := a.Cache.Mark(ctx, keys, ttl); err != nil {
		a.Stats.CacheErrors.Add(1)
	}
}

func (a *Accruer) seenKey(g *group, digest []byte) string {
	return a.Cache.Key("billing", "seen", strconv.Itoa(g.orgSeq), g.period.Format("2006-01"),
		hex.EncodeToString(digest))
}

// count is the transaction. It reports whether the players are now known to PostgreSQL.
func (a *Accruer) count(ctx context.Context, tx *ent.Tx, g *group, pricing *Pricing, digests [][]byte) (bool, error) {
	// The account-month's lock. Everything after this sees the count every earlier accrual
	// left, so positions are never priced twice.
	if err := tx.MonthlyUsage.Create().
		SetAccountID(pricing.AccountID).SetPeriodStart(g.period).
		OnConflictColumns(monthlyusage.FieldAccountID, monthlyusage.FieldPeriodStart).
		DoNothing().Exec(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("open the month: %w", err)
	}
	usage, err := tx.MonthlyUsage.Query().
		Where(monthlyusage.AccountID(pricing.AccountID), monthlyusage.PeriodStart(g.period)).
		ForUpdate().Only(ctx)
	if err != nil {
		return false, fmt.Errorf("lock the month: %w", err)
	}

	added, err := a.insert(ctx, tx, pricing.AccountID, g.period, digests)
	if err != nil {
		return false, err
	}
	if added == 0 {
		return true, nil
	}
	a.Stats.Counted.Add(added)

	from, to := usage.TrackedPlayers, usage.TrackedPlayers+added
	card := pricing.CardAt(g.latest)
	var amount int64
	if card != nil {
		amount = billing.Card{Mode: card.Mode, Tiers: card.Tiers}.Charge(from, to, pricing.Included)
	}

	if err := tx.MonthlyUsage.UpdateOne(usage).
		SetTrackedPlayers(to).
		AddChargedMicros(amount).
		Exec(ctx); err != nil {
		return false, fmt.Errorf("advance the month: %w", err)
	}

	// No row when nothing is owed — included players, or a plan with no card — so the queue
	// holds only what the drain has to move.
	if amount != 0 {
		// Extend the account's open row for this month and card when it ends where these
		// players begin (ADR 0044): one row per settlement rather than one per delivery. The
		// WHERE carries the whole condition, so a row the drain is paying right now — locked
		// by it — is re-checked once that commits, found settled, and left alone; these
		// players then open a new row.
		extended, err := tx.UsageCharge.Update().
			Where(
				usagecharge.AccountID(pricing.AccountID),
				usagecharge.PeriodStart(g.period),
				usagecharge.RateCardID(card.ID),
				usagecharge.SettledAtIsNil(),
				usagecharge.LastUnit(from),
			).
			SetLastUnit(to).
			AddAmountMicros(amount).
			Save(ctx)
		if err != nil {
			return false, fmt.Errorf("extend the open charge: %w", err)
		}
		if extended == 0 {
			if err := tx.UsageCharge.Create().
				SetAccountID(pricing.AccountID).
				SetPeriodStart(g.period).
				SetRateCardID(card.ID).
				SetFirstUnit(from + 1).
				SetLastUnit(to).
				SetAmountMicros(amount).
				SetCurrency(card.Currency).
				Exec(ctx); err != nil {
				return false, fmt.Errorf("queue the charge: %w", err)
			}
			a.Stats.Queued.Add(1)
		} else {
			a.Stats.Extended.Add(1)
		}
	}
	return true, nil
}

// insert adds the players PostgreSQL has not counted this month and reports how many that was.
//
// ON CONFLICT DO NOTHING RETURNING: the returned rows are exactly the ones this statement inserted,
// which is the whole definition of "new". A player another consumer inserted a moment ago is a
// conflict, not a row, and is not counted here.
func (a *Accruer) insert(ctx context.Context, tx *ent.Tx, accountID int, period time.Time, digests [][]byte) (int64, error) {
	size := a.MaxInsert
	if size <= 0 {
		size = 1000
	}
	var added int64
	for start := 0; start < len(digests); start += size {
		chunk := digests[start:min(start+size, len(digests))]

		var q strings.Builder
		q.WriteString("INSERT INTO billable_player (account_id, period_start, digest) VALUES ")
		args := make([]any, 0, 2+len(chunk))
		args = append(args, accountID, period)
		for i, digest := range chunk {
			if i > 0 {
				q.WriteString(", ")
			}
			fmt.Fprintf(&q, "($1, $2, $%d)", i+3)
			args = append(args, digest)
		}
		q.WriteString(" ON CONFLICT (account_id, period_start, digest) DO NOTHING RETURNING 1")

		rows, err := tx.QueryContext(ctx, q.String(), args...)
		if err != nil {
			return 0, fmt.Errorf("count players: %w", err)
		}
		for rows.Next() {
			added++
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return 0, fmt.Errorf("count players: %w", err)
		}
	}
	return added, nil
}

// pricingFor is an organization's pricing, giving it a billing account on the default plan first if
// it has none. Nil when there is no account and no default plan.
func (a *Accruer) pricingFor(ctx context.Context, orgSeq int) (*Pricing, error) {
	a.once.Do(func() {
		if a.Pricing == nil {
			a.Pricing = &PricingCache{DB: a.DB, Cache: a.Cache}
		}
	})
	pricing, err := a.Pricing.Get(ctx, orgSeq)
	if !errors.Is(err, ErrNoAccount) {
		return pricing, err
	}

	tx, err := a.DB.Client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	account, err := provision.Account(ctx, tx, orgSeq)
	if err != nil {
		_ = tx.Rollback()
		if errors.Is(err, provision.ErrNoOrganization) {
			return nil, nil
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if account == nil {
		return nil, nil
	}
	return a.Pricing.Get(ctx, orgSeq)
}

func (a *Accruer) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}
