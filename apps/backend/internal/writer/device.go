package writer

import (
	"context"
	"encoding/json"
	"time"

	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/valkey"
)

// DeviceState is what makes every cohort query a single-table GROUP BY (§29.5).
//
// These are the *sticky* dimensions: the values as of the device's first sighting, never
// the current ones. Because they never change, a device belongs to exactly one cohort
// bucket for its whole life, cohorts are disjoint, and plain counters sum correctly — no
// join, no dictionary, no uniqExact (§29, Appendix C.16).
//
// Stamping them at write time is the whole trick. Deriving them at read time is the join
// this design exists to avoid.
type DeviceState struct {
	CohortDate        string `json:"cohort_date"` // YYYY-MM-DD
	InstallPlatform   string `json:"install_platform"`
	InstallCountry    string `json:"install_country"`
	FirstAppVersion   string `json:"first_app_version"`
	AcquisitionSource string `json:"acquisition_source"`
}

// deviceKey matches events_raw's MATERIALIZED device_key exactly.
//
// The cohort follows the *device*, never the coalesced actor key (Appendix C.17): the two
// diverge the moment an anonymous player signs in, and a cohort that moved when someone
// logged in would be a cohort nobody could reason about.
func (w *Writer) deviceKey(msg *eventbus.Message, installID string) string {
	return w.cache.Key("device",
		itoa(uint64(msg.ProjectSeq)), itoa(uint64(msg.EnvironmentSeq)), installID)
}

// deviceState resolves the sticky dimensions for one batch.
//
// One lookup per batch, not per event: a batch is one flush from one installation, so
// every event in it shares a device.
//
// Three outcomes, and each is deliberate:
//
//   - **Hit** — the stored values are used, and the entry's life is extended in the same
//     round trip, so a device that keeps playing keeps its entry.
//   - **Miss** — this is the first batch this installation has sent. The entry is created
//     from it.
//   - **Valkey unavailable** — the state is returned empty and the row is stamped with
//     nulls. §65's rule is that losing Valkey costs speed rather than data, and the nightly
//     derivation backfills from player_lifetime, which is the authority.
//
// A device absent for longer than the entry's life is stamped as a new cohort here, and
// the nightly derivation corrects it from player_lifetime for the same reason. The
// write-time stamp is a fast path for fresh data, not the source of truth.
func (w *Writer) deviceState(ctx context.Context, msg *eventbus.Message, batch *ingest.NormalizedBatch) DeviceState {
	fresh := DeviceState{
		CohortDate:      firstEventDate(batch),
		InstallPlatform: batch.Platform,
		InstallCountry:  batch.CountryCode,
		FirstAppVersion: batch.AppVersion,
		// Acquisition source is empty under child_directed and kids_strict (§98), and is
		// not yet collected for anyone — the field exists so the cohort key's shape does
		// not change when it is.
		AcquisitionSource: "",
	}

	// §98: a kids_strict project has no persistent identifier, because ingest-api already
	// replaced it per batch. Caching device state for it would write one entry per batch
	// that nothing will ever read again — and, worse, would imply a linkability the tier
	// exists to prevent. The batch is its own cohort.
	if msg.PolicyTier == "kids_strict" {
		return fresh
	}

	key := w.deviceKey(msg, batch.InstallID)

	raw, err := w.cache.GetEx(ctx, key, w.deviceTTL)
	switch {
	case err != nil:
		// Unavailable. Stamp nulls and let the nightly job fill them in.
		w.stats.deviceLookupFailures.Add(1)
		return DeviceState{}
	case raw != nil:
		var stored DeviceState
		if err := json.Unmarshal(raw, &stored); err == nil && stored.CohortDate != "" {
			return stored
		}
		// A corrupt entry is treated as a miss and overwritten below.
	}

	if encoded, err := json.Marshal(fresh); err == nil {
		_ = w.cache.Set(ctx, key, encoded, w.deviceTTL)
	}
	return fresh
}

// firstEventDate is the cohort date for a device seen for the first time.
//
// The earliest *event* time rather than the arrival time: a batch spooled offline for two
// days and delivered on Wednesday belongs to Monday's cohort, and using arrival would put
// every offline player in the wrong one.
func firstEventDate(batch *ingest.NormalizedBatch) string {
	earliest := time.Time{}
	for i := range batch.Events {
		t := batch.Events[i].EventTime
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	if earliest.IsZero() {
		earliest = batch.ReceivedAt
	}
	return earliest.UTC().Format(time.DateOnly)
}

// tombstoneKey names a project whose data has been deleted (§61).
func tombstoneKey(cache *valkey.Client, projectID string) string {
	return cache.Key("project", "tombstoned", projectID)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
