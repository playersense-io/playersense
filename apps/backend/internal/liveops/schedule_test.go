package liveops_test

import (
	"testing"
	"time"

	"playersense.dev/backend/internal/liveops"
)

func at(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func starts(windows []liveops.Window) []string {
	out := make([]string, len(windows))
	for i, window := range windows {
		out[i] = window.Start.UTC().Format("2006-01-02 15:04")
	}
	return out
}

func equal(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d windows %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("window %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// The case Hasan asked for: one button, active the 1st to the 5th of every month, and the
// studio does not create twelve events.
func TestTheFirstFiveDaysOfEveryMonth(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Monthly,
		Anchor:   at("2026-01-01T00:00:00Z"),
		Duration: 5 * 24 * time.Hour,
	}
	if err := schedule.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2026-05-01T00:00:00Z"))
	equal(t, starts(windows),
		"2026-01-01 00:00", "2026-02-01 00:00", "2026-03-01 00:00", "2026-04-01 00:00")

	// On during the window, off after it.
	if _, on := schedule.ActiveAt(at("2026-02-03T12:00:00Z")); !on {
		t.Error("the event is not on during its own window")
	}
	if _, on := schedule.ActiveAt(at("2026-02-06T12:00:00Z")); on {
		t.Error("the event is still on after its window closed")
	}

	// And the next one is the following month rather than tomorrow.
	next, found := schedule.NextAfter(at("2026-02-10T00:00:00Z"))
	if !found || next.Start.UTC().Format("2006-01-02") != "2026-03-01" {
		t.Errorf("next = %v, want 1 March", next.Start)
	}
}

// A day past the end of a month clamps rather than disappearing. Skipping February would be a
// silent absence nobody notices until a player asks why the offer never came.
func TestADayPastTheEndOfAMonthClamps(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Monthly,
		Anchor:   at("2026-01-31T18:00:00Z"),
		Duration: 6 * time.Hour,
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2026-05-01T00:00:00Z"))
	equal(t, starts(windows),
		"2026-01-31 18:00", "2026-02-28 18:00", "2026-03-31 18:00", "2026-04-30 18:00")
}

// Every weekend, as one window that crosses two midnights.
func TestAWeekendEventIsOneWindowNotThree(t *testing.T) {
	// 2026-01-02 is a Friday.
	schedule := liveops.Schedule{
		Mode:     liveops.Weekly,
		Anchor:   at("2026-01-02T18:00:00Z"),
		Duration: 54 * time.Hour, // Friday 18:00 to Monday 00:00
	}
	if err := schedule.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2026-01-29T00:00:00Z"))
	equal(t, starts(windows),
		"2026-01-02 18:00", "2026-01-09 18:00", "2026-01-16 18:00", "2026-01-23 18:00")

	if _, on := schedule.ActiveAt(at("2026-01-04T12:00:00Z")); !on {
		t.Error("not on during Sunday afternoon of its own weekend")
	}
	if _, on := schedule.ActiveAt(at("2026-01-06T12:00:00Z")); on {
		t.Error("still on during the following Tuesday")
	}
}

func TestSeveralWeekdays(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Weekly,
		Anchor:   at("2026-01-05T20:00:00Z"), // a Monday
		Duration: 2 * time.Hour,
		Weekdays: []time.Weekday{time.Monday, time.Wednesday},
	}

	windows := schedule.Occurrences(at("2026-01-05T00:00:00Z"), at("2026-01-19T00:00:00Z"))
	equal(t, starts(windows),
		"2026-01-05 20:00", "2026-01-07 20:00",
		"2026-01-12 20:00", "2026-01-14 20:00")
}

func TestADailyHappyHour(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Daily,
		Anchor:   at("2026-03-01T20:00:00Z"),
		Duration: 2 * time.Hour,
	}

	windows := schedule.Occurrences(at("2026-03-01T00:00:00Z"), at("2026-03-04T00:00:00Z"))
	equal(t, starts(windows), "2026-03-01 20:00", "2026-03-02 20:00", "2026-03-03 20:00")

	if _, on := schedule.ActiveAt(at("2026-03-02T21:00:00Z")); !on {
		t.Error("not on during happy hour")
	}
	if _, on := schedule.ActiveAt(at("2026-03-02T23:00:00Z")); on {
		t.Error("still on an hour after it closed")
	}
}

// A window running over midnight, a month end and a year end is one occurrence, not three
// special cases. This is what a duration buys over an end time.
func TestAWinterEventCrossesTheYear(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Yearly,
		Anchor:   at("2025-12-20T00:00:00Z"),
		Duration: 17 * 24 * time.Hour, // to 6 January
	}

	if _, on := schedule.ActiveAt(at("2026-01-02T12:00:00Z")); !on {
		t.Error("not on in January, during a window that started in December")
	}
	windows := schedule.Occurrences(at("2025-01-01T00:00:00Z"), at("2028-01-01T00:00:00Z"))
	equal(t, starts(windows), "2025-12-20 00:00", "2026-12-20 00:00", "2027-12-20 00:00")
}

// A one-off happens once, however far ahead somebody looks.
func TestAOneOffHappensOnce(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Once,
		Anchor:   at("2026-10-20T00:00:00Z"),
		Duration: 14 * 24 * time.Hour,
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2030-01-01T00:00:00Z"))
	equal(t, starts(windows), "2026-10-20 00:00")

	if _, found := schedule.NextAfter(at("2026-11-04T00:00:00Z")); found {
		t.Error("a one-off has a second occurrence")
	}
}

// Until stops the repetition, which is how an offer runs "every month until the end of the
// quarter" rather than forever.
func TestUntilStopsTheRepetition(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Monthly,
		Anchor:   at("2026-01-01T00:00:00Z"),
		Duration: 24 * time.Hour,
		Until:    at("2026-03-15T00:00:00Z"),
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2026-12-01T00:00:00Z"))
	equal(t, starts(windows), "2026-01-01 00:00", "2026-02-01 00:00", "2026-03-01 00:00")
}

// A daily 20:00 window opens at 20:00 local even on the day a clock moves. Adding twenty-four
// hours would drift it by an hour and the event would open at 19:00 for half the year.
func TestATimeOfDaySurvivesDaylightSaving(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Daily,
		Anchor:   at("2026-03-27T20:00:00Z"), // before Europe/Istanbul... uses a zone that shifts
		Duration: time.Hour,
		Timezone: "Europe/Berlin",
	}
	if err := schedule.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}

	// Germany moves its clocks on the last Sunday in March 2026 (the 29th).
	windows := schedule.Occurrences(at("2026-03-27T00:00:00Z"), at("2026-04-02T00:00:00Z"))
	if len(windows) < 4 {
		t.Fatalf("got %d windows, want at least 4", len(windows))
	}
	for _, window := range windows {
		local := window.Start.In(berlin)
		if local.Hour() != windows[0].Start.In(berlin).Hour() {
			t.Errorf("%s opens at %02d:00 local, want %02d:00 every day",
				local.Format("2006-01-02"), local.Hour(), windows[0].Start.In(berlin).Hour())
		}
	}
}

func TestAnInvalidScheduleIsRefusedWhenWritten(t *testing.T) {
	valid := liveops.Schedule{Mode: liveops.Daily, Anchor: at("2026-01-01T00:00:00Z"), Duration: time.Hour}

	cases := map[string]liveops.Schedule{
		"no mode":          {Anchor: valid.Anchor, Duration: time.Hour},
		"unknown mode":     {Mode: "fortnightly", Anchor: valid.Anchor, Duration: time.Hour},
		"no anchor":        {Mode: liveops.Daily, Duration: time.Hour},
		"no duration":      {Mode: liveops.Daily, Anchor: valid.Anchor},
		"ends before":      {Mode: liveops.Daily, Anchor: valid.Anchor, Duration: time.Hour, Until: at("2025-01-01T00:00:00Z")},
		"unknown timezone": {Mode: liveops.Daily, Anchor: valid.Anchor, Duration: time.Hour, Timezone: "Mars/Olympus"},
		// 30 hours daily would be on twice at once, which a player cannot observe.
		"overlaps itself":   {Mode: liveops.Daily, Anchor: valid.Anchor, Duration: 30 * time.Hour},
		"weekdays on daily": {Mode: liveops.Daily, Anchor: valid.Anchor, Duration: time.Hour, Weekdays: []time.Weekday{time.Monday}},
	}
	for name, schedule := range cases {
		if err := schedule.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("a valid schedule was refused: %v", err)
	}
}

// "Every X days" and "every X hours", which a game needs as much as a calendar date: a raid
// that opens every three days, an energy refill every six hours.
func TestEveryNDays(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Daily,
		Interval: 3,
		Anchor:   at("2026-01-01T12:00:00Z"),
		Duration: 12 * time.Hour,
	}
	if err := schedule.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2026-01-14T00:00:00Z"))
	equal(t, starts(windows),
		"2026-01-01 12:00", "2026-01-04 12:00", "2026-01-07 12:00",
		"2026-01-10 12:00", "2026-01-13 12:00")

	// The days in between are off, which is the whole point of an interval.
	if _, on := schedule.ActiveAt(at("2026-01-02T13:00:00Z")); on {
		t.Error("on during a day the interval skips")
	}
	if _, on := schedule.ActiveAt(at("2026-01-04T13:00:00Z")); !on {
		t.Error("off during a day the interval includes")
	}
}

func TestEveryNHours(t *testing.T) {
	schedule := liveops.Schedule{
		Mode:     liveops.Hourly,
		Interval: 6,
		Anchor:   at("2026-01-01T00:00:00Z"),
		Duration: time.Hour,
	}
	if err := schedule.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2026-01-02T00:00:00Z"))
	equal(t, starts(windows),
		"2026-01-01 00:00", "2026-01-01 06:00", "2026-01-01 12:00", "2026-01-01 18:00")

	if _, on := schedule.ActiveAt(at("2026-01-01T12:30:00Z")); !on {
		t.Error("off inside an hourly window")
	}
	if _, on := schedule.ActiveAt(at("2026-01-01T13:30:00Z")); on {
		t.Error("on after an hourly window closed")
	}
}

func TestEveryOtherWeekAndEveryThirdMonth(t *testing.T) {
	fortnightly := liveops.Schedule{
		Mode:     liveops.Weekly,
		Interval: 2,
		Anchor:   at("2026-01-05T00:00:00Z"), // a Monday
		Duration: 24 * time.Hour,
	}
	equal(t, starts(fortnightly.Occurrences(at("2026-01-05T00:00:00Z"), at("2026-02-16T00:00:00Z"))),
		"2026-01-05 00:00", "2026-01-19 00:00", "2026-02-02 00:00")

	quarterly := liveops.Schedule{
		Mode:     liveops.Monthly,
		Interval: 3,
		Anchor:   at("2026-01-15T00:00:00Z"),
		Duration: 7 * 24 * time.Hour,
	}
	equal(t, starts(quarterly.Occurrences(at("2026-01-01T00:00:00Z"), at("2027-01-01T00:00:00Z"))),
		"2026-01-15 00:00", "2026-04-15 00:00", "2026-07-15 00:00", "2026-10-15 00:00")
}

// An interval makes a longer window legal: every seven days lasting five is fine, every day
// lasting five is an event that is on twice at once.
func TestAnIntervalMakesALongerWindowLegal(t *testing.T) {
	overlapping := liveops.Schedule{
		Mode: liveops.Daily, Anchor: at("2026-01-01T00:00:00Z"), Duration: 5 * 24 * time.Hour,
	}
	if err := overlapping.Validate(); err == nil {
		t.Error("a five-day window repeating daily was accepted")
	}

	spaced := overlapping
	spaced.Interval = 7
	if err := spaced.Validate(); err != nil {
		t.Errorf("a five-day window repeating weekly was refused: %v", err)
	}
}

// Zero and one both mean "every", so a schedule written before intervals existed still means
// what it meant.
func TestNoIntervalMeansEvery(t *testing.T) {
	plain := liveops.Schedule{Mode: liveops.Daily, Anchor: at("2026-01-01T09:00:00Z"), Duration: time.Hour}
	explicit := plain
	explicit.Interval = 1

	from, to := at("2026-01-01T00:00:00Z"), at("2026-01-05T00:00:00Z")
	equal(t, starts(plain.Occurrences(from, to)), starts(explicit.Occurrences(from, to))...)
	if plain.Every() != 1 {
		t.Errorf("Every() = %d with no interval set", plain.Every())
	}
}

// An hourly cadence is elapsed time, not a time of day: "every six hours" must not gain or
// lose an occurrence on the day a clock moves.
func TestAnHourlyCadenceIgnoresTheClockChange(t *testing.T) {
	schedule := liveops.Schedule{
		Mode: liveops.Hourly, Interval: 6,
		Anchor:   at("2026-03-28T00:00:00Z"),
		Duration: time.Hour,
		Timezone: "Europe/Berlin",
	}

	// Two days across Germany's spring change: 8 windows, six hours apart, regardless.
	windows := schedule.Occurrences(at("2026-03-28T00:00:00Z"), at("2026-03-30T00:00:00Z"))
	if len(windows) != 8 {
		t.Fatalf("got %d windows across a clock change, want 8", len(windows))
	}
	for i := 1; i < len(windows); i++ {
		if gap := windows[i].Start.Sub(windows[i-1].Start); gap != 6*time.Hour {
			t.Errorf("gap %d is %s, want 6h", i, gap)
		}
	}
}

// An event created long ago must not cost more to evaluate than one created yesterday.
func TestAnOldAnchorIsJumpedToRatherThanWalked(t *testing.T) {
	schedule := liveops.Schedule{
		Mode: liveops.Hourly, Interval: 1,
		Anchor:   at("2020-01-01T00:00:00Z"), // ~53,000 hours before the window below
		Duration: 30 * time.Minute,
	}

	windows := schedule.Occurrences(at("2026-01-01T00:00:00Z"), at("2026-01-01T03:00:00Z"))
	equal(t, starts(windows), "2026-01-01 00:00", "2026-01-01 01:00", "2026-01-01 02:00")
}

// A one-off does not repeat, so an interval on one is a mistake worth naming rather than
// silently ignoring.
func TestAnIntervalOnAOneOffIsRefused(t *testing.T) {
	schedule := liveops.Schedule{
		Mode: liveops.Once, Interval: 3,
		Anchor: at("2026-01-01T00:00:00Z"), Duration: time.Hour,
	}
	if err := schedule.Validate(); err == nil {
		t.Error("an interval on a one-off was accepted")
	}

	negative := liveops.Schedule{
		Mode: liveops.Daily, Interval: -2,
		Anchor: at("2026-01-01T00:00:00Z"), Duration: time.Hour,
	}
	if err := negative.Validate(); err == nil {
		t.Error("a negative interval was accepted")
	}
}
