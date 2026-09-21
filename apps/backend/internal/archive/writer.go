package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"

	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/pgdb"
)

// Options configure the archive writer.
type Options struct {
	// MaxRowsPerObject splits a very large delivery into several objects rather than
	// building one enormous buffer in memory. A ceiling, not a target.
	MaxRowsPerObject int

	// MaxAttempts is how many times a delivery is retried whole before the writer starts
	// looking for a single message it cannot archive. See the note on isolate.
	MaxAttempts int
}

type stats struct {
	messages     atomic.Int64
	rows         atomic.Int64
	objects      atomic.Int64
	bytes        atomic.Int64
	retries      atomic.Int64
	deadLettered atomic.Int64
	undecodable  atomic.Int64
}

// Writer consumes the event stream and writes immutable archive objects.
//
// It acknowledges only after the object is durable **and** its manifest is committed
// (Appendix C.2). Both halves matter: an object with no manifest is invisible to §60's
// expiry, §39's billing and §104's breach scoping, which makes it indistinguishable from
// data nobody knows about.
type Writer struct {
	bus   eventbus.Bus
	store blobstore.Store
	db    *pgdb.DB
	log   *slog.Logger
	opts  Options

	// attempts counts consecutive failures of the delivery at the head of the queue. In
	// memory deliberately: a restart resetting it is correct, because a restart is itself
	// a reason the next attempt might succeed.
	attempts map[string]int

	stats  stats
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns an unstarted writer. Construction connects to nothing.
func New(bus eventbus.Bus, store blobstore.Store, db *pgdb.DB, log *slog.Logger, opts Options) *Writer {
	if opts.MaxRowsPerObject <= 0 {
		opts.MaxRowsPerObject = 200_000
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if log == nil {
		log = slog.Default()
	}
	return &Writer{
		bus: bus, store: store, db: db, log: log, opts: opts,
		attempts: map[string]int{},
		done:     make(chan struct{}),
	}
}

// Name identifies the component in start-up and shutdown logs.
func (w *Writer) Name() string { return "archive-writer" }

// Start begins consuming, on its own subscription (§20).
//
// The context is derived from Background rather than from the start-up context, which is
// cancelled once start-up finishes. Start returns immediately: a Start that never returns is
// a service that never becomes ready.
func (w *Writer) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel

	go func() {
		defer close(w.done)
		if err := w.bus.Subscribe(ctx, eventbus.GroupArchiveWriter, w.handle); err != nil && ctx.Err() == nil {
			w.log.Error("the archive subscription stopped", "error", err)
		}
	}()
	return nil
}

// Stop cancels the subscription and waits for the delivery in flight.
func (w *Writer) Stop(ctx context.Context) error {
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	select {
	case <-w.done:
		w.log.Info("archive writer stopped", "objects", w.stats.objects.Load(),
			"rows", w.stats.rows.Load(), "bytes", w.stats.bytes.Load())
		return nil
	case <-ctx.Done():
		return fmt.Errorf("the archive writer did not stop within the shutdown timeout")
	}
}

// Stats exposes the counters §67 asks for.
func (w *Writer) Stats() map[string]int64 {
	return map[string]int64{
		"messages":      w.stats.messages.Load(),
		"rows":          w.stats.rows.Load(),
		"objects":       w.stats.objects.Load(),
		"bytes":         w.stats.bytes.Load(),
		"retries":       w.stats.retries.Load(),
		"dead_lettered": w.stats.deadLettered.Load(),
		"undecodable":   w.stats.undecodable.Load(),
	}
}

// group is one object's worth of rows: one project, one environment, one hour.
//
// §14.1 requires an object to hold one project and environment so that storage can be
// attributed, a project deletion is practical, retention is practical, an export is
// practical and a cold query can prune prefixes. All five are properties of the *prefix*,
// which is why the grouping is the path.
type group struct {
	orgUID     string
	projectUID string
	envUID     string
	env        string
	hour       time.Time

	retentionTier uint16
	rows          []Row
	dedupKeys     []string
	minTime       time.Time
	maxTime       time.Time
	inputBytes    uint64
}

type groupKey struct {
	project string
	env     string
	hour    time.Time
}

// handle turns one delivery into objects, and returns nil only once they are durable.
//
// There is no buffering across deliveries, and that is a decision rather than an omission.
// Holding rows back would mean acknowledging messages whose effect is still in memory, which
// Appendix C.2 forbids — a crash would lose events the queue believes were archived. The
// cost is that a trickle of traffic produces small objects, which §10.4 already anticipates
// and hands to §10.7's compaction worker.
func (w *Writer) handle(ctx context.Context, msgs []*eventbus.Message) error {
	if err := w.write(ctx, msgs); err == nil {
		// Counted here rather than inside write, which runs again on every retry and once
		// more per message during isolation: counting there reports a delivery retried
		// five times as five times the traffic.
		w.stats.messages.Add(int64(len(msgs)))
		delete(w.attempts, deliveryKey(msgs))
		return nil
	} else if n := w.recordAttempt(msgs); n < w.opts.MaxAttempts {
		w.stats.retries.Add(1)
		w.log.Warn("a delivery failed and will be retried",
			"attempt", n, "of", w.opts.MaxAttempts, "messages", len(msgs), "error", err)
		return err
	}

	w.log.Warn("a delivery has failed repeatedly; isolating the messages",
		"attempts", w.opts.MaxAttempts, "messages", len(msgs))
	return w.isolate(ctx, msgs)
}

// write archives a whole delivery.
func (w *Writer) write(ctx context.Context, msgs []*eventbus.Message) error {
	groups := map[groupKey]*group{}

	for _, msg := range msgs {
		var batch ingest.NormalizedBatch
		if err := json.Unmarshal(msg.Payload, &batch); err != nil {
			// A message that cannot be decoded will never decode. Retrying it forever
			// would stall the subscription behind one bad record.
			w.stats.undecodable.Add(1)
			w.log.Error("dropped an undecodable message", "dedup_key", msg.DedupKey, "error", err)
			continue
		}
		w.collect(groups, msg, &batch)
	}

	for _, g := range groups {
		if err := w.flush(ctx, g); err != nil {
			// The position does not move, so this delivery arrives again. Returning the
			// error rather than logging and continuing is what makes that true.
			return err
		}
	}
	return nil
}

// recordAttempt counts a consecutive failure of this delivery.
func (w *Writer) recordAttempt(msgs []*eventbus.Message) int {
	key := deliveryKey(msgs)
	w.attempts[key]++
	n := w.attempts[key]
	if len(w.attempts) > 64 {
		for k := range w.attempts {
			if k != key {
				delete(w.attempts, k)
			}
		}
	}
	return n
}

// deliveryKey names a delivery by its contents, so a redelivery that picked up new messages
// starts its own count rather than inheriting one.
func deliveryKey(msgs []*eventbus.Message) string {
	sum := sha256.New()
	for _, m := range msgs {
		sum.Write([]byte(m.DedupKey))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil)[:16])
}

// isolate archives the delivery one message at a time to find the ones that cannot be.
//
// The same shape as the storage writer's, and for the same reason: a message that will
// never archive must not stall every message behind it, and attempt count alone cannot tell
// it from an unreachable bucket. The archive has two dependencies rather than one — the
// object store and the database, since an object with no manifest is invisible to
// §60's expiry, §39's billing and §104's breach scoping — so both are probed before
// anything is parked.
func (w *Writer) isolate(ctx context.Context, msgs []*eventbus.Message) error {
	var parked, stored int

	for _, msg := range msgs {
		if err := w.write(ctx, []*eventbus.Message{msg}); err == nil {
			w.stats.messages.Add(1)
			stored++
			continue
		} else if probeErr := w.probe(ctx); probeErr != nil {
			w.log.Error("isolation stopped: a dependency is unavailable",
				"stored_before_failure", stored, "error", probeErr)
			return fmt.Errorf("isolate after %d archived: %w", stored, err)
		} else {
			reason := eventbus.DeadLetterReason{
				Group:    eventbus.GroupArchiveWriter,
				Reason:   err.Error(),
				FailedAt: time.Now().UTC(),
				Attempts: w.opts.MaxAttempts,
			}
			if dlErr := w.bus.DeadLetter(ctx, msg, reason); dlErr != nil {
				// Not durable, so the message must not be acknowledged. A stalled queue
				// beats an archive with a hole nobody knows about.
				return fmt.Errorf("dead-letter %s: %w", msg.DedupKey, dlErr)
			}
			w.stats.deadLettered.Add(1)
			w.log.Error("a message could not be archived and was dead-lettered",
				"dedup_key", msg.DedupKey, "project_id", msg.ProjectID, "error", err)
			parked++
		}
	}

	delete(w.attempts, deliveryKey(msgs))
	w.log.Warn("isolation finished", "archived", stored, "dead_lettered", parked)
	return nil
}

// probe asks both dependencies whether they are answering.
//
// The store first, because it is the one that is remote in every deployment. Neither write
// is attempted: Ping reads metadata, so a probe leaves nothing behind in a bucket §60
// expires and §39 bills for.
func (w *Writer) probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := w.store.Ping(ctx); err != nil {
		return fmt.Errorf("object store: %w", err)
	}
	if !w.db.Ready() {
		return fmt.Errorf("database: %w", pgdb.ErrNotStarted)
	}
	if err := w.db.DB().PingContext(ctx); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	return nil
}

// collect places each event in the group its own timestamp belongs to.
//
// Per event rather than per batch, because a batch spooled on a device can straddle an hour
// and §104's breach scoping is only as precise as the time bounds on the manifest. A batch
// filed wholesale under its first event's hour would widen every range it touched.
func (w *Writer) collect(groups map[groupKey]*group, msg *eventbus.Message, batch *ingest.NormalizedBatch) {
	for i := range batch.Events {
		ev := &batch.Events[i]
		hour := ev.EventTime.UTC().Truncate(time.Hour)
		key := groupKey{project: msg.ProjectID, env: msg.EnvironmentID, hour: hour}

		g := groups[key]
		if g == nil {
			g = &group{
				orgUID:        msg.OrgID,
				projectUID:    msg.ProjectID,
				envUID:        msg.EnvironmentID,
				env:           environmentName(msg.Environment),
				hour:          hour,
				retentionTier: msg.RetentionTier,
				minTime:       ev.EventTime,
				maxTime:       ev.EventTime,
			}
			groups[key] = g
		}
		if !slices.Contains(g.dedupKeys, msg.DedupKey) {
			g.dedupKeys = append(g.dedupKeys, msg.DedupKey)
		}
		if ev.EventTime.Before(g.minTime) {
			g.minTime = ev.EventTime
		}
		if ev.EventTime.After(g.maxTime) {
			g.maxTime = ev.EventTime
		}
		g.inputBytes += uint64(ev.PayloadBytes)
		g.rows = append(g.rows, rowFrom(msg, batch, ev))
	}
}

// flush writes one group's objects and records a manifest for each.
func (w *Writer) flush(ctx context.Context, g *group) error {
	for chunk := range slices.Chunk(g.rows, w.opts.MaxRowsPerObject) {
		body, err := Encode(chunk)
		if err != nil {
			return fmt.Errorf("encode parquet: %w", err)
		}

		path := objectPath(g, digest(g.dedupKeys, len(chunk), chunk[0].EventID))
		object, err := w.store.Put(ctx, path, body)
		if err != nil {
			return fmt.Errorf("write archive object: %w", err)
		}

		// Only now. The manifest is the record that the bytes exist, so writing it first
		// would create a row §60 would later try to delete an object for, and §39 would
		// bill for, and neither would be there.
		if err := w.record(ctx, g, object, uint64(len(chunk))); err != nil {
			return fmt.Errorf("record archive manifest: %w", err)
		}

		w.stats.objects.Add(1)
		w.stats.rows.Add(int64(len(chunk)))
		w.stats.bytes.Add(int64(object.Size))
	}
	return nil
}

// record commits the manifest, tolerating a redelivery.
//
// The unique index on (bucket, object_path) is what makes this idempotent: a redelivered
// flush produces the same content, the same digest, the same path, and therefore the same
// object and a manifest that already exists. Doing nothing is the correct outcome, and it is
// also the only one that does not double the storage §39 bills for.
func (w *Writer) record(ctx context.Context, g *group, object blobstore.Object, rows uint64) error {
	if !w.db.Ready() {
		return pgdb.ErrNotStarted
	}
	orgUID, err := uuid.Parse(g.orgUID)
	if err != nil {
		return fmt.Errorf("parse org id: %w", err)
	}
	projectUID, err := uuid.Parse(g.projectUID)
	if err != nil {
		return fmt.Errorf("parse project id: %w", err)
	}
	envUID, err := uuid.Parse(g.envUID)
	if err != nil {
		return fmt.Errorf("parse environment id: %w", err)
	}

	// Computed from the retention tier on the message rather than read from the control
	// plane at deletion time, so lowering a customer's retention does not retroactively
	// destroy an archive they already paid for (§59, §60).
	deleteAfter := g.maxTime.UTC().AddDate(0, 0, int(g.retentionTier))

	return w.db.Client.ArchiveManifest.Create().
		SetBucket(w.store.Bucket()).
		SetObjectPath(object.Path).
		SetOrgUID(orgUID).
		SetProjectUID(projectUID).
		SetEnvironmentUID(envUID).
		SetMinEventTime(g.minTime.UTC()).
		SetMaxEventTime(g.maxTime.UTC()).
		SetEventCount(rows).
		SetObjectSizeBytes(object.Size).
		SetNormalizedInputBytes(g.inputBytes).
		SetSchemaVersion(SchemaVersion).
		SetDeleteAfter(deleteAfter).
		SetStatus(archivemanifest.StatusDurable).
		OnConflictColumns(archivemanifest.FieldBucket, archivemanifest.FieldObjectPath).
		DoNothing().
		Exec(ctx)
}

// Encode writes the rows as compressed Parquet (§10.4).
//
// Zstd because it is the best ratio per CPU second among the codecs every Parquet reader
// understands, and the archive is written once and read rarely — the asymmetry that makes
// compression worth paying for.
//
// Exported because the writer is no longer the only thing that produces an archive object:
// §61's erasure rewrites one without the rows belonging to an erased actor, and a rewrite
// that encoded differently from the original would be a second definition of the archive
// format.
func Encode(rows []Row) ([]byte, error) {
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[Row](&buf, parquet.Compression(&zstd.Codec{}))
	if _, err := w.Write(rows); err != nil {
		return nil, err
	}
	// Close writes the footer. A Parquet file without one is not a Parquet file, so the
	// error here is the one that decides whether the object is readable at all.
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode reads an object back into rows.
//
// The whole object at once, which is what the erasure path wants: it rewrites the object
// without some of its rows, so it needs all of them. A reader that walked row groups would
// be the right shape for §34's cold queries and the wrong one here.
func Decode(body []byte) ([]Row, error) {
	rows, err := parquet.Read[Row](bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("read parquet: %w", err)
	}
	return rows, nil
}

// objectPath is §14.1's convention.
//
// Hive-style `key=value` segments, so the prefix is both readable and prunable: §34's cold
// queries select objects by walking it, §60's expiry and §61's project deletion act on it,
// and a lifecycle rule can match it.
//
// §14.1's own example opens the prefix with `region=eu-1`. There is one bucket and one
// deployment now, so that segment named a constant and is gone: the organisation is the
// first thing that actually distinguishes an object.
func objectPath(g *group, digest string) string {
	return fmt.Sprintf("org=%s/project=%s/env=%s/dt=%s/hour=%02d/part-%s.parquet",
		g.orgUID, g.projectUID, g.env,
		g.hour.Format(time.DateOnly), g.hour.Hour(), digest)
}

// digest names an object from what is inside it.
//
// Deterministic on purpose: delivery is at-least-once (§21), so the same content must
// produce the same path or a redelivery writes a second copy of events that are already
// archived. Derived from the batches present rather than from a clock or a random id, both
// of which would make every redelivery a new object.
//
// A *partial* redelivery is a different set and therefore a different object, which is the
// residual this does not close: the two objects overlap, the manifests make the overlap
// visible as intersecting time ranges, and a reader deduplicates on event_id. That is
// §21's own position — "design the observable result to be idempotent" rather than claim
// exactly-once.
func digest(dedupKeys []string, rows int, firstEvent string) string {
	keys := slices.Clone(dedupKeys)
	slices.Sort(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	// The chunk boundary too, so splitting a large group produces distinct objects that are
	// still stable across a redelivery of the same group.
	fmt.Fprintf(h, "|%d|%s", rows, firstEvent)
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// environmentName falls back to the UUID's stead rather than producing `env=` with nothing
// after it, which would collapse two environments into one prefix.
func environmentName(name string) string {
	if name == "" {
		return "unknown"
	}
	return name
}

// rowFrom flattens one event into an archive row.
func rowFrom(msg *eventbus.Message, batch *ingest.NormalizedBatch, ev *ingest.NormalizedEvent) Row {
	installID := ev.InstallID
	if installID == "" {
		installID = batch.InstallID
	}
	return Row{
		OrgSeq:         msg.OrgSeq,
		ProjectSeq:     msg.ProjectSeq,
		EnvironmentSeq: uint32(msg.EnvironmentSeq),

		OrgID:         msg.OrgID,
		ProjectID:     msg.ProjectID,
		EnvironmentID: msg.EnvironmentID,

		RetentionTier: uint32(msg.RetentionTier),
		PolicyTier:    msg.PolicyTier,

		EventTime:       ev.EventTime.UTC(),
		IngestTime:      batch.IngestTime.UTC(),
		ReceivedAt:      batch.ReceivedAt.UTC(),
		ClientEventTime: ev.ClientEventTime,

		EventID:   ev.EventID,
		BatchID:   batch.BatchID,
		SessionID: ev.SessionID,
		PlayerID:  ev.PlayerID,
		InstallID: installID,
		Sequence:  ev.Sequence,

		EventName: ev.EventName,
		EventType: ev.EventType,

		Platform:    batch.Platform,
		CountryCode: batch.CountryCode,
		AppVersion:  batch.AppVersion,
		SDKName:     batch.SDKName,
		SDKVersion:  batch.SDKVersion,

		ConsentAnalytics:     uint32(batch.ConsentAnalytics),
		ConsentPolicyVersion: batch.ConsentPolicyVersion,
		ATTStatus:            batch.ATTStatus,

		PropsString: ev.PropsString,
		PropsInt:    ev.PropsInt,
		PropsFloat:  ev.PropsFloat,
		PropsBool:   boolProps(ev.PropsBool),

		RevenueMicros: ev.RevenueMicros,
		Currency:      ev.Currency,
		PayloadBytes:  ev.PayloadBytes,
	}
}

func boolProps(in map[string]uint8) map[string]uint32 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]uint32, len(in))
	for k, v := range in {
		out[k] = uint32(v)
	}
	return out
}
