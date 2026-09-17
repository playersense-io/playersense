// Package facts answers what PlayerSense already knows about a player, in one lookup.
//
// This is the half of §41's segmentation that cannot arrive with the request. "Whales",
// "paid users" and "returning users" are questions about a player's history, and a client
// cannot be asked them: a modified build would answer whatever it liked, which is the case
// ADR 0027 exists for. So the answers are derived from the player's own events and read
// back at request time.
//
// # Read through, do not preload
//
// The authority is ClickHouse — `player_lifetime_v` and `account_lifetime_v` — and reading one
// player from either is a **point lookup, not a scan**: both are ordered by
// `(project_seq, environment_seq, <surrogate>)`, so naming all three prunes to a granule.
// Measured on this machine at 5,000,000 rows: **2–3 ms**.
//
// That measurement replaced the first design, which preloaded every active player into Valkey
// overnight because a columnar point lookup was *assumed* to be the wrong shape. At 50M
// monthly-active installs across two subjects that is 19 GB of memory — measured at 256 bytes
// a record — to serve a population most of whom never request a config at all.
//
// So the cache is read-through and holds only players who actually asked for something. That
// is a daily-active population rather than a monthly-active one, and it trims itself: about
// 1.9 GB at 5M DAU instead of 19 GB.
//
// # The shape of the flexibility
//
// The vocabulary is closed and ours; the thresholds are open and the studio's. A record holds
// the same eighteen numbers for every player, and "whale" is a rule one studio writes as
// `>= $100` and the next writes as `>= $1000`. That is what lets an organisation define its
// own segments without any of them costing a query.
package facts

import (
	"context"

	"playersense.dev/backend/internal/targeting"
)

// Record is one subject's history, as of the last load.
//
// Two subjects, held apart on purpose (ADR 0004, §29.2): the install is what
// `player_lifetime` has always keyed on, and the account is the person across their
// installations. Somebody with $60 on a phone and $60 on a tablet is not an install whale and
// is an account whale, and a rule has to be able to say which it meant.
//
// Durations are days and money is micros, because those are the units the rules are written
// in and converting at read time would be a conversion per request.
type Record struct {
	SpendMicros int64 `json:"spend,omitempty"`
	Purchases   int64 `json:"purchases,omitempty"`

	// Nil when the player has never bought (ADR 0031). "Has not bought in 30 days" and "has
	// never bought" are different audiences — a win-back offer and a first-purchase offer —
	// and a zero would have merged them.
	DaysSinceFirstPurchase *int64 `json:"first_purchase_days,omitempty"`
	DaysSinceLastPurchase  *int64 `json:"last_purchase_days,omitempty"`

	DaysActive      int64 `json:"days_active,omitempty"`
	Sessions        int64 `json:"sessions,omitempty"`
	PlaytimeSeconds int64 `json:"playtime,omitempty"`
	DaysSinceSeen   int64 `json:"last_seen_days,omitempty"`
	DaysSinceFirst  int64 `json:"install_days,omitempty"`
}

// Context renders a record into the reserved namespace a rule names.
//
// `subject` is targeting.NamespaceCommerce's middle segment — "install" or "player" — so one
// record renders into either family without the caller assembling key names by hand.
//
// An absent purchase date is an absent key, never a zero. §41's rules distinguish "we do not
// know" from "we know it is zero", and a rule written against the wrong one silently never
// fires.
func (r Record) Context(subject string, into targeting.Context) {
	into["commerce."+subject+".lifetime_spend_micros"] = r.SpendMicros
	into["commerce."+subject+".purchases"] = r.Purchases
	if r.DaysSinceFirstPurchase != nil {
		into["commerce."+subject+".days_since_first_purchase"] = *r.DaysSinceFirstPurchase
	}
	if r.DaysSinceLastPurchase != nil {
		into["commerce."+subject+".days_since_last_purchase"] = *r.DaysSinceLastPurchase
	}

	into["activity."+subject+".days_active"] = r.DaysActive
	into["activity."+subject+".sessions"] = r.Sessions
	into["activity."+subject+".playtime_seconds"] = r.PlaytimeSeconds
	into["activity."+subject+".days_since_last_seen"] = r.DaysSinceSeen
	into["activity."+subject+".days_since_install"] = r.DaysSinceFirst
}

// Subjects, as they appear in a key name.
const (
	SubjectInstall = "install"
	SubjectPlayer  = "player"
)

// Store answers what is known about one subject.
//
// An interface because the config path reads it and a test should not need Valkey to assert
// what a rule does with a whale — the interesting assertions in this package are about
// thresholds and absence, and neither is about a cache.
type Store interface {
	// Get returns a record and whether one was found. A store that cannot answer returns
	// (zero, false) rather than an error: see Cache.Get.
	Get(ctx context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier string) (Record, bool)
}
