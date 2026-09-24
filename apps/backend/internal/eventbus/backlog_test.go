package eventbus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestTheSpoolSaysHowFarBehindEachGroupIs: a group that consumed part of the spool is behind by
// the rest, from the first batch it has not read; a group that read nothing is behind by all of
// it; and each sees only its own dead letters.
func TestTheSpoolSaysHowFarBehindEachGroupIs(t *testing.T) {
	bus := NewLocal(t.TempDir())
	ctx := context.Background()
	if err := bus.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Stop(ctx) })

	first := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	for i := range 3 {
		if err := bus.Publish(ctx, &Message{
			PublishedAt: first.Add(time.Duration(i) * time.Minute), DedupKey: string(rune('a' + i)),
			ProjectID: "p", EventCount: 2, Payload: []byte(`[]`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// The archive writer takes everything; the analytics writer takes nothing yet.
	consumed, cancel := context.WithCancel(ctx)
	err := bus.Subscribe(consumed, GroupArchiveWriter, func(context.Context, []*Message) error {
		cancel()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if err := bus.DeadLetter(ctx, &Message{DedupKey: "x", ProjectID: "p", EventCount: 5},
		DeadLetterReason{Group: GroupAnalyticsWriter, Reason: "poison", Attempts: 4}); err != nil {
		t.Fatal(err)
	}

	behind, err := bus.Backlog(ctx, GroupAnalyticsWriter, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if behind.Messages != 3 || behind.Bytes == 0 || behind.OldestUnread == nil || !behind.OldestUnread.Equal(first) {
		t.Fatalf("the analytics writer is %+v, want three batches from %s", behind, first)
	}
	if behind.DeadLettered != 1 || len(behind.Parked) != 1 || behind.Parked[0].Reason != "poison" ||
		behind.Parked[0].EventCount != 5 {
		t.Fatalf("parked %+v", behind.Parked)
	}

	caught, err := bus.Backlog(ctx, GroupArchiveWriter, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if caught.Messages != 0 || caught.Bytes != 0 || caught.OldestUnread != nil || caught.DeadLettered != 0 {
		t.Fatalf("the archive writer is %+v, want caught up", caught)
	}
}
