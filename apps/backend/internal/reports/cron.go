package reports

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a five-field schedule: minute, hour, day of month, month, day of week. Each field is
// "*", a number, a range "a-b", a step "*/n" or "a-b/n", or a list of those; day of week is 0 to 7
// with both 0 and 7 Sunday. When both day fields are restricted a day matching either runs, as
// cron has always done.
type Cron struct {
	minute, hour, dom, month, dow [64]bool
	domAny, dowAny                bool
}

// ParseCron reads a cron line.
func ParseCron(line string) (Cron, error) {
	var c Cron
	fields := strings.Fields(line)
	if len(fields) != 5 {
		return c, fmt.Errorf("a schedule is five fields: minute, hour, day, month and weekday")
	}
	specs := []struct {
		into     *[64]bool
		lo, hi   int
		name     string
		wildcard *bool
	}{
		{&c.minute, 0, 59, "minute", nil}, {&c.hour, 0, 23, "hour", nil}, {&c.dom, 1, 31, "day", &c.domAny},
		{&c.month, 1, 12, "month", nil}, {&c.dow, 0, 7, "weekday", &c.dowAny},
	}
	for i, spec := range specs {
		if spec.wildcard != nil {
			*spec.wildcard = fields[i] == "*"
		}
		for _, part := range strings.Split(fields[i], ",") {
			if err := setField(spec.into, part, spec.lo, spec.hi); err != nil {
				return c, fmt.Errorf("the %s field %q: %w", spec.name, fields[i], err)
			}
		}
	}
	if c.dow[7] {
		c.dow[0] = true
	}
	return c, nil
}

func setField(into *[64]bool, part string, lo, hi int) error {
	rangePart, stepPart, hasStep := strings.Cut(part, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepPart)
		if err != nil || n < 1 {
			return fmt.Errorf("the step is not a positive number")
		}
		step = n
	}
	from, to := lo, hi
	if rangePart != "*" {
		a, b, isRange := strings.Cut(rangePart, "-")
		x, err := strconv.Atoi(a)
		if err != nil {
			return fmt.Errorf("%q is not a number", a)
		}
		from, to = x, x
		if isRange {
			y, err := strconv.Atoi(b)
			if err != nil {
				return fmt.Errorf("%q is not a number", b)
			}
			to = y
		} else if hasStep {
			to = hi
		}
	}
	if from < lo || to > hi || from > to {
		return fmt.Errorf("out of range %d to %d", lo, hi)
	}
	for v := from; v <= to; v += step {
		into[v] = true
	}
	return nil
}

func (c Cron) dayMatches(t time.Time) bool {
	dom, dow := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}

// Next is the first moment strictly after `after` the schedule runs, in its location. The zero
// time when it never does in the next five years (the 30th of February).
func (c Cron) Next(after time.Time, loc *time.Location) time.Time {
	t := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case !c.month[int(t.Month())]:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case !c.hour[t.Hour()]:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
		case !c.minute[t.Minute()]:
			t = t.Add(time.Minute)
		default:
			return t.UTC()
		}
	}
	return time.Time{}
}

// NextRun is the soonest any of a graph's running schedules fires after `after`; zero when none.
func (g Graph) NextRun(after time.Time) time.Time {
	var soonest time.Time
	for _, s := range g.Schedules() {
		c, err := ParseCron(s.Cron)
		if err != nil {
			continue
		}
		loc, err := time.LoadLocation(s.Timezone)
		if err != nil || s.Timezone == "" {
			loc = time.UTC
		}
		if next := c.Next(after, loc); !next.IsZero() && (soonest.IsZero() || next.Before(soonest)) {
			soonest = next
		}
	}
	return soonest
}
