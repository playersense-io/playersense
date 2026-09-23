// Sample data for the Players pages, generated in the browser. What each part will come
// from once a player endpoint exists (there is none yet — todos.md, "Players"):
//
//   the directory and a player's lifetime figures — `player_lifetime` (project.md §29.6):
//   one row per player with the key it was seen under, first and last seen, the platform,
//   country and version of its first event, its acquisition source, and lifetime playtime,
//   revenue, purchases and active days;
//   a player's day-by-day activity — `player_activity_daily` (§29.7), kept about 400 days;
//   a player's latest events — the raw events, for which no query path exists yet.
//
// Seeded, so a reload draws the same players.

export type PlayerStatus = 'active' | 'lapsed' | 'churned'
export type PlayerPeriod = '7d' | '30d' | '90d'

export const PLAYER_STATUS_LABELS: Record<PlayerStatus, string> = {
  active: 'Active',
  lapsed: 'Lapsed',
  churned: 'Churned'
}

/** Days since last seen after which a player counts as lapsed, and as churned. */
export const LAPSED_AFTER = 7
export const CHURNED_AFTER = 30

export interface PlayerSummary {
  id: string
  /** What identifies the player: the studio's own `actor_key`, or the SDK's `install_id`. */
  identifiedBy: 'actor_key' | 'install_id'
  platform: string
  country: string
  version: string
  source: string
  firstSeen: number
  lastSeen: number
  activeDays: number
  sessions: number
  playtimeMs: number
  revenueMicros: number
  purchases: number
  status: PlayerStatus
}

const PLATFORMS = [['iOS', 0.47], ['Android', 0.42], ['Steam', 0.11]] as const
const COUNTRIES = [['United States', 0.31], ['Japan', 0.14], ['Brazil', 0.11], ['Germany', 0.09], ['Türkiye', 0.07], ['France', 0.06], ['Korea', 0.06], ['Mexico', 0.16]] as const
const VERSIONS = [['2.14.0', 0.62], ['2.13.2', 0.27], ['2.13.1', 0.11]] as const
const SOURCES = [['Organic', 0.52], ['Meta Ads', 0.17], ['Google Ads', 0.14], ['TikTok Ads', 0.11], ['Referral', 0.06]] as const

export const PLAYER_PLATFORMS = PLATFORMS.map(([value]) => value)
export const PLAYER_SOURCES = SOURCES.map(([value]) => value)

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

function pick<T extends string>(shares: readonly (readonly [T, number])[], random: () => number): T {
  let roll = random()
  for (const [value, share] of shares) {
    roll -= share
    if (roll <= 0) return value
  }
  return shares[shares.length - 1]![0]
}

export function playerStatusOf(lastSeen: number, now = Date.now()): PlayerStatus {
  const days = (now - lastSeen) / DAY
  if (days >= CHURNED_AFTER) return 'churned'
  if (days >= LAPSED_AFTER) return 'lapsed'
  return 'active'
}

function hex(random: () => number, length: number): string {
  return Array.from({ length }, () => Math.floor(random() * 16).toString(16)).join('')
}

function samplePlayer(index: number, now: number): PlayerSummary {
  const random = seeded(`player:${index}`)
  const identifiedBy = random() < 0.63 ? 'actor_key' : 'install_id'
  const id = identifiedBy === 'actor_key'
    ? `u_${(100_000 + Math.floor(random() * 900_000)).toString()}`
    : `${hex(random, 8)}-${hex(random, 4)}-4${hex(random, 3)}-${hex(random, 4)}-${hex(random, 12)}`

  const age = Math.floor(random() ** 1.6 * 240) + 1
  const firstSeen = now - age * DAY - Math.floor(random() * DAY)
  // Most players stop coming back soon; the ones who stay play most days.
  const staying = random()
  const lifespan = staying < 0.45 ? Math.min(age, 1 + Math.floor(random() * 3)) : staying < 0.75 ? Math.floor(age * random() * 0.6) : age
  const lastSeen = Math.min(now - Math.floor(random() * 3_600_000), firstSeen + lifespan * DAY + Math.floor(random() * DAY))
  const activeDays = Math.max(1, Math.round((lastSeen - firstSeen) / DAY * (0.35 + random() * 0.5)))
  const sessions = Math.round(activeDays * (1.6 + random() * 2.2))
  const payer = random() < 0.06 + Math.min(0.2, activeDays / 400)
  const purchases = payer ? 1 + Math.floor(random() ** 2 * activeDays / 3) : 0

  return {
    id,
    identifiedBy,
    platform: pick(PLATFORMS, random),
    country: pick(COUNTRIES, random),
    version: pick(VERSIONS, random),
    source: pick(SOURCES, random),
    firstSeen,
    lastSeen,
    activeDays,
    sessions,
    playtimeMs: Math.round(sessions * (8 + random() * 22) * 60_000),
    revenueMicros: Math.round(purchases * (2 + random() * 14) * 1_000_000),
    purchases,
    status: playerStatusOf(lastSeen, now)
  }
}

/** A page of the directory: the players seen most recently first. */
export function samplePlayers(count = 240): PlayerSummary[] {
  const now = Date.now()
  return Array.from({ length: count }, (_, index) => samplePlayer(index, now))
    .sort((a, b) => b.lastSeen - a.lastSeen)
}

export interface PlayerBase {
  active: number
  previousActive: number
  fresh: number
  previousFresh: number
  payersShare: number
  previousPayersShare: number
  lifetimeValue: number
  previousLifetimeValue: number
  playtimePerPlayer: number
  previousPlaytimePerPlayer: number
  /** Daily active players split by where they are in their life, oldest day first. */
  lifecycle: { at: number, fresh: number, returning: number, reactivated: number }[]
}

const PERIOD_DAYS: Record<PlayerPeriod, number> = { '7d': 7, '30d': 30, '90d': 90 }

/** The project's whole player base over the period, against the one before. */
export function samplePlayerBase(period: PlayerPeriod): PlayerBase {
  const days = PERIOD_DAYS[period]
  const random = seeded(`base:${period}`)
  const end = Math.floor(Date.now() / DAY) * DAY
  const lifecycle = Array.from({ length: days }, (_, index) => {
    const at = end - (days - index) * DAY
    const weekend = [0, 6].includes(new Date(at).getDay()) ? 1.16 : 1
    const trend = 1 + index / days * 0.08
    const active = 30_000 * weekend * trend * (0.94 + random() * 0.12)
    const fresh = Math.round(active * (0.07 + random() * 0.03))
    const reactivated = Math.round(active * (0.03 + random() * 0.015))
    return { at, fresh, reactivated, returning: Math.round(active) - fresh - reactivated }
  })
  const active = Math.round(30_000 * Math.sqrt(days) * 1.9)
  return {
    active,
    previousActive: Math.round(active / 1.07),
    fresh: lifecycle.reduce((sum, day) => sum + day.fresh, 0),
    previousFresh: Math.round(lifecycle.reduce((sum, day) => sum + day.fresh, 0) / 1.11),
    payersShare: 4.8,
    previousPayersShare: 4.5,
    lifetimeValue: 3.42,
    previousLifetimeValue: 3.31,
    playtimePerPlayer: 41,
    previousPlaytimePerPlayer: 39.5,
    lifecycle
  }
}

export interface PlayerDetail {
  player: PlayerSummary
  /** The last 30 days, oldest first; days the player did not play are zero. */
  days: { at: number, sessions: number, playtimeMs: number, events: number, revenueMicros: number, purchases: number }[]
  recent: { at: number, name: string, payload: Record<string, string | number> }[]
}

const EVENT_NAMES = ['level_complete', 'ad_impression', 'currency_spend', 'item_equip', 'match_end', 'session_start']

/** One player in full, or null for an id nothing has been seen under. */
export function samplePlayerDetail(id: string): PlayerDetail | null {
  const player = samplePlayers().find(candidate => candidate.id === id)
  if (!player) return null
  const random = seeded(`detail:${id}`)
  const end = Math.floor(Date.now() / DAY) * DAY
  const share = Math.min(0.95, player.activeDays / Math.max(1, (player.lastSeen - player.firstSeen) / DAY))

  const days = Array.from({ length: 30 }, (_, index) => {
    const at = end - (29 - index) * DAY
    const played = at >= player.firstSeen - DAY && at <= player.lastSeen && random() < share
    const sessions = played ? 1 + Math.floor(random() * 4) : 0
    const purchases = played && player.purchases > 0 && random() < 0.08 ? 1 : 0
    return {
      at,
      sessions,
      playtimeMs: Math.round(sessions * (6 + random() * 24) * 60_000),
      events: sessions * Math.round(25 + random() * 60),
      revenueMicros: Math.round(purchases * (2 + random() * 14) * 1_000_000),
      purchases
    }
  })

  const recent = Array.from({ length: 10 }, (_, index) => {
    const name = EVENT_NAMES[Math.floor(random() * EVENT_NAMES.length)]!
    const payload: Record<string, string | number> = name === 'level_complete'
      ? { level: 10 + Math.floor(random() * 30), result: random() < 0.6 ? 'win' : 'loss' }
      : name === 'ad_impression'
        ? { format: random() < 0.7 ? 'rewarded' : 'interstitial', placement: 'level_end' }
        : name === 'currency_spend'
          ? { currency: 'coins', amount: 50 * (1 + Math.floor(random() * 40)) }
          : name === 'item_equip'
            ? { item_id: 'skin_winter', slot: 'outfit' }
            : name === 'match_end'
              ? { mode: 'ranked', placement: 1 + Math.floor(random() * 4) }
              : { entry_point: 'app_icon' }
    return { at: player.lastSeen - Math.round((index * 2.5 + random()) * 60_000), name, payload }
  })

  return { player, days, recent }
}
