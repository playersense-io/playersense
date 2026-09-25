// Package metering turns analytics into the numbers a bill is made of.
//
// §40 is explicit about the direction: **PlayerSense, not Stripe, is the source of truth for
// usage.** A worker derives a day from the analytics tables, a month close sums it, and Stripe is
// told the total. Nothing sends a meter event per analytics event — a billion events a day would be
// a billion meter calls to a service that charges for them.
//
// # It reads the same fact the dashboard reads
//
// `metrics_daily`, not `events_raw`. §38 asks for exactly this and says why: "the billing meter and
// the cohort tier are reading the same fact. Deriving the two independently would eventually produce
// a month where the invoice and the dashboard disagree, and no amount of investigation would
// establish which was right."
//
// So the meter is one INSERT..SELECT away from the table the retention curve came from, and a studio
// comparing their usage page against their DAU chart is comparing two readings of one number.
//
// # A day is rewritten until it is final
//
// Late events land: §19.2 clamps a timestamp up to seven days old, so yesterday's number can still
// change tomorrow. The meter therefore re-runs over a trailing window and upserts, and a day is
// marked `final` only once it is old enough that nothing can move it. A closed month is
// reproducible because it is made of final days.
package metering

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/usagedaily"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/logs"
	"playersense.dev/backend/internal/pgdb"
)

// SettleAfter is how long a day stays open to correction.
//
// Eight days: §19.2's seven-day clamp plus one. An event older than the clamp has its timestamp
// rewritten to now, so it lands on a later day and can no longer change an earlier one — which makes
// seven days the point after which a day is arithmetically finished, and the extra day is the margin
// for a writer that was behind.
const SettleAfter = 8 * 24 * time.Hour

// Window is how many days back a pass re-reads.
//
// The same span, because there is no point re-reading a day nothing can change. A pass is one
// grouped scan per day over a table partitioned by month, so the cost of the window is small and
// the cost of getting it wrong is an invoice that is quietly short.
const Window = 8

// Meter writes one project's usage for one day.
//
// Two stores, and each is the only one that can answer its part. ClickHouse holds the activity;
// PostgreSQL holds the archive manifests §39 measures storage from, and is where the usage row
// is written.
type Meter struct {
	DB  *pgdb.DB
	CH  *chdb.SQL
	Log *slog.Logger

	// Logs reads Cloud Logging's two meters for the day (ADR 0050): the log bytes each project
	// held when the day ended and took in during it. Nil writes neither.
	Logs interface {
		DB() (driver.Conn, error)
	}

	// Now is overridable so a test can decide which days are settled.
	Now func() time.Time
}

// Result is what a pass did.
type Result struct {
	Days     int
	Rows     int
	Finalled int
}

// Run meters every day in the trailing window that is not already final.
//
// Idempotent, and not merely safe to repeat: repeating it is how a day converges. The first pass over
// yesterday writes what had arrived by then, and the next seven correct it as late events land.
func (m *Meter) Run(ctx context.Context) (Result, error) {
	now := m.now()
	var out Result

	// Oldest first, so a failure part way through leaves the recent days unwritten rather than
	// the old ones — the recent days are the ones the next pass will reach anyway.
	for back := Window; back >= 1; back-- {
		day := now.AddDate(0, 0, -back).Truncate(24 * time.Hour)
		rows, err := m.MeterDay(ctx, day)
		if err != nil {
			return out, fmt.Errorf("meter %s: %w", day.Format(time.DateOnly), err)
		}
		out.Days++
		out.Rows += rows
		if now.Sub(day) >= SettleAfter {
			finalled, err := m.finalize(ctx, day)
			if err != nil {
				return out, fmt.Errorf("finalize %s: %w", day.Format(time.DateOnly), err)
			}
			out.Finalled += finalled
		}
	}
	return out, nil
}

// MeterDay writes one activity day for every project that had any.
//
// A project with no activity gets no row rather than a row of zeroes. The difference matters on the
// usage page: absent is "nothing happened", and a zero row is indistinguishable from it while costing
// a row per project per day forever.
func (m *Meter) MeterDay(ctx context.Context, day time.Time) (int, error) {
	activity, err := m.activity(ctx, day)
	if err != nil {
		return 0, err
	}
	logUsage, err := m.logUsage(ctx, day)
	if err != nil {
		return 0, err
	}
	// A project that only sent logs that day is metered too: it has a bill even with no players.
	for id := range logUsage {
		if _, found := activity[id]; !found {
			activity[id] = counts{}
		}
	}
	if len(activity) == 0 {
		return 0, nil
	}

	// Which organisation each project belongs to, and its uid for the storage query. One read
	// rather than one per project.
	projects, err := m.DB.Client.Project.Query().
		Where(project.IDIn(keysOf(activity)...)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("read projects: %w", err)
	}

	stored, err := m.storage(ctx, projects, day)
	if err != nil {
		return 0, err
	}

	written := 0
	for _, proj := range projects {
		counts := activity[proj.ID]
		// A day already marked final is not rewritten. It is the promise a closed month
		// rests on: a re-run must not move a number somebody has already been invoiced for.
		already, err := m.DB.Client.UsageDaily.Query().
			Where(usagedaily.ProjectID(proj.ID), usagedaily.Day(day), usagedaily.Final(true)).
			Exist(ctx)
		if err != nil {
			return written, fmt.Errorf("check whether %s is final: %w", day.Format(time.DateOnly), err)
		}
		if already {
			continue
		}

		err = m.DB.Client.UsageDaily.Create().
			SetProjectID(proj.ID).
			SetOrganizationID(proj.OrganizationID).
			SetDay(day).
			SetTrackedPlayers(counts.Players).
			SetEventsAccepted(counts.Events).
			SetRetainedBytes(stored[proj.ID]).
			SetLogBytes(logUsage[proj.ID].Kept).
			SetLogIngestedBytes(logUsage[proj.ID].Ingested).
			OnConflict().
			UpdateTrackedPlayers().
			UpdateEventsAccepted().
			UpdateRetainedBytes().
			UpdateLogBytes().
			UpdateLogIngestedBytes().
			Exec(ctx)
		if err != nil {
			return written, fmt.Errorf("write usage for project %d: %w", proj.ID, err)
		}
		written++
	}
	return written, nil
}

// logUsage reads Cloud Logging's meters for one day, by project, under each project's retention
// as it stands now.
func (m *Meter) logUsage(ctx context.Context, day time.Time) (map[int]logs.Usage, error) {
	if m.Logs == nil {
		return nil, nil
	}
	conn, err := m.Logs.DB()
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}
	projects, err := m.DB.Client.Project.Query().
		Select(project.FieldID, project.FieldLogRetentionDays).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read projects' log retention: %w", err)
	}
	retention := make(map[uint32]uint16, len(projects))
	for _, p := range projects {
		retention[uint32(p.ID)] = p.LogRetentionDays
	}
	usage, err := logs.DayUsage(ctx, conn, day.AddDate(0, 0, 1), func(seq uint32) (uint16, bool) {
		days, found := retention[seq]
		return days, found
	})
	if err != nil {
		return nil, err
	}
	out := make(map[int]logs.Usage, len(usage))
	for seq, u := range usage {
		if _, known := retention[seq]; known && (u.Kept > 0 || u.Ingested > 0) {
			out[int(seq)] = u
		}
	}
	return out, nil
}

// counts is one project's activity on one day.
type counts struct {
	Players uint64
	Events  uint64
}

// activity reads the day from the same table the dashboard's DAU comes from.
//
// Summed across environments, because `usage_daily` is keyed by project and has no environment
// column. That is a **commercial** decision the schema has already made — development and staging
// traffic counts toward the meter — and changing it is a WHERE clause here rather than a migration.
// It is called out in docs/compliance/10-open-items.md rather than left for somebody to discover in
// an invoice.
func (m *Meter) activity(ctx context.Context, day time.Time) (map[int]counts, error) {
	const q = `
		SELECT project_seq, sum(active_players) AS players, sum(events) AS events
		FROM metrics_daily
		WHERE activity_date = ?
		GROUP BY project_seq`

	db, err := m.CH.DB()
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}
	rows, err := db.QueryContext(ctx, q, day.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("read metrics_daily: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int]counts{}
	for rows.Next() {
		var (
			seq     uint32
			players uint64
			events  uint64
		)
		if err := rows.Scan(&seq, &players, &events); err != nil {
			return nil, fmt.Errorf("scan metrics_daily: %w", err)
		}
		out[int(seq)] = counts{Players: players, Events: events}
	}
	return out, rows.Err()
}

// storage reads §39's second meter: canonical compressed archive bytes still inside retention.
//
// The manifests rather than ClickHouse parts, and §39 says why — a shared part holds many
// organisations and replication overhead is our implementation detail, not the customer's. The
// manifest's own object sizes are objective and are what a customer can be told they are paying for.
//
// Keyed by `project_uid`, because a manifest names a project by uid and never by its row id —
// the writer knows the UUID the SDK sent and resolving it per object would be a lookup on the
// flush path.
func (m *Meter) storage(ctx context.Context, projects []*ent.Project, day time.Time) (map[int]uint64, error) {
	out := map[int]uint64{}
	if m.DB == nil {
		// A deployment with no database handle meters activity and not storage, rather
		// than refusing to meter at all. The column is then behind rather than wrong, and the
		// next pass fills it.
		return out, nil
	}

	byUID := make(map[string]int, len(projects))
	for _, proj := range projects {
		byUID[proj.UID.String()] = proj.ID
	}

	rows, err := m.DB.Client.ArchiveManifest.Query().
		Where(
			// Still retained as of the day being metered: an object deleted on the
			// twentieth was billable for the twenty days it existed (§60), so the
			// question is about `day` and never about now.
			archivemanifest.DeleteAfterGT(day),
			archivemanifest.CreatedAtLTE(day.AddDate(0, 0, 1)),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read archive manifests: %w", err)
	}
	for _, manifest := range rows {
		if id, found := byUID[manifest.ProjectUID.String()]; found {
			out[id] += manifest.ObjectSizeBytes
		}
	}
	return out, nil
}

// finalize marks a settled day untouchable.
//
// After this the day is not rewritten, which is what makes a closed month reproducible. Separate from
// the write so that the decision — "nothing can change this now" — is made by the clock in one place
// rather than inferred at every call site.
func (m *Meter) finalize(ctx context.Context, day time.Time) (int, error) {
	return m.DB.Client.UsageDaily.Update().
		Where(usagedaily.Day(day), usagedaily.Final(false)).
		SetFinal(true).
		Save(ctx)
}

func (m *Meter) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func keysOf(m map[int]counts) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
