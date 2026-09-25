package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
)

// Writer consumes the logs topic and stores each record in ClickHouse.
//
// The same contract as the analytics writer (Appendix C.2): a delivery is acknowledged only once
// its rows are durable, and a payload that no retry could ever store is parked on the dead-letter
// topic rather than retried forever or dropped. It runs inside the analytics-writer process, on
// its own subscription: one always-on process rather than two is the cheaper deployment, and the
// subscriptions keep their positions apart so neither can hold the other back.
type Writer struct {
	Queue *Queue
	CH    *chdb.Conn
	Log   *slog.Logger

	// MaxRows bounds one INSERT. Rows rather than messages, as ADR 0023 argues for events: a
	// message is one record or a thousand.
	MaxRows int

	cancel context.CancelFunc
	done   chan struct{}

	messages     atomic.Int64
	rows         atomic.Int64
	deadLettered atomic.Int64
}

// Name identifies the component in start-up and shutdown logs.
func (w *Writer) Name() string { return "logs-writer" }

// Start subscribes. It dials nothing itself: the ClickHouse handle is its own lifecycle.
func (w *Writer) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan struct{})
	go func() {
		defer close(w.done)
		if err := w.Queue.Subscribe(ctx, GroupWriter, w.Handle); err != nil && ctx.Err() == nil {
			w.log().Error("the logs subscription stopped", "error", err)
		}
	}()
	return nil
}

// Stop waits for the delivery in flight.
func (w *Writer) Stop(ctx context.Context) error {
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	select {
	case <-w.done:
	case <-ctx.Done():
		return fmt.Errorf("the logs writer did not stop within the shutdown timeout")
	}
	w.log().Info("logs writer stopped", "messages", w.messages.Load(), "rows", w.rows.Load(),
		"dead_lettered", w.deadLettered.Load())
	return nil
}

func (w *Writer) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// row is one record, with the message it came on.
type row struct {
	msg    *eventbus.Message
	batch  *ingest.NormalizedLogBatch
	record *ingest.NormalizedLogRecord
	id     uuid.UUID
	inst   uuid.UUID
}

// Handle stores one delivery.
func (w *Writer) Handle(ctx context.Context, msgs []*eventbus.Message) error {
	var rows []row
	for _, msg := range msgs {
		batch, err := DecodeBatch(msg.Payload)
		if err != nil {
			if err := w.park(ctx, msg, "the payload is not a log batch: "+err.Error()); err != nil {
				return err
			}
			continue
		}
		install, err := uuid.Parse(batch.InstallID)
		if err != nil {
			if err := w.park(ctx, msg, "install_id is not a uuid"); err != nil {
				return err
			}
			continue
		}
		for i := range batch.Records {
			id, err := uuid.Parse(batch.Records[i].RecordID)
			if err != nil {
				continue
			}
			rows = append(rows, row{msg: msg, batch: batch, record: &batch.Records[i], id: id, inst: install})
		}
	}

	limit := w.MaxRows
	if limit <= 0 {
		limit = 50_000
	}
	for start, chunk := 0, 0; start < len(rows); start, chunk = start+limit, chunk+1 {
		end := min(start+limit, len(rows))
		if err := w.insert(ctx, rows[start:end], token(msgs, chunk)); err != nil {
			return err
		}
		w.rows.Add(int64(end - start))
	}
	w.messages.Add(int64(len(msgs)))
	return nil
}

// park dead-letters one message. Not durable, not acknowledged: an error here stalls the
// delivery, which is better than a batch that was neither stored nor parked.
func (w *Writer) park(ctx context.Context, msg *eventbus.Message, reason string) error {
	if err := w.Queue.DeadLetter(ctx, msg, eventbus.DeadLetterReason{
		Group: GroupWriter, Reason: reason, FailedAt: time.Now().UTC(), Attempts: 1,
	}); err != nil {
		return fmt.Errorf("dead-letter %s: %w", msg.DedupKey, err)
	}
	w.deadLettered.Add(1)
	w.log().Error("a log batch could not be stored and was dead-lettered",
		"dedup_key", msg.DedupKey, "project_id", msg.ProjectID, "reason", reason)
	return nil
}

// token names one insert so a redelivered one is discarded by ClickHouse rather than stored twice
// (§21): the delivery's batches and the chunk's place in it.
func token(msgs []*eventbus.Message, chunk int) string {
	sum := sha256.New()
	for _, m := range msgs {
		sum.Write([]byte(m.DedupKey))
		sum.Write([]byte{0})
	}
	sum.Write([]byte(strconv.Itoa(chunk)))
	return "logs-" + hex.EncodeToString(sum.Sum(nil)[:16])
}

const insertStatement = `INSERT INTO logs (
	org_seq, project_seq, environment_seq, retention_days,
	timestamp, received_at, record_id, severity, kind, logger,
	message, exception_type, exception_value, stacktrace, breadcrumbs, handled, culprit,
	fingerprint, session_id, install_id, player_id,
	app_version, app_build, platform, os_version, device_model, country_code, sdk_name, sdk_version,
	attributes, size
)`

func (w *Writer) insert(ctx context.Context, rows []row, token string) error {
	conn, err := w.CH.DB()
	if err != nil {
		return err
	}
	// Synchronous, with a token, for the reasons the analytics writer gives: the block is already
	// assembled here, and a token is what makes a redelivery land once.
	ctx = clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"insert_deduplication_token": token,
	}))
	block, err := conn.PrepareBatch(ctx, insertStatement)
	if err != nil {
		return fmt.Errorf("prepare logs insert: %w", err)
	}
	for _, r := range rows {
		rec, b, m := r.record, r.batch, r.msg
		var player *string
		if rec.PlayerID != "" {
			player = &rec.PlayerID
		}
		attributes := rec.Attributes
		if attributes == nil {
			attributes = map[string]string{}
		}
		if err := block.Append(
			m.OrgSeq, m.ProjectSeq, m.EnvironmentSeq, retentionOf(m),
			rec.Time, b.ReceivedAt, r.id, rec.Severity, rec.Kind, rec.Logger,
			rec.Message, rec.ExceptionType, rec.ExceptionValue, jsonOrEmpty(rec.Frames), jsonOrEmpty(rec.Breadcrumbs),
			boolByte(rec.Handled), Culprit(rec),
			Fingerprint(rec), rec.SessionID, r.inst, player,
			b.AppVersion, b.AppBuild, b.Platform, b.OSVersion, b.DeviceModel, b.CountryCode, b.SDKName, b.SDKVersion,
			attributes, rec.Size,
		); err != nil {
			return fmt.Errorf("append log row: %w", err)
		}
	}
	if err := block.Send(); err != nil {
		return fmt.Errorf("insert logs: %w", err)
	}
	return nil
}

// retentionOf is the retention the gateway stamped, or the default for a message published
// before it stamped one.
func retentionOf(m *eventbus.Message) uint16 {
	if m.RetentionTier == 0 {
		return 14
	}
	return m.RetentionTier
}

func jsonOrEmpty[T any](items []T) string {
	if len(items) == 0 {
		return ""
	}
	out, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(out)
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
