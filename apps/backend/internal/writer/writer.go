// Package writer stores accepted batches in a cell's ClickHouse (§10.3).
//
// It is cell-local and it never touches the control plane. The tenant travels on the
// message — surrogates, UUIDs, policy tier, retention tier — which is precisely what lets
// storage keep working through a control-plane outage rather than only ingestion (§64).
// Its only dependencies are its cell's bus, its cell's Valkey and its cell's ClickHouse.
//
// Two rules shape everything here:
//
//   - **Acknowledge only after the durable insert completes** (§21, Appendix C.2). A
//     message acknowledged before ClickHouse has the rows is a batch that was accepted and
//     then lost, and the 202 that promised otherwise is already in the client's log.
//   - **Never one event per insert** (§86). A message is one SDK batch of up to a thousand
//     events, so the block is a block either way.
package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/tombstone"
	"playersense.dev/backend/internal/valkey"
)

// Options configures the writer.
type Options struct {
	// Cell is the data plane this writer belongs to. A message from another cell is a
	// routing mistake and is refused rather than stored (§6).
	Cell string

	// MaxRowsPerInsert bounds one insert block. A message carrying more events than this
	// is split, and the split is deterministic so a redelivery produces the same blocks
	// with the same deduplication tokens.
	MaxRowsPerInsert int

	// DeviceStateTTL is how long a device's sticky dimensions are remembered (§29.5). A
	// device that keeps playing keeps its entry, because every read renews it.
	DeviceStateTTL time.Duration

	// MaxAttempts is how many times a delivery is retried whole before the writer starts
	// looking for a single message it cannot store.
	//
	// It is not a dead-letter threshold. Attempt counting alone cannot tell a poisoned
	// batch from an unavailable ClickHouse, and treating them the same moves an entire
	// backlog to the dead-letter topic during an outage.
	MaxAttempts int
}

type stats struct {
	messages          atomic.Int64
	rows              atomic.Int64
	inserts           atomic.Int64
	retries           atomic.Int64
	deadLettered      atomic.Int64
	droppedTombstoned atomic.Int64
	droppedErased     atomic.Int64
	droppedWrongCell  atomic.Int64
	// Normally zero. Non-zero means a batch carried more distinct identities than one
	// message is asked about, so the tombstone check stopped early (§61).
	tombstoneChecksSkipped atomic.Int64
	deviceLookupFailures   atomic.Int64
}

// Writer consumes the cell's bus and stores what it finds.
type Writer struct {
	bus   eventbus.Bus
	cache *valkey.Client
	ch    *chdb.Conn
	log   *slog.Logger

	cell             string
	maxRowsPerInsert int
	deviceTTL        time.Duration
	maxAttempts      int

	// attempts counts consecutive failures of the delivery currently at the head of the
	// queue. In memory deliberately: a restart resetting it is correct, because a restart
	// is itself a reason the next attempt might succeed.
	attempts map[string]int

	stats  stats
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a writer. Construction connects to nothing; the dependencies are started by
// the application around it.
func New(bus eventbus.Bus, cache *valkey.Client, ch *chdb.Conn, log *slog.Logger, opts Options) *Writer {
	if opts.MaxRowsPerInsert <= 0 {
		opts.MaxRowsPerInsert = 5000
	}
	if opts.DeviceStateTTL <= 0 {
		opts.DeviceStateTTL = 30 * 24 * time.Hour
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if log == nil {
		log = slog.Default()
	}
	return &Writer{
		bus: bus, cache: cache, ch: ch, log: log,
		cell:             opts.Cell,
		maxRowsPerInsert: opts.MaxRowsPerInsert,
		deviceTTL:        opts.DeviceStateTTL,
		maxAttempts:      opts.MaxAttempts,
		attempts:         map[string]int{},
		done:             make(chan struct{}),
	}
}

// Name identifies the component in start-up and shutdown logs.
func (w *Writer) Name() string { return "analytics-writer" }

// Start begins consuming, in the background.
//
// Start returns once the subscription is running rather than blocking on it, because the
// application starts its lifecycle components before opening the health socket and a Start
// that never returns is a service that never becomes ready.
func (w *Writer) Start(context.Context) error {
	// Deliberately not derived from the start-up context: that one is cancelled once
	// start-up finishes, and a consumer tied to it would stop the moment the service was
	// ready.
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel

	go func() {
		defer close(w.done)
		if err := w.bus.Subscribe(ctx, eventbus.GroupAnalyticsWriter, w.handle); err != nil && ctx.Err() == nil {
			w.log.Error("the subscription stopped", "error", err)
		}
	}()
	return nil
}

// Stop ends the subscription and waits for the message in flight.
//
// Waiting is the point. A message part way through its insert must either finish or be
// redelivered, and cutting the process off in between is the one case where at-least-once
// delivery does not save us: the rows would land and the position would not move, so the
// batch is inserted twice — which the deduplication token covers, but only because this
// waits rather than because it happened to.
func (w *Writer) Stop(ctx context.Context) error {
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	select {
	case <-w.done:
	case <-ctx.Done():
		return fmt.Errorf("the writer did not stop within the shutdown timeout")
	}
	w.log.Info("writer stopped",
		"messages", w.stats.messages.Load(),
		"rows", w.stats.rows.Load(),
		// Rows per insert is the number that says whether this writer is healthy at
		// scale: it is merge pressure made visible (§67). One means the coalescing is
		// not working and the node is accumulating a part per message.
		"inserts", w.stats.inserts.Load(),
		"dropped_tombstoned", w.stats.droppedTombstoned.Load(),
		"dropped_erased", w.stats.droppedErased.Load(),
		"dropped_wrong_cell", w.stats.droppedWrongCell.Load())
	return nil
}

// Stats reports what the writer has done, for the health endpoint and for tests.
func (w *Writer) Stats() map[string]int64 {
	return map[string]int64{
		"messages":                 w.stats.messages.Load(),
		"rows":                     w.stats.rows.Load(),
		"inserts":                  w.stats.inserts.Load(),
		"retries":                  w.stats.retries.Load(),
		"dead_lettered":            w.stats.deadLettered.Load(),
		"dropped_tombstoned":       w.stats.droppedTombstoned.Load(),
		"dropped_erased":           w.stats.droppedErased.Load(),
		"dropped_wrong_cell":       w.stats.droppedWrongCell.Load(),
		"tombstone_checks_skipped": w.stats.tombstoneChecksSkipped.Load(),
		"device_lookup_failures":   w.stats.deviceLookupFailures.Load(),
	}
}

// handle stores a whole delivery, returning nil only once every row is durable.
//
// The delivery is coalesced into as few ClickHouse blocks as it can be, and that is the
// single most important property of this function at the document's scale ceiling.
//
// The previous shape inserted once per message, which is the natural thing to write and
// does not survive §4's envelope. One insert per message means one **part** per message:
// `insert_deduplication_token` is part of an async insert's grouping key, so two inserts
// carrying different tokens are never coalesced server-side no matter what `async_insert`
// is set to. Measured on the local stack: 200 concurrent inserts produce 200 parts with a
// token each and 9 without. At a billion events a day and five hundred events a batch that
// is two million parts a day landing in a handful of daily partitions, against a default
// `parts_to_throw_insert` of three thousand. Merges never catch up, and the node stops
// accepting inserts — the throttle this design exists to avoid.
//
// So rows from many messages are grouped by ClickHouse's own partition key and written as
// one block per partition. See ADR 0023 for what that costs, which is a real weakening of
// ADR 0018's deduplication guarantee rather than a free win.
func (w *Writer) handle(ctx context.Context, msgs []*eventbus.Message) error {
	if err := w.write(ctx, msgs); err == nil {
		delete(w.attempts, deliveryKey(msgs))
		return nil
	} else if n := w.record(msgs); n < w.maxAttempts {
		w.stats.retries.Add(1)
		w.log.Warn("a delivery failed and will be retried",
			"attempt", n, "of", w.maxAttempts, "messages", len(msgs), "error", err)
		return err
	}

	// The delivery has failed maxAttempts times as a block. That is the point at which the
	// two possible causes have to be told apart, because they want opposite treatment:
	// ClickHouse being unavailable wants patience, and one unstorable message wants to be
	// taken out of the way. Attempt count alone cannot distinguish them, and treating them
	// the same either loses a backlog to the dead-letter topic or stalls the queue forever.
	w.log.Warn("a delivery has failed repeatedly; isolating the messages",
		"attempts", w.maxAttempts, "messages", len(msgs))
	return w.isolate(ctx, msgs)
}

// write stores a whole delivery.
func (w *Writer) write(ctx context.Context, msgs []*eventbus.Message) error {
	blocks := w.plan(ctx, msgs)
	if len(blocks) == 0 {
		return nil
	}

	// Sorted, so a redelivery of the same messages writes the same blocks in the same
	// order with the same tokens. Map iteration order would make the chunk index — and
	// therefore the token — depend on Go's hash seed, which is the kind of
	// non-determinism that turns a redelivery into a duplicate on some restarts and not
	// others.
	for _, key := range slices.SortedFunc(maps.Keys(blocks), compareBlockKey) {
		b := blocks[key]
		for start, chunk := 0, 0; start < len(b.rows); start, chunk = start+w.maxRowsPerInsert, chunk+1 {
			end := min(start+w.maxRowsPerInsert, len(b.rows))
			token := blockToken(w.cell, key, chunk, b.dedupKeys)
			if err := w.insert(ctx, b.rows[start:end], token); err != nil {
				// The position does not move, so this delivery arrives again. Returning
				// the error rather than logging and continuing is what makes that true.
				return fmt.Errorf("insert %s: %w", token, err)
			}
			w.stats.rows.Add(int64(end - start))
			w.stats.inserts.Add(1)
		}
	}
	w.stats.messages.Add(int64(len(msgs)))
	return nil
}

// record counts a consecutive failure of this delivery.
//
// Keyed by the delivery's contents rather than by position, so a redelivery that has picked
// up new messages starts its own count instead of inheriting one. Not concurrency-guarded
// because a handler is called from one goroutine at a time — the Pub/Sub grouper serializes
// flushes and the local bus delivers in a single loop.
func (w *Writer) record(msgs []*eventbus.Message) int {
	key := deliveryKey(msgs)
	w.attempts[key]++
	n := w.attempts[key]

	// A delivery that has moved on must not leave its counter behind for the lifetime of
	// the process. Only the head of the queue retries, so anything else is stale.
	if len(w.attempts) > 64 {
		for k := range w.attempts {
			if k != key {
				delete(w.attempts, k)
			}
		}
	}
	return n
}

func deliveryKey(msgs []*eventbus.Message) string {
	sum := sha256.New()
	for _, m := range msgs {
		sum.Write([]byte(m.DedupKey))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil)[:16])
}

// isolate stores the delivery one message at a time to find the ones that cannot be stored.
//
// A message that fails alone is only dead-lettered once ClickHouse has been shown to be
// answering — `probe` runs a trivial query, so "this data is unstorable" is distinguished
// from "the database is down" by asking the database rather than by counting attempts. The
// difference is the whole design: without the probe, an outage would move an entire backlog
// to the dead-letter topic, one delivery at a time, and call it poison.
//
// Returning nil after dead-lettering is deliberate. The messages that succeeded are durable
// and the ones that did not are parked, so there is nothing left to redeliver — and
// replaying the successful ones would insert them again under a different token, because
// the group's membership would have changed (ADR 0023).
func (w *Writer) isolate(ctx context.Context, msgs []*eventbus.Message) error {
	var parked, stored int

	for _, msg := range msgs {
		if err := w.write(ctx, []*eventbus.Message{msg}); err == nil {
			stored++
			continue
		} else if probeErr := w.probe(ctx); probeErr != nil {
			// ClickHouse is not answering, so nothing here is poison. Keep the whole
			// delivery and let it be redelivered; the attempt counter keeps climbing and
			// the bus backs off.
			w.log.Error("isolation stopped: clickhouse is unavailable",
				"stored_before_failure", stored, "error", probeErr)
			return fmt.Errorf("isolate after %d stored: %w", stored, err)
		} else {
			// The database is fine and this message still will not go in. No number of
			// redeliveries changes that, and leaving it at the head of the queue stalls
			// everything behind it.
			reason := eventbus.DeadLetterReason{
				Group:    eventbus.GroupAnalyticsWriter,
				Reason:   err.Error(),
				FailedAt: time.Now().UTC(),
				Attempts: w.maxAttempts,
			}
			if dlErr := w.bus.DeadLetter(ctx, msg, reason); dlErr != nil {
				// The dead letter is not durable, so the message must not be
				// acknowledged. Better a stalled queue than a silently discarded batch.
				return fmt.Errorf("dead-letter %s: %w", msg.DedupKey, dlErr)
			}
			w.stats.deadLettered.Add(1)
			w.log.Error("a message could not be stored and was dead-lettered",
				"dedup_key", msg.DedupKey, "project_id", msg.ProjectID,
				"events", msg.EventCount, "error", err)
			parked++
		}
	}

	delete(w.attempts, deliveryKey(msgs))
	w.log.Warn("isolation finished", "stored", stored, "dead_lettered", parked)
	return nil
}

// probe asks ClickHouse whether it is answering at all.
//
// This is what separates a poisoned message from an unavailable database, and it is worth
// the round trip: it runs once per failing message during isolation, which is a path that
// only executes after a delivery has already failed several times.
func (w *Writer) probe(ctx context.Context) error {
	conn, err := w.ch.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var one uint8
	return conn.QueryRow(ctx, "SELECT 1").Scan(&one)
}

// erasedSet is the identities in one batch that have been erased (§61).
//
// A set of identities rather than of actors, because an erasure may name either an actor key
// or an install (§05) and both have to match. `names` asks the question the way a row
// answers it.
type erasedSet map[string]struct{}

func (e erasedSet) names(batch *ingest.NormalizedBatch, ev *ingest.NormalizedEvent) bool {
	if _, ok := e[actorKeyOf(batch, ev)]; ok {
		return true
	}
	_, ok := e[installIDOf(batch, ev)]
	return ok
}

// maxTombstoneChecks bounds how many distinct identities one message is asked about.
//
// A batch is one flush from one installation, so the realistic number is one or two — the
// install, and the account signed in on it. The bound exists for the shape that is not
// realistic: §19.1 allows a thousand events in a batch, and a server-side integration
// aggregating a thousand different players into one would otherwise turn a single message
// into a thousand sequential cache round trips on the storage path, which is the kind of
// per-event lookup §3.2 exists to keep off it.
//
// Past the bound the checks stop rather than continue, and the skip is counted. That is the
// same trade the rest of this path makes: the tombstone is a fast path and §61's purge is
// the guarantee, so the cost of not asking is that an erased actor's late event is removed
// hours later instead of never being written.
const maxTombstoneChecks = 64

// erasedActors asks Valkey about each distinct identity in the batch, once.
//
// Distinct, because a batch of five hundred events is usually five hundred events by the
// same player and asking five hundred times would be five hundred round trips to learn one
// thing. Usually one or two keys: the actor, and the install it arrived from.
//
// An error is absence. That is the documented behaviour of both tombstones (§06): the fast
// path fails open and the purge is what guarantees the removal, because a cache outage that
// silently dropped events would lose data nobody asked to delete.
func (w *Writer) erasedActors(ctx context.Context, projectID string, batch *ingest.NormalizedBatch) erasedSet {
	var out erasedSet
	asked := make(map[string]struct{}, 2)

	ask := func(key string) bool {
		if key == "" {
			return true
		}
		if _, seen := asked[key]; seen {
			return true
		}
		if len(asked) >= maxTombstoneChecks {
			return false
		}
		asked[key] = struct{}{}
		if yes, err := w.cache.Exists(ctx, tombstone.Actor(w.cache, projectID, key)); err == nil && yes {
			if out == nil {
				out = erasedSet{}
			}
			out[key] = struct{}{}
		}
		return true
	}

	if !ask(batch.InstallID) {
		return out
	}
	for i := range batch.Events {
		if !ask(actorKeyOf(batch, &batch.Events[i])) || !ask(installIDOf(batch, &batch.Events[i])) {
			w.stats.tombstoneChecksSkipped.Add(1)
			w.log.Warn("stopped checking erasure tombstones part way through a batch",
				"project_id", projectID,
				"identities_checked", len(asked),
				"note", "§61's purge removes anything this let through")
			return out
		}
	}
	return out
}

// actorKeyOf computes the identity ClickHouse materializes as `actor_key`.
//
//	actor_key = if(player_id != '', player_id, toString(install_id))
//
// Written here as well as in the archive's Row because the writer has to recognise an erased
// actor *before* the row exists — the column it would read is computed by the insert this
// check decides whether to perform.
func actorKeyOf(batch *ingest.NormalizedBatch, ev *ingest.NormalizedEvent) string {
	if ev.PlayerID != "" {
		return ev.PlayerID
	}
	return installIDOf(batch, ev)
}

// installIDOf is the event's install, falling back to the batch's, as the archive does.
func installIDOf(batch *ingest.NormalizedBatch, ev *ingest.NormalizedEvent) string {
	if ev.InstallID != "" {
		return ev.InstallID
	}
	return batch.InstallID
}

// blockKey is ClickHouse's own partition key for events_raw: (retention_tier, event_date).
//
// Grouping on it rather than on arrival is what makes one insert produce one part. An
// insert spanning two partitions produces a part in each, so a delivery mixing three
// retention tiers over two days would otherwise write six parts where six blocks of one
// partition each write six — the same count, but only by accident, and it stops being true
// the moment a delivery is large.
type blockKey struct {
	retentionTier uint16
	eventDate     string
}

func compareBlockKey(a, b blockKey) int {
	if a.retentionTier != b.retentionTier {
		return int(a.retentionTier) - int(b.retentionTier)
	}
	return strings.Compare(a.eventDate, b.eventDate)
}

// block is the rows destined for one partition, and the messages that contributed them.
type block struct {
	rows      []pendingRow
	dedupKeys []string
}

// decoded is one message, parsed once.
//
// The UUID parses and the device-state lookup are per message, not per row: a batch is one
// flush from one installation, so every row in it shares them. Parsing them per row was
// affordable when a block was one message and is not when a block is thousands.
type decoded struct {
	msg   *eventbus.Message
	batch *ingest.NormalizedBatch
	state DeviceState

	orgID         uuid.UUID
	projectID     uuid.UUID
	environmentID uuid.UUID
	installID     uuid.UUID
	batchID       uuid.UUID
	cohortDate    *time.Time
}

// pendingRow is one event and the message-level values it inherits.
type pendingRow struct {
	d  *decoded
	ev *ingest.NormalizedEvent
}

// plan decodes a delivery, drops what must not be stored, and groups the rest by partition.
//
// Every rejection here is a message that is *not* retried, which is why each one is counted
// rather than returned: a message that will never be storable must not stall the
// subscription behind it.
func (w *Writer) plan(ctx context.Context, msgs []*eventbus.Message) map[blockKey]*block {
	blocks := map[blockKey]*block{}

	for _, msg := range msgs {
		// A message from another cell is a routing mistake, and storing it would put one
		// region's player data in another region's ClickHouse — the same failure the
		// gateway's 421 prevents, arriving by a different road (§6, §99). Dropped rather
		// than retried: redelivering it here would never make it belong.
		if msg.Cell != "" && msg.Cell != w.cell {
			w.stats.droppedWrongCell.Add(1)
			w.log.Warn("dropped a message belonging to another cell",
				"message_cell", msg.Cell, "this_cell", w.cell, "project_id", msg.ProjectID)
			continue
		}

		// §61: a queue can hold messages for hours, so a batch arriving for a project that
		// has since been deleted is ordinary rather than exceptional. Writing it would
		// resurrect data somebody asked to have erased.
		//
		// If Valkey cannot answer, the batch is written and the asynchronous purge removes
		// it. The purge is the guarantee; this is a fast path. Making the fast path the
		// guarantee would let a cache outage silently resurrect deleted data.
		if tombstoned, err := w.cache.Exists(ctx, tombstone.Project(w.cache, msg.ProjectID)); err == nil && tombstoned {
			w.stats.droppedTombstoned.Add(1)
			continue
		}

		batch := new(ingest.NormalizedBatch)
		if err := json.Unmarshal(msg.Payload, batch); err != nil {
			// A message that cannot be decoded will never decode. Retrying it forever
			// would stall the subscription behind one bad record, so it is counted and
			// skipped.
			w.log.Error("undecodable message", "dedup_key", msg.DedupKey, "error", err)
			continue
		}
		if len(batch.Events) == 0 {
			continue
		}

		d, err := w.decode(ctx, msg, batch)
		if err != nil {
			// Same class as an undecodable payload: a malformed identifier is a bug in
			// whatever produced the message, and no number of redeliveries fixes it.
			w.log.Error("unusable message", "dedup_key", msg.DedupKey, "error", err)
			continue
		}

		// §61's other tombstone, and it is per event rather than per message. A batch is
		// one flush from one installation, but the actor can change inside it — an
		// anonymous player signs in half way through and the rows after that point belong
		// to an account (§29.2) — so a message is not the unit an erasure applies to.
		//
		// Same fast-path reasoning as the project tombstone above: an unavailable Valkey
		// stores the batch and §61's purge removes it.
		erased := w.erasedActors(ctx, msg.ProjectID, batch)

		for i := range batch.Events {
			ev := &batch.Events[i]
			if len(erased) > 0 && erased.names(batch, ev) {
				w.stats.droppedErased.Add(1)
				continue
			}
			key := blockKey{
				retentionTier: msg.RetentionTier,
				// toDate(event_time), which is what the partition expression computes.
				eventDate: ev.EventTime.UTC().Format(time.DateOnly),
			}
			b := blocks[key]
			if b == nil {
				b = &block{}
				blocks[key] = b
			}
			if len(b.dedupKeys) == 0 || b.dedupKeys[len(b.dedupKeys)-1] != msg.DedupKey {
				b.dedupKeys = append(b.dedupKeys, msg.DedupKey)
			}
			b.rows = append(b.rows, pendingRow{d: d, ev: ev})
		}
	}
	return blocks
}

// decode parses the per-message values every row in the message shares.
func (w *Writer) decode(ctx context.Context, msg *eventbus.Message, batch *ingest.NormalizedBatch) (*decoded, error) {
	d := &decoded{msg: msg, batch: batch, state: w.deviceState(ctx, msg, batch)}

	var err error
	if d.orgID, err = uuid.Parse(msg.OrgID); err != nil {
		return nil, fmt.Errorf("org_id %q: %w", msg.OrgID, err)
	}
	if d.projectID, err = uuid.Parse(msg.ProjectID); err != nil {
		return nil, fmt.Errorf("project_id %q: %w", msg.ProjectID, err)
	}
	if d.environmentID, err = uuid.Parse(msg.EnvironmentID); err != nil {
		return nil, fmt.Errorf("environment_id %q: %w", msg.EnvironmentID, err)
	}
	if d.installID, err = uuid.Parse(batch.InstallID); err != nil {
		return nil, fmt.Errorf("install_id %q: %w", batch.InstallID, err)
	}
	if d.batchID, err = uuid.Parse(batch.BatchID); err != nil {
		return nil, fmt.Errorf("batch_id %q: %w", batch.BatchID, err)
	}
	d.cohortDate = parseDate(d.state.CohortDate)
	return d, nil
}

// blockToken names a block by what is in it (§21, ADR 0023).
//
// Derived from the contributing batches rather than from a clock or a counter, so a
// redelivery of the same messages produces the same token and ClickHouse discards the
// repeat before the materialized views see it. That last clause is why block-level
// deduplication is the only mechanism that works here: a materialized view fires on the
// inserted block, so anything that removes duplicates *after* the insert — a
// ReplacingMergeTree, a dedupe on read — leaves player_activity_daily and player_lifetime
// already inflated.
//
// The chunk index is part of the token and must be. Two chunks of one block carry the same
// dedup keys, and a token reused across two different blocks makes ClickHouse discard the
// second as a duplicate of the first — a silent loss, which is worse than the duplicate it
// was meant to prevent.
//
// What this does **not** cover is a delivery whose membership changes between attempts.
// That is a real weakening of ADR 0018 and it is recorded rather than hidden: see ADR 0023.
func blockToken(cell string, key blockKey, chunk int, dedupKeys []string) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s|%d|%s|%d|", cell, key.retentionTier, key.eventDate, chunk)
	// Sorted, so the token depends on the set of contributing batches and not on the order
	// the queue happened to hand them over.
	for _, k := range slices.Sorted(slices.Values(dedupKeys)) {
		// The separator matters: without it "ab"+"c" and "a"+"bc" are the same token, and
		// two different sets of batches would deduplicate against each other.
		sum.Write([]byte(k))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil)[:16])
}

// insertColumns is the column list, in the order the batch appends them. Written out
// rather than inferred, so adding a column to the table is a compile-time conversation
// rather than a runtime surprise about which value landed where.
const insertStatement = `INSERT INTO events_raw (
	org_seq, project_seq, environment_seq,
	org_id, project_id, environment_id,
	retention_tier, policy_tier,
	event_time, ingest_time, received_at,
	event_id, batch_id, session_id, player_id, install_id,
	event_name, event_type,
	platform, country_code, app_version, sdk_name, sdk_version,
	cohort_date, install_platform, install_country, first_app_version, acquisition_source,
	consent_analytics, consent_policy_version, att_status,
	props_string, props_int, props_float, props_bool,
	revenue_micros, currency, playtime_ms,
	payload_bytes
)`

// insert writes one block and returns once ClickHouse has it.
//
// The rows may come from many messages and many projects; everything that varies per
// message was parsed once in decode and is read off the row's own decoded pointer.
func (w *Writer) insert(ctx context.Context, rows []pendingRow, token string) error {
	conn, err := w.ch.DB()
	if err != nil {
		return err
	}

	// A plain synchronous insert, not an asynchronous one.
	//
	// ADR 0018 set async_insert=1 with wait_for_async_insert=1, and the wait was doing the
	// work: async_insert exists to coalesce many small inserts server-side, and a
	// per-insert deduplication token prevents exactly that coalescing — the token is part
	// of the grouping key, so every insert became its own buffer and its own part. With
	// the block already assembled here the server has nothing left to coalesce, so the
	// setting bought the fragmentation and none of the benefit.
	//
	// Dropping it is also strictly stronger on durability: a synchronous insert returns
	// when the block is written, with no server-side buffer that a ClickHouse restart
	// could lose. Appendix C.2 is satisfied by construction rather than by remembering to
	// set a second setting.
	//
	// insert_deduplication_token is what makes §21's at-least-once delivery land once, and
	// it works because events_raw is a ReplicatedMergeTree: the engine remembers recent
	// block tokens and discards a repeat before the materialized views fire.
	ctx = clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"insert_deduplication_token": token,
	}))

	block, err := conn.PrepareBatch(ctx, insertStatement)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}

	for _, row := range rows {
		d, ev := row.d, row.ev
		eventID, err := uuid.Parse(ev.EventID)
		if err != nil {
			return fmt.Errorf("event_id %q: %w", ev.EventID, err)
		}
		if err := block.Append(
			d.msg.OrgSeq, d.msg.ProjectSeq, d.msg.EnvironmentSeq,
			d.orgID, d.projectID, d.environmentID,
			d.msg.RetentionTier, d.msg.PolicyTier,
			ev.EventTime, d.batch.IngestTime, d.batch.ReceivedAt,
			eventID, d.batchID, ev.SessionID, ev.PlayerID, d.installID,
			ev.EventName, ev.EventType,
			d.batch.Platform, d.batch.CountryCode, d.batch.AppVersion,
			d.batch.SDKName, d.batch.SDKVersion,
			d.cohortDate, d.state.InstallPlatform, d.state.InstallCountry,
			d.state.FirstAppVersion, d.state.AcquisitionSource,
			d.batch.ConsentAnalytics, d.batch.ConsentPolicyVersion, d.batch.ATTStatus,
			orEmptyString(ev.PropsString), orEmptyInt(ev.PropsInt),
			orEmptyFloat(ev.PropsFloat), orEmptyBool(ev.PropsBool),
			ev.RevenueMicros, ev.Currency, playtimeMillis(ev),
			ev.PayloadBytes,
		); err != nil {
			return fmt.Errorf("append row: %w", err)
		}
	}

	// Send returns once ClickHouse has the block. Everything above this line is buffering;
	// this is the durability the acknowledgement is waiting on.
	if err := block.Send(); err != nil {
		return fmt.Errorf("send block: %w", err)
	}
	return nil
}

// parseDate turns the stored cohort date into the Nullable(Date) the column holds.
//
// A nil means "Valkey could not say", which the nightly derivation backfills from
// player_lifetime. It is deliberately distinguishable from a zero date, which would be a
// 1970 cohort with a day-offset of twenty thousand.
func parseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil
	}
	return &t
}

// playtimeMillis reads the playtime a session event carries as a property.
//
// It is a column rather than a map lookup at query time because §29's playtime buckets
// aggregate on it, and a Map access per row over a year of data is the cost this table
// exists to avoid.
func playtimeMillis(ev *ingest.NormalizedEvent) uint64 {
	if v, ok := ev.PropsInt["playtime_ms"]; ok && v > 0 {
		return uint64(v)
	}
	if v, ok := ev.PropsInt["duration_ms"]; ok && v > 0 {
		return uint64(v)
	}
	return 0
}

// The driver rejects a nil map for a non-Nullable Map column, and an SDK that sent no
// properties of a given type is the ordinary case rather than an error.
func orEmptyString(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func orEmptyInt(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

func orEmptyFloat(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}

func orEmptyBool(m map[string]uint8) map[string]uint8 {
	if m == nil {
		return map[string]uint8{}
	}
	return m
}
