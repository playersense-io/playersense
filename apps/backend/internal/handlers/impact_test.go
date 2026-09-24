package handlers

import (
	"math"
	"testing"
	"time"

	"playersense.dev/backend/internal/query"
)

// TestAWindowIsMeasuredAgainstItsOwnWeekdays: a project with 100 players every day and 150 while
// the event is on reads a lift of one half, and a quiet Sunday is compared with Sundays.
func TestAWindowIsMeasuredAgainstItsOwnWeekdays(t *testing.T) {
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC) // a Thursday
	end := start.AddDate(0, 0, 2)
	byDay := map[time.Time]query.DayTotals{}
	var days []time.Time
	for d := start.AddDate(0, 0, -28); !d.After(end.AddDate(0, 0, 7)); d = d.AddDate(0, 0, 1) {
		players := uint64(100)
		if d.Weekday() == time.Sunday {
			players = 40
		}
		if !d.Before(start) && !d.After(end) {
			players = players * 3 / 2
		}
		byDay[d] = query.DayTotals{Day: d, Players: players, Revenue: int64(players) * 1_000_000}
		if !d.Before(start.AddDate(0, 0, -14)) {
			days = append(days, d)
		}
	}
	daily, metrics := compareWindow(byDay, days, start, end, 5)

	players := metrics["players"]
	if players.Lift == nil || math.Abs(*players.Lift-0.5) > 1e-9 {
		t.Fatalf("players %+v", players)
	}
	if arpdau := metrics["arpdau"]; arpdau.Lift == nil || math.Abs(*arpdau.Lift) > 1e-9 {
		t.Fatalf("revenue per player did not change, and the lift says %v", *arpdau.Lift)
	}
	for i, d := range days {
		if d.Weekday() == time.Sunday && d.Before(start) && *daily["players"].Expected[i] != 40 {
			t.Fatalf("a Sunday expected %v, want Sundays' 40", *daily["players"].Expected[i])
		}
	}

	// Under the threshold, nothing is shown.
	_, small := compareWindow(byDay, days, start, end, 1000)
	if small["players"].Actual != nil {
		t.Fatalf("a project under the threshold showed %v", *small["players"].Actual)
	}
}
