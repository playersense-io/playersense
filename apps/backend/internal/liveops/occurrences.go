package liveops

import "time"

// Occurrences returns every window that overlaps [from, to), in order.
//
// Overlaps rather than starts within: an event that began yesterday and runs for five days is
// on today, and a caller asking "what is on this week" means exactly that.
func (s Schedule) Occurrences(from, to time.Time) []Window {
	var found []Window
	if !to.After(from) {
		return nil
	}

	location := s.Location()
	anchor := s.Anchor.In(location)

	for cursor := s.firstCandidate(anchor, from, location); !cursor.IsZero(); cursor = s.advance(cursor, location) {
		if !s.Until.IsZero() && cursor.After(s.Until) {
			break
		}
		if cursor.After(to) || cursor.Equal(to) {
			break
		}

		window := Window{Start: cursor, End: cursor.Add(s.Duration)}
		if window.End.After(from) {
			found = append(found, window)
			if len(found) >= MaxOccurrences {
				break
			}
		}
		if s.Mode == Once {
			break
		}
	}
	return found
}

// ActiveAt returns the window covering an instant, if there is one.
func (s Schedule) ActiveAt(at time.Time) (Window, bool) {
	// One duration back, so a window that started before `at` and still covers it is found.
	for _, window := range s.Occurrences(at.Add(-s.Duration), at.Add(time.Nanosecond)) {
		if window.Contains(at) {
			return window, true
		}
	}
	return Window{}, false
}

// NextAfter returns the first window starting after an instant.
//
// Bounded rather than unbounded: two years covers every mode this package has, and a schedule
// whose next occurrence is further away than that has effectively ended.
func (s Schedule) NextAfter(at time.Time) (Window, bool) {
	for _, window := range s.Occurrences(at, at.AddDate(2, 0, 0)) {
		if window.Start.After(at) {
			return window, true
		}
	}
	return Window{}, false
}

// firstCandidate finds the first occurrence start at or before `from`, so a window already
// running is not missed.
//
// Jumps rather than steps. An hourly schedule anchored a year ago is nine thousand steps away,
// and walking to it would make a config request's cost depend on how long ago somebody created
// the event. So the number of elapsed periods is computed, the jump lands at or before `from`,
// and a short correction walk fixes the months of unequal length that arithmetic cannot.
func (s Schedule) firstCandidate(anchor, from time.Time, location *time.Location) time.Time {
	if s.Mode == Once || !from.After(anchor) {
		return anchor
	}

	cursor := s.jumpNear(anchor, from, location)

	// Deliberately land at or before `from` and walk forward: a jump that overshot would
	// hide an occurrence that is running right now.
	for i := 0; i < 64; i++ {
		next := s.advance(cursor, location)
		if next.IsZero() || !next.After(cursor) || next.After(from) {
			break
		}
		cursor = next
	}
	return cursor
}

// jumpNear lands on an occurrence at or before `from` without walking there.
//
// Approximate on purpose: months and years are not a fixed number of hours, so this gets close
// and firstCandidate's walk finishes the job. Undershooting is safe, overshooting is not,
// which is why every division rounds down and the result is clamped to the anchor.
func (s Schedule) jumpNear(anchor, from time.Time, location *time.Location) time.Time {
	every := s.Every()
	elapsed := from.Sub(anchor)

	switch s.Mode {
	case Hourly:
		step := time.Duration(every) * time.Hour
		periods := int(elapsed / step)
		if periods < 0 {
			return anchor
		}
		return anchor.Add(time.Duration(periods) * step)

	case Daily:
		days := int(elapsed.Hours()/24) / every * every
		if days < 0 {
			return anchor
		}
		return atTimeOfDay(anchor.AddDate(0, 0, days), anchor, location)

	case Weekly:
		weeks := int(elapsed.Hours()/(24*7)) / every * every
		if weeks < 0 {
			return anchor
		}
		return atTimeOfDay(anchor.AddDate(0, 0, weeks*7), anchor, location)

	case Monthly:
		months := (from.Year()-anchor.Year())*12 + int(from.Month()) - int(anchor.Month())
		months = months / every * every
		if months < 0 {
			return anchor
		}
		year, month := shiftMonth(anchor.Year(), anchor.Month(), months)
		return s.placeInMonth(anchor, year, month, location)

	case Yearly:
		years := (from.Year() - anchor.Year()) / every * every
		if years < 0 {
			return anchor
		}
		return s.placeInYear(anchor, anchor.Year()+years, location)
	}
	return anchor
}

// advance moves one occurrence forward.
func (s Schedule) advance(cursor time.Time, location *time.Location) time.Time {
	every := s.Every()
	anchor := s.Anchor.In(location)

	switch s.Mode {
	case Once:
		return time.Time{}

	case Hourly:
		// Added as elapsed time rather than rebuilt from parts: "every six hours" is a
		// cadence, not a time of day, so it is the one mode a clock change must not move.
		return cursor.Add(time.Duration(every) * time.Hour)

	case Daily:
		return atTimeOfDay(cursor.AddDate(0, 0, every), anchor, location)

	case Weekly:
		wanted := s.weekdaySet(cursor)
		// Up to a full interval of weeks ahead: with several weekdays the next one may be
		// tomorrow, and with an interval it may be a fortnight away.
		for i := 1; i <= 7*every+7; i++ {
			candidate := cursor.AddDate(0, 0, i)
			if !wanted[candidate.Weekday()] {
				continue
			}
			// Only weeks that are a whole number of intervals from the anchor's own
			// week count, or "every other Monday" would fire every Monday.
			if every > 1 && weekIndex(anchor, candidate, location)%every != 0 {
				continue
			}
			return atTimeOfDay(candidate, anchor, location)
		}
		return time.Time{}

	case Monthly:
		// The month is shifted arithmetically, never with AddDate. Adding one month to
		// 31 January gives 3 March, because Go normalises what February cannot hold —
		// so the cursor would skip February entirely and an event set for the end of
		// the month would quietly miss one every year.
		year, month := shiftMonth(cursor.In(location).Year(), cursor.In(location).Month(), every)
		return s.placeInMonth(anchor, year, month, location)

	case Yearly:
		return s.placeInYear(anchor, cursor.Year()+every, location)
	}
	return time.Time{}
}

// weekIndex counts whole weeks between two instants, measured from the anchor's week.
func weekIndex(anchor, candidate time.Time, location *time.Location) int {
	start := startOfWeek(anchor.In(location))
	return int(startOfWeek(candidate.In(location)).Sub(start).Hours() / (24 * 7))
}

func startOfWeek(at time.Time) time.Time {
	year, month, day := at.Date()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, at.Location())
	return midnight.AddDate(0, 0, -int(midnight.Weekday()))
}

// weekdaySet is which days a weekly schedule fires on, defaulting to the anchor's own.
func (s Schedule) weekdaySet(cursor time.Time) map[time.Weekday]bool {
	set := make(map[time.Weekday]bool, len(s.Weekdays))
	for _, weekday := range s.Weekdays {
		set[weekday] = true
	}
	if len(set) == 0 {
		set[s.Anchor.In(cursor.Location()).Weekday()] = true
	}
	return set
}

// placeInMonth puts the anchor's day and time into the month a candidate lands in.
//
// **A day past the end of a month clamps to its last day rather than skipping it.** Somebody
// who picks the 31st means the end of the month; skipping February would be a silent absence
// nobody notices until a player asks why the offer never appeared, while clamping is visible
// in the calendar and closer to what was meant.
//
// Rebuilt from parts because Go's AddDate normalises 31 February to 3 March, which is neither.
func (s Schedule) placeInMonth(anchor time.Time, year int, month time.Month, location *time.Location) time.Time {
	day := anchor.Day()
	if last := daysIn(year, month, location); day > last {
		day = last
	}
	return time.Date(year, month, day,
		anchor.Hour(), anchor.Minute(), anchor.Second(), 0, location)
}

// placeInYear is placeInMonth's yearly twin, and clamps 29 February in a common year.
func (s Schedule) placeInYear(anchor time.Time, year int, location *time.Location) time.Time {
	day := anchor.Day()
	if last := daysIn(year, anchor.Month(), location); day > last {
		day = last
	}
	return time.Date(year, anchor.Month(), day,
		anchor.Hour(), anchor.Minute(), anchor.Second(), 0, location)
}

// atTimeOfDay keeps a candidate's date and takes the anchor's clock time.
//
// Rebuilt from parts rather than added as a duration, so a day that is 23 or 25 hours long
// still opens the event at the hour a person chose. Adding 24 hours across a daylight-saving
// boundary would drift it.
func atTimeOfDay(candidate, source time.Time, location *time.Location) time.Time {
	year, month, day := candidate.In(location).Date()
	return time.Date(year, month, day,
		source.Hour(), source.Minute(), source.Second(), 0, location)
}

// shiftMonth moves a year and month forward by n months, carrying into the year.
func shiftMonth(year int, month time.Month, n int) (int, time.Month) {
	total := (year*12 + int(month) - 1) + n
	return total / 12, time.Month(total%12 + 1)
}

func daysIn(year int, month time.Month, location *time.Location) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, location).Day()
}
