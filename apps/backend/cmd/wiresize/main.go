// Command wiresize measures what one SDK batch actually weighs (§4.1, §85).
//
// §85 asks for "compressed bytes/event" as a benchmark and §4.1 makes the whole HTTP design
// rest on batching, so the number deserves to be measured rather than assumed. It builds a
// representative Unity batch against the real §17 envelope and prints it four ways.
//
// Two things it is for:
//
//   - deciding whether request compression is worth implementing (§17, §19 step 3 specify
//     zstd with a gzip fallback, and nothing decompresses today);
//   - deciding whether a compact binary encoding would pay for itself, by showing how much
//     of a compressed event is syntax and how much is entropy that no encoding removes.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"

	"playersense.dev/backend/internal/archive"
	"playersense.dev/backend/internal/protocol"
)

func main() {
	fmt.Println("An SDK batch as the protocol recommends sending it: no event_id.")
	fmt.Println()
	for _, n := range []int{50, 500, 1000} {
		batch := build(n, idNone, contextNone)
		raw, _ := json.Marshal(batch)
		fmt.Printf("%d events\n", n)
		fmt.Printf("  json                %8d bytes  (%5.1f B/event)\n", len(raw), float64(len(raw))/float64(n))
		g := gz(raw)
		fmt.Printf("  json + gzip         %8d bytes  (%5.1f B/event)  %.2fx\n", len(g), float64(len(g))/float64(n), float64(len(raw))/float64(len(g)))
		z := zs(raw)
		fmt.Printf("  json + zstd         %8d bytes  (%5.1f B/event)  %.2fx\n", len(z), float64(len(z))/float64(n), float64(len(raw))/float64(len(z)))
		p := parquet(batch)
		fmt.Printf("  parquet + zstd      %8d bytes  (%5.1f B/event)  %.2fx\n", len(p), float64(len(p))/float64(n), float64(len(raw))/float64(len(p)))
		fmt.Println()
	}

	// Where the compressed bytes actually go. A v4 UUID is 16 bytes of entropy, so no
	// compressor can do much with it however it is spelled — which makes the *spelling* the
	// only lever, and the syntax around it almost free.
	// The context question (ADR 0028): a timeline once per batch, against a copy on every
	// event. The wire is only half the argument — §39 bills on normalized *logical* bytes,
	// so duplication multiplies what the studio is charged for one transmission.
	fmt.Println("500 events carrying 4 context values, json + zstd:")
	for _, v := range []struct {
		name string
		mode contextMode
	}{
		{"no context at all", contextNone},
		{"a timeline: one snapshot per batch", contextFrames},
		{"a copy on every event", contextPerEvent},
	} {
		batch := build(500, idNone, v.mode)
		raw, _ := json.Marshal(batch)
		z := zs(raw)
		fmt.Printf("  %-36s %6d raw  %5d gz  (%4.1f B/event)\n",
			v.name, len(raw), len(z), float64(len(z))/500)
	}
	fmt.Println()

	fmt.Println("500 events, json + zstd, by how event_id is spelled:")
	for _, v := range []struct {
		name string
		kind idKind
	}{
		{"uuid, 36 chars", idUUID},
		{"base64url of the same 16 bytes, 22 chars", idBase64},
		{"omitted — derived by the gateway (recommended)", idNone},
	} {
		raw, _ := json.Marshal(build(500, v.kind, contextNone))
		z := zs(raw)
		fmt.Printf("  %-44s %6d bytes  (%5.1f B/event)\n", v.name, len(z), float64(len(z))/500)
	}
}

type idKind int

const (
	idUUID idKind = iota
	idBase64
	idNone
)

func eventID(kind idKind) string {
	switch kind {
	case idBase64:
		u := uuid.New()
		return base64.RawURLEncoding.EncodeToString(u[:])
	case idNone:
		return ""
	default:
		return uuid.NewString()
	}
}

type contextMode int

const (
	contextNone contextMode = iota
	contextFrames
	contextPerEvent
)

var studioContext = map[string]any{
	"rank": "diamond", "chapter": 12, "map": "desert_03", "game_mode": "ranked",
}

func build(n int, kind idKind, ctx contextMode) *protocol.Batch {
	install := uuid.NewString()
	now := time.Now().UTC()
	b := &protocol.Batch{
		SchemaVersion: 1,
		BatchID:       uuid.NewString(),
		Environment:   "production",
		InstallID:     install,
		SDK:           protocol.SDK{Name: "unity", Version: "1.4.0"},
		App:           protocol.App{Version: "2.7.1", Build: "342"},
		Device:        protocol.Device{Platform: "ios", OSVersion: "18.2", Locale: "tr-TR"},
		Consent: protocol.Consent{
			Analytics: true, PolicyVersion: "2026-01-15",
			ATTStatus: "authorized", GDPRApplies: true, CollectedAt: now,
		},
	}
	if ctx == contextFrames {
		b.Context = []protocol.ContextFrame{{At: 0, Values: studioContext}}
	}

	for i := range n {
		properties := map[string]any{
			"level": i % 120, "difficulty": "normal",
			"duration_ms": 41230 + i, "retries": i % 4, "won": true,
		}
		if ctx == contextPerEvent {
			for key, value := range studioContext {
				properties[key] = value
			}
		}
		b.Events = append(b.Events, protocol.Event{
			EventID:    eventID(kind),
			EventName:  "level_completed",
			EventTime:  now.Add(time.Duration(i) * time.Second),
			SessionID:  "sess-8f21c0",
			PlayerID:   "player-91827364",
			Sequence:   int64(i),
			Properties: properties,
		})
	}
	return b
}

func gz(in []byte) []byte {
	var out bytes.Buffer
	w, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	w.Write(in)
	w.Close()
	return out.Bytes()
}

func zs(in []byte) []byte {
	var out bytes.Buffer
	w, _ := zstd.NewWriter(&out, zstd.WithEncoderLevel(zstd.SpeedDefault))
	w.Write(in)
	w.Close()
	return out.Bytes()
}

// parquet is the columnar-binary reference point: the same events in the format the archive
// already writes.
func parquet(b *protocol.Batch) []byte {
	org, project, env := uuid.NewString(), uuid.NewString(), uuid.NewString()
	rows := make([]archive.Row, 0, len(b.Events))
	for _, ev := range b.Events {
		props := map[string]int64{}
		strs := map[string]string{}
		bools := map[string]uint32{}
		for k, v := range ev.Properties {
			switch t := v.(type) {
			case int:
				props[k] = int64(t)
			case string:
				strs[k] = t
			case bool:
				if t {
					bools[k] = 1
				} else {
					bools[k] = 0
				}
			}
		}
		rows = append(rows, archive.Row{
			OrgSeq: 1, ProjectSeq: 7, EnvironmentSeq: 1,
			OrgID: org, ProjectID: project, EnvironmentID: env,
			RetentionTier: 30, PolicyTier: "standard",
			EventTime: ev.EventTime, IngestTime: ev.EventTime, ReceivedAt: ev.EventTime,
			EventID: ev.EventID, BatchID: b.BatchID, SessionID: ev.SessionID,
			PlayerID: ev.PlayerID, InstallID: b.InstallID, Sequence: ev.Sequence,
			EventName: ev.EventName, EventType: "custom",
			Platform: b.Device.Platform, CountryCode: "TR", AppVersion: b.App.Version,
			SDKName: b.SDK.Name, SDKVersion: b.SDK.Version,
			ConsentAnalytics: 1, ConsentPolicyVersion: b.Consent.PolicyVersion,
			ATTStatus:   b.Consent.ATTStatus,
			PropsString: strs, PropsInt: props, PropsBool: bools,
			PayloadBytes: 200,
		})
	}
	out, err := archive.Encode(rows)
	if err != nil {
		panic(err)
	}
	return out
}
