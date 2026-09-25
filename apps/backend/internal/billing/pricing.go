package billing

import (
	"fmt"
	"strconv"
	"strings"
)

// Prices, as data.
//
// §37's rule applies to prices exactly as it applies to allowances: "do not hardcode commercial
// values in application logic". A rate card is a row, and what this file owns is only the
// arithmetic that turns a card and a count into money — the same split Limits makes between a
// closed vocabulary here and the amounts in the database.
//
// # A price is charged per unit, as the unit arrives
//
// Charging is real time (ADR 0039): the tenth thousand player of the month is priced the moment
// they are first seen, at the tier their *position* in the month falls in. That is why every
// function here is a function of a count rather than of an invoice. The charge for k new units is
// always
//
//	Total(n + k) − Total(n)
//
// and never "k × the current price". The difference is not cosmetic: it is what makes a batch that
// straddles a tier boundary price each side of the boundary correctly, what makes a volume card's
// retroactive discount fall out of the same formula, and what guarantees that a month's charges sum
// to exactly Total(month's count) however the arrivals were split — there is no rounding to drift,
// because every total is integer micros.

// Meter is what a rate card prices.
type Meter string

// TrackedPlayers is §36's primary meter: distinct players seen in a calendar month.
const TrackedPlayers Meter = "tracked_players"

// Cloud Logging's two meters (ADR 0050). Each is priced by its own rate cards, per unit, as the
// month's units arrive, exactly as players are: Total(n + k) - Total(n).
//
// Two, because a log costs twice. Taking it in costs the queue, the CPU that bounds and groups it,
// and the object-store writes that land its parts: all of it proportional to the bytes that arrive,
// once. Keeping it costs disk and object storage for as long as the studio chooses to keep it. One
// price for both would either overcharge a studio that keeps a day or undercharge one that keeps
// ninety; two let a retention choice be exactly what it costs.
const (
	// LogIngest counts megabytes (10^6 bytes) of logs taken in during a month, as the game sent
	// them, uncompressed. A megabyte rather than a gigabyte so a unit price in integer micros can
	// say "$0.10 a gigabyte" exactly: 100 micros a megabyte.
	LogIngest Meter = "log_ingest_mb"

	// LogStorage counts gigabyte-days: a gigabyte kept for a day is one. A month's gigabyte-days
	// divided by its days are its GB-months, which is how a pricing page states it.
	LogStorage Meter = "log_storage_gb_days"
)

// Meters is every meter a rate card can price.
func Meters() []Meter { return []Meter{TrackedPlayers, LogIngest, LogStorage} }

// Mode is how a card's tiers combine.
type Mode string

const (
	// Graduated prices each unit at the tier its position falls in. The 150,000th player of
	// the month costs the second tier's price and the first 100,000 keep costing the first
	// tier's. A month's bill only ever grows.
	Graduated Mode = "graduated"

	// Volume prices every unit at the tier the month's *total* has reached. Crossing a
	// boundary therefore reprices the whole month downward, which in real time is a credit —
	// and it has a cliff: with 100,000 at $0.0005 and above at $0.0004, the 100,001st player
	// makes the month cheaper than the 100,000th did. Supported because it is a real pricing
	// model; not the default, because of that cliff.
	Volume Mode = "volume"
)

// MaxTiers is a typo guard, not a policy. Nobody sells twenty-one price bands.
const MaxTiers = 20

// MaxUnitPrice bounds a single unit's price, and exists for arithmetic rather than commerce.
//
// $100 per unit. With counts bounded by what a month can hold, it keeps unit × count inside an
// int64 of micros with orders of magnitude to spare, so no multiplication here can overflow into a
// negative bill.
const MaxUnitPrice int64 = 100 * Micros

// maxUnits bounds a count for the same reason: ten billion distinct players in a month.
const maxUnits int64 = 10_000_000_000

// Tier is one price band.
//
// UpTo is the **inclusive** position the band ends at, counted from the first unit of the month;
// nil is "and everything above", which only the last band may be. Positions rather than widths,
// because "0–100k at $0.0005, 100k–500k at $0.0004" is how a pricing page states it and a person
// copying from the page must not have to subtract.
type Tier struct {
	UpTo            *int64 `json:"up_to,omitempty"`
	UnitPriceMicros int64  `json:"unit_price_micros"`
}

// Tiers is a card's bands, lowest first.
type Tiers []Tier

// TierError names what is wrong with a set of tiers, so the admin form can put it beside the
// field rather than answer with a 500.
type TierError struct{ Reason string }

func (e *TierError) Error() string { return "the tiers cannot be priced: " + e.Reason }

// Validate refuses a set of tiers that cannot price every count exactly one way.
//
// Every rule here is a way a card could quietly price something wrong: a gap between bands leaves
// units with no price, an overlap gives them two, and a bounded last band leaves the busiest
// customers unpriced — which in a real-time charger means the most valuable players are free.
func (t Tiers) Validate() error {
	if len(t) == 0 {
		return &TierError{"a card needs at least one tier"}
	}
	if len(t) > MaxTiers {
		return &TierError{fmt.Sprintf("%d tiers is more than %d", len(t), MaxTiers)}
	}
	var previous int64
	for i, tier := range t {
		if tier.UnitPriceMicros < 0 {
			return &TierError{fmt.Sprintf("tier %d has a negative price", i+1)}
		}
		if tier.UnitPriceMicros > MaxUnitPrice {
			return &TierError{fmt.Sprintf("tier %d costs %s per unit, above the %s ceiling",
				i+1, UnitPrice(tier.UnitPriceMicros), UnitPrice(MaxUnitPrice))}
		}
		last := i == len(t)-1
		switch {
		case tier.UpTo == nil && !last:
			return &TierError{fmt.Sprintf("tier %d is unbounded but is not the last", i+1)}
		case tier.UpTo != nil && last:
			return &TierError{"the last tier must be unbounded, or the largest customers have no price"}
		case tier.UpTo != nil && *tier.UpTo <= previous:
			return &TierError{fmt.Sprintf("tier %d ends at %d, which is not above where tier %d ended (%d)",
				i+1, *tier.UpTo, i, previous)}
		}
		if tier.UpTo != nil {
			previous = *tier.UpTo
		}
	}
	return nil
}

// Card is a rate card as the arithmetic sees it: the row, without the row.
type Card struct {
	Mode  Mode
	Tiers Tiers
}

// Validate checks the mode and the tiers together.
func (c Card) Validate() error {
	if c.Mode != Graduated && c.Mode != Volume {
		return &TierError{fmt.Sprintf("%q is not a pricing mode; want graduated or volume", c.Mode)}
	}
	return c.Tiers.Validate()
}

// TierAt returns the index of the band that position n falls in, counting from 1.
func (c Card) TierAt(n int64) int {
	for i, tier := range c.Tiers {
		if tier.UpTo == nil || n <= *tier.UpTo {
			return i
		}
	}
	return len(c.Tiers) - 1
}

// Total is what the first n units of a month cost, with the first `included` free.
//
// Included units still occupy positions. A plan including 10,000 players does not move the 100,000
// boundary to 110,000: the pricing page says "100,000 to 500,000 at $0.0004", and the 100,001st
// player is in that band however many before them were free.
//
// Callers must pass a validated card; an unvalidated one may panic on an empty tier list.
func (c Card) Total(n, included int64) int64 {
	n = min(max(n, 0), maxUnits)
	included = max(included, 0)
	if n <= included {
		return 0
	}
	if c.Mode == Volume {
		return c.Tiers[c.TierAt(n)].UnitPriceMicros * (n - included)
	}

	// Graduated: each band charges for the part of (included, n] that falls inside it.
	var total, lower int64 // lower is the last position of the previous band
	for _, tier := range c.Tiers {
		upper := n
		if tier.UpTo != nil {
			upper = min(*tier.UpTo, n)
		}
		from := max(lower, included) // exclusive
		if upper > from {
			total += (upper - from) * tier.UnitPriceMicros
		}
		if tier.UpTo == nil || *tier.UpTo >= n {
			break
		}
		lower = *tier.UpTo
	}
	return total
}

// Charge is what units (from, to] cost: Total(to) − Total(from).
//
// Negative only on a volume card whose count just crossed into a cheaper band, and that is a
// credit rather than an error — the customer has already paid the higher price for every unit
// before this one.
func (c Card) Charge(from, to, included int64) int64 {
	return c.Total(to, included) - c.Total(from, included)
}

// UnitPrice renders a per-unit price, which is routinely smaller than a cent.
//
// USD rounds to cents because a balance is read in cents; a unit price read that way would render
// $0.0005 as $0.00, which is the one number a pricing page must not get wrong. Trailing zeroes are
// dropped, so the page reads "$0.0004" rather than "$0.000400".
func UnitPrice(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}
	whole := micros / Micros
	frac := strings.TrimRight(fmt.Sprintf("%06d", micros%Micros), "0")
	if len(frac) < 2 {
		frac += strings.Repeat("0", 2-len(frac))
	}
	return sign + "$" + strconv.FormatInt(whole, 10) + "." + frac
}
