package eventbus

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The grouper decides the insert rate, so these pin the two ways it can be wrong: holding
// a small group hostage to a target it will never reach, and sizing groups by a unit that
// is not the one ClickHouse cares about.

type recorder struct {
	mu         sync.Mutex
	deliveries [][]*Message
}

func (r *recorder) handle(context.Context, []*Message) error { return nil }

func (r *recorder) record(_ context.Context, msgs []*Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries = append(r.deliveries, msgs)
	return nil
}

func (r *recorder) counts() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, len(r.deliveries))
	for i, d := range r.deliveries {
		for _, m := range d {
			out[i] += m.EventCount
		}
	}
	return out
}

// A group is sized in rows, not in messages.
//
// An SDK batch is one event to a thousand (§19.1), so a message count is not a size. Ten
// messages of a thousand events must hand over on the row target rather than waiting for a
// message count it will not reach.
func TestAGroupIsSizedInRows(t *testing.T) {
	r := &recorder{}
	g := &grouper{handle: r.record, maxRows: 1000, maxMessages: 10_000}

	// Ten messages of 200 events: the row target is reached at the fifth.
	for range 10 {
		g.add(t.Context(), &Message{EventCount: 200}, nil)
	}

	counts := r.counts()
	if len(counts) != 2 {
		t.Fatalf("want 2 deliveries of 1000 rows, got %d: %v", len(counts), counts)
	}
	for i, n := range counts {
		if n != 1000 {
			t.Errorf("delivery %d carried %d rows, want 1000", i, n)
		}
	}
}

// The message cap is the backstop for a producer that reported no count, not the target.
//
// Without it a stream of messages carrying EventCount 0 would accumulate until memory ran
// out, because the row trigger would never fire.
func TestAGroupWithoutRowCountsStillHandsOver(t *testing.T) {
	r := &recorder{}
	g := &grouper{handle: r.record, maxRows: 1_000_000, maxMessages: 4}

	for range 8 {
		g.add(t.Context(), &Message{}, nil)
	}

	if got := len(r.counts()); got != 2 {
		t.Fatalf("want 2 deliveries of 4 messages, got %d", got)
	}
}

// A partly-filled group is handed over rather than held until the target is reached.
//
// This is the failure that would make a quiet cell look broken: a handful of batches
// waiting indefinitely for a hundred thousand rows that are never coming. The timer in
// Subscribe is what prevents it; flush is what the timer calls.
func TestAPartlyFilledGroupIsNotHeld(t *testing.T) {
	r := &recorder{}
	g := &grouper{handle: r.record, maxRows: 100_000, maxMessages: 1000}

	g.add(t.Context(), &Message{EventCount: 3}, nil)
	if len(r.counts()) != 0 {
		t.Fatal("a group under target should not have been handed over by add")
	}

	// What the ticker does.
	g.flush(t.Context())

	counts := r.counts()
	if len(counts) != 1 || counts[0] != 3 {
		t.Fatalf("the timer should hand over the partial group; got %v", counts)
	}
}

// Flushing an empty group must do nothing, or an idle cell writes an empty part every tick.
func TestAnIdleGroupProducesNothing(t *testing.T) {
	r := &recorder{}
	g := &grouper{handle: r.record, maxRows: 100, maxMessages: 10}

	for range 5 {
		g.flush(t.Context())
	}
	if got := len(r.counts()); got != 0 {
		t.Fatalf("an idle grouper handed over %d deliveries, want 0", got)
	}
}

// The defaults are the insert rate, so they are worth asserting rather than assuming.
func TestTheDeliveryDefaultsAreSane(t *testing.T) {
	rows, msgs, wait := (&PubSub{}).delivery()
	if rows != 100_000 {
		t.Errorf("default row target is %d", rows)
	}
	if msgs != 1_000 {
		t.Errorf("default message cap is %d", msgs)
	}
	if wait != 2*time.Second {
		t.Errorf("default wait is %s", wait)
	}
}
