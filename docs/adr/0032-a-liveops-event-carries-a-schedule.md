# 0032. A LiveOps event carries a schedule, and the server evaluates it

**Status:** Accepted
**Date:** 2026-09-18
**Relates to:** project.md §41, §42, §45; [ADR 0027](0027-targeting-is-evaluated-on-the-server.md)

## Context

§45 described a LiveOps **campaign** with `start_at` and `end_at`, and said "workers publish a
new compiled config version at campaign boundaries". Two problems, one of vocabulary and one
of shape.

**The word.** A studio does not think in campaigns; it thinks in events — a double-XP weekend,
a holiday shop, an offer that runs the first five days of every month. "Campaign" is marketing
vocabulary borrowed for a thing the game shows a player, and PlayerSense takes no MMP feed and
has no ad-campaign or ad-set dimension — so the word arrives in the product carrying a meaning
nothing here implements, which is how a dashboard label becomes a support question about
attribution that has no answer.

**The shape.** Two timestamps express exactly one window. "The first five days of every month"
is not expressible, so a studio would create twelve events a year by hand — and the failure
mode of that is not an error, it is December quietly missing.

## Decision

The entity is **`LiveOpsEvent`**, never `Event`: `events_raw`, `event_name` and `event_time`
mean analytics events throughout the codebase, and two things called Event in one product is a
dashboard section nobody can name. The permission namespace, the package and the table are all
spelled `liveops`.

`starts_at`/`ends_at` are replaced by one `schedule` field, `internal/liveops.Schedule`:
**an anchor, a duration, and how it repeats.** Every shape a game needs is a case of it.

| what a studio asks for | mode | anchor | duration | interval |
|---|---|---|---|---|
| the first five days of every month | monthly | day 1, 00:00 | 5 days | |
| every weekend | weekly | Friday 18:00 | 54 hours | |
| every three days | daily | a time of day | | 3 |
| every six hours | hourly | | | 6 |
| the winter event | yearly | 20 December | 17 days | |

A **duration rather than an end time** is what makes a window crossing midnight, a month or a
year ordinary instead of three special cases.

**The published document carries the schedule, not expanded windows, and the server evaluates
it on the request it is already serving.** The response carries concrete instants, so the SDK
compares two numbers and holds no calendar — which also means a studio changing "every Friday"
to "every Saturday" does not need players to update the game.

There is therefore **no worker publishing at event boundaries**. That job is deleted, not
rewritten.

`status` loses `scheduled`, `live` and `ended` and becomes `draft`/`active`/`paused`/
`archived`. Every removed value was a lie waiting to happen: an event is live when its calendar
says so, and storing the word means a cron job writing rows at midnight to keep it true. A
recurring event is never ended at all.

## Consequences

Evaluation moves from publish time to request time, so it is paid per request per event rather
than once. `BenchmarkActiveAt` puts it at **66–658 ns** for schedules anchored six years back,
against a config path already spending milliseconds on the network. Twenty events in a project
is ~13 µs.

Two things had to be true for that number to hold, and only one of them was:

- Walking from the anchor had to be **jumped, not stepped**. An hourly schedule anchored in
  2020 is fifty-three thousand periods from today; the elapsed count is computed and jumped,
  with a short walk to correct what month arithmetic cannot.
- `time.LoadLocation` had to be **cached**, which the standard library does not do. The same
  weekly schedule cost 193 ns in UTC and **12.4 µs and 5.3 KB of garbage** in
  `Europe/Istanbul` — sixty times the work, all of it re-reading a zone file that changes
  twice a year. A `sync.Map` of successful loads brought it to 658 ns and 624 B. Without that
  cache this decision would have been wrong.

The timezone is the **studio's**, not the player's. A first-of-the-month event in player-local
time starts twenty-six hours apart across the world, which means the server cannot compute one
window and the document would need one per zone. Some games genuinely want that; it is
deliberately not built, and this is the paragraph to reread when one asks.

A recurring window longer than its own period is refused when written, because an event on
twice at once has no meaning a player can observe. The interval is what makes "every seven
days, lasting five" legal where "every day, lasting five" is not.

## Alternatives considered

**An iCalendar RRULE.** A game needs five shapes; RRULE expresses five hundred. The dashboard
would expose the five anyway, so storing the general form buys a parser no UI can reach and a
class of schedule nobody can render — and the first support ticket is somebody who typed
`BYSETPOS=-1` into a text box.

**Expanding windows at publish time into the document.** Requires choosing a horizon, and a
horizon requires something to republish before it runs out: exactly the cron job §45 described
and this decision removes. A frozen answer is also wrong the moment the job does not run,
which is the failure mode that is invisible until a holiday.

**Twelve events a year, created by hand.** The status quo. It works until somebody is on
holiday in December.

**Keeping `starts_at`/`ends_at` beside a schedule**, for the one-off case. Two ways to say
when an event is on is two code paths at evaluation, and `Once` is already a mode.

## Enforcement

`liveops.Schedule.Validate` refuses a schedule that overlaps itself, repeats backwards, ends
before it begins or names a zone tzdata does not know. `liveconfig.Compile` calls it, so a
document that compiles is a document that evaluates — `TestAnImpossibleScheduleIsRefused`.

`internal/liveops/schedule_test.go` covers the calendar cases that arithmetic gets wrong: a
day past the end of a month clamps rather than skipping February, a daily window keeps its
time of day across a clock change, an hourly one does not, and an old anchor does not cost
more than a new one. `TestAMonthlyEventSurvivesPublishing` is Hasan's own example end to end.

Each was confirmed to have teeth by breaking the behaviour and watching the test fail.
