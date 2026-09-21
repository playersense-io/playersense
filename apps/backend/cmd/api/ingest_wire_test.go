package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
)

// The wire half of §17 and §19: how a batch is encoded on its way in, and what identifies an
// event once it arrives.
//
// Both were specified and unbuilt. §17 asks SDKs for "zstd where supported, gzip fallback"
// and §19 step 3 lists `decompress` among the gateway's operations, but nothing decompressed
// — so a compressed body was a parse failure and every SDK had to send JSON in the clear, at
// 290 bytes an event against 31.6 compressed (`cmd/wiresize`).

func compressed(t *testing.T, encoding string, body map[string]any) testclient.RequestOption {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	return testclient.Body("application/json", bytes.NewReader(squeeze(t, encoding, raw)))
}

func squeeze(t *testing.T, encoding string, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	var w io.WriteCloser
	switch encoding {
	case "gzip":
		w = gzip.NewWriter(&out)
	case "zstd":
		zw, err := zstd.NewWriter(&out)
		if err != nil {
			t.Fatalf("zstd writer: %v", err)
		}
		w = zw
	default:
		t.Fatalf("unknown encoding %q", encoding)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("finish compressing: %v", err)
	}
	return out.Bytes()
}

// TestACompressedBatchIsAccepted is §17's transport, both spellings.
//
// The assertion that matters is not the status but the payload on the bus: a decompression
// that produced a body the binder merely tolerated would pass a status check and store
// nothing.
func TestACompressedBatchIsAccepted(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			f := newIngestFixture(t, "standard")

			body := batch(event("session_start", nil), event("level_completed", nil))
			f.client.Post("/v1/batches",
				compressed(t, encoding, body),
				testclient.Header("Content-Encoding", encoding),
			).AssertStatus(http.StatusAccepted)

			msgs := f.published(t)
			if len(msgs) != 1 {
				t.Fatalf("the bus holds %d messages, want 1", len(msgs))
			}
			var payload ingest.NormalizedBatch
			if err := json.Unmarshal(msgs[0].Payload, &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if len(payload.Events) != 2 {
				t.Fatalf("the decompressed batch carried %d events, want 2", len(payload.Events))
			}
			if payload.Events[0].EventName != "session_start" {
				t.Errorf("first event is %q; the body decoded to something else",
					payload.Events[0].EventName)
			}
		})
	}
}

// TestAnUnsupportedEncodingIsRefused keeps the accepted set closed.
//
// `br` and `deflate` are the two a client will try. Refusing them by name is better than
// letting the body through undecoded, which would surface as an unreadable JSON error and
// send an SDK author looking at their serializer.
func TestAnUnsupportedEncodingIsRefused(t *testing.T) {
	f := newIngestFixture(t, "standard")

	for _, encoding := range []string{"br", "deflate", "gzip, gzip"} {
		res := f.client.Post("/v1/batches",
			testclient.JSON(batch(event("session_start", nil))),
			testclient.Header("Content-Encoding", encoding),
		)
		if res.Status != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Encoding %q gave %d, want 415", encoding, res.Status)
		}
		if !strings.Contains(res.String(), "content_encoding_unsupported") {
			t.Errorf("the refusal of %q does not name itself: %s", encoding, res.String())
		}
	}
}

// TestADecompressionBombIsRefusedBeforeItIsRead is the attack accepting compression opens.
//
// Sixty-four megabytes of zeroes compresses to a few kilobytes. A gateway that decompressed
// first and measured afterwards would allocate all of it. The body here is far under the
// compressed limit precisely so that the test proves the *decompressed* bound does the work
// — the compressed one never fires.
func TestADecompressionBombIsRefusedBeforeItIsRead(t *testing.T) {
	f := newIngestFixture(t, "standard")

	bomb := squeeze(t, "zstd", bytes.Repeat([]byte("0"), 64<<20))
	if len(bomb) > 1<<20 {
		t.Fatalf("the bomb is %d compressed bytes, which the wire limit would refuse on its own", len(bomb))
	}

	res := f.client.Post("/v1/batches",
		testclient.Body("application/json", bytes.NewReader(bomb)),
		testclient.Header("Content-Encoding", "zstd"),
	)
	if res.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("a 64 MB bomb gave %d, want 413", res.Status)
	}
	if len(f.published(t)) != 0 {
		t.Error("the bomb reached the bus")
	}
}

// TestAnOversizedCompressedBodyIsRefusedOnTheWire is the other limit §19.1 names.
func TestAnOversizedCompressedBodyIsRefusedOnTheWire(t *testing.T) {
	f := newIngestFixture(t, "standard")

	// Genuinely random, so it does not compress and the body on the wire stays the size of
	// the input — well past the one-megabyte wire limit. A pattern would not do: anything
	// periodic squashes to nothing and would test the opposite of what this claims.
	noise := make([]byte, 2<<20)
	if _, err := rand.Read(noise); err != nil {
		t.Fatalf("make noise: %v", err)
	}
	res := f.client.Post("/v1/batches",
		testclient.Body("application/json", bytes.NewReader(squeeze(t, "zstd", noise))),
		testclient.Header("Content-Encoding", "zstd"),
	)
	if res.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized compressed body gave %d, want 413", res.Status)
	}
}

// TestAnUncompressedBatchStillWorks is the regression the middleware could cause.
func TestAnUncompressedBatchStillWorks(t *testing.T) {
	f := newIngestFixture(t, "standard")

	f.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusAccepted)
	if len(f.published(t)) != 1 {
		t.Fatal("an ordinary batch stopped reaching the bus")
	}
}

// ---- event identity ---------------------------------------------------------

// TestEventIDIsDerivedWhenOmitted is the other half of the byte problem.
//
// A v4 UUID is sixteen bytes of entropy and no compressor reduces it: at a 500-event batch
// the whole event compresses to 31.6 bytes and twenty-four of them are `event_id`. Deriving
// it from the batch and the event's position takes the same batch under 10 bytes an event —
// more than any change of encoding would buy — and costs nothing, because a derived id is
// unique and stable for the same reason a generated one is.
func TestEventIDIsDerivedWhenOmitted(t *testing.T) {
	f := newIngestFixture(t, "standard")

	body := batch(
		bare("session_start"),
		bare("level_completed"),
		bare("level_completed"),
	)
	f.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)

	payload := f.onlyPayload(t)
	if len(payload.Events) != 3 {
		t.Fatalf("stored %d events, want 3", len(payload.Events))
	}

	seen := map[string]bool{}
	for i, ev := range payload.Events {
		if ev.EventID == "" {
			t.Fatalf("event %d has no id", i)
		}
		if _, err := uuid.Parse(ev.EventID); err != nil {
			t.Errorf("event %d has id %q, which the column cannot hold: %v", i, ev.EventID, err)
		}
		if seen[ev.EventID] {
			t.Errorf("event %d reuses id %s; two events would deduplicate into one", i, ev.EventID)
		}
		seen[ev.EventID] = true
	}
}

// TestADerivedEventIDSurvivesARedelivery is the property the derivation rests on.
//
// §21 guarantees at-least-once delivery and §18 requires a batch to be immutable once
// created, so the same batch_id arrives again carrying the same events in the same order.
// The ids must come out identical, or read-side deduplication has nothing to match on.
func TestADerivedEventIDSurvivesARedelivery(t *testing.T) {
	f := newIngestFixture(t, "standard")

	body := batch(bare("session_start"), bare("level_completed"))
	f.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)
	f.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)

	msgs := f.published(t)
	if len(msgs) != 2 {
		t.Fatalf("the bus holds %d messages, want both deliveries", len(msgs))
	}
	first, second := decodePayload(t, msgs[0]), decodePayload(t, msgs[1])
	for i := range first.Events {
		if first.Events[i].EventID != second.Events[i].EventID {
			t.Errorf("event %d got id %s then %s; a redelivery must reproduce the id",
				i, first.Events[i].EventID, second.Events[i].EventID)
		}
	}
}

// TestASuppliedEventIDMustBeAUUID closes the gap between what the gateway took and what
// storage can hold.
//
// It used to accept any string here and answer 202; the writer then found it unparseable and
// dropped the whole message with a log line no SDK could see. Refused per event now, with a
// reason, which keeps §19's promise that one bad event does not fail the batch around it.
func TestASuppliedEventIDMustBeAUUID(t *testing.T) {
	f := newIngestFixture(t, "standard")

	res := f.client.Post("/v1/batches", testclient.JSON(batch(
		event("session_start", nil),
		event("level_completed", map[string]any{"event_id": "not-a-uuid"}),
	))).AssertStatus(http.StatusAccepted)

	accepted := decodeAccepted(t, res)
	if accepted.Accepted != 1 || accepted.Dropped != 1 {
		t.Fatalf("accepted %d and dropped %d, want one of each", accepted.Accepted, accepted.Dropped)
	}
	if len(accepted.Reasons) == 0 || !strings.Contains(accepted.Reasons[0], "uuid") {
		t.Errorf("the SDK is not told why: %v", accepted.Reasons)
	}
}

// TestBatchIDMustBeAUUID is the same gap one level up, and it was worse.
//
// A non-UUID batch_id was accepted with a 202 and then failed in the writer for the *whole*
// message — every event in it lost, with nothing anywhere the SDK could observe.
func TestBatchIDMustBeAUUID(t *testing.T) {
	f := newIngestFixture(t, "standard")

	body := batch(event("session_start", nil))
	body["batch_id"] = "abc12345"

	f.client.Post("/v1/batches", testclient.JSON(body)).
		AssertStatus(http.StatusUnprocessableEntity)
	if len(f.published(t)) != 0 {
		t.Error("a batch storage cannot key reached the bus")
	}
}

// bare is an event with no client-supplied identifier, which is what an SDK should send.
func bare(name string) map[string]any {
	e := event(name, nil)
	delete(e, "event_id")
	return e
}

func (f *ingestFixture) onlyPayload(t *testing.T) ingest.NormalizedBatch {
	t.Helper()
	msgs := f.published(t)
	if len(msgs) != 1 {
		t.Fatalf("the bus holds %d messages, want 1", len(msgs))
	}
	return decodePayload(t, msgs[0])
}

func decodePayload(t *testing.T, msg *eventbus.Message) ingest.NormalizedBatch {
	t.Helper()
	var out ingest.NormalizedBatch
	if err := json.Unmarshal(msg.Payload, &out); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return out
}
