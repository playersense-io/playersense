package scheduler_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"playersense.dev/backend/internal/erasure"
	"playersense.dev/backend/internal/retention"
	"playersense.dev/backend/internal/scheduler"
)

// The janitor is thin — the three jobs it drives are tested where they live — so what is
// worth proving here is the part that only exists at this level: that one broken job does
// not take the other two down with it.

func silent() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestAJanitorWithNothingToDoIsARefusal.
//
// A worker that started a loop over an empty list would log "janitor started" every boot and
// do nothing forever, which is the most expensive kind of silence: it looks exactly like a
// healthy service.
func TestAJanitorWithNothingToDoIsARefusal(t *testing.T) {
	if _, err := scheduler.NewJanitor(silent(), scheduler.JanitorOptions{}); err == nil {
		t.Fatal("a janitor with no jobs should not be constructible")
	}
}

// TestOneBrokenJobDoesNotStopTheOthers is the reason the errors are joined.
//
// An archive expiry that cannot reach its bucket must not stop an erasure that has a legal
// deadline attached to it. Here the sweeper is pointed at a cell tier that was never opened,
// which fails the way an unreachable dependency does, and the erasure fails for its own
// unrelated reason — and the pass reports both rather than the first.
func TestOneBrokenJobDoesNotStopTheOthers(t *testing.T) {
	j, err := scheduler.NewJanitor(silent(), scheduler.JanitorOptions{
		Every:   time.Hour,
		Sweeper: &retention.Sweeper{Log: silent()},
		Purger:  &erasure.Purger{Log: silent()},
	})
	if err != nil {
		t.Fatalf("build janitor: %v", err)
	}

	runErr := j.Run(t.Context())
	if runErr == nil {
		t.Fatal("a pass whose jobs both failed reported success")
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(runErr, &joined) || len(joined.Unwrap()) != 2 {
		t.Errorf("the pass reported %v; both failures should be in it, not just the first", runErr)
	}
	// And the pass is still counted, so a run that failed is visible as a run.
	if j.Stats()["runs"] != 1 {
		t.Errorf("runs = %d, want 1", j.Stats()["runs"])
	}
}
