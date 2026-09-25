package main

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/internal/schemas"
)

// TestRealtimeIsTheLastMinutesAgainstYesterday: events a few minutes ago land in their minute, the
// same minute yesterday beside them, the busiest names drawn and the rest folded into other, and a
// minute played by fewer than the threshold carries no player count.
func TestRealtimeIsTheLastMinutesAgainstYesterday(t *testing.T) {
	r := newReporting(t, "standard")
	at := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	var rows []rawEvent
	for i := 0; i < 6; i++ {
		install := uuid.NewString()
		rows = append(rows, rawEvent{install: install, session: "s", name: "level_complete", platform: "ios", country: "US",
			version: "1.0", at: at.Add(10 * time.Second)})
		rows = append(rows, rawEvent{install: install, session: "s", name: "level_complete", platform: "ios", country: "US",
			version: "1.0", at: at.Add(-24 * time.Hour).Add(5 * time.Second), cohort: at.Add(-24 * time.Hour)})
	}
	// Fourteen more names, one event each: two past the twelve drawn.
	lone := uuid.NewString()
	for i := 0; i < 14; i++ {
		rows = append(rows, rawEvent{install: lone, session: "s", name: fmt.Sprintf("rare_%02d", i), platform: "ios",
			country: "US", version: "1.0", at: at.Add(20 * time.Second)})
	}
	r.insert(t, rows)

	out := get[schemas.RealtimeOut](t, r.member, r.analytics()+"/realtime", url.Values{"minutes": {"10"}}, http.StatusOK)
	if len(out.Minutes) != 10 || len(out.Yesterday) != 10 || out.Elapsed < 0 || out.Elapsed > 1 {
		t.Fatalf("windows: %d and %d minutes, elapsed %v", len(out.Minutes), len(out.Yesterday), out.Elapsed)
	}
	var now, then *schemas.RealtimeMinuteOut
	for i := range out.Minutes {
		if out.Minutes[i].At.Equal(at) {
			now, then = &out.Minutes[i], &out.Yesterday[i]
		}
	}
	if now == nil {
		t.Fatalf("no minute at %v in %v", at, out.Minutes[0].At)
	}
	if now.Events["level_complete"] != 6 || now.Total != 20 || now.Players == nil || *now.Players != 7 {
		t.Fatalf("the minute: %+v players %v", now, now.Players)
	}
	if then.Events["level_complete"] != 6 || !then.At.Equal(at.Add(-24*time.Hour)) {
		t.Fatalf("yesterday's minute: %+v", then)
	}
	if len(out.Names) != 12 || out.Names[0].Name != "level_complete" || now.Events["other"] != 3 {
		t.Fatalf("names %v, other %d", out.Names, now.Events["other"])
	}
	for _, m := range out.Minutes {
		if !m.At.Equal(at) && m.Players != nil && *m.Players > 0 && *m.Players < uint64(out.SuppressionThreshold) {
			t.Fatalf("a thin minute shows its players: %+v", m)
		}
	}
}
