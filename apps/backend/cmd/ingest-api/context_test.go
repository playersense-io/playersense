package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/ingest"
)

// Context frames (ADR 0028): the studio's own description of the player, carried once per
// batch and materialized onto every event before storage.
//
// The materialization is what keeps §29.1 intact. A breakdown by `rank` is a GROUP BY over
// one denormalized table rather than a join to reconstruct what the player's rank was at the
// time — and §32.1's compiler has no join to offer even if somebody wanted one.

// frames is the timeline in the shape an SDK sends it.
func frames(f ...map[string]any) []map[string]any { return f }

func snapshot(values map[string]any) map[string]any {
	return map[string]any{"at": 0, "values": values}
}

func delta(at int, set map[string]any) map[string]any {
	return map[string]any{"at": at, "set": set}
}

// TestContextIsMaterializedOntoEveryEvent is the whole feature in one batch.
func TestContextIsMaterializedOntoEveryEvent(t *testing.T) {
	f := newFixture(t, "standard")

	body := batch(
		bare("match_started"),
		bare("enemy_killed"),
		bare("match_completed"),
	)
	body["context"] = frames(
		snapshot(map[string]any{"rank": "gold", "chapter": 4}),
		delta(2, map[string]any{"rank": "diamond"}),
	)

	f.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)

	events := f.onlyPayload(t).Events
	if len(events) != 3 {
		t.Fatalf("stored %d events, want 3", len(events))
	}

	// The first two happened while the player was gold; the third did not.
	assertString(t, events[0], "ctx.rank", "gold")
	assertString(t, events[1], "ctx.rank", "gold")
	assertString(t, events[2], "ctx.rank", "diamond")

	// And a value that did not change is still on every row, because the row is what a
	// query reads and a query must not have to look elsewhere.
	for i, ev := range events {
		if got := ev.PropsInt["ctx.chapter"]; got != 4 {
			t.Errorf("event %d has ctx.chapter = %d, want 4", i, got)
		}
	}
}

// TestADroppedEventDoesNotShiftTheContext is the hazard this design has to survive.
//
// The gateway drops a malformed event and keeps the rest (§19). Frames are keyed on the
// event's position **in the batch as sent**, so resolving them against the surviving events
// instead would hand every event after a dropped one somebody else's context — a corruption
// rather than a loss, and nothing anywhere would report it.
func TestADroppedEventDoesNotShiftTheContext(t *testing.T) {
	f := newFixture(t, "standard")

	body := batch(
		bare("match_started"),
		bare("Bad Name!"), // refused: a space and a capital
		bare("match_completed"),
	)
	body["context"] = frames(
		snapshot(map[string]any{"rank": "gold"}),
		delta(2, map[string]any{"rank": "diamond"}),
	)

	res := f.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)
	accepted := decodeAccepted(t, res)
	if accepted.Accepted != 2 || accepted.Dropped != 1 {
		t.Fatalf("accepted %d and dropped %d, want 2 and 1", accepted.Accepted, accepted.Dropped)
	}

	events := f.onlyPayload(t).Events
	assertString(t, events[0], "ctx.rank", "gold")
	assertString(t, events[1], "ctx.rank", "diamond")
}

// TestAnOutOfOrderTimelineIsRefused keeps the single pass honest.
//
// Resolving the timeline is one walk over the events, so a frame that applies from before its
// predecessor would simply never be reached. Refusing the batch is better than applying most
// of a timeline and saying nothing.
func TestAnOutOfOrderTimelineIsRefused(t *testing.T) {
	f := newFixture(t, "standard")

	body := batch(bare("a"), bare("b"), bare("c"))
	body["context"] = frames(
		snapshot(map[string]any{"rank": "gold"}),
		delta(2, map[string]any{"rank": "diamond"}),
		delta(1, map[string]any{"rank": "bronze"}),
	)

	f.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusBadRequest)
	if len(f.published(t)) != 0 {
		t.Error("a batch with an unresolvable timeline reached the bus")
	}
}

// TestTheBillingMeterCountsWhatTheStudioSent is why the timeline exists at all.
//
// §39 bills on normalized logical bytes. The studio sent the context once for the batch, so
// counting it again on every event would bill them two hundred and fifty times for one
// transmission — the exact cost the timeline avoids, reintroduced at the meter.
func TestTheBillingMeterCountsWhatTheStudioSent(t *testing.T) {
	plain := newFixture(t, "standard")
	plain.client.Post("/v1/batches", testclient.JSON(batch(bare("match_started")))).
		AssertStatus(http.StatusAccepted)
	bare := plain.onlyPayload(t).Events[0].PayloadBytes

	withContext := newFixture(t, "standard")
	body := batch(bareEvent("match_started"))
	body["context"] = frames(snapshot(map[string]any{
		"rank": "diamond", "chapter": 12, "map": "desert_03", "game_mode": "ranked",
	}))
	withContext.client.Post("/v1/batches", testclient.JSON(body)).
		AssertStatus(http.StatusAccepted)
	decorated := withContext.onlyPayload(t).Events[0]

	if decorated.PayloadBytes != bare {
		t.Errorf("payload_bytes = %d with context and %d without; the meter is counting "+
			"context the studio sent once per batch", decorated.PayloadBytes, bare)
	}
	// The row still carries it — the meter and the row are deliberately different things.
	if decorated.PropsString["ctx.rank"] != "diamond" {
		t.Error("the context did not reach the row")
	}
}

// TestAnEventPropertyIsNotOverwrittenByContext keeps the two spaces apart.
func TestAnEventPropertyIsNotOverwrittenByContext(t *testing.T) {
	f := newFixture(t, "standard")

	body := batch(event("match_completed", map[string]any{
		"event_id":   nil,
		"properties": map[string]any{"rank": "the event's own"},
	}))
	delete(body["events"].([]map[string]any)[0], "event_id")
	body["context"] = frames(snapshot(map[string]any{"rank": "the player's"}))

	f.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)

	ev := f.onlyPayload(t).Events[0]
	if ev.PropsString["rank"] != "the event's own" {
		t.Errorf("rank = %q; a property describes the occurrence and must win",
			ev.PropsString["rank"])
	}
	if ev.PropsString["ctx.rank"] != "the player's" {
		t.Errorf("ctx.rank = %q; the prefix is what keeps the two apart",
			ev.PropsString["ctx.rank"])
	}
}

func assertString(t *testing.T, ev ingest.NormalizedEvent, key, want string) {
	t.Helper()
	if got := ev.PropsString[key]; got != want {
		t.Errorf("%s of %q = %q, want %q", key, ev.EventName, got, want)
	}
}

// bareEvent is an event with no client-supplied identifier and no properties.
func bareEvent(name string) map[string]any { return bare(name) }
