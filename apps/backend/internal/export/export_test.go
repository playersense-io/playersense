package export_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"playersense.dev/backend/ent/regional"
	"playersense.dev/backend/ent/regional/dataexportrequest"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/export"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
)

// §101's access and portability right, against the real stack.
//
// The properties worth testing are the ones a portability answer is judged on: that it holds
// the person who was asked about and nobody else, that it is recognisable as complete, and
// that the copy it creates does not outlive its own deadline.

const (
	subject   = "player-subject"
	bystander = "player-bystander"
)

type fixture struct {
	exporter *export.Exporter
	cell     *pgdb.Cell
	db       *sql.DB
	store    *blobstore.Local

	project    uint32
	projectUID uuid.UUID
	orgUID     uuid.UUID
	envUID     uuid.UUID
}

type pool struct{ db *sql.DB }

func (p pool) DB() (*sql.DB, error) { return p.db, nil }

func newFixture(t *testing.T) *fixture {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.ArchiveDir = t.TempDir()

	cell := testsupport.CellDB(t, settings)

	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		t.Fatal(err)
	}
	db := clickhouse.OpenDB(opts)
	if err := db.Ping(); err != nil {
		t.Skipf("no clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := blobstore.NewLocal(settings.ArchiveDir, settings.ArchiveBucket)
	if err := store.Start(t.Context()); err != nil {
		t.Fatalf("start store: %v", err)
	}

	f := &fixture{
		cell: cell, db: db, store: store,
		project:    uint32(900_000_000 + time.Now().UnixNano()%50_000_000),
		projectUID: uuid.New(),
		orgUID:     uuid.New(),
		envUID:     uuid.New(),
	}
	f.exporter = &export.Exporter{
		Cell: cell, CH: pool{db}, Store: store, Log: quiet(),
	}
	return f
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (f *fixture) insertEvent(t *testing.T, player string, at time.Time, level int64) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), `
		INSERT INTO events_raw
		(org_seq, project_seq, environment_seq, org_id, project_id, environment_id,
		 retention_tier, policy_tier, event_time, ingest_time, received_at,
		 event_id, batch_id, session_id, player_id, install_id,
		 event_name, event_type, platform, country_code, app_version, sdk_name, sdk_version,
		 cohort_date, install_platform, install_country, first_app_version,
		 consent_analytics, consent_policy_version, att_status,
		 props_int, revenue_micros, currency, playtime_ms, payload_bytes)
		VALUES (1, ?, 1, toUUID(?), toUUID(?), toUUID(?),
		        30, 'standard', ?, ?, ?,
		        generateUUIDv4(), generateUUIDv4(), 'session-1', ?, generateUUIDv4(),
		        'level_completed', 'custom', 'ios', 'TR', '2.7.1', 'unity', '1.4.0',
		        toDate(?), 'ios', 'TR', '2.7.1',
		        1, '2026-01-15', 'authorized',
		        map('level', ?), 0, '', 1000, 200)`,
		f.project, f.orgUID.String(), f.projectUID.String(), f.envUID.String(),
		at, at, at, player, at, level); err != nil {
		t.Fatalf("seed event for %s: %v", player, err)
	}
}

func (f *fixture) request(t *testing.T, actorKey string) *regional.DataExportRequest {
	t.Helper()
	req, err := f.cell.Client.DataExportRequest.Create().
		SetProjectUID(f.projectUID).
		SetProjectSeq(f.project).
		SetActorKey(actorKey).
		Save(t.Context())
	if err != nil {
		t.Fatalf("record export request: %v", err)
	}
	t.Cleanup(func() {
		_ = f.cell.Client.DataExportRequest.DeleteOneID(req.ID).Exec(context.Background())
	})
	return req
}

func (f *fixture) reload(t *testing.T, req *regional.DataExportRequest) *regional.DataExportRequest {
	t.Helper()
	got, err := f.cell.Client.DataExportRequest.Get(t.Context(), req.ID)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	return got
}

// lines reads the bundle back as the studio would.
func (f *fixture) lines(t *testing.T, path string) []map[string]any {
	t.Helper()
	body, err := f.store.Get(t.Context(), path)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var out []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("the bundle is not newline-delimited JSON: %v", err)
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	return out
}

func sections(lines []map[string]any, name string) []map[string]any {
	var out []map[string]any
	for _, line := range lines {
		if line["section"] == name {
			out = append(out, line)
		}
	}
	return out
}

// TestAnExportHoldsTheSubjectAndNobodyElse is the disclosure rule.
//
// An export that over-answered would hand the requester somebody else's events, which is a
// worse failure than an incomplete answer and the reason §101's resolution reads the
// identifier narrowly where §61's reads it widely.
func TestAnExportHoldsTheSubjectAndNobodyElse(t *testing.T) {
	f := newFixture(t)
	at := time.Now().UTC().Add(-time.Hour)

	f.insertEvent(t, subject, at, 3)
	f.insertEvent(t, subject, at.Add(time.Minute), 4)
	f.insertEvent(t, bystander, at, 9)
	req := f.request(t, subject)

	got, err := f.exporter.Run(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got.Completed == 0 {
		t.Fatalf("completed %d exports, want at least this one", got.Completed)
	}

	done := f.reload(t, req)
	if done.Status != dataexportrequest.StatusCompleted {
		t.Fatalf("status = %s, want completed (%s)", done.Status, done.FailureReason)
	}
	if done.RowCount != 2 {
		t.Errorf("row_count = %d, want 2", done.RowCount)
	}
	if done.ExpiresAt == nil {
		t.Error("the bundle has no expiry; a second copy of a person's history must have one")
	}

	lines := f.lines(t, done.ObjectPath)
	events := sections(lines, "event")
	if len(events) != 2 {
		t.Fatalf("the bundle holds %d events, want 2", len(events))
	}
	for _, line := range events {
		event, _ := line["event"].(map[string]any)
		if event["player_id"] != subject {
			t.Errorf("the bundle holds an event for %v; it must hold nobody but the subject",
				event["player_id"])
		}
	}

	// The properties are exported as nested JSON rather than as a string holding JSON, so
	// the recipient does not have to parse the answer twice.
	first, _ := events[0]["event"].(map[string]any)
	props, ok := first["props_int"].(map[string]any)
	if !ok || props["level"] == nil {
		t.Errorf("props_int = %v, want the event's own properties", first["props_int"])
	}

	// Device and daily records travel with the events: both are keyed on a device and hold
	// what that person did, which is §59's test for player-level data.
	if len(sections(lines, "device")) == 0 {
		t.Error("no device record; the export holds less than the system does")
	}
	if len(sections(lines, "activity_day")) == 0 {
		t.Error("no daily activity record")
	}
}

// TestABundleSaysWhatItIsAndWhereItStops covers the two lines that bracket the answer.
//
// The first carries the caveats, because a file handed to a person is read where the
// documentation is not. The last is a summary, which makes a truncated bundle recognisable
// as one rather than a quietly short answer.
func TestABundleSaysWhatItIsAndWhereItStops(t *testing.T) {
	f := newFixture(t)
	f.insertEvent(t, subject, time.Now().UTC().Add(-time.Hour), 1)
	req := f.request(t, subject)

	if _, err := f.exporter.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("export: %v", err)
	}
	lines := f.lines(t, f.reload(t, req).ObjectPath)
	if len(lines) < 2 {
		t.Fatalf("the bundle has %d lines", len(lines))
	}

	if lines[0]["section"] != "meta" {
		t.Errorf("the first line is %v, want meta", lines[0]["section"])
	}
	meta, _ := lines[0]["meta"].(map[string]any)
	if meta["actor_key"] != subject {
		t.Errorf("meta names %v, want the subject", meta["actor_key"])
	}
	if limits, _ := meta["limits"].([]any); len(limits) == 0 {
		t.Error("the bundle carries no caveats; §05 asks for them to be said plainly")
	}

	last := lines[len(lines)-1]
	if last["section"] != "summary" {
		t.Fatalf("the last line is %v, want summary — it is what makes a truncated bundle visible",
			last["section"])
	}
	summary, _ := last["summary"].(map[string]any)
	if rows, _ := summary["rows"].(float64); int(rows) != len(lines) {
		t.Errorf("the summary counts %v rows and the file holds %d", summary["rows"], len(lines))
	}
}

// TestAnEmptyAnswerIsStillAnAnswer is §05's "zero is a real answer".
//
// A studio asking about an identifier PlayerSense holds nothing under must be told that,
// rather than handed a failure that reads as "we could not check".
func TestAnEmptyAnswerIsStillAnAnswer(t *testing.T) {
	f := newFixture(t)
	req := f.request(t, "nobody-by-this-name")

	if _, err := f.exporter.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("export: %v", err)
	}
	done := f.reload(t, req)
	if done.Status != dataexportrequest.StatusCompleted {
		t.Fatalf("status = %s, want completed (%s)", done.Status, done.FailureReason)
	}
	if done.RowCount != 0 {
		t.Errorf("row_count = %d, want 0", done.RowCount)
	}
	if len(sections(f.lines(t, done.ObjectPath), "meta")) != 1 {
		t.Error("an empty bundle still has to say what question it answers")
	}
}

// TestABundleIsRemovedWhenItsTermIsUp is the other half of writing one.
func TestABundleIsRemovedWhenItsTermIsUp(t *testing.T) {
	f := newFixture(t)
	f.exporter.TTL = time.Nanosecond

	f.insertEvent(t, subject, time.Now().UTC().Add(-time.Hour), 1)
	req := f.request(t, subject)

	if _, err := f.exporter.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("export: %v", err)
	}
	done := f.reload(t, req)
	path := done.ObjectPath

	// A later pass finds it expired.
	if _, err := f.exporter.Run(t.Context(), time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := f.reload(t, req).Status; got != dataexportrequest.StatusExpired {
		t.Errorf("status = %s, want expired", got)
	}
	if _, err := f.store.Get(t.Context(), path); err == nil {
		t.Error("the bundle is still in the bucket after its term")
	}
}

// TestASubjectLargerThanOneBundleIsRefused is §05's refusal to over-claim.
//
// A truncated file presented as a complete answer is the one outcome the whole right is
// supposed to prevent, so the bound produces a failure with a number in it rather than a
// shorter bundle.
func TestASubjectLargerThanOneBundleIsRefused(t *testing.T) {
	f := newFixture(t)
	f.exporter.MaxRows = 1

	at := time.Now().UTC().Add(-time.Hour)
	f.insertEvent(t, subject, at, 1)
	f.insertEvent(t, subject, at.Add(time.Minute), 2)
	req := f.request(t, subject)

	if _, err := f.exporter.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("export: %v", err)
	}
	done := f.reload(t, req)
	if done.Status != dataexportrequest.StatusFailed {
		t.Fatalf("status = %s, want failed", done.Status)
	}
	if done.FailureReason == "" {
		t.Error("the refusal does not say what to do about it")
	}
	if done.ObjectPath != "" {
		t.Error("a refused export wrote a bundle anyway")
	}
}
