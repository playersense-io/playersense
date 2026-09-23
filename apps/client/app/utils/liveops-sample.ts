// Sample data for Growth > Live Ops. What it stands in for:
//
//   The events are what `GET /liveops-events` returns (the table exists; the dashboard is not
//   wired to it yet). The figures are what a daily query over sessions and verified purchases
//   will answer: the whole game's day, and the same for the players an event reached, set
//   against what those players would have done without it.
//
// Each sample event has a true effect on a few metrics while it is on, and some an effect after
// it ends (a sale pulls purchases forward, so the days after it dip), and a recurring one can
// wear off as players get used to it. The figures are drawn around those truths with the noise
// a group of that size has, seeded by the event and the day, so a reload draws the same days.

const HOUR = 3_600_000

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

const gauss = (random: () => number) => Math.sqrt(-2 * Math.log(random() || 1e-12)) * Math.cos(2 * Math.PI * random())

export const startOfDay = (time: number) => {
  const at = new Date(time)
  at.setHours(0, 0, 0, 0)
  return at.getTime()
}

/** Today at a clock time, moved by whole days. */
const dayAt = (days: number, hour = 0, minute = 0) => {
  const at = new Date(startOfDay(Date.now()))
  at.setDate(at.getDate() + days)
  at.setHours(hour, minute, 0, 0)
  return at.getTime()
}

/** The last given weekday on or before today, a number of weeks further back, at a clock time. */
const weekdayBack = (weekday: number, weeks: number, hour = 0) => {
  const today = new Date(startOfDay(Date.now()))
  const back = (today.getDay() - weekday + 7) % 7
  return dayAt(-back - weeks * 7, hour)
}

const iso = (at: number) => new Date(at).toISOString()

// --- The events ---------------------------------------------------------------------------

type Effects = Partial<Record<LiveOpsMetricKey, number>>

interface Truth {
  /** The share of the game's daily players its audience is. */
  share: number
  /** Relative change on each metric for the players it reached, while it is on. */
  during: Effects
  /** Relative change in the three days after each window ends. */
  after?: Effects
  /** How much of the effect each later window loses, as players get used to it. */
  fatigue?: number
  /** How much more than the average player its audience spends: whales, for one. */
  spend?: number
}

interface SampleEvent {
  event: Omit<LiveOpsEventDef, 'created_at' | 'updated_at' | 'updated_by' | 'history' | 'salt' | 'description'> & { description: string }
  truth: Truth
  created: number
  actor: string
  history: [at: number, text: string][]
}

const TEAM = { mira: 'mira@acme.games', kaan: 'kaan@acme.games', deniz: 'deniz@acme.games', me: CURRENT_ACTOR }

const SAMPLES: SampleEvent[] = [
  {
    event: {
      key: 'new_year',
      kind: 'seasonal_event',
      name: 'New Year fireworks',
      description: 'Fireworks over the hub, a countdown quest line and a gift at midnight. One event, every year: the dates carry over on their own.',
      status: 'active',
      schedule: { mode: 'yearly', anchor: iso(new Date(new Date().getFullYear() - 1, 11, 30, 0, 0).getTime()), duration: '120h' },
      priority: 10,
      audience: {},
      payload: { theme: 'new_year', hub_skin: 'fireworks', quests: ['ny_countdown_1', 'ny_countdown_2', 'ny_countdown_3'], midnight_gift: { gems: 200, frame: 'ny_frame' } },
      rollout: FULL_ROLLOUT,
      goal: 'players',
      watch: ['revenue']
    },
    truth: { share: 1, during: { players: 0.16, new_players: 0.08, sessions_per_player: 0.1, playtime_minutes: 0.12, d1_retention: 0.04 }, after: { players: -0.03 } },
    created: dayAt(-300),
    actor: TEAM.mira,
    history: [[dayAt(-300), 'Created'], [dayAt(-299), 'Activated: every Dec 30 at 00:00 for 5 days']]
  },
  {
    event: {
      key: 'double_xp_weekend',
      kind: 'multiplier',
      name: 'Double XP weekend',
      description: 'Twice the XP from matches and quests, Friday evening to the end of Sunday.',
      status: 'active',
      schedule: { mode: 'weekly', anchor: iso(weekdayBack(5, 12, 18)), duration: '54h' },
      priority: 0,
      audience: {},
      payload: { xp_multiplier: 2, applies_to: ['matches', 'quests'], banner: 'double_xp' },
      rollout: FULL_ROLLOUT,
      goal: 'playtime_minutes',
      watch: ['revenue', 'd1_retention']
    },
    truth: { share: 1, during: { playtime_minutes: 0.24, sessions_per_player: 0.13, players: 0.05, d1_retention: 0.02, arpdau: -0.02 }, after: { playtime_minutes: -0.04 }, fatigue: 0.045 },
    created: weekdayBack(5, 13),
    actor: TEAM.kaan,
    history: [[weekdayBack(5, 13), 'Created'], [weekdayBack(5, 12, 12), 'Activated: every Friday at 18:00 for 2 days 6 hours']]
  },
  {
    event: {
      key: 'happy_hour_gems',
      kind: 'offer',
      name: 'Happy hour gem bonus',
      description: '50% more gems on every pack, for two hours each evening. One player in ten is held back so the bonus can be measured against them.',
      status: 'active',
      schedule: { mode: 'daily', anchor: iso(dayAt(-20, 20)), duration: '2h' },
      priority: 0,
      audience: { key: 'app.version', op: 'version_gte', value: '2.5.0' },
      payload: { bonus_percent: 50, products: ['gems_small', 'gems_medium', 'gems_large'], badge: 'HAPPY HOUR' },
      rollout: 9000,
      goal: 'arpdau',
      watch: ['payer_conversion']
    },
    truth: { share: 0.9, during: { arpdau: 0.85, payer_conversion: 0.6 } },
    created: dayAt(-22),
    actor: TEAM.deniz,
    history: [[dayAt(-22), 'Created'], [dayAt(-21), 'Rollout 100% → 90%, to hold a tenth back'], [dayAt(-20, 12), 'Activated: every day at 20:00 for 2 hours']]
  },
  {
    event: {
      key: 'season_7',
      kind: 'season',
      name: 'Season 7: Tidebreakers',
      description: 'The seventh battle pass: 60 tiers, a free and a premium track, and a ranked reset at the end.',
      status: 'active',
      schedule: { mode: 'once', anchor: iso(dayAt(-12, 10)), duration: '1008h' },
      priority: 5,
      audience: { key: 'context.progress.level', op: 'gte', value: 5 },
      payload: { season: 7, name: 'Tidebreakers', tiers: 60, premium_product_id: 'bp_s7_premium', free_track: true, ranked_reset: true },
      rollout: FULL_ROLLOUT,
      goal: 'revenue',
      watch: ['players', 'd1_retention']
    },
    truth: { share: 0.82, during: { arpdau: 0.19, payer_conversion: 0.26, players: 0.05, sessions_per_player: 0.06 } },
    created: dayAt(-30),
    actor: TEAM.mira,
    history: [[dayAt(-30), 'Created'], [dayAt(-14), 'Payload: tiers 50 → 60'], [dayAt(-13), 'Activated: once, from 10:00 for 42 days']]
  },
  {
    event: {
      key: 'season_6',
      kind: 'season',
      name: 'Season 6: Emberfall',
      description: 'The sixth battle pass.',
      status: 'active',
      schedule: { mode: 'once', anchor: iso(dayAt(-54, 10)), duration: '1008h' },
      priority: 5,
      audience: { key: 'context.progress.level', op: 'gte', value: 5 },
      payload: { season: 6, name: 'Emberfall', tiers: 50, premium_product_id: 'bp_s6_premium', free_track: true, ranked_reset: true },
      rollout: FULL_ROLLOUT,
      goal: 'revenue',
      watch: ['players']
    },
    truth: { share: 0.8, during: { arpdau: 0.14, payer_conversion: 0.2, players: 0.03 } },
    created: dayAt(-70),
    actor: TEAM.mira,
    history: [[dayAt(-70), 'Created'], [dayAt(-55), 'Activated: once, from 10:00 for 42 days']]
  },
  {
    event: {
      key: 'summer_festival',
      kind: 'seasonal_event',
      name: 'Summer festival',
      description: 'A beach map, a festival currency and a shop tab of summer skins.',
      status: 'active',
      schedule: { mode: 'once', anchor: iso(dayAt(-82, 10)), duration: '504h' },
      priority: 10,
      audience: { key: 'context.progress.level', op: 'gte', value: 10 },
      payload: { theme: 'summer', map: 'beach', currency: 'shells', store_tab: true },
      rollout: FULL_ROLLOUT,
      goal: 'players',
      watch: ['revenue']
    },
    truth: { share: 0.7, during: { players: 0.15, new_players: 0.1, sessions_per_player: 0.12, playtime_minutes: 0.16, arpdau: 0.07 }, after: { players: -0.05, sessions_per_player: -0.04 } },
    created: dayAt(-100),
    actor: TEAM.kaan,
    history: [[dayAt(-100), 'Created'], [dayAt(-84), 'Activated: once, from 10:00 for 21 days']]
  },
  {
    event: {
      key: 'weekend_cup',
      kind: 'tournament',
      name: 'Weekend Cup',
      description: 'A ranked tournament from Saturday noon, with a leaderboard and gem prizes.',
      status: 'active',
      schedule: { mode: 'weekly', anchor: iso(weekdayBack(6, 9, 12)), duration: '30h' },
      priority: 0,
      audience: { key: 'context.progress.level', op: 'gte', value: 20 },
      payload: { leaderboard: 'weekend_cup', entry_fee: 0, prizes: [{ rank: 1, gems: 500 }, { rank: 10, gems: 150 }, { rank: 100, gems: 50 }] },
      rollout: FULL_ROLLOUT,
      goal: 'sessions_per_player',
      watch: ['d1_retention']
    },
    truth: { share: 0.34, during: { sessions_per_player: 0.17, playtime_minutes: 0.11, d1_retention: 0.02 }, fatigue: 0.01 },
    created: weekdayBack(6, 10),
    actor: TEAM.kaan,
    history: [[weekdayBack(6, 10), 'Created'], [weekdayBack(6, 9, 9), 'Activated: every Saturday at 12:00 for 1 day 6 hours']]
  },
  {
    event: {
      key: 'starter_flash_sale',
      kind: 'offer',
      name: 'Starter pack flash sale',
      description: 'The starter pack at half price for three days, for new players who have not paid.',
      status: 'active',
      schedule: { mode: 'once', anchor: iso(dayAt(-6, 9)), duration: '72h' },
      priority: 20,
      audience: { all: [{ segment: 'new_players' }, { not: { segment: 'payers' } }] },
      payload: { product_id: 'starter_pack', discount_percent: 50, badge: '50% OFF', countdown: true },
      rollout: FULL_ROLLOUT,
      goal: 'payer_conversion',
      watch: ['arpdau']
    },
    truth: { share: 0.07, during: { payer_conversion: 0.95, arpdau: 0.55 }, after: { payer_conversion: -0.3, arpdau: -0.2 } },
    created: dayAt(-8),
    actor: TEAM.deniz,
    history: [[dayAt(-8), 'Created'], [dayAt(-7), 'Activated: once, from 09:00 for 3 days']]
  },
  {
    event: {
      key: 'latam_gem_sale',
      kind: 'offer',
      name: 'Gem sale in Latin America',
      description: '30% more gems on every pack in Brazil, Mexico, Argentina and Colombia. A fifth are held back to measure it.',
      status: 'active',
      schedule: { mode: 'once', anchor: iso(dayAt(-1, 15)), duration: '96h' },
      priority: 10,
      audience: { key: 'geo.country', op: 'in', values: ['BR', 'MX', 'AR', 'CO'] },
      payload: { bonus_percent: 30, products: ['gems_small', 'gems_medium', 'gems_large'], badge: '+30%', currency_display: 'local' },
      rollout: 8000,
      goal: 'revenue',
      watch: ['payer_conversion']
    },
    truth: { share: 0.12, during: { arpdau: 0.34, payer_conversion: 0.42 }, spend: 0.6 },
    created: dayAt(-3),
    actor: TEAM.me,
    history: [[dayAt(-3), 'Created'], [dayAt(-2), 'Audience: added Colombia'], [dayAt(-2, 14), 'Activated: once, from 15:00 for 4 days']]
  },
  {
    event: {
      key: 'login_calendar',
      kind: 'reward',
      name: 'Monthly login calendar',
      description: 'A reward a day for the first five days of every month.',
      status: 'active',
      schedule: { mode: 'monthly', anchor: iso(new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1, 0, 0).getTime()), duration: '120h' },
      priority: 0,
      audience: {},
      payload: { days: [{ gems: 20 }, { gems: 30 }, { energy: 50 }, { gems: 50 }, { chest: 'rare' }] },
      rollout: FULL_ROLLOUT,
      goal: 'd1_retention',
      watch: ['players']
    },
    truth: { share: 1, during: { d1_retention: 0.06, players: 0.04 } },
    created: dayAt(-190),
    actor: TEAM.mira,
    history: [[dayAt(-190), 'Created'], [dayAt(-185), 'Activated: the 1st of every month at 00:00 for 5 days']]
  },
  {
    event: {
      key: 'collectors_bundle',
      kind: 'offer',
      name: 'Collector\'s bundle',
      description: 'A $49.99 bundle of three legendary skins, for Whales only.',
      status: 'active',
      schedule: { mode: 'once', anchor: iso(dayAt(-9, 12)), duration: '168h' },
      priority: 30,
      audience: { segment: 'whales' },
      payload: { product_id: 'collectors_bundle_s7', price_usd: 49.99, items: ['skin_leviathan', 'skin_tidecaller', 'skin_abyss'] },
      rollout: FULL_ROLLOUT,
      goal: 'revenue',
      watch: ['players']
    },
    truth: { share: 0.012, during: { arpdau: 0.42, payer_conversion: 0.18 }, after: { arpdau: -0.1 }, spend: 32 },
    created: dayAt(-12),
    actor: TEAM.deniz,
    history: [[dayAt(-12), 'Created'], [dayAt(-10), 'Activated: once, from 12:00 for 7 days']]
  },
  {
    event: {
      key: 'comeback_energy',
      kind: 'multiplier',
      name: 'Double energy for returning players',
      description: 'Energy refills twice as fast for players back after two weeks away.',
      status: 'paused',
      schedule: { mode: 'once', anchor: iso(dayAt(-30, 0)), duration: '1440h' },
      priority: 0,
      audience: { segment: 'lapsed_players' },
      payload: { energy_refill_multiplier: 2, max_energy_bonus: 20 },
      rollout: FULL_ROLLOUT,
      goal: 'd1_retention',
      watch: ['arpdau']
    },
    truth: { share: 0.06, during: { d1_retention: 0.07, players: 0.03, arpdau: -0.12 } },
    created: dayAt(-33),
    actor: TEAM.kaan,
    history: [[dayAt(-33), 'Created'], [dayAt(-31), 'Activated: once, from 00:00 for 60 days'], [dayAt(-4, 11), 'Paused: revenue from returning players fell by a tenth']]
  },
  {
    event: {
      key: 'maintenance_notice',
      kind: 'announcement',
      name: 'Maintenance notice',
      description: 'A banner the day before the servers go down for the 2.6 update.',
      status: 'active',
      schedule: { mode: 'once', anchor: iso(dayAt(4, 6)), duration: '24h' },
      priority: 0,
      audience: {},
      payload: { title: 'Maintenance on Tuesday', body: 'Servers are down from 06:00 to 07:00 UTC for the 2.6 update.', severity: 'warning', dismissible: true },
      rollout: FULL_ROLLOUT,
      goal: null,
      watch: []
    },
    truth: { share: 1, during: {} },
    created: dayAt(-1),
    actor: TEAM.me,
    history: [[dayAt(-1), 'Created'], [dayAt(-1, 1), 'Activated: once, from 06:00 for 1 day']]
  },
  {
    event: {
      key: 'halloween',
      kind: 'seasonal_event',
      name: 'Halloween: Night of the Pumpkin King',
      description: 'A haunted map, candy as a currency, a quest line and a shop of Halloween skins. Every year from Oct 17 to the end of Nov 1.',
      status: 'active',
      schedule: { mode: 'yearly', anchor: iso(new Date(new Date().getFullYear() - 1, 9, 17, 10, 0).getTime()), duration: '374h' },
      priority: 10,
      audience: { all: [{ key: 'context.progress.level', op: 'gte', value: 10 }, { key: 'app.version', op: 'version_gte', value: '2.5.0' }] },
      payload: { theme: 'halloween', map: 'haunted_manor', currency: 'candy', store_tab: true, quests: ['q_pumpkin_1', 'q_pumpkin_2', 'q_pumpkin_3'], boss: { id: 'pumpkin_king', hp: 250000 } },
      rollout: FULL_ROLLOUT,
      goal: 'players',
      watch: ['revenue', 'd1_retention']
    },
    truth: { share: 0.72, during: { players: 0.18, sessions_per_player: 0.14, playtime_minutes: 0.18, arpdau: 0.1 }, after: { players: -0.04 } },
    created: dayAt(-360),
    actor: TEAM.mira,
    history: [[dayAt(-360), 'Created'], [dayAt(-345), 'Activated: every Oct 17 at 10:00 for 15 days 14 hours'], [dayAt(-6), 'Payload: added the Pumpkin King boss']]
  },
  {
    event: {
      key: 'ranked_rush',
      kind: 'game_mode',
      name: 'Ranked Rush',
      description: 'A short ranked queue with three-minute matches, on Wednesday evenings.',
      status: 'draft',
      schedule: { mode: 'weekly', anchor: iso(weekdayBack(3, -2, 18)), duration: '6h' },
      priority: 0,
      audience: { key: 'context.progress.level', op: 'gte', value: 15 },
      payload: { mode: 'ranked_rush', queue: 'rush', match_minutes: 3, map_pool: ['harbor', 'canyon'] },
      rollout: 5000,
      goal: 'sessions_per_player',
      watch: ['playtime_minutes']
    },
    truth: { share: 0.4, during: { sessions_per_player: 0.1 } },
    created: dayAt(-2),
    actor: TEAM.kaan,
    history: [[dayAt(-2), 'Created']]
  }
]

/** The sample events as a new visit finds them. The truths stay here. */
export function seedLiveOps(): LiveOpsEventDef[] {
  return SAMPLES.map(sample => ({
    ...JSON.parse(JSON.stringify(sample.event)) as SampleEvent['event'],
    salt: '',
    created_at: iso(sample.created),
    updated_at: iso(sample.history.at(-1)![0]),
    updated_by: sample.actor,
    history: sample.history.map(([at, text]) => ({ at: iso(at), actor: sample.actor, text }))
  }))
}

/** An event made here has no truth; it is given a modest one on the metric it is meant to move. */
function truthOf(event: LiveOpsEventDef): Truth {
  const sample = SAMPLES.find(entry => entry.event.key === event.key)
  if (sample) return sample.truth
  return { share: event.audience && Object.keys(event.audience).length ? 0.3 : 1, during: event.goal ? { [event.goal]: 0.08 } : {} }
}

/** The last moment an event was served: when it was paused or archived, or never for an active one. */
export function servedUntil(event: LiveOpsEventDef): number {
  if (event.status === 'draft') return Number.NEGATIVE_INFINITY
  if (event.status === 'active') return Number.POSITIVE_INFINITY
  const stop = [...event.history].reverse().find(entry => /^(Paused|Archived)/.test(entry.text))
  return stop ? Date.parse(stop.at) : Number.POSITIVE_INFINITY
}

/** The windows an event was really served in, overlapping [from, to). */
export function servedWindows(event: LiveOpsEventDef, from: number, to: number): Occurrence[] {
  const until = servedUntil(event)
  return occurrencesOf(event.schedule, from, Math.min(to, until)).map(entry => ({ start: entry.start, end: Math.min(entry.end, until) }))
}

// --- The game, day by day -----------------------------------------------------------------

export type DayFigures = Record<LiveOpsMetricKey, number>

/** A player of this game on an ordinary day, before any event. */
const BASE: Record<Exclude<LiveOpsMetricKey, 'revenue' | 'ad_revenue'>, number> = {
  players: 48_000,
  new_players: 3_300,
  arpdau: 0.38,
  payer_conversion: 0.026,
  sessions_per_player: 3.1,
  playtime_minutes: 38,
  d1_retention: 0.52
}

/** How far each metric wanders from one day to the next, relative. */
const NOISE: Record<keyof typeof BASE, number> = {
  players: 0.025,
  new_players: 0.06,
  arpdau: 0.07,
  payer_conversion: 0.06,
  sessions_per_player: 0.025,
  playtime_minutes: 0.03,
  d1_retention: 0.015
}

const WEEKEND: Partial<Record<keyof typeof BASE, number>> = { players: 0.12, new_players: 0.1, playtime_minutes: 0.14, sessions_per_player: 0.06, arpdau: 0.05 }

/** An ordinary day of the game with no event on: the weekday, a slow trend, and the day's noise. */
function ordinaryDay(dayStart: number, salt = 'game'): Record<keyof typeof BASE, number> {
  const random = seeded(`${salt}:${new Date(dayStart).toISOString().slice(0, 10)}`)
  const weekday = new Date(dayStart).getDay()
  const weekend = weekday === 0 || weekday === 6 ? 1 : weekday === 5 ? 0.4 : 0
  const trend = 1 + ((dayStart - Date.now()) / (365 * DAY)) * 0.12
  const out = {} as Record<keyof typeof BASE, number>
  for (const key of Object.keys(BASE) as (keyof typeof BASE)[]) {
    const scale = key === 'players' || key === 'new_players' ? trend : 1
    out[key] = BASE[key] * scale * (1 + (WEEKEND[key] ?? 0) * weekend) * (1 + NOISE[key] * gauss(random))
  }
  return out
}

/** The share of a day a window covers. */
const overlapOf = (dayStart: number, window: Occurrence) => Math.max(0, Math.min(window.end, dayStart + DAY) - Math.max(window.start, dayStart)) / DAY

/** Which of its windows an occurrence is, from its first: how worn the effect is. */
const indexOf = (event: LiveOpsEventDef, occurrence: Occurrence) =>
  event.schedule.mode === 'once' ? 0 : occurrencesOf(event.schedule, Date.parse(event.schedule.anchor), occurrence.start).length

const wear = (truth: Truth, index: number) => Math.max(0.25, 1 - (truth.fatigue ?? 0) * index)

/** Each metric's relative change on a day for an event's audience: on during its windows, after. */
function effectOn(event: LiveOpsEventDef, dayStart: number): Effects {
  const truth = truthOf(event)
  const windows = servedWindows(event, dayStart - 3 * DAY, dayStart + DAY)
  const out: Effects = {}
  for (const window of windows) {
    const scale = wear(truth, indexOf(event, window))
    const covered = overlapOf(dayStart, window)
    for (const [key, value] of Object.entries(truth.during) as [LiveOpsMetricKey, number][]) out[key] = (out[key] ?? 0) + value * covered * scale
    const days = (dayStart - startOfDay(window.end)) / DAY
    if (truth.after && days >= 0 && days < 3 && window.end <= dayStart + DAY) {
      const fade = window.end > dayStart ? (dayStart + DAY - window.end) / DAY : 1
      for (const [key, value] of Object.entries(truth.after) as [LiveOpsMetricKey, number][]) out[key] = (out[key] ?? 0) + value * fade * (1 - days / 3) * scale
    }
  }
  return out
}

/** Money and totals follow from the rest: revenue is players times what each spends. */
function complete(figures: Record<keyof typeof BASE, number>): DayFigures {
  return { ...figures, revenue: figures.players * figures.arpdau, ad_revenue: figures.players * 0.064 * (figures.playtime_minutes / BASE.playtime_minutes) }
}

/**
 * The whole game's day, every event that ran in it included, each weighed by the share of the
 * game it reached and the share of the day it was on. Null for a day that has not begun.
 */
export function sampleGameDay(dayStart: number, events: readonly LiveOpsEventDef[]): DayFigures | null {
  if (dayStart > Date.now()) return null
  const base = ordinaryDay(dayStart)
  const lift: Effects = {}
  for (const event of events) {
    const truth = truthOf(event)
    const reach = truth.share * (event.rollout / FULL_ROLLOUT) * (truth.spend ? Math.min(3, 1 + truth.spend * 0.02) : 1)
    for (const [key, value] of Object.entries(effectOn(event, dayStart)) as [LiveOpsMetricKey, number][]) lift[key] = (lift[key] ?? 0) + value * reach
  }
  /* Today is part of a day: its totals are the hours so far. */
  const sofar = dayStart === startOfDay(Date.now()) ? Math.max(0.05, (Date.now() - dayStart) / DAY) : 1
  const out = {} as Record<keyof typeof BASE, number>
  for (const key of Object.keys(BASE) as (keyof typeof BASE)[]) out[key] = base[key] * (1 + (lift[key] ?? 0)) * (key === 'players' || key === 'new_players' ? sofar : 1)
  return complete(out)
}

// --- An event's effect --------------------------------------------------------------------

export type ImpactMethod = 'holdout' | 'baseline'

export interface MetricImpact {
  metric: LiveOpsMetricKey
  /** What its players did while it was on, a day's worth. */
  during: number
  /** What they would have done without it, by the method below. */
  expected: number
  comparison: Comparison
  /** The three days after it ended against expected, or null while it is on or for a one-day gap. */
  after: Comparison | null
}

export interface EventImpact {
  occurrence: Occurrence
  method: ImpactMethod
  /** Players it reached, a day, while it was on. */
  players: number
  /** Players held back from it by the rollout, a day, and measured against. */
  heldBack: number
  /** Revenue from the players it reached while it was on, in all. */
  revenue: number
  /** The window is still on. */
  live: boolean
  /** Day starts, from two weeks before the window to a week after it or today. */
  days: number[]
  /** Per metric, what the players did each day, and what they would have without it. */
  daily: Record<LiveOpsMetricKey, { actual: number[], expected: number[] }>
  metrics: Record<LiveOpsMetricKey, MetricImpact>
}

const round = (value: number) => Math.round(value * 10_000) / 10_000

/**
 * What an event did in one of its windows, for the players it reached, metric by metric.
 *
 * Measured against the players the rollout held back where there are any: they match the same
 * audience and differ only by not getting it, which is the cleanest answer there is. Where
 * everybody got it, against the same players' own four weeks before, weekday for weekday and
 * carried forward, which is sound for a short event and weaker for a long one, and said so.
 * Null for a window that has not begun.
 */
export function sampleEventImpact(event: LiveOpsEventDef, occurrence: Occurrence, now = Date.now()): EventImpact | null {
  if (occurrence.start > now) return null
  const truth = truthOf(event)
  const method: ImpactMethod = event.rollout < FULL_ROLLOUT ? 'holdout' : 'baseline'
  const first = startOfDay(occurrence.start) - 14 * DAY
  const last = Math.min(startOfDay(now), startOfDay(occurrence.end - 1) + 7 * DAY)
  const days: number[] = []
  for (let at = first; at <= last; at = startOfDay(at + DAY + HOUR)) days.push(at)

  const reach = truth.share * (event.rollout / FULL_ROLLOUT)
  const spend = 1 + (truth.spend ?? 0)
  /* The expected line's own noise: smaller with a big holdout, bigger for a small audience. */
  const heldShare = 1 - event.rollout / FULL_ROLLOUT
  const expectedNoise = method === 'holdout' ? 0.35 / Math.sqrt(Math.max(heldShare * truth.share * 40, 0.4)) : 0.5

  const daily = {} as EventImpact['daily']
  for (const key of LIVEOPS_METRIC_KEYS) daily[key] = { actual: [], expected: [] }

  for (const at of days) {
    const base = ordinaryDay(at, `audience:${event.key}`)
    const shade = ordinaryDay(at, `expected:${event.key}`)
    const effect = effectOn({ ...event, status: event.status === 'draft' ? 'active' : event.status }, at)
    const sofar = at === startOfDay(now) ? Math.max(0.05, (now - at) / DAY) : 1
    const actual = {} as Record<keyof typeof BASE, number>
    const expected = {} as Record<keyof typeof BASE, number>
    for (const key of Object.keys(BASE) as (keyof typeof BASE)[]) {
      const size = key === 'players' || key === 'new_players' ? reach * sofar : key === 'arpdau' ? spend : 1
      const typical = BASE[key] * size
      actual[key] = base[key] * size * (1 + (effect[key] ?? 0))
      // The estimate of the day without the event: the ordinary day, with the estimate's own error.
      expected[key] = typical + (shade[key] * size - typical) * expectedNoise + (base[key] * size - typical) * (1 - expectedNoise)
    }
    const a = complete(actual)
    const e = complete(expected)
    for (const key of LIVEOPS_METRIC_KEYS) {
      daily[key].actual.push(round(a[key]))
      daily[key].expected.push(round(e[key]))
    }
  }

  /* Days, weighted by how much of each the window covered. */
  const weights = days.map(at => overlapOf(at, { start: occurrence.start, end: Math.min(occurrence.end, now) }))
  const afterWeights = days.map(at => (at >= startOfDay(occurrence.end) + DAY && at < startOfDay(occurrence.end) + 4 * DAY && occurrence.end <= now ? 1 : 0))
  const baselineDays = days.map(at => (at < startOfDay(occurrence.start) ? 1 : 0))

  const mean = (values: number[], weight: number[]) => {
    const total = weight.reduce((sum, value) => sum + value, 0)
    return total ? values.reduce((sum, value, index) => sum + value * weight[index]!, 0) / total : 0
  }

  const compare = (key: LiveOpsMetricKey, weight: number[]): Comparison | null => {
    const count = weight.reduce((sum, value) => sum + value, 0)
    if (count < 0.05) return null
    const actual = mean(daily[key].actual, weight)
    const expected = mean(daily[key].expected, weight)
    // The spread of the gap on ordinary days is the noise any gap has to beat.
    const gaps = daily[key].actual.map((value, index) => value - daily[key].expected[index]!).filter((_, index) => baselineDays[index])
    const gapMean = gaps.reduce((sum, value) => sum + value, 0) / Math.max(gaps.length, 1)
    const sd = Math.sqrt(gaps.reduce((sum, value) => sum + (value - gapMean) ** 2, 0) / Math.max(gaps.length - 1, 1)) || Math.abs(expected) * 0.02
    const se = sd / Math.sqrt(Math.max(count, 1)) * (method === 'holdout' ? 1 : 1.25)
    const difference = actual - expected
    const base = expected || 1e-9
    return { lift: difference / base, low: (difference - 1.96 * se) / base, high: (difference + 1.96 * se) / base, chance: normalCdf(difference / (se || 1e-9)), difference }
  }

  const metrics = {} as Record<LiveOpsMetricKey, MetricImpact>
  for (const key of LIVEOPS_METRIC_KEYS) {
    const comparison = compare(key, weights)
    metrics[key] = {
      metric: key,
      during: mean(daily[key].actual, weights),
      expected: mean(daily[key].expected, weights),
      comparison: comparison ?? { lift: 0, low: 0, high: 0, chance: 0.5, difference: 0 },
      after: compare(key, afterWeights)
    }
  }

  const onDays = weights.reduce((sum, value) => sum + value, 0)
  const players = metrics.players.during
  return {
    occurrence,
    method,
    players,
    heldBack: Math.round(players / Math.max(event.rollout / FULL_ROLLOUT, 0.01) * heldShare),
    revenue: metrics.revenue.during * onDays,
    live: occurrence.end > now,
    days,
    daily,
    metrics
  }
}

/**
 * The stretches an event is read in. A window each for most; for one that opens every day or
 * every few hours, a week of them at a time, because a two-hour happy hour is a sliver of a
 * day and only a week of them says anything.
 */
export function runsOf(event: LiveOpsEventDef, now: number, count = 8): Occurrence[] {
  const until = Math.min(now, servedUntil(event))
  if (!isRhythm(event.schedule)) return pastOccurrences(event.schedule, until, count).map(entry => ({ start: entry.start, end: Math.min(entry.end, servedUntil(event)) }))
  const windows = occurrencesOf(event.schedule, until - count * 7 * DAY, until)
  if (!windows.length) return []
  const runs: Occurrence[] = []
  for (let end = startOfDay(until) + DAY; runs.length < count; end -= 7 * DAY) {
    const inside = windows.filter(entry => entry.start >= end - 7 * DAY && entry.start < end)
    if (!inside.length) break
    runs.push({ start: inside[0]!.start, end: inside.at(-1)!.end })
  }
  return runs
}

/** What else was on while a window was: other events and A/B tests, and for how long. */
export interface Overlap {
  kind: 'event' | 'experiment'
  id: string
  name: string
  tone: number
  /** Share of the window the other was on for. */
  share: number
  to: string
}

export function overlapsOf(event: LiveOpsEventDef, window: Occurrence, events: readonly LiveOpsEventDef[], experiments: readonly ExperimentDef[]): Overlap[] {
  const length = window.end - window.start
  const shareOf = (others: Occurrence[]) => others.reduce((sum, other) => sum + Math.max(0, Math.min(other.end, window.end) - Math.max(other.start, window.start)), 0) / length
  const out: Overlap[] = []
  for (const other of events) {
    if (other.key === event.key || other.status === 'draft') continue
    const share = shareOf(servedWindows(other, window.start, window.end))
    if (share > 0.02) out.push({ kind: 'event', id: other.key, name: other.name, tone: kindLook(other.kind).tone, share, to: `/growth/liveops/${other.key}` })
  }
  for (const test of experiments) {
    if (!test.started_at) continue
    const start = Date.parse(test.started_at)
    const end = test.ended_at ? Date.parse(test.ended_at) : Date.now()
    const share = shareOf([{ start, end }])
    if (share > 0.02) out.push({ kind: 'experiment', id: test.id, name: test.name, tone: 1, share, to: `/growth/experiments/${test.id}` })
  }
  return out.sort((a, b) => b.share - a.share)
}
