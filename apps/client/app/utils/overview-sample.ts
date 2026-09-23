// Sample data for the Overview page, generated in the browser. Shaped after what the page
// will be built from once the API client is wired in:
//
//   POST /v1/organizations/{org}/projects/{project}/analytics/production/metrics/query
//     metric active_players | new_players | returning_players | sessions | playtime |
//     revenue | purchases | payers, interval=day, per project, summed here; and once more
//     with group_by platform / country for the breakdowns (at most one dimension).
//   GET /v1/organizations/{org}/usage        tracked players, events, retained bytes,
//                                            by day and by project, with the allowances
//   GET /v1/users/me                         the wallet's live balance
//
// Seeded, so a reload draws the same page and the previous period is a believable twin of
// the current one rather than noise.

import type { Overview, OverviewDay as Day } from '~/types'

const PROJECTS = [
  { id: 'prj_starfall', name: 'Starfall Tactics', weight: 0.58 },
  { id: 'prj_moonrise', name: 'Moonrise Farm', weight: 0.31 },
  { id: 'prj_pocketrally', name: 'Pocket Rally', weight: 0.11 }
]

const PLATFORMS = [['iOS', 0.46], ['Android', 0.41], ['Steam', 0.09], ['Web', 0.04]] as const
const COUNTRIES = [['United States', 0.31], ['Japan', 0.14], ['Brazil', 0.11], ['Germany', 0.09], ['Türkiye', 0.07], ['Other', 0.28]] as const

function seeded(seed: number): () => number {
  let state = seed >>> 0
  return () => {
    state = (state + 0x6D2B79F5) >>> 0
    let t = state
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

/** `count` days ending yesterday (today is still being written), oldest first. */
function series(count: number, endOffset: number, seed: number, scale = 1): Day[] {
  const random = seeded(seed)
  const days: Day[] = []
  const today = new Date()
  today.setUTCHours(0, 0, 0, 0)
  for (let i = count; i >= 1; i--) {
    const date = new Date(today)
    date.setUTCDate(today.getUTCDate() - i - endOffset)
    const weekend = [0, 6].includes(date.getUTCDay()) ? 1.18 : 1
    const trend = 1 + (count - i) * 0.0035
    const active = Math.round(48_000 * scale * weekend * trend * (0.94 + random() * 0.12))
    const fresh = Math.round(active * (0.075 + random() * 0.03))
    const payers = Math.round(active * (0.021 + random() * 0.006))
    const purchases = Math.round(payers * (1.25 + random() * 0.3))
    days.push({
      day: date.toISOString(),
      active_players: active,
      new_players: fresh,
      returning_players: active - fresh,
      sessions: Math.round(active * (2.6 + random() * 0.5)),
      playtime_ms: Math.round(active * (38 + random() * 9) * 60_000),
      revenue_micros: Math.round(purchases * (6.4 + random() * 2.2) * 1_000_000),
      purchases,
      payers
    })
  }
  return days
}

function sum(days: Day[], key: keyof Omit<Day, 'day'>): number {
  return days.reduce((total, day) => total + day[key], 0)
}

/** The Overview page's data for the last `days` days. */
export function sampleOverview(days: 7 | 30 | 90): Overview {
  const current = series(days, 0, 7 + days)
  // A little smaller, so the sample reads as a growing studio rather than noise.
  const previous = series(days, days, 1_000 + days, 0.9)

  const projects = PROJECTS.map(project => ({
    id: project.id,
    name: project.name,
    active_players: Math.round(sum(current, 'active_players') / days * project.weight),
    new_players: Math.round(sum(current, 'new_players') * project.weight),
    revenue_micros: Math.round(sum(current, 'revenue_micros') * project.weight),
    previous_revenue_micros: Math.round(sum(previous, 'revenue_micros') * project.weight * (0.9 + project.weight / 3)),
    events: Math.round(sum(current, 'sessions') * 41 * project.weight)
  }))

  const averageActive = sum(current, 'active_players') / days
  const breakdown = (shares: readonly (readonly [string, number])[]) =>
    shares.map(([value, share]) => ({ value, active_players: Math.round(averageActive * share) }))

  // The billing month, which is not the period above: usage is billed per calendar month.
  const month = series(new Date().getUTCDate() - 1 || 1, 0, 42)
  const busiest = Math.max(...month.map(day => day.active_players))

  return {
    days,
    current,
    previous,
    projects,
    by_platform: breakdown(PLATFORMS),
    by_country: breakdown(COUNTRIES),
    usage: {
      period_start: new Date(Date.UTC(new Date().getUTCFullYear(), new Date().getUTCMonth(), 1)).toISOString(),
      // Tracked players is the busiest day, not a sum: adding days counts a player once per day.
      tracked_players: Math.round(busiest * 2.9),
      events: Math.round(sum(month, 'sessions') * 41),
      retained_bytes: 412 * 1024 ** 3,
      daily_tracked_players: month.map(day => Math.round(day.active_players * 2.9)),
      daily_events: month.map(day => day.sessions * 41),
      included: { tracked_players: 250_000, events: 600_000_000, retained_bytes: 500 * 1024 ** 3 },
      charged_micros: 318_420_000,
      pending_micros: 4_870_000,
      available_micros: 1_240_500_000
    }
  }
}
