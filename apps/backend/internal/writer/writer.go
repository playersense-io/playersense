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
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
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
}

type stats struct {
	messages             atomic.Int64
	rows                 atomic.Int64
	droppedTombstoned    atomic.Int64
	droppedWrongCell     atomic.Int64
	deviceLookupFailures atomic.Int64
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
	if log == nil {
		log = slog.Default()
	}
	return &Writer{
		bus: bus, cache: cache, ch: ch, log: log,
		cell:             opts.Cell,
		maxRowsPerInsert: opts.MaxRowsPerInsert,
		deviceTTL:        opts.DeviceStateTTL,
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
		if err := w.bus.Subscribe(ctx, w.handle); err != nil && ctx.Err() == nil {
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
		"dropped_tombstoned", w.stats.droppedTombstoned.Load(),
		"dropped_wrong_cell", w.stats.droppedWrongCell.Load())
	return nil
}

// Stats reports what the writer has done, for the health endpoint and for tests.
func (w *Writer) Stats() map[string]int64 {
	return map[string]int64{
		"messages":               w.stats.messages.Load(),
		"rows":                   w.stats.rows.Load(),
		"dropped_tombstoned":     w.stats.droppedTombstoned.Load(),
		"dropped_wrong_cell":     w.stats.droppedWrongCell.Load(),
		"device_lookup_failures": w.stats.deviceLookupFailures.Load(),
	}
}

// handle stores a group of messages, returning nil only once every row is durable.
func (w *Writer) handle(ctx context.Context, msgs []*eventbus.Message) error {
	for _, msg := range msgs {
		if err := w.store(ctx, msg); err != nil {
			// The position does not move, so this message is delivered again. Returning
			// the error rather than logging and continuing is what makes that true.
			return fmt.Errorf("store %s: %w", msg.DedupKey, err)
		}
	}
	return nil
}

func (w *Writer) store(ctx context.Context, msg *eventbus.Message) error {
	// A message from another cell is a routing mistake, and storing it would put one
	// region's player data in another region's ClickHouse — the same failure the gateway's
	// 421 prevents, arriving by a different road (§6, §99). Dropped rather than retried:
	// redelivering it here would never make it belong.
	if msg.Cell != "" && msg.Cell != w.cell {
		w.stats.droppedWrongCell.Add(1)
		w.log.Warn("dropped a message belonging to another cell",
			"message_cell", msg.Cell, "this_cell", w.cell, "project_id", msg.ProjectID)
		return nil
	}

	// §61: a queue can hold messages for hours, so a batch arriving for a project that has
	// since been deleted is ordinary rather than exceptional. Writing it would resurrect
	// data somebody asked to have erased.
	//
	// If Valkey cannot answer, the batch is written and the asynchronous purge removes it.
	// The purge is the guarantee; this is a fast path. Making the fast path the guarantee
	// would let a cache outage silently resurrect deleted data.
	if tombstoned, err := w.cache.Exists(ctx, tombstoneKey(w.cache, msg.ProjectID)); err == nil && tombstoned {
		w.stats.droppedTombstoned.Add(1)
		return nil
	}

	var batch ingest.NormalizedBatch
	if err := json.Unmarshal(msg.Payload, &batch); err != nil {
		// A message that cannot be decoded will never decode. Retrying it forever would
		// stall the subscription behind one bad record, so it is counted and skipped.
		w.log.Error("undecodable message", "dedup_key", msg.DedupKey, "error", err)
		return nil
	}
	if len(batch.Events) == 0 {
		return nil
	}

	state := w.deviceState(ctx, msg, &batch)

	// Split deterministically, so a redelivery produces the same blocks with the same
	// tokens. A token reused across two different blocks would make ClickHouse discard the
	// second as a duplicate of the first, which is a silent loss rather than a duplicate.
	for start, chunk := 0, 0; start < len(batch.Events); start, chunk = start+w.maxRowsPerInsert, chunk+1 {
		end := min(start+w.maxRowsPerInsert, len(batch.Events))
		token := fmt.Sprintf("%s#%d", msg.DedupKey, chunk)
		if err := w.insert(ctx, msg, &batch, state, batch.Events[start:end], token); err != nil {
			return err
		}
		w.stats.rows.Add(int64(end - start))
	}
	w.stats.messages.Add(1)
	return nil
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

func (w *Writer) insert(
	ctx context.Context,
	msg *eventbus.Message,
	batch *ingest.NormalizedBatch,
	state DeviceState,
	events []ingest.NormalizedEvent,
	token string,
) error {
	conn, err := w.ch.DB()
	if err != nil {
		return err
	}

	// §25: asynchronous inserts, waited on. The wait is what makes the acknowledgement
	// honest — async_insert without wait_for_async_insert buffers in the *server*, so a
	// ClickHouse restart loses rows this writer has already acknowledged.
	//
	// insert_deduplication_token is what makes §21's at-least-once delivery land once. It
	// works because events_raw is a ReplicatedMergeTree: the engine remembers recent block
	// tokens and discards a repeat.
	ctx = clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"async_insert":               1,
		"wait_for_async_insert":      1,
		"insert_deduplication_token": token,
	}))

	block, err := conn.PrepareBatch(ctx, insertStatement)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}

	orgID, err := uuid.Parse(msg.OrgID)
	if err != nil {
		return fmt.Errorf("org_id %q: %w", msg.OrgID, err)
	}
	projectID, err := uuid.Parse(msg.ProjectID)
	if err != nil {
		return fmt.Errorf("project_id %q: %w", msg.ProjectID, err)
	}
	environmentID, err := uuid.Parse(msg.EnvironmentID)
	if err != nil {
		return fmt.Errorf("environment_id %q: %w", msg.EnvironmentID, err)
	}
	installID, err := uuid.Parse(batch.InstallID)
	if err != nil {
		return fmt.Errorf("install_id %q: %w", batch.InstallID, err)
	}
	batchID, err := uuid.Parse(batch.BatchID)
	if err != nil {
		return fmt.Errorf("batch_id %q: %w", batch.BatchID, err)
	}

	cohortDate := parseDate(state.CohortDate)

	for i := range events {
		ev := &events[i]
		eventID, err := uuid.Parse(ev.EventID)
		if err != nil {
			return fmt.Errorf("event_id %q: %w", ev.EventID, err)
		}
		if err := block.Append(
			msg.OrgSeq, msg.ProjectSeq, msg.EnvironmentSeq,
			orgID, projectID, environmentID,
			msg.RetentionTier, msg.PolicyTier,
			ev.EventTime, batch.IngestTime, batch.ReceivedAt,
			eventID, batchID, ev.SessionID, ev.PlayerID, installID,
			ev.EventName, ev.EventType,
			batch.Platform, batch.CountryCode, batch.AppVersion, batch.SDKName, batch.SDKVersion,
			cohortDate, state.InstallPlatform, state.InstallCountry,
			state.FirstAppVersion, state.AcquisitionSource,
			batch.ConsentAnalytics, batch.ConsentPolicyVersion, batch.ATTStatus,
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
