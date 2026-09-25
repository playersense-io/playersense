package ingest

import (
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/protocol"
)

func TestNormalizeLogs(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	limits := DefaultLogLimits()
	batch := &protocol.LogBatch{
		BatchID: "7d1c1d1e-1111-4111-8111-111111111111", InstallID: "7d1c1d1e-2222-4222-8222-222222222222",
		SDK: protocol.SDK{Name: "unity", Version: "1.0.0"}, App: protocol.App{Version: "1.2.3"},
		Device: protocol.LogDevice{Platform: "Android", Model: "SM-S918B"},
		Records: []protocol.LogRecord{
			{Time: now.Add(-time.Minute), Level: "Warning", Message: "low memory", PlayerID: "p1",
				Attributes: map[string]any{"scene": "Main", "fps": 58.5, "bad key!": "x"}},
			{Time: now.Add(time.Hour), Level: "error", Exception: &protocol.LogException{Type: "NullReferenceException",
				Frames: make([]protocol.StackFrame, 300)}, Crash: true},
			{Time: now.AddDate(0, 0, -90), Level: "info", Message: strings.Repeat("é", 10_000)},
			{Level: "loud", Message: "dropped"},
			{Level: "info"},
		},
	}
	out, err := NormalizeLogs(batch, &core.Tenant{PolicyTier: "kids_strict"}, "", now, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Batch.Records) != 3 || out.Dropped != 2 {
		t.Fatalf("kept %d, dropped %d: %v", len(out.Batch.Records), out.Dropped, out.Reasons)
	}
	warn, crash, long := out.Batch.Records[0], out.Batch.Records[1], out.Batch.Records[2]
	if warn.Severity != "warn" || warn.PlayerID != "" || warn.Attributes["fps"] != "58.5" || len(warn.Attributes) != 2 {
		t.Fatalf("warn %+v", warn)
	}
	if crash.Kind != "crash" || crash.Severity != "fatal" || !crash.Time.Equal(now) || len(crash.Frames) != limits.MaxFrames {
		t.Fatalf("crash %+v", crash)
	}
	if !long.Time.Equal(now.Add(-limits.SkewPast)) || len(long.Message) > limits.MaxMessageBytes || !strings.HasSuffix(long.Message, "…") {
		t.Fatalf("an old, long record is kept, pinned and cut: %v %d", long.Time, len(long.Message))
	}
	if out.Batch.Platform != "android" || out.Batch.DeviceModel != "SM-S918B" || out.Bytes == 0 {
		t.Fatalf("batch %+v", out.Batch)
	}
	if warn.RecordID == "" || warn.RecordID == crash.RecordID {
		t.Fatal("a record without an id gets one of its own, derived from the batch")
	}
	again, _ := NormalizeLogs(batch, &core.Tenant{}, "", now, limits)
	if again.Batch.Records[0].RecordID != warn.RecordID {
		t.Fatal("a retried batch must derive the same ids")
	}
}
