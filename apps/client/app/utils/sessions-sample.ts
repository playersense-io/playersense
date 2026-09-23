// Sample data for the Sessions page, generated in the browser. What each part will come
// from once the API client is wired in:
//
//   sessions, playtime and players per day — `metrics_daily` through the metrics query
//   (metric=sessions / playtime / active_players), so average length is playtime over
//   sessions; session starts by hour — `events_1h`, event_name=session_start;
//   the length distribution, how sessions ended and the list of recent sessions — derived
//   session facts, which need the session finalizer (project.md §30) that is not built yet.
//
// Seeded, so a reload draws the same page.

export type SessionPeriod = '7d' | '30d' | '90d'
export type SessionEnd = 'session_end' | 'timeout' | 'crash'

export const SESSION_END_LABELS: Record<SessionEnd, string> = {
  session_end: 'Closed',
  timeout: 'Timed out',
  crash: 'Crashed'
}

const PERIOD_DAYS: Record<SessionPeriod, number> = { '7d': 7, '30d': 30, '90d': 90 }
const DAY = 86_400_000

function seeded(text: string): () => number {
  let state = 0
  for (const char of text) state = (Math.imul(state, 31) + char.charCodeAt(0)) >>> 0
  return () => {
    state = (state + 0x6D2B79F5) >>> 0
    let t = state
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export interface SessionDay {
  at: number
  sessions: number
  previous: number
  playtimeMs: number
  previousPlaytimeMs: number
  players: number
}

export interface SessionOverview {
  days: SessionDay[]
  /** Session starts by weekday (0 Monday … 6 Sunday) and hour of the day. */
  heatmap: number[][]
  lengths: { value: string, count: number }[]
  platforms: { platform: string, sessions: number, playtimeMs: number, players: number }[]
  ends: { end: SessionEnd, count: number }[]
}

/** How busy an hour of the day is, 0–1: quiet before dawn, busiest in the evening. */
function hourShape(hour: number): number {
  return 0.25 + 0.75 * Math.max(0, Math.sin((hour - 7) / 17 * Math.PI)) ** 1.4
}

export function sampleSessionOverview(period: SessionPeriod): SessionOverview {
  const count = PERIOD_DAYS[period]
  const random = seeded(`sessions:${period}`)
  const end = Math.floor(Date.now() / DAY) * DAY

  const days = Array.from({ length: count }, (_, index) => {
    const at = end - (count - index) * DAY
    const weekend = [0, 6].includes(new Date(at).getDay()) ? 1.2 : 1
    const players = Math.round(30_000 * weekend * (1 + index / count * 0.06) * (0.94 + random() * 0.12))
    const sessions = Math.round(players * (2.7 + random() * 0.4))
    const playtimeMs = Math.round(sessions * (14 + random() * 2.5) * 60_000)
    const previous = Math.round(sessions / (1.05 + random() * 0.06))
    return { at, sessions, previous, playtimeMs, previousPlaytimeMs: Math.round(previous * (14 + random() * 2) * 60_000), players }
  })

  const total = days.reduce((sum, day) => sum + day.sessions, 0)
  const heatmap = Array.from({ length: 7 }, (_, weekday) => Array.from({ length: 24 }, (_, hour) =>
    Math.round(total / (7 * 24) * hourShape(hour) * (weekday >= 5 ? 1.25 : 1) * (0.85 + random() * 0.3) * 1.6)))

  const lengths = [
    ['Under 1 min', 0.09],
    ['1–5 min', 0.21],
    ['5–15 min', 0.31],
    ['15–30 min', 0.22],
    ['30–60 min', 0.12],
    ['Over an hour', 0.05]
  ].map(([value, share]) => ({ value: value as string, count: Math.round(total * (share as number) * (0.92 + random() * 0.16)) }))

  const platforms = [['iOS', 0.46, 15.2], ['Android', 0.43, 13.1], ['Steam', 0.11, 27.4]].map(([platform, share, minutes]) => {
    const sessions = Math.round(total * (share as number))
    return {
      platform: platform as string,
      sessions,
      playtimeMs: Math.round(sessions * (minutes as number) * 60_000),
      players: Math.round(sessions / (platform === 'Steam' ? 2.2 : 2.9))
    }
  })

  const ends = ([['session_end', 0.71], ['timeout', 0.26], ['crash', 0.03]] as const)
    .map(([endReason, share]) => ({ end: endReason, count: Math.round(total * share) }))

  return { days, heatmap, lengths, platforms, ends }
}

export interface SessionRow {
  id: string
  player: string
  platform: string
  version: string
  startedAt: number
  durationMs: number
  events: number
  end: SessionEnd
}

function hex(random: () => number, length: number): string {
  return Array.from({ length }, () => Math.floor(random() * 16).toString(16)).join('')
}

/** The latest sessions, newest first, from the players on file. */
export function sampleRecentSessions(count = 60): SessionRow[] {
  const random = seeded('recent-sessions')
  const players = samplePlayers().slice(0, 40)
  let at = Date.now() - 20_000
  return Array.from({ length: count }, () => {
    const player = players[Math.floor(random() * players.length)]!
    const durationMs = Math.round((random() ** 1.8 * 55 + 0.5) * 60_000)
    const roll = random()
    const end: SessionEnd = roll < 0.03 ? 'crash' : roll < 0.28 ? 'timeout' : 'session_end'
    at -= Math.round((5 + random() * 40) * 1_000)
    return {
      id: `${hex(random, 8)}-${hex(random, 4)}-4${hex(random, 3)}-${hex(random, 4)}-${hex(random, 12)}`,
      player: player.id,
      platform: player.platform,
      version: player.version,
      startedAt: at - durationMs,
      durationMs,
      events: Math.round(durationMs / 60_000 * (4 + random() * 6)) + 2,
      end
    }
  })
}
