package charging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/invoicing"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/suspension"
	"playersense.dev/backend/internal/valkey"
)

// Service runs the charger: the bus subscription, the drain, automatic top-up, suspension, invoicing
// and the prune, as one component.
//
// The three are independent loops on purpose. The subscription is paced by traffic, the drain by a
// clock, and the prune by the calendar; a slow drain must not hold back counting, and a burst of
// counting must not delay a drain.
//
// # It opens its own stores
//
// The framework starts components in parallel, so a loop started beside the database pool can run
// before the pool exists — the first build of this service did exactly that, and the drain
// dereferenced a nil client within a millisecond of start-up. analytics-writer is safe from it only
// because its ClickHouse handle reports "not connected" as an error; a nil ent client panics. So the
// service owns the order: PostgreSQL first, and failing to reach it fails start-up; then Valkey,
// whose absence only costs speed (§65); then the loops. Stop runs it backwards.
type Service struct {
	// DB and Cache are opened by Start and closed by Stop. Pass them here rather than
	// registering them as separate components.
	DB    *pgdb.DB
	Cache *valkey.Client

	Bus     eventbus.Bus
	Accruer *Accruer
	Drainer *Drainer
	Pruner  *Pruner
	Log     *slog.Logger

	// DrainEvery is how often queued charges are moved into wallets.
	DrainEvery time.Duration

	// PruneEvery is how often last-but-one month's player set is removed.
	PruneEvery time.Duration

	// TopUp charges saved cards for balances below their threshold (payments.AutoTopUp): Run
	// works the queue every TopUpEvery, Sweep scans for anything the queue missed every
	// TopUpSweepEvery. An interface so charging does not import the payment provider; nil runs
	// without it.
	TopUp interface {
		Run(context.Context) (int, error)
		Sweep(context.Context) (int, error)
	}
	TopUpEvery      time.Duration
	TopUpSweepEvery time.Duration

	// Suspend moves accounts past their grace period or credit limit to suspended, and publishes
	// which accounts are suspended to the ingestion path (suspension.Job, ADR 0043).
	Suspend interface {
		Run(context.Context) (suspension.Result, error)
	}
	SuspendEvery time.Duration

	// Invoice closes invoiced accounts' months and issues their invoices at Stripe
	// (invoicing.Job, ADR 0045). Nil runs without it, as automatic top-up does without Stripe.
	Invoice interface {
		Run(context.Context) (invoicing.Result, error)
	}
	InvoiceEvery time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Name identifies the component in start-up and shutdown logs.
func (s *Service) Name() string { return "billing-writer" }

// Start runs the three loops in the background and returns.
//
// Not derived from the start-up context, for the reason analytics-writer gives: that one is
// cancelled once start-up finishes.
func (s *Service) Start(startup context.Context) error {
	if s.DrainEvery <= 0 {
		return fmt.Errorf("the drain needs an interval")
	}
	if s.DB != nil {
		if err := s.DB.Start(startup); err != nil {
			return err
		}
	}
	if s.Cache != nil {
		if err := s.Cache.Start(startup); err != nil {
			// Not fatal. An unconnected client answers every lookup with ErrUnavailable,
			// and the accruer then asks PostgreSQL about every player.
			s.log().Warn("valkey is unavailable; every player will be checked in PostgreSQL",
				"error", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	s.wg.Add(7)
	go func() {
		defer s.wg.Done()
		if err := s.Bus.Subscribe(ctx, eventbus.GroupBillingWriter, s.Accruer.Handle); err != nil && ctx.Err() == nil {
			s.log().Error("the subscription stopped", "error", err)
		}
	}()
	go func() {
		defer s.wg.Done()
		s.every(ctx, s.DrainEvery, func(ctx context.Context) {
			if _, err := s.Drainer.Run(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("a drain pass left charges queued", "error", err)
			}
		})
	}()
	go func() {
		defer s.wg.Done()
		if s.TopUp == nil || s.TopUpEvery <= 0 {
			return
		}
		s.every(ctx, s.TopUpEvery, func(ctx context.Context) {
			if _, err := s.TopUp.Run(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("an automatic top-up pass did not finish; claims are kept and retried",
					"error", err)
			}
		})
	}()
	go func() {
		defer s.wg.Done()
		if s.TopUp == nil || s.TopUpSweepEvery <= 0 {
			return
		}
		s.every(ctx, s.TopUpSweepEvery, func(ctx context.Context) {
			if _, err := s.TopUp.Sweep(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("an automatic top-up sweep did not finish", "error", err)
			}
		})
	}()
	go func() {
		defer s.wg.Done()
		if s.Suspend == nil || s.SuspendEvery <= 0 {
			return
		}
		s.every(ctx, s.SuspendEvery, func(ctx context.Context) {
			if _, err := s.Suspend.Run(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("a suspension pass did not finish", "error", err)
			}
		})
	}()
	go func() {
		defer s.wg.Done()
		if s.Invoice == nil || s.InvoiceEvery <= 0 {
			return
		}
		s.every(ctx, s.InvoiceEvery, func(ctx context.Context) {
			if _, err := s.Invoice.Run(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("an invoicing pass did not finish; drafts are kept and retried", "error", err)
			}
		})
	}()
	go func() {
		defer s.wg.Done()
		if s.Pruner == nil || s.PruneEvery <= 0 {
			return
		}
		s.every(ctx, s.PruneEvery, func(ctx context.Context) {
			if _, err := s.Pruner.Run(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("the player-set prune failed; it runs again next interval", "error", err)
			}
		})
	}()
	return nil
}

// Stop ends the loops and waits for whatever is in flight.
//
// Waiting matters for the drain above all: a transaction that is cut off rolls back, which is safe,
// but one that is allowed to finish is one fewer pass of latency on somebody's balance.
func (s *Service) Stop(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("the billing writer did not stop within the shutdown timeout")
	}
	if s.Cache != nil {
		_ = s.Cache.Stop(ctx)
	}
	if s.DB != nil {
		_ = s.DB.Stop(ctx)
	}
	s.log().Info("billing writer stopped",
		"messages", s.Accruer.Stats.Messages.Load(),
		"counted", s.Accruer.Stats.Counted.Load(),
		"known_by_cache", s.Accruer.Stats.KnownByCache.Load(),
		"queued", s.Accruer.Stats.Queued.Load(),
		"drained_charges", s.Drainer.Stats.Charges.Load(),
		"drain_failures", s.Drainer.Stats.Failures.Load(),
		"unbilled", s.Accruer.Stats.Unbilled.Load())
	return nil
}

// every runs fn now and then on an interval until the context ends. A pass that overruns the
// interval delays the next rather than overlapping it.
func (s *Service) every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		fn(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Pruner removes player sets nobody can be charged against any more.
//
// §38 says the monthly actor set is computed and discarded. A real-time meter has to keep it while
// the month runs, and this is the "discarded": once a month is over and the one after it has begun
// too, its digests decide nothing and are deleted. The month before the current one is kept, so a
// late batch published at 23:59:59 on the last day and delivered after midnight still finds its
// month's set.
type Pruner struct {
	DB  *pgdb.DB
	Now func() time.Time

	// Chunk bounds one DELETE, so pruning a large month is many short statements rather than
	// one long lock.
	Chunk int
}

// ChargeRetention is how long a settled usage charge is kept (ADR 0044). The ledger entry it was paid
// with is the permanent record; the charge row adds which positions and which card, which matters
// while somebody might still dispute a recent statement and not for years after.
const ChargeRetention = 395 * 24 * time.Hour

// Run deletes player sets from before last month and settled charges past ChargeRetention, and
// reports how many rows went.
func (p *Pruner) Run(ctx context.Context) (int64, error) {
	players, err := p.players(ctx)
	if err != nil {
		return players, err
	}
	charges, err := p.chunked(ctx,
		`DELETE FROM usage_charges WHERE id IN (
		     SELECT id FROM usage_charges WHERE settled_at IS NOT NULL AND settled_at < $1 LIMIT $2)`,
		p.clock().Add(-ChargeRetention))
	return players + charges, err
}

func (p *Pruner) clock() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// chunked runs a bounded DELETE until it deletes less than a chunk.
func (p *Pruner) chunked(ctx context.Context, query string, cutoff time.Time) (int64, error) {
	chunk := p.Chunk
	if chunk <= 0 {
		chunk = 10_000
	}
	var total int64
	for {
		res, err := p.DB.Client.ExecContext(ctx, query, cutoff, chunk)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(chunk) || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}

// players deletes everything before the start of last month.
func (p *Pruner) players(ctx context.Context) (int64, error) {
	return p.chunked(ctx,
		`DELETE FROM billable_player WHERE id IN (
		     SELECT id FROM billable_player WHERE period_start < $1 LIMIT $2)`,
		PeriodOf(p.clock()).AddDate(0, -1, 0))
}
