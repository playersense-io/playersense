// Command archivecheck verifies Appendix C.6 against the live stack.
//
//	Local ClickHouse data is not expired until archive durability is confirmed when
//	archive is required.
//
// §66 states the same rule less gently: **never expire the only remaining raw copy of an
// event.**
//
// Nothing enforces that ordering, and nothing can. `events_raw` carries
// `TTL expire_at DELETE` with `ttl_only_drop_parts = 1`, which is a declarative instruction
// to a background merge — there is no hook, no gate and no way to make a partition drop wait
// for an object in a bucket. The invariant is therefore held by *margin*: a day's events are
// archived within minutes and expire after the project's retention tier, so the gap is weeks.
//
// A margin nobody measures is a margin nobody has. This measures it: for every
// (project, environment, day) still in ClickHouse, does a manifest cover it, and how long
// until the TTL takes it?
//
//	go run ./apps/backend/cmd/archivecheck
//	go run ./apps/backend/cmd/archivecheck -warn 14
//
// It exits non-zero when a day would expire inside the warning window with no archive, which
// is the condition that turns a lagging archive into permanent loss.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/ent/regional/archivemanifest"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/pgdb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "archivecheck:", err)
		os.Exit(1)
	}
}

// day is one (project, environment, date) still held locally.
type day struct {
	projectUID uuid.UUID
	envUID     uuid.UUID
	date       time.Time

	rows          uint64
	minEventTime  time.Time
	maxEventTime  time.Time
	retentionTier uint16
}

// expiresAt is when ClickHouse will drop this day's last row.
//
// Derived the same way the table does — `event_time + retention_tier` — so the number here
// and the number the TTL acts on cannot drift apart.
func (d day) expiresAt() time.Time {
	return d.maxEventTime.AddDate(0, 0, int(d.retentionTier))
}

func run() error {
	warn := flag.Int("warn", 7, "days of remaining margin below which an unarchived day is an error")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		return err
	}
	settings, err := config.Load(filepath.Join(root, ".env"))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		return err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	cell := pgdb.NewCell(settings.Cell, settings.CellPostgresDSN)
	if err := cell.Start(ctx); err != nil {
		return fmt.Errorf("open the cell tier: %w", err)
	}
	defer cell.Close()

	days, err := localDays(ctx, conn)
	if err != nil {
		return err
	}
	if len(days) == 0 {
		fmt.Println("no local raw data; nothing to archive")
		return nil
	}

	var unarchived, urgent int
	var closest time.Duration = 1<<63 - 1

	for _, d := range days {
		covered, err := archived(ctx, cell, d)
		if err != nil {
			return err
		}
		margin := time.Until(d.expiresAt())
		if covered {
			if margin < closest {
				closest = margin
			}
			continue
		}
		unarchived++
		if margin < time.Duration(*warn)*24*time.Hour {
			urgent++
			fmt.Printf("  URGENT  %s %s/%s has no archive and expires in %s\n",
				d.date.Format(time.DateOnly), d.projectUID, d.envUID, humanise(margin))
			continue
		}
		fmt.Printf("  lagging %s %s/%s has no archive; %s of margin\n",
			d.date.Format(time.DateOnly), d.projectUID, d.envUID, humanise(margin))
	}

	fmt.Printf("\n%d project-days held locally, %d without an archive\n", len(days), unarchived)
	if unarchived < len(days) {
		fmt.Printf("closest archived day to its own expiry: %s of margin\n", humanise(closest))
	}

	if urgent > 0 {
		return fmt.Errorf("%d project-day(s) would expire within %d days with no archive; "+
			"Appendix C.6 says local data is not expired until archive durability is confirmed, "+
			"and nothing can hold a ClickHouse TTL back", urgent, *warn)
	}
	fmt.Println("ok   every local day either has an archive or has margin to get one")
	return nil
}

// localDays reads what ClickHouse still holds.
//
// Grouped by the UUIDs rather than the surrogates, because the manifest lives in another
// database in another tier and carries only the public identifiers — there is no join across
// that boundary, by design (§99). This is two queries against two systems compared in Go,
// which is the only shape available and also the honest one.
func localDays(ctx context.Context, conn driver.Conn) ([]day, error) {
	res, err := conn.Query(ctx, `
		SELECT
		    project_id,
		    environment_id,
		    event_date,
		    count()            AS rows,
		    min(event_time)    AS min_event_time,
		    max(event_time)    AS max_event_time,
		    max(retention_tier) AS retention_tier
		FROM events_raw
		GROUP BY project_id, environment_id, event_date
		ORDER BY event_date, project_id`)
	if err != nil {
		return nil, fmt.Errorf("read local days: %w", err)
	}
	defer res.Close()

	var out []day
	for res.Next() {
		var d day
		if err := res.Scan(&d.projectUID, &d.envUID, &d.date,
			&d.rows, &d.minEventTime, &d.maxEventTime, &d.retentionTier); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, res.Err()
}

// archived reports whether a manifest covers this day.
//
// Coverage is by time range rather than by a date column, because an object is keyed on the
// hour and a day is twenty-four of them: the question is whether the day's span is inside
// some durable object's span, not whether a row happens to carry the same date string.
func archived(ctx context.Context, cell *pgdb.Cell, d day) (bool, error) {
	n, err := cell.Client.ArchiveManifest.Query().
		Where(
			archivemanifest.ProjectUIDEQ(d.projectUID),
			archivemanifest.EnvironmentUIDEQ(d.envUID),
			archivemanifest.StatusEQ(archivemanifest.StatusDurable),
			archivemanifest.MaxEventTimeGTE(d.minEventTime),
			archivemanifest.MinEventTimeLTE(d.maxEventTime),
		).
		Count(ctx)
	if err != nil {
		return false, fmt.Errorf("read manifests: %w", err)
	}
	return n > 0, nil
}

func humanise(d time.Duration) string {
	if d < 0 {
		return "no margin left"
	}
	if days := int(d.Hours() / 24); days > 0 {
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find the repository root")
		}
		dir = parent
	}
}
