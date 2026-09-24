package projects

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/apikey"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/testsupport"
)

func seedEvents(t *testing.T, ch *chdb.SQL, p testsupport.Project, days ...int) {
	t.Helper()
	db, err := ch.DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, ago := range days {
		at := time.Now().UTC().AddDate(0, 0, -ago)
		if _, err := db.ExecContext(context.Background(), `
			INSERT INTO events_raw (org_seq, project_seq, environment_seq, retention_tier, event_time, ingest_time,
				event_type, event_name, install_id, event_id, batch_id)
			VALUES (?, ?, ?, 365, ?, ?, 'custom', 'level_up', ?, ?, ?)`,
			uint32(p.OrgSeq), uint32(p.ProjectSeq), uint16(p.EnvironmentID), at, at,
			uuid.New(), uuid.New(), uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
}

func countEvents(t *testing.T, ch *chdb.SQL, p testsupport.Project) int {
	t.Helper()
	db, _ := ch.DB()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count() FROM events_raw WHERE project_seq = ?`, uint32(p.ProjectSeq)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stores(t *testing.T) (*testsupport.Project, *Retention, *Deletion, *chdb.SQL) {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	ch := chdb.NewSQL(settings.ClickHouseDSN)
	if err := ch.Start(t.Context()); err != nil {
		t.Skipf("no clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	p := testsupport.SeedProject(t, db, "standard")
	return &p, &Retention{DB: db, CH: ch}, &Deletion{DB: db, CH: ch, Cache: testsupport.Cache(t, settings)}, ch
}

// TestALoweredRetentionReachesRowsAlreadyWritten: once the key cache has settled, rows older than
// the new tier go, the newer stay, and the pass is not repeated within the day.
func TestALoweredRetentionReachesRowsAlreadyWritten(t *testing.T) {
	p, retention, _, ch := stores(t)
	seedEvents(t, ch, *p, 1, 20, 60)
	now := time.Now().UTC()
	if err := retention.DB.Client.Project.UpdateOneID(p.ProjectSeq).SetRetentionDays(30).
		SetRetentionChangedAt(now.Add(-time.Hour)).SetRetentionPurgeUntil(now.AddDate(0, 0, 90)).
		Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	done, err := retention.Run(t.Context(), now)
	if err != nil || done.Projects < 1 {
		t.Fatalf("enforced %d, %v", done.Projects, err)
	}
	if n := countEvents(t, ch, *p); n != 2 {
		t.Fatalf("%d rows left, want the two younger than 30 days", n)
	}
	row, _ := retention.DB.Client.Project.Get(t.Context(), p.ProjectSeq)
	if row.RetentionPurgedAt == nil {
		t.Fatal("the pass was not recorded")
	}
	if _, err := retention.Run(t.Context(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if row2, _ := retention.DB.Client.Project.Get(t.Context(), p.ProjectSeq); !row2.RetentionPurgedAt.Equal(*row.RetentionPurgedAt) {
		t.Fatal("enforced twice within a day")
	}
}

// TestADeletionClosesThenPurges: when due, the keys are revoked and the project hidden; an hour
// later its analytics go.
func TestADeletionClosesThenPurges(t *testing.T) {
	p, _, deletion, ch := stores(t)
	seedEvents(t, ch, *p, 1, 2)
	now := time.Now().UTC()
	if err := deletion.DB.Client.Project.UpdateOneID(p.ProjectSeq).SetDeletionAt(now.Add(-time.Minute)).
		Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := deletion.Run(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	row, _ := deletion.DB.Client.Project.Get(t.Context(), p.ProjectSeq)
	if row.DeletedAt == nil || row.DeletionPurgedAt != nil {
		t.Fatalf("after the first step %+v", row)
	}
	live, err := deletion.DB.Client.APIKey.Query().
		Where(apikey.HasEnvironmentWith(environment.ProjectID(p.ProjectSeq)), apikey.RevokedAtIsNil()).Count(t.Context())
	if err != nil || live != 0 {
		t.Fatalf("%d keys still live, %v", live, err)
	}
	if n := countEvents(t, ch, *p); n != 2 {
		t.Fatalf("%d rows before the purge delay, want both still there", n)
	}

	if _, err := deletion.Run(t.Context(), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	row, _ = deletion.DB.Client.Project.Get(t.Context(), p.ProjectSeq)
	if row.DeletionPurgedAt == nil {
		t.Fatal("not purged after the delay")
	}
	if n := countEvents(t, ch, *p); n != 0 {
		t.Fatalf("%d rows after the purge", n)
	}
}
