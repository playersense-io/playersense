package liveops_test

import (
	"testing"
	"time"

	"playersense.dev/backend/internal/liveops"
)

// The config path evaluates every event's schedule on every request rather than publishing
// expanded windows, which is only defensible if evaluation is cheap and stays cheap as a
// schedule ages. These are the two shapes that could have failed: an old anchor, and the mode
// with the shortest period.
func BenchmarkActiveAt(b *testing.B) {
	now := time.Date(2026, time.September, 18, 13, 0, 0, 0, time.UTC)
	old := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		name     string
		schedule liveops.Schedule
	}{
		{"monthly, anchored six years ago", liveops.Schedule{
			Mode: liveops.Monthly, Anchor: old, Duration: 5 * 24 * time.Hour,
		}},
		{"every six hours, anchored six years ago", liveops.Schedule{
			Mode: liveops.Hourly, Anchor: old, Duration: time.Hour, Interval: 6,
		}},
		{"weekend, in a named timezone", liveops.Schedule{
			Mode: liveops.Weekly, Anchor: old.Add(96 * time.Hour), Duration: 54 * time.Hour,
			Weekdays: []time.Weekday{time.Friday}, Timezone: "Europe/Istanbul",
		}},
	} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				c.schedule.ActiveAt(now)
			}
		})
	}
}
