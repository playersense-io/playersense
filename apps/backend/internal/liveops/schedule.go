// Package liveops schedules what players see and when.
//
// A LiveOps **event** is a thing that happens in a game on a calendar: a double-XP weekend, a
// holiday shop, an offer that runs the first five days of every month. Deliberately not called
// a campaign — a studio thinks in events, and the word also has to survive being read next to
// `events_raw`, which is something else entirely, so the type is `LiveOpsEvent` and never just
// `Event`.
//
// # One anchor and one duration
//
// Every schedule is "it starts *here* and lasts *this long*", where "here" repeats. That is
// the whole model, and it was chosen over an iCalendar RRULE because a game needs five shapes
// and RRULE can express five hundred — a dashboard would expose the five anyway, so storing
// the general form would be a parser nobody's UI can reach.
//
//	first five days of every month   monthly, day 1, 00:00, 5 days
//	every weekend                    weekly, Friday, 18:00, 54 hours
//	happy hour                       daily, 20:00, 2 hours
//	the winter event                 yearly, 20 December, 17 days
//	a one-off                        once, an absolute instant, 14 days
//
// A duration rather than an end time is what makes a window crossing midnight, a month or a
// year ordinary rather than a special case.
//
// # The client never evaluates a schedule
//
// The server computes concrete windows and the compiled document carries timestamps, so an
// SDK compares two numbers and holds no calendar. That keeps recurrence in one place with one
// set of tests, and it is why a studio changing "every Friday" to "every Saturday" does not
// need every player to have updated their game.
package liveops

import (
	"fmt"
	"time"
)

// Mode is how a schedule repeats.
type Mode string

const (
	// Once is a single window at an absolute instant.
	Once Mode = "once"
	// Hourly repeats on a clock interval — every six hours, every ninety minutes if the
	// interval is set in hours. The only mode measured in elapsed time rather than in
	// calendar days, which is why it is the one daylight saving does not shift.
	Hourly Mode = "hourly"
	// Daily repeats every day at a time of day.
	Daily Mode = "daily"
	// Weekly repeats on chosen weekdays.
	Weekly Mode = "weekly"
	// Monthly repeats on a day of the month.
	Monthly Mode = "monthly"
	// Yearly repeats on a date.
	Yearly Mode = "yearly"
)

// MaxOccurrences bounds what one query may expand to, so a daily event asked about a decade
// cannot allocate for four thousand windows.
const MaxOccurrences = 366

// Schedule says when an event is on.
type Schedule struct {
	Mode Mode `json:"mode"`

	// Anchor is the first instant the event is on. For a recurring schedule its date parts
	// are what recurs — Monthly reads its day, Weekly its weekday, Yearly its month and
	// day — and its clock time is the time of day every occurrence opens at.
	Anchor time.Time `json:"anchor"`

	// Duration is how long one occurrence lasts. Five days for the first-five-days case,
	// two hours for a happy hour.
	Duration time.Duration `json:"duration"`

	// Interval is how many periods to skip: 3 with Daily is every third day, 2 with Weekly
	// every other week, 3 with Monthly quarterly, 6 with Hourly every six hours.
	//
	// Zero and one both mean "every", so a schedule written before this existed still means
	// what it meant. A separate field rather than five more modes, because "every third
	// day" and "every day" differ by a number rather than in kind.
	Interval int `json:"interval,omitempty"`

	// Until bounds the repetition. Zero means it repeats until somebody stops it, which is
	// what an evergreen offer is.
	Until time.Time `json:"until,omitempty"`

	// Weekdays chooses which days a Weekly schedule fires on. Empty means the anchor's own
	// weekday, so "every Friday" needs nothing said.
	Weekdays []time.Weekday `json:"weekdays,omitempty"`

	// Timezone is an IANA name. Empty means UTC.
	//
	// The studio's zone, not the player's. A "first of the month" event in player-local
	// time starts twenty-six hours apart across the world, which means the server cannot
	// compute one window and the document would need one per zone. That is a real product
	// choice some games want and it is deliberately not built: see Location.
	Timezone string `json:"timezone,omitempty"`
}

// Window is one concrete occurrence.
type Window struct {
	Start time.Time
	End   time.Time
}

// Contains reports whether an instant falls inside. Start is inclusive and End exclusive, so
// two back-to-back occurrences never both claim the same moment.
func (w Window) Contains(at time.Time) bool {
	return !at.Before(w.Start) && at.Before(w.End)
}

// Location resolves the schedule's timezone.
//
// An unknown name falls back to UTC rather than failing. A schedule is validated when it is
// saved, so an unknown zone here means the tzdata on this machine differs from the one that
// accepted it — and an event running in the wrong hour is a smaller failure than a config
// endpoint that refuses to serve anything.
func (s Schedule) Location() *time.Location {
	if s.Timezone == "" {
		return time.UTC
	}
	location, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.UTC
	}
	return location
}

// Validate checks a schedule when it is written.
func (s Schedule) Validate() error {
	switch s.Mode {
	case Once, Hourly, Daily, Weekly, Monthly, Yearly:
	case "":
		return fmt.Errorf("a schedule needs a mode")
	default:
		return fmt.Errorf("unknown schedule mode %q", s.Mode)
	}

	if s.Anchor.IsZero() {
		return fmt.Errorf("a schedule needs a start")
	}
	if s.Duration <= 0 {
		return fmt.Errorf("a schedule needs a duration")
	}
	if !s.Until.IsZero() && !s.Until.After(s.Anchor) {
		return fmt.Errorf("the schedule ends before it begins")
	}
	if s.Interval < 0 {
		return fmt.Errorf("an interval of %d repeats backwards", s.Interval)
	}
	if s.Interval > 0 && s.Mode == Once {
		return fmt.Errorf("a one-off does not repeat, so an interval means nothing")
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("unknown timezone %q", s.Timezone)
		}
	}

	// A recurring window longer than its own period would overlap itself, and an event
	// that is on twice at once has no meaning a player could observe.
	if period := s.period(); period > 0 && s.Duration > period {
		return fmt.Errorf(
			"a %s occurrence lasting %s overlaps the next one; shorten it or change the repeat",
			s.Mode, s.Duration)
	}
	for _, weekday := range s.Weekdays {
		if weekday < time.Sunday || weekday > time.Saturday {
			return fmt.Errorf("%d is not a weekday", weekday)
		}
	}
	if s.Mode != Weekly && len(s.Weekdays) > 0 {
		return fmt.Errorf("weekdays only mean something for a weekly schedule")
	}
	return nil
}

// Every returns the interval, treating zero as one.
//
// One accessor rather than the same `if interval == 0` at four call sites, which is where a
// schedule that silently never advances comes from.
func (s Schedule) Every() int {
	if s.Interval < 1 {
		return 1
	}
	return s.Interval
}

// period is the shortest gap between two occurrences, or zero when there is no fixed one.
//
// Monthly and yearly have no constant period — February and a leap year — so they are checked
// against their shortest possible gap rather than an average. The interval multiplies it,
// which is what makes "every seven days, lasting five" legal where "every day, lasting five"
// is not.
func (s Schedule) period() time.Duration {
	every := time.Duration(s.Every())

	switch s.Mode {
	case Hourly:
		return every * time.Hour
	case Daily:
		return every * 24 * time.Hour
	case Weekly:
		if len(s.Weekdays) > 1 {
			// Several days a week: the tightest gap is a day, whatever the interval,
			// because two chosen weekdays can be adjacent.
			return 24 * time.Hour
		}
		return every * 7 * 24 * time.Hour
	case Monthly:
		return every * 28 * 24 * time.Hour
	case Yearly:
		return every * 365 * 24 * time.Hour
	default:
		return 0
	}
}
