package cohort_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"playersense.dev/backend/internal/cohort"
	"playersense.dev/backend/internal/config"
)

// The cohort-key backfill is §65's other half: "losing Valkey costs speed rather than data".
//
// Without it the promise is false in a way that points the wrong direction. An unstamped
// device-day is dropped by the derivation, while `cohort_sizes` — derived from
// player_lifetime, which never needed Valkey — still counts the player. The numerator loses
// them and the denominator keeps them, so retention reads **low** and nothing errors.

func open(t *testing.T) *sql.DB {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.Load(filepath.Join(root, ".env"))
	if err != nil {
		t.Skipf("no local environment: %v", err)
	}
	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		t.Fatal(err)
	}
	db := clickhouse.OpenDB(opts)
	if err := db.Ping(); err != nil {
		t.Skipf("no clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// uniqueProject returns a tenant surrogate no previous run has used.
//
// Not tidiness. `ALTER TABLE ... DELETE` on a ReplicatedMergeTree leaves a mutation whose
// predicate keeps being applied to parts inserted *afterwards*, so a fixed project_seq that
// a previous run cleaned up is a project_seq whose rows silently vanish on the next run —
// the insert succeeds, the part is written with zero rows, and the test reports a fixture
// that "was never seeded". Measured: eight accumulated mutations on this table, several of
// them the same DELETE.
//
// The same property is worth knowing for §61's purge: a mutation that keeps deleting is
// exactly what an erasure wants, and it means mutations accumulate one per request.
func uniqueProject() uint32 {
	return uint32(900_000_000 + time.Now().UnixNano()%50_000_000)
}

// seedUnstamped writes one device whose arrival is known to player_lifetime but whose
// activity rows carry the 1970 sentinel — exactly what a Valkey outage leaves behind.
func seedUnstamped(t *testing.T, db *sql.DB, project uint32, arrival time.Time) {
	t.Helper()
	ctx := context.Background()

	for _, offset := range []int{0, 1, 3} {
		day := arrival.AddDate(0, 0, offset)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO player_activity_daily
			(project_seq, environment_seq, activity_date, device_id, cohort_date,
			 install_platform, install_country, first_app_version, acquisition_source,
			 sessions, playtime_ms, events, revenue_micros, purchases)
			VALUES (?, 1, ?, 42, toDate(0), '', '', '', '', 1, 1000, 5, 0, 0)`,
			project, day); err != nil {
			t.Fatalf("seed activity: %v", err)
		}
	}

	// The arrival fact, which is what the repair reads. first_seen is min(ingest_time) and
	// is independent of Valkey, which is the whole reason it can answer.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO player_lifetime
		(project_seq, environment_seq, device_id, device_key, first_seen, last_seen,
		 first_attrs, first_source, playtime_ms, revenue_micros, purchases, active_days)
		SELECT ?, 1, 42, 'device-42', ?, ?,
		       argMinState(tuple('ios','TR','1.0'),
		                   (toDateTime64(?, 3, 'UTC'), generateUUIDv4())),
		       argMinIfState('organic',
		                     (toDateTime64(?, 3, 'UTC'), generateUUIDv4()), 1),
		       3000, 0, 0, uniqState(toDate(?))`,
		project, arrival, arrival.AddDate(0, 0, 3), arrival, arrival, arrival); err != nil {
		t.Fatalf("seed lifetime: %v", err)
	}
}

func unstampedRows(t *testing.T, db *sql.DB, project uint32) int {
	t.Helper()
	var n uint64
	if err := db.QueryRow(`
		SELECT count() FROM player_activity_daily
		WHERE project_seq = ? AND cohort_date = toDate(0)`, project).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return int(n)
}

func TestTheBackfillResolvesAnUnstampedDeviceDay(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	project := uniqueProject()
	arrival := time.Now().UTC().AddDate(0, 0, -5).Truncate(24 * time.Hour)
	month := cohort.MonthOf(arrival)

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx,
			"ALTER TABLE player_activity_daily DELETE WHERE project_seq = ? SETTINGS mutations_sync=2", project)
		_, _ = db.ExecContext(ctx,
			"ALTER TABLE player_lifetime DELETE WHERE project_seq = ? SETTINGS mutations_sync=2", project)
	})

	seedUnstamped(t, db, project, arrival)

	if got := unstampedRows(t, db, project); got != 3 {
		t.Fatalf("seeded %d unstamped device-days, want 3", got)
	}

	// Before the repair the derivation would drop all three, because every statement in it
	// filters `cohort_date != toDate(0)`.
	var visible uint64
	if err := db.QueryRow(`
		SELECT count() FROM player_activity_daily
		WHERE project_seq = ? AND cohort_date != toDate(0)`, project).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("%d device-days were already visible; the fixture is wrong", visible)
	}

	backfill := &cohort.Backfill{Deriver: &cohort.Deriver{DB: db}}
	result, err := backfill.Run(ctx, month)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// At least, rather than exactly: a run is scoped to a month and not to a tenant, which
	// is what §65's repair has to be. Another package's test running concurrently — the
	// writer's `TestRowsAreStoredWithValkeyDown` produces an unstamped device-day on
	// purpose — lands in the same month and is counted here too. The claim this test owns
	// is about its own three rows, and it is made against the table below.
	if result.DeviceDays < 3 {
		t.Errorf("the backfill saw %d device-days, want at least the 3 seeded here", result.DeviceDays)
	}
	if result.Cohorts < 1 {
		t.Errorf("the backfill repaired %d cohorts, want at least this one", result.Cohorts)
	}
	if got := unstampedRows(t, db, project); got != 0 {
		t.Errorf("%d of this project's device-days are still unstamped", got)
	}

	// The repaired value must be the arrival day the *server* assigned (§29.4), not the
	// activity day and not today.
	var repaired time.Time
	if err := db.QueryRow(`
		SELECT DISTINCT cohort_date FROM player_activity_daily
		WHERE project_seq = ? AND cohort_date != toDate(0)`, project).Scan(&repaired); err != nil {
		t.Fatalf("read the repaired cohort: %v", err)
	}
	if want := arrival.Format(time.DateOnly); repaired.Format(time.DateOnly) != want {
		t.Errorf("repaired cohort_date is %s, want %s", repaired.Format(time.DateOnly), want)
	}

	// Idempotent: a second run leaves this project exactly where the first one did. Asserted
	// against the table rather than against the result, for the reason above — the result
	// counts every tenant in the month.
	if _, err := backfill.Run(ctx, month); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var stillRepaired time.Time
	if err := db.QueryRow(`
		SELECT DISTINCT cohort_date FROM player_activity_daily
		WHERE project_seq = ? AND cohort_date != toDate(0)`, project).Scan(&stillRepaired); err != nil {
		t.Fatalf("read the cohort after the second run: %v", err)
	}
	if !stillRepaired.Equal(repaired) {
		t.Errorf("a second run moved the cohort from %s to %s",
			repaired.Format(time.DateOnly), stillRepaired.Format(time.DateOnly))
	}
}

// A device with no arrival fact cannot be resolved, and that must be reported rather than
// looking like success. It happens when the outage covered the device's first sighting too,
// so player_lifetime never learned about it either.
func TestAnUnresolvableDeviceIsCountedRatherThanHidden(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	project := uniqueProject()
	arrival := time.Now().UTC().AddDate(0, 0, -5).Truncate(24 * time.Hour)

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx,
			"ALTER TABLE player_activity_daily DELETE WHERE project_seq = ? SETTINGS mutations_sync=2", project)
	})

	// Activity with no matching player_lifetime row.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO player_activity_daily
		(project_seq, environment_seq, activity_date, device_id, cohort_date,
		 install_platform, install_country, first_app_version, acquisition_source,
		 sessions, playtime_ms, events, revenue_micros, purchases)
		VALUES (?, 1, ?, 77, toDate(0), '', '', '', '', 1, 1000, 5, 0, 0)`,
		project, arrival); err != nil {
		t.Fatalf("seed: %v", err)
	}

	backfill := &cohort.Backfill{Deriver: &cohort.Deriver{DB: db}}
	result, err := backfill.Run(ctx, cohort.MonthOf(arrival))
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if result.DeviceDays != 1 {
		t.Fatalf("saw %d device-days, want 1", result.DeviceDays)
	}
	if result.Remaining != 1 {
		t.Errorf("Remaining = %d, want 1 — an unresolvable device must be reported, not "+
			"silently counted as repaired", result.Remaining)
	}
}

// The prune must forget a dormant device and keep an active one, and the decision must be
// made on the *merged* last_seen rather than on a stored partial row.
//
// This is why it is a job and not a TTL. player_lifetime is an AggregatingMergeTree, so
// until parts merge one device exists as several rows each carrying a partial maximum. A
// row-level TTL evaluates those partials, so an active player's older fragment can look
// stale and be expired — taking its playtime and revenue with it and silently understating
// the lifetime totals §29.11's buckets are computed from. Measured on the development stack
// while writing this: 171 stored rows for 170 devices.
func TestThePruneForgetsDormantDevicesAndKeepsActiveOnes(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	project := uniqueProject()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx,
			"ALTER TABLE player_lifetime DELETE WHERE project_seq = ? SETTINGS mutations_sync=2", project)
	})

	long := time.Now().UTC().AddDate(0, 0, -900) // dormant well past any sane window
	recent := time.Now().UTC().AddDate(0, 0, -2)

	// Two devices, and the dormant one is written as *two* partial rows whose maxima
	// differ — the shape that defeats a row-level TTL.
	seedLifetime(t, db, project, 1, long.AddDate(0, 0, -30), long)
	seedLifetime(t, db, project, 2, recent.AddDate(0, 0, -400), recent.AddDate(0, 0, -300))
	seedLifetime(t, db, project, 2, recent.AddDate(0, 0, -400), recent) // the newer partial

	prune := &cohort.Prune{
		Deriver: &cohort.Deriver{DB: db},
		After:   550 * 24 * time.Hour,
	}
	result, err := prune.Run(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if result.Deleted == 0 {
		t.Fatal("the prune deleted nothing")
	}

	var survivors []uint64
	rows, err := db.QueryContext(ctx,
		"SELECT device_id FROM player_lifetime_v WHERE project_seq = ? ORDER BY device_id", project)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var d uint64
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		survivors = append(survivors, d)
	}

	if len(survivors) != 1 || survivors[0] != 2 {
		t.Fatalf("survivors = %v, want only device 2 — device 1 is dormant and device 2 is "+
			"active once its partial rows are merged", survivors)
	}
}

func seedLifetime(t *testing.T, db *sql.DB, project uint32, device uint64, first, last time.Time) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO player_lifetime
		(project_seq, environment_seq, device_id, device_key, first_seen, last_seen,
		 first_attrs, first_source, playtime_ms, revenue_micros, purchases, active_days)
		SELECT ?, 1, ?, 'd', ?, ?,
		       argMinState(tuple('ios','TR','1.0'), (toDateTime64(?, 3, 'UTC'), generateUUIDv4())),
		       argMinIfState('organic', (toDateTime64(?, 3, 'UTC'), generateUUIDv4()), 1),
		       1000, 0, 0, uniqState(toDate(?))`,
		project, device, first, last, first, first, first); err != nil {
		t.Fatalf("seed lifetime: %v", err)
	}
}
