package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"playersense.dev/backend/internal/cohort"
)

func date(y int, m time.Month, d, hour, min int) time.Time {
	return time.Date(y, m, d, hour, min, 0, 0, time.UTC)
}

// The window is the pair of decisions the whole job rests on: which activity months are
// rebuilt, and how far back the denominators reach. Both are off-by-one prone and neither
// fails loudly when it is wrong — a missing month is an empty chart, not an error.

func TestTheWindowEndsAtTheCurrentMonth(t *testing.T) {
	got := trailingMonths(date(2026, time.September, 16, 2, 0), 2)
	want := []string{"202608", "202609"}
	assertMonths(t, got, want)
}

// The bug this catches is the reason the window is anchored to the first of the month.
//
// AddDate normalises an overflowing day rather than clamping it, so a month subtracted
// from the 31st of March lands on the 3rd of March — the same month it started in. Run
// naively, a job on the 29th, 30th or 31st would rebuild the current month twice and never
// rebuild the previous one, and nothing would report an error: last month's late events
// would simply stop being absorbed, on three days in twelve.
func TestTheWindowIsCorrectOnTheLastDayOfALongMonth(t *testing.T) {
	for _, tc := range []struct {
		name string
		when time.Time
		want []string
	}{
		{"31 March, back to February", date(2026, time.March, 31, 2, 0), []string{"202602", "202603"}},
		{"30 March", date(2026, time.March, 30, 2, 0), []string{"202602", "202603"}},
		{"29 March", date(2026, time.March, 29, 2, 0), []string{"202602", "202603"}},
		{"31 May, back to April", date(2026, time.May, 31, 2, 0), []string{"202604", "202605"}},
		{"31 December, across the year", date(2026, time.December, 31, 2, 0), []string{"202611", "202612"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertMonths(t, trailingMonths(tc.when, 2), tc.want)
		})
	}
}

// A denominator window shorter than the offsets the facts carry leaves a numerator divided
// by a cohort size computed before its players arrived, which is how retention goes above
// 100% (§29.9).
func TestTheDenominatorWindowReachesBackAYear(t *testing.T) {
	got := trailingMonths(date(2026, time.September, 16, 2, 0), 13)
	if len(got) != 13 {
		t.Fatalf("want 13 cohort months, got %d", len(got))
	}
	if got[0].String() != "202509" {
		t.Errorf("the window should start 12 months back, got %s", got[0])
	}
	if got[12].String() != "202609" {
		t.Errorf("the window should end at the current month, got %s", got[12])
	}
}

func TestTheWindowCrossesAYearBoundary(t *testing.T) {
	assertMonths(t, trailingMonths(date(2027, time.January, 5, 2, 0), 3),
		[]string{"202611", "202612", "202701"})
}

// A schedule that fires again the instant it finishes would rebuild every partition in a
// loop, so "next" must be strictly after now even when now is exactly the scheduled time.
func TestTheNextRunIsAlwaysInTheFuture(t *testing.T) {
	at := TimeOfDay{Hour: 2, Minute: 0}

	for _, tc := range []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before today's run", date(2026, time.September, 16, 1, 59), date(2026, time.September, 16, 2, 0)},
		{"exactly at it", date(2026, time.September, 16, 2, 0), date(2026, time.September, 17, 2, 0)},
		{"just after it", date(2026, time.September, 16, 2, 1), date(2026, time.September, 17, 2, 0)},
		{"late in the day", date(2026, time.September, 16, 23, 30), date(2026, time.September, 17, 2, 0)},
		{"across a month end", date(2026, time.September, 30, 5, 0), date(2026, time.October, 1, 2, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := at.next(tc.now); !got.Equal(tc.want) {
				t.Errorf("next(%s) = %s, want %s",
					tc.now.Format(time.RFC3339), got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
		})
	}
}

// A schedule that does not parse must not silently become midnight: a job running twenty-six
// hours from when somebody intended is a job whose logs nobody can reconcile.
func TestAnUnreadableScheduleIsRefused(t *testing.T) {
	// "02:00:00" is the one worth naming: it is the obvious thing to write for two in the
	// morning, and a scanner that stops at the last verb it matched accepts it silently.
	for _, bad := range []string{"", "2", "25:00", "02:60", "-1:00", "two o'clock", "02:00:00", "02:00x", ":00", "02:"} {
		if _, err := ParseTimeOfDay(bad); err == nil {
			t.Errorf("ParseTimeOfDay(%q) should have failed", bad)
		}
	}
	for _, good := range []struct {
		in   string
		want TimeOfDay
	}{
		{"02:00", TimeOfDay{2, 0}},
		{"0:00", TimeOfDay{0, 0}},
		{"23:59", TimeOfDay{23, 59}},
	} {
		got, err := ParseTimeOfDay(good.in)
		if err != nil {
			t.Errorf("ParseTimeOfDay(%q): %v", good.in, err)
			continue
		}
		if got != good.want {
			t.Errorf("ParseTimeOfDay(%q) = %v, want %v", good.in, got, good.want)
		}
	}
}

// A pass that cannot reach ClickHouse must say so rather than dereference a handle nobody
// opened. The long-running form constructs the scheduler before the lifecycle opens the
// pool, so this is the ordinary state for a moment on every boot.
func TestAPassWithoutAPoolFails(t *testing.T) {
	s, err := New(unopened{}, discard(), Options{At: TimeOfDay{Hour: 2}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Run(t.Context()); err == nil {
		t.Fatal("a pass with no pool should have failed")
	}
	if s.Stats()["runs"] != 0 {
		t.Error("a failed pass should not count as a run")
	}
}

func assertMonths(t *testing.T, got []cohort.Month, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("want %d months %v, got %d %v", len(want), want, len(got), render(got))
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("month %d: want %s, got %s", i, want[i], got[i])
		}
	}
}

func render(ms []cohort.Month) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.String()
	}
	return out
}

// unopened is the pool before the lifecycle has started it.
type unopened struct{}

func (unopened) DB() (*sql.DB, error) { return nil, errors.New("clickhouse is not started") }

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A prune window shorter than the cohort rebuild window must stop the service at start-up.
//
// DeriveSizes republishes CohortMonths of denominators from player_lifetime_v every night.
// Forgetting a device that still belongs to one of those cohorts shrinks its denominator,
// and retention silently rises — the mirror of the bug the cohort backfill fixes. The two
// settings are coupled, so the coupling is checked rather than left in a comment.
func TestAPruneWindowInsideTheRebuildWindowIsRefused(t *testing.T) {
	_, err := New(unopened{}, discard(), Options{
		At:                TimeOfDay{Hour: 2},
		CohortMonths:      13,
		LifetimeRetention: 200 * 24 * time.Hour, // inside the 13-month rebuild window
	})
	if err == nil {
		t.Fatal("a prune window shorter than the rebuild window should have been refused")
	}

	// And the documented default must clear it, or the product ships refusing to start.
	if _, err := New(unopened{}, discard(), Options{
		At:                TimeOfDay{Hour: 2},
		CohortMonths:      13,
		LifetimeRetention: 13_200 * time.Hour, // the .env default, 550 days
	}); err != nil {
		t.Errorf("the default prune window is refused by its own floor: %v", err)
	}
}

// Zero disables the prune rather than meaning "immediately", which would delete everyone.
func TestAZeroPruneWindowDisablesIt(t *testing.T) {
	if _, err := New(unopened{}, discard(), Options{At: TimeOfDay{Hour: 2}}); err != nil {
		t.Fatalf("a scheduler with no prune window should build: %v", err)
	}
	p := &cohortPruneProbe{}
	if err := p.runsWithZeroWindow(); err == nil {
		t.Error("a zero window must be refused by Prune itself, as a second line of defence")
	}
}

type cohortPruneProbe struct{}

func (cohortPruneProbe) runsWithZeroWindow() error {
	p := &cohort.Prune{Deriver: &cohort.Deriver{}, After: 0}
	_, err := p.Run(context.Background(), time.Now())
	return err
}
