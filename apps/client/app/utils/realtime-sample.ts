// Sample data for the Realtime page, generated in the browser and advanced on a timer.
// Shaped after what the page will poll once the API client is wired in:
//
//   POST /v1/organizations/{org}/projects/{project}/analytics/production/metrics/query
//     for the selected project, interval=minute over the last 30 minutes — `events_1m`,
//     one row per minute and event name: the count, approximate unique players, and
//     revenue for revenue events. Events are visible within about ten seconds at p95
//     (project.md, "event visibility in realtime analytics"), so polling faster than that
//     shows nothing new.
//
// There is no raw-event query path yet, so the page shows minutes and event names, never a
// stream of individual events.

export const REALTIME_WINDOW = 30

// `pace` is today against yesterday at the same minute, so the rows move by different
// amounts rather than all by the project's.
export const REALTIME_EVENTS = [
  { name: 'session_start', weight: 0.14, pace: 1.08 },
  { name: 'level_complete', weight: 0.21, pace: 1.12 },
  { name: 'match_end', weight: 0.17, pace: 0.96 },
  { name: 'ad_impression', weight: 0.24, pace: 1.19 },
  { name: 'item_equip', weight: 0.11, pace: 1.03 },
  { name: 'tutorial_step', weight: 0.07, pace: 1.31 },
  { name: 'purchase', weight: 0.006, pace: 1.1 }
] as const

export type RealtimeEvent = typeof REALTIME_EVENTS[number]['name']

export interface RealtimeMinute {
  /** Start of the minute. */
  at: number
  events: Record<RealtimeEvent, number>
  /** Approximate unique players seen in the minute. */
  players: number
  revenue: number
}

/** Events per minute for the project, at the time of day `at` falls on. */
function rateAt(at: number): number {
  const hour = new Date(at).getHours() + new Date(at).getMinutes() / 60
  // Busiest in the evening, quietest before dawn.
  const daily = 0.65 + 0.35 * Math.sin((hour - 13) / 24 * Math.PI * 2)
  return 15_000 * daily
}

function emptyMinute(at: number): RealtimeMinute {
  return {
    at,
    events: Object.fromEntries(REALTIME_EVENTS.map(event => [event.name, 0])) as Record<RealtimeEvent, number>,
    players: 0,
    revenue: 0
  }
}

/**
 * Adds `share` of a minute's worth of events to `minute`: 1 fills a whole minute, 2/60 is
 * two seconds of it. Noisy, so consecutive ticks and minutes differ. `past` draws the
 * minute as yesterday had it, each row slower by its own pace.
 */
export function fillMinute(minute: RealtimeMinute, share: number, past = false): void {
  const total = rateAt(minute.at) * share * (0.85 + Math.random() * 0.3)
  for (const event of REALTIME_EVENTS) {
    const pace = past ? 1 / event.pace : 1
    minute.events[event.name] += Math.round(total * event.weight * pace * (0.8 + Math.random() * 0.4))
  }
  // Roughly six events per player per minute, so the minute's players so far.
  const events = Object.values(minute.events).reduce((sum, count) => sum + count, 0)
  minute.players = Math.round(events / 6)
  const purchases = past ? 1 / REALTIME_EVENTS[REALTIME_EVENTS.length - 1]!.pace : 1
  minute.revenue += Math.round(total * 0.006 * purchases * (0.8 + Math.random() * 0.4)) * (4 + Math.random() * 6)
}

/** One whole minute starting at `at`. */
export function sampleMinute(at: number, past = false): RealtimeMinute {
  const minute = emptyMinute(at)
  fillMinute(minute, 1, past)
  return minute
}

function minuteStart(time: number): number {
  return Math.floor(time / 60_000) * 60_000
}

/** The last `REALTIME_WINDOW` minutes, the current one filled as far as `now`. */
export function sampleWindow(now: number): RealtimeMinute[] {
  const current = minuteStart(now)
  const minutes: RealtimeMinute[] = []
  for (let i = REALTIME_WINDOW - 1; i >= 0; i--) {
    const at = current - i * 60_000
    const minute = emptyMinute(at)
    fillMinute(minute, i === 0 ? (now - at) / 60_000 : 1)
    minutes.push(minute)
  }
  return minutes
}

/** The same minutes a day earlier, every one whole: yesterday's minutes are all over. */
export function sampleYesterday(now: number): RealtimeMinute[] {
  return sampleWindow(now).map(minute => sampleMinute(minute.at - 86_400_000, true))
}

/**
 * Moves the window on to `now`: fills the current minute by the time that passed since the
 * last tick, and starts a new minute (dropping the oldest) when the clock crosses one.
 */
export function advanceWindow(window: RealtimeMinute[], last: number, now: number): void {
  let from = last
  while (from < now) {
    const current = window[window.length - 1]!
    const end = Math.min(now, current.at + 60_000)
    fillMinute(current, (end - from) / 60_000)
    from = end
    if (from >= current.at + 60_000) {
      window.push(emptyMinute(current.at + 60_000))
      window.shift()
    }
  }
}
