package logs

import (
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/protocol"
)

func exception(typ, value string, frames ...protocol.StackFrame) *ingest.NormalizedLogRecord {
	return &ingest.NormalizedLogRecord{Kind: "exception", Severity: "error", ExceptionType: typ,
		ExceptionValue: value, Frames: frames}
}

func TestAnIssueSurvivesWhatChangesBetweenOccurrences(t *testing.T) {
	a := exception("NullReferenceException", "Object reference not set",
		protocol.StackFrame{Module: "UnityEngine", Function: "Internal"},
		protocol.StackFrame{Module: "Game.Player", Function: "Update", Line: 42, InApp: true})
	b := exception("NullReferenceException", "Object reference not set (id 99)",
		protocol.StackFrame{Module: "UnityEngine", Function: "SomethingElse"},
		protocol.StackFrame{Module: "Game.Player", Function: "Update", Line: 57, InApp: true})
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("a moved line, a changed message and a different engine frame split one bug into two issues")
	}

	c := exception("NullReferenceException", "Object reference not set",
		protocol.StackFrame{Module: "Game.Enemy", Function: "Update", InApp: true})
	if Fingerprint(a) == Fingerprint(c) {
		t.Fatal("the same exception thrown from two places of the game's own code is two bugs")
	}
	d := exception("IndexOutOfRangeException", "Object reference not set",
		protocol.StackFrame{Module: "Game.Player", Function: "Update", InApp: true})
	if Fingerprint(a) == Fingerprint(d) {
		t.Fatal("two exception types from one place are two bugs")
	}
}

func TestErrorLogsAreGroupedByTheirTemplate(t *testing.T) {
	one := &ingest.NormalizedLogRecord{Kind: "log", Severity: "error", Logger: "Levels", Message: "Failed to load level 12 in 0.53s"}
	two := &ingest.NormalizedLogRecord{Kind: "log", Severity: "error", Logger: "Levels", Message: "Failed to load level 13 in 1.2s"}
	if Fingerprint(one) == 0 || Fingerprint(one) != Fingerprint(two) {
		t.Fatal("one error log with different numbers should be one issue")
	}
	info := &ingest.NormalizedLogRecord{Kind: "log", Severity: "info", Message: "Loaded"}
	if Fingerprint(info) != 0 {
		t.Fatal("an info line is not an issue")
	}
	custom := &ingest.NormalizedLogRecord{Kind: "log", Severity: "error", Message: "x", Fingerprint: []string{"store", "timeout"}}
	other := &ingest.NormalizedLogRecord{Kind: "log", Severity: "error", Message: "y", Fingerprint: []string{"store", "timeout"}}
	if Fingerprint(custom) != Fingerprint(other) {
		t.Fatal("a studio's own fingerprint decides the issue")
	}
}

func TestTheCulpritIsTheInnermostFrameOfTheGamesOwnCode(t *testing.T) {
	r := exception("E", "",
		protocol.StackFrame{Module: "Game.Menu", Function: "Open", InApp: true},
		protocol.StackFrame{Module: "Game.Shop", Function: "Buy", InApp: true},
		protocol.StackFrame{Module: "System.Linq", Function: "First"})
	if got := Culprit(r); got != "Game.Shop.Buy" {
		t.Fatalf("culprit %q, want Game.Shop.Buy", got)
	}
}

func TestTheSearchLanguage(t *testing.T) {
	s, err := ParseSearch(`Timeout "failed to connect" level:error,fatal logger:Net -heartbeat attr.region:eu release:1.4.2 -logger:Ads`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Words, " ") != "timeout failed to connect" {
		t.Fatalf("words %v", s.Words)
	}
	if len(s.Phrases) != 1 || s.Phrases[0] != "failed to connect" {
		t.Fatalf("phrases %v", s.Phrases)
	}
	if strings.Join(s.Severities, ",") != "error,fatal" || s.Attributes["region"] != "eu" {
		t.Fatalf("filters %+v", s)
	}
	if s.Fields["logger"][0] != "Net" || s.NotFields["logger"][0] != "Ads" || s.Fields["app_version"][0] != "1.4.2" {
		t.Fatalf("fields %+v %+v", s.Fields, s.NotFields)
	}
	if s.NotWords[0] != "heartbeat" || s.OnlyVolume() {
		t.Fatalf("negation %+v", s)
	}
	where, args := s.Where()
	if len(where) != len(args)-1 { // the attribute is one condition with two arguments
		t.Fatalf("%d conditions for %d arguments", len(where), len(args))
	}
	if !strings.Contains(where[0], searchText) {
		t.Fatal("a word search must use the index's own expression or the index is not used")
	}

	levels, _ := ParseSearch("-level:trace,debug")
	if strings.Join(levels.Severities, ",") != "info,warn,error,fatal" || !levels.OnlyVolume() {
		t.Fatalf("an excluded level is every other level: %v", levels.Severities)
	}
	if _, err := ParseSearch("level:loud"); err == nil {
		t.Fatal("an unknown level is refused rather than matching nothing")
	}
	player, _ := ParseSearch("player:abc")
	if !player.NamesPlayer() {
		t.Fatal("a player filter needs player.read")
	}
}

func TestTokensSplitAsTheIndexDoes(t *testing.T) {
	got := strings.Join(tokens("Player.Update() failed: NullReference-Exception#42 Ünïcode 30000ms 0x7f3a"), "|")
	// 0x7f3a splits as the table's regular expression splits it, matches never overlapping.
	if got != "player|update|failed|null|reference|exception|42|ünïcode|30000|ms|0|x7|f3|a" {
		t.Fatalf("tokens %q", got)
	}
}

func TestTheQueuedPayloadIsCompressedAndReadsBack(t *testing.T) {
	batch := &ingest.NormalizedLogBatch{BatchID: "b", InstallID: "i", ReceivedAt: time.Unix(1, 0).UTC()}
	for range 200 {
		batch.Records = append(batch.Records, ingest.NormalizedLogRecord{
			RecordID: "r", Severity: "info", Kind: "log", Message: "Loaded scene MainMenu in 0.53 seconds"})
	}
	payload, err := EncodeBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeBatch(payload)
	if err != nil || len(back.Records) != 200 || back.Records[7].Message != batch.Records[7].Message {
		t.Fatalf("round trip: %v", err)
	}
	if len(payload) > 1000 {
		t.Fatalf("200 repetitive records queued as %d bytes; compression is not doing its job", len(payload))
	}
	if _, err := DecodeBatch([]byte(`{"batch_id":"plain","records":[]}`)); err != nil {
		t.Fatal("a plain JSON payload from before compression must still read")
	}
}

func TestAStepGivesAboutNinetyBars(t *testing.T) {
	now := time.Now()
	for _, span := range []time.Duration{time.Minute, 15 * time.Minute, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 90 * 24 * time.Hour} {
		step := StepFor(Range{From: now.Add(-span), To: now})
		if bars := span / step; bars > 90 || bars < 10 {
			t.Errorf("%s draws %d bars", span, bars)
		}
	}
}
