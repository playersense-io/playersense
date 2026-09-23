// Sample data for the Overview page, generated in the browser. Shaped after what the page
// will be built from once the API client is wired in:
//
//   POST /v1/organizations/{org}/projects/{project}/analytics/production/metrics/query
//     metric active_players | new_players | returning_players | sessions | playtime |
//     revenue | purchases | payers, interval=day, per project — kept per project for the
//     projects table and summed here for the organization.
//   GET /v1/organizations/{org}/usage        tracked players, events, retained bytes,
//                                            by day and by project, with the allowances
//   GET /v1/users/me                         the wallet's live balance
//
// Seeded, so a reload draws the same page and the previous period is a believable twin of
// the current one rather than noise.

import type { Overview, OverviewDay as Day } from '~/types'

// Each game's share of the organization's players, and how its players differ from the
// average: a farming game plays long sessions and converts little, a tactics game earns
// more from fewer payers, a racer is played in short bursts.
const PROJECTS = [
  { id: 'prj_starfall', name: 'Starfall Tactics', weight: 0.58, growth: 1.06, sessions: 0.9, playtime: 1.05, conversion: 1.2, spend: 1.25 },
  { id: 'prj_moonrise', name: 'Moonrise Farm', weight: 0.31, growth: 1.14, sessions: 1.25, playtime: 1.35, conversion: 0.7, spend: 0.8 },
  { id: 'prj_pocketrally', name: 'Pocket Rally', weight: 0.11, growth: 0.93, sessions: 1.1, playtime: 0.55, conversion: 0.8, spend: 0.6 }
]

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

  const activeDays = sum(current, 'active_players')
  const projects = PROJECTS.map((project) => {
    const active = activeDays / days * project.weight
    const payers = sum(current, 'payers') / days * project.weight * project.conversion
    const revenue = sum(current, 'revenue_micros') * project.weight * project.conversion * project.spend
    return {
      id: project.id,
      name: project.name,
      active_players: Math.round(active),
      previous_active_players: Math.round(active / project.growth),
      new_players: Math.round(sum(current, 'new_players') * project.weight * project.growth),
      sessions_per_player: sum(current, 'sessions') / activeDays * project.sessions,
      playtime_per_player_ms: sum(current, 'playtime_ms') / activeDays * project.playtime,
      payers: Math.round(payers),
      revenue_micros: Math.round(revenue),
      previous_revenue_micros: Math.round(revenue / (project.growth + (project.spend - 1) / 4)),
      events: Math.round(sum(current, 'sessions') * 41 * project.weight * project.sessions)
    }
  })

  // The billing month, which is not the period above: usage is billed per calendar month.
  const month = series(new Date().getUTCDate() - 1 || 1, 0, 42)
  const busiest = Math.max(...month.map(day => day.active_players))

  return {
    days,
    current,
    previous,
    projects,
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
