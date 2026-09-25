// Live Ops events as the dashboard holds them: the LiveOpsEventOut the API returns
// (apps/backend/internal/schemas/liveops.go), the schedule internal/liveops expands into
// windows, and the arithmetic that says what an event did to the game while it was on.
//
// An event is a schedule, an audience and a payload. PlayerSense owns the first two and never
// reads the third: the studio decides what a "double XP weekend" or a "holiday shop" means in
// its game, and the SDK hands the payload over while the event is on for that player. So the
// same few fields serve a seasonal event, an offer, a tournament, an economy multiplier and an
// announcement, and `kind` is the studio's own word for which one it is.
//
// Three things are the dashboard's own and not stored by the API yet: `description`, the metric
// an event is meant to move (`goal`) and the ones it must not hurt (`watch`). They decide what
// the results page leads with; nothing a player receives depends on them.

export type LiveOpsStatus = 'draft' | 'active' | 'paused' | 'archived'

export type ScheduleMode = 'once' | 'hourly' | 'daily' | 'weekly' | 'monthly' | 'yearly'

/** When an event is on: where it starts, how long each time lasts, and how it repeats. */
export interface LiveOpsSchedule {
  mode: ScheduleMode
  /** The first instant it is on. For a repeating schedule its date parts are what repeats. */
  anchor: string
  /** How long one occurrence lasts, as a Go duration the API takes: "2h", "54h", "30m". */
  duration: string
  /** Periods to skip: 3 with daily is every third day. Absent, 0 and 1 all mean every. */
  interval?: number
  /** When repetition stops. Absent repeats until somebody stops it. */
  until?: string
  /** 0 is Sunday. For weekly only; empty means the anchor's own weekday. */
  weekdays?: number[]
  /** An IANA name. Absent means UTC. The studio's clock, not each player's. */
  timezone?: string
}

export interface LiveOpsHistoryEntry {
  at: string
  actor: string
  text: string
}

export interface LiveOpsEventDef {
  /** Immutable: the game reads the event by it. */
  key: string
  /** The studio's own type: seasonal_event, offer, tournament. Not an enum. */
  kind: string
  name: string
  description: string
  status: LiveOpsStatus
  schedule: LiveOpsSchedule
  /** Higher wins when two events of the same kind are on for one player at once. */
  priority: number
  /** Who it is for. Empty is every player. */
  audience: TargetRule
  /** What the game gets while it is on. PlayerSense never reads it. */
  payload: Json
  /** Hundredths of a percent of the audience. The rest are held back, and measured against. */
  rollout: number
  salt: string
  /** The metric it is meant to move. Null for one that is not meant to move any: a notice. */
  goal: LiveOpsMetricKey | null
  /** Metrics that must not get worse while it is on. */
  watch: LiveOpsMetricKey[]
  created_at: string
  updated_at: string
  updated_by: string
  history: LiveOpsHistoryEntry[]
}

// --- Kinds ----------------------------------------------------------------------------------

export interface KindLook {
  label: string
  /** Plural, for a lane of the calendar. */
  lane: string
  icon: string
  /** A chart palette slot. */
  tone: 1 | 2 | 3 | 4 | 5 | 6
  note: string
  /** What a new event of this kind starts with, so nobody faces an empty payload. */
  payload: Json
  goal: LiveOpsMetricKey | null
}

/**
 * The kinds a studio usually has, offered first. Any other word is as good: an unknown kind is
 * drawn with a plain glyph and a colour of its own, and gets a lane of its own on the calendar.
 */
export const KIND_PRESETS: Record<string, KindLook> = {
  seasonal_event: {
    label: 'Seasonal event',
    lane: 'Seasonal events',
    icon: 'material-symbols:celebration-outline',
    tone: 2,
    note: 'A themed event for a holiday or a season: its own map, currency, quests and shop.',
    payload: { theme: 'halloween', currency: 'candy', store_tab: true, quests: ['q_pumpkin_1', 'q_pumpkin_2'] },
    goal: 'players'
  },
  season: {
    label: 'Season',
    lane: 'Seasons',
    icon: 'material-symbols:stacks-outline',
    tone: 6,
    note: 'A battle pass or ranked season: weeks long, with tiers and a reset at the end.',
    payload: { season: 8, tiers: 60, premium_product_id: 'bp_s8_premium' },
    goal: 'revenue'
  },
  offer: {
    label: 'Offer',
    lane: 'Offers',
    icon: 'material-symbols:sell-outline',
    tone: 3,
    note: 'A sale, a bundle or a bonus on purchases, for a while or for some players.',
    payload: { product_id: 'gems_pack_large', discount_percent: 30, badge: 'SALE' },
    goal: 'revenue'
  },
  tournament: {
    label: 'Tournament',
    lane: 'Tournaments',
    icon: 'material-symbols:trophy-outline',
    tone: 4,
    note: 'A competition with a leaderboard and prizes.',
    payload: { leaderboard: 'weekend_cup', entry_fee: 0, prizes: [{ rank: 1, gems: 500 }, { rank: 10, gems: 100 }] },
    goal: 'sessions_per_player'
  },
  multiplier: {
    label: 'Boost',
    lane: 'Boosts',
    icon: 'material-symbols:bolt-outline',
    tone: 1,
    note: 'A multiplier on the economy: double XP, faster energy, more drops.',
    payload: { xp_multiplier: 2 },
    goal: 'playtime_minutes'
  },
  reward: {
    label: 'Reward',
    lane: 'Rewards',
    icon: 'material-symbols:redeem-outline',
    tone: 5,
    note: 'Something given away: a login calendar, a comeback gift, a compensation.',
    payload: { reward: { gems: 100 }, claim_once: true },
    goal: 'players'
  },
  game_mode: {
    label: 'Game mode',
    lane: 'Game modes',
    icon: 'material-symbols:stadia-controller-outline',
    tone: 1,
    note: 'A mode switched on for a while: a limited queue, a special map.',
    payload: { mode: 'ranked_rush', queue: 'rush', map_pool: ['harbor', 'canyon'] },
    goal: 'sessions_per_player'
  },
  announcement: {
    label: 'Announcement',
    lane: 'Announcements',
    icon: 'material-symbols:campaign-outline',
    tone: 6,
    note: 'A message in the game: maintenance, news, a patch note. Usually not meant to move a metric.',
    payload: { title: 'Scheduled maintenance', body: 'Servers are down for an hour.', severity: 'info' },
    goal: null
  }
}

export const KIND_ORDER = Object.keys(KIND_PRESETS)

/** "seasonal_event" as "Seasonal event", for a kind nobody gave a name. */
const humanise = (kind: string) => {
  const words = kind.replace(/[_.-]+/g, ' ').trim()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

export function kindLook(kind: string): KindLook {
  const preset = KIND_PRESETS[kind]
  if (preset) return preset
  let hash = 0
  for (const char of kind) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  const label = humanise(kind) || 'Event'
  return {
    label,
    lane: label,
    icon: 'material-symbols:event-outline',
    tone: ((hash % 6) + 1) as KindLook['tone'],
    note: 'A kind of your own.',
    payload: {},
    goal: 'players'
  }
}

/** A palette slot as a colour a style attribute takes. */
export const toneColour = (tone: number) => `rgb(var(--v-theme-chart-${tone}))`

// --- Metrics --------------------------------------------------------------------------------

export type LiveOpsMetricKey
  = | 'players'
    | 'new_players'
    | 'revenue'
    | 'arpdau'
    | 'payer_conversion'
    | 'sessions_per_player'
    | 'playtime_minutes'
    | 'd1_retention'
    | 'ad_revenue'

export interface LiveOpsMetricDef {
  key: LiveOpsMetricKey
  label: string
  kind: 'count' | 'money' | 'rate' | 'minutes' | 'ratio'
  note: string
}

/** What an event is read against: the game's own daily figures, for the players it reached. */
export const LIVEOPS_METRICS: Record<LiveOpsMetricKey, LiveOpsMetricDef> = {
  players: { key: 'players', label: 'Daily players', kind: 'count', note: 'Players who played that day.' },
  new_players: { key: 'new_players', label: 'New players', kind: 'count', note: 'First sessions that day.' },
  revenue: { key: 'revenue', label: 'Revenue', kind: 'money', note: 'Verified purchases that day, in USD, after refunds.' },
  arpdau: { key: 'arpdau', label: 'Revenue per daily player', kind: 'money', note: 'Revenue over the day\'s players, payers or not (ARPDAU).' },
  payer_conversion: { key: 'payer_conversion', label: 'Payer conversion', kind: 'rate', note: 'The share of the day\'s players who bought anything.' },
  sessions_per_player: { key: 'sessions_per_player', label: 'Sessions per player', kind: 'ratio', note: 'Sessions that day, over the day\'s players.' },
  playtime_minutes: { key: 'playtime_minutes', label: 'Playtime per player', kind: 'minutes', note: 'Minutes played that day, over the day\'s players.' },
  d1_retention: { key: 'd1_retention', label: 'Came back next day', kind: 'rate', note: 'The share of the day\'s players who played again the day after.' },
  ad_revenue: { key: 'ad_revenue', label: 'Ad revenue', kind: 'money', note: 'Estimated ad revenue that day.' }
}

/** What an event's goal and watch can name: what its impact is measured on. Ad revenue is not measured yet. */
export const LIVEOPS_METRIC_KEYS = (Object.keys(LIVEOPS_METRICS) as LiveOpsMetricKey[]).filter(key => key !== 'ad_revenue')

/** A metric's value as a table writes it: "48.2k", "$12.4k", "3.1%", "42 min", "2.4". */
export function writeOpsMetric(metric: LiveOpsMetricKey, value: number, compact = true): string {
  const kind = LIVEOPS_METRICS[metric].kind
  if (kind === 'rate') return `${(value * 100).toFixed(1)}%`
  if (kind === 'minutes') return `${Math.round(value)} min`
  if (kind === 'ratio') return value.toFixed(1)
  if (kind === 'money') return value < 10 ? `$${value.toFixed(3)}` : `$${writeFigure(value, { compact })}`
  return writeFigure(Math.round(value), { compact })
}

/** A lift as a signed percent, or for a rate in points: "+12.4%", "+0.8 pts". */
export function writeOpsLift(metric: LiveOpsMetricKey, comparison: Pick<Comparison, 'lift' | 'difference'>): string {
  if (LIVEOPS_METRICS[metric].kind === 'rate') {
    const points = comparison.difference * 100
    return `${points >= 0 ? '+' : ''}${points.toFixed(1)} pts`
  }
  const percent = comparison.lift * 100
  return `${percent >= 0 ? '+' : ''}${percent.toFixed(1)}%`
}

// --- Durations ------------------------------------------------------------------------------

const MINUTE = 60_000
const HOUR = 3_600_000
export const DAY = 86_400_000

/** A Go duration, "54h", "1h30m", "90m", in milliseconds. Zero for one that does not parse. */
export function parseDuration(text: string): number {
  const parts = [...text.trim().matchAll(/(\d+(?:\.\d+)?)(h|m|s)/g)]
  if (!parts.length || parts.map(part => part[0]).join('') !== text.trim()) return 0
  return parts.reduce((sum, [, amount, unit]) => sum + Number(amount) * (unit === 'h' ? HOUR : unit === 'm' ? MINUTE : 1000), 0)
}

/** Milliseconds as the API's Go duration: "54h", "2h30m", "45m". */
export function formatDuration(ms: number): string {
  const minutes = Math.round(ms / MINUTE)
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  if (!hours) return `${rest}m`
  return rest ? `${hours}h${rest}m` : `${hours}h`
}

/** Milliseconds in words: "2 days 6 hours", "2 hours", "45 minutes". */
export function durationWords(ms: number): string {
  const minutes = Math.round(ms / MINUTE)
  const days = Math.floor(minutes / 1440)
  const hours = Math.floor((minutes % 1440) / 60)
  const rest = minutes % 60
  const unit = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`
  return [days ? unit(days, 'day') : '', hours ? unit(hours, 'hour') : '', rest ? unit(rest, 'minute') : ''].filter(Boolean).join(' ') || '0 minutes'
}

/** How long until, or since, in the fewest words: "in 3 days", "in 4 hours", "2 days ago". */
export function relativeWords(at: number, now: number): string {
  const gap = at - now
  const size = Math.abs(gap)
  const unit = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`
  const text = size >= 2 * DAY ? unit(Math.round(size / DAY), 'day') : size >= HOUR ? unit(Math.round(size / HOUR), 'hour') : unit(Math.max(1, Math.round(size / MINUTE)), 'minute')
  return gap >= 0 ? `in ${text}` : `${text} ago`
}

// --- Occurrences ----------------------------------------------------------------------------

/** One concrete window. The end is exclusive, so back-to-back windows never share an instant. */
export interface Occurrence {
  start: number
  end: number
}

/** The most one expansion returns, as internal/liveops caps it. */
export const MAX_OCCURRENCES = 366

/** The length of one period of a repeating schedule; zero for a one-off and the calendar ones. */
function periodOf(schedule: LiveOpsSchedule): number {
  const every = Math.max(1, schedule.interval ?? 1)
  if (schedule.mode === 'hourly') return every * HOUR
  if (schedule.mode === 'daily') return every * DAY
  if (schedule.mode === 'weekly') return every * 7 * DAY
  return 0
}

/**
 * The windows a schedule opens that overlap [from, to), oldest first. The same expansion the
 * server performs, so what the calendar draws is what a player gets. Clock times are read in
 * this browser's zone, which is close enough to draw and is marked as such where it matters.
 */
export function occurrencesOf(schedule: LiveOpsSchedule, from: number, to: number): Occurrence[] {
  const anchor = Date.parse(schedule.anchor)
  const length = parseDuration(schedule.duration)
  if (!Number.isFinite(anchor) || length <= 0) return []
  const until = schedule.until ? Date.parse(schedule.until) : Number.POSITIVE_INFINITY
  const every = Math.max(1, schedule.interval ?? 1)
  const out: Occurrence[] = []
  const push = (start: number) => {
    if (start + length > from && start < to && start < until && start >= anchor) out.push({ start, end: start + length })
  }

  if (schedule.mode === 'once') {
    push(anchor)
    return out
  }

  if (schedule.mode === 'hourly' || schedule.mode === 'daily') {
    const period = periodOf(schedule)
    const first = Math.max(0, Math.floor((from - length - anchor) / period))
    for (let index = first; out.length < MAX_OCCURRENCES; index += 1) {
      const start = anchor + index * period
      if (start >= to || start >= until) break
      push(start)
    }
    return out
  }

  const base = new Date(anchor)
  if (schedule.mode === 'weekly') {
    const weekdays = schedule.weekdays?.length ? [...schedule.weekdays].sort() : [base.getDay()]
    const weekStart = new Date(base)
    weekStart.setDate(base.getDate() - base.getDay())
    const firstWeek = Math.max(0, Math.floor((from - length - weekStart.getTime()) / (7 * DAY * every)) - 1)
    for (let week = firstWeek; out.length < MAX_OCCURRENCES; week += 1) {
      const sunday = new Date(weekStart)
      sunday.setDate(weekStart.getDate() + week * 7 * every)
      if (sunday.getTime() >= to || sunday.getTime() >= until) break
      for (const weekday of weekdays) {
        const at = new Date(sunday)
        at.setDate(sunday.getDate() + weekday)
        push(at.getTime())
      }
    }
    return out
  }

  // Monthly and yearly step the calendar, and skip a month without the anchor's day.
  const stepMonths = schedule.mode === 'monthly' ? every : 12 * every
  for (let index = 0; out.length < MAX_OCCURRENCES && index < 2400; index += 1) {
    const at = new Date(base)
    at.setDate(1)
    at.setMonth(base.getMonth() + index * stepMonths)
    const month = at.getMonth()
    at.setDate(base.getDate())
    if (at.getMonth() !== month) continue
    const start = at.getTime()
    if (start >= to || start >= until) break
    push(start)
  }
  return out
}

/** The next window that has not ended, from now. */
export function nextOccurrence(schedule: LiveOpsSchedule, now: number, horizon = 400 * DAY): Occurrence | null {
  return occurrencesOf(schedule, now, now + horizon)[0] ?? null
}

/** Windows that have ended or begun, newest first, up to a count. */
export function pastOccurrences(schedule: LiveOpsSchedule, now: number, count = 12, lookback = 400 * DAY): Occurrence[] {
  return occurrencesOf(schedule, now - lookback, now).filter(entry => entry.start <= now).slice(-count).reverse()
}

/** Whether a schedule is worth drawing as one rhythm rather than bar by bar: many short windows. */
export const isRhythm = (schedule: LiveOpsSchedule) =>
  (schedule.mode === 'hourly' || schedule.mode === 'daily') && parseDuration(schedule.duration) < DAY

/**
 * Why the server would refuse a schedule, in its own words (liveops.Schedule.Validate), or
 * null. Checked as it is typed, so the refusal is beside the field it is about.
 */
export function scheduleProblem(schedule: LiveOpsSchedule): string | null {
  if (!Number.isFinite(Date.parse(schedule.anchor))) return 'A schedule needs a start.'
  const length = parseDuration(schedule.duration)
  if (length <= 0) return 'A schedule needs a duration.'
  if (schedule.until && Date.parse(schedule.until) <= Date.parse(schedule.anchor)) return 'The schedule ends before it begins.'
  const period = periodOf(schedule)
  if (period > 0 && length > period) return `A ${schedule.mode} occurrence lasting ${durationWords(length)} overlaps the next one. Shorten it or change the repeat.`
  if (schedule.mode === 'monthly' && length > 28 * DAY * Math.max(1, schedule.interval ?? 1)) return 'A monthly occurrence this long overlaps the next one.'
  return null
}

// --- The repeat, as a person sets it ---------------------------------------------------------

export type RepeatUnit = 'hours' | 'days' | 'weeks' | 'months' | 'years'
export type LengthUnit = 'minutes' | 'hours' | 'days'

/**
 * A schedule the way somebody says it: "on for 3 days, every 7 days", "on for 3 days, then 7
 * days off", "every Friday and Saturday", "ending after 10 times". The form edits this, and it
 * compiles to the anchor, duration and repeat the API stores, so one event can stand for every
 * new year or every weekend there will ever be.
 *
 * `countFrom` is the one choice people read two ways. Every 7 days from each start keeps the
 * event on the same weekday: Jan 1 to 3, Jan 8 to 10. Seven days off after each one keeps the
 * break the same length whatever the event's: Jan 1 to 3, Jan 11 to 13. The second is stored
 * as a repeat every length plus gap, in hours, which is exact.
 */
export interface RepeatDraft {
  repeats: boolean
  every: number
  unit: RepeatUnit
  countFrom: 'start' | 'end'
  /** For weeks counted from the start: which days it opens on. */
  weekdays: number[]
  /** The first window's start, as a datetime-local input holds it: "2026-01-01T00:00". */
  start: string
  length: number
  lengthUnit: LengthUnit
  ends: 'never' | 'on' | 'after'
  /** For `on`: the last day a window may start, "2026-12-31". */
  until: string
  /** For `after`: how many windows in all. */
  times: number
  timezone: string
}

const LENGTH_MS: Record<LengthUnit, number> = { minutes: MINUTE, hours: HOUR, days: DAY }

const pad = (value: number) => String(value).padStart(2, '0')

/** A time as a datetime-local input holds it. */
export const localInput = (at: number) => {
  const date = new Date(at)
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/** The draft's windows' length, in milliseconds. */
export const draftLength = (draft: RepeatDraft) => Math.max(0, draft.length) * LENGTH_MS[draft.lengthUnit]

export function scheduleFromDraft(draft: RepeatDraft): LiveOpsSchedule {
  const anchor = new Date(draft.start).toISOString()
  const length = draftLength(draft)
  const timezone = draft.timezone === 'UTC' ? undefined : draft.timezone
  const every = Math.max(1, Math.round(draft.every) || 1)
  const base = { anchor, duration: formatDuration(length), ...(timezone ? { timezone } : {}) }
  if (!draft.repeats) return { mode: 'once', ...base }

  let schedule: LiveOpsSchedule
  if (draft.countFrom === 'end' && ['hours', 'days', 'weeks'].includes(draft.unit)) {
    const gap = every * ({ hours: HOUR, days: DAY, weeks: 7 * DAY } as Record<string, number>)[draft.unit]!
    schedule = { mode: 'hourly', ...base, interval: Math.round((length + gap) / HOUR) }
  } else {
    const mode = ({ hours: 'hourly', days: 'daily', weeks: 'weekly', months: 'monthly', years: 'yearly' } as const)[draft.unit]
    schedule = { mode, ...base, ...(every > 1 ? { interval: every } : {}) }
    if (mode === 'weekly' && draft.weekdays.length) {
      const own = new Date(draft.start).getDay()
      if (!(draft.weekdays.length === 1 && draft.weekdays[0] === own)) schedule.weekdays = [...draft.weekdays].sort()
    }
  }

  if (draft.ends === 'on' && draft.until) {
    const until = new Date(`${draft.until}T23:59`).getTime() + MINUTE
    schedule.until = new Date(until).toISOString()
  } else if (draft.ends === 'after' && draft.times > 0) {
    // Bounded just past the start of the last window it should open.
    const windows = occurrencesOf(schedule, Date.parse(anchor), Date.parse(anchor) + 60 * 366 * DAY).slice(0, Math.round(draft.times))
    const last = windows.at(-1)
    if (last) schedule.until = new Date(last.start + MINUTE).toISOString()
  }
  return schedule
}

/** The draft a stored schedule is edited as. */
export function draftFromSchedule(schedule: LiveOpsSchedule): RepeatDraft {
  const anchor = Date.parse(schedule.anchor)
  const length = parseDuration(schedule.duration)
  const [amount, lengthUnit]: [number, LengthUnit] = length % DAY === 0 ? [length / DAY, 'days'] : length % HOUR === 0 ? [length / HOUR, 'hours'] : [Math.round(length / MINUTE), 'minutes']
  const every = Math.max(1, schedule.interval ?? 1)
  const draft: RepeatDraft = {
    repeats: schedule.mode !== 'once',
    every,
    unit: 'weeks',
    countFrom: 'start',
    weekdays: schedule.weekdays?.length ? [...schedule.weekdays] : [new Date(anchor).getDay()],
    start: localInput(anchor),
    length: amount,
    lengthUnit,
    ends: schedule.until ? 'on' : 'never',
    until: schedule.until ? localInput(Date.parse(schedule.until) - MINUTE).slice(0, 10) : '',
    times: 10,
    timezone: schedule.timezone ?? 'UTC'
  }
  if (schedule.mode === 'hourly') {
    const gap = every * HOUR - length
    if (every >= 24 && gap > 0) {
      const [size, unit]: [number, RepeatUnit] = gap % (7 * DAY) === 0 ? [gap / (7 * DAY), 'weeks'] : gap % DAY === 0 ? [gap / DAY, 'days'] : [Math.round(gap / HOUR), 'hours']
      return { ...draft, countFrom: 'end', every: size, unit }
    }
    return { ...draft, unit: 'hours' }
  }
  if (schedule.mode === 'once') return draft
  return { ...draft, unit: ({ daily: 'days', weekly: 'weeks', monthly: 'months', yearly: 'years' } as const)[schedule.mode] }
}

// --- In words -------------------------------------------------------------------------------

const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
export const WEEKDAY_SHORT = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

const clock = (at: Date) => at.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })

const ordinal = (day: number) => {
  const tail = day % 100 >= 11 && day % 100 <= 13 ? 'th' : ({ 1: 'st', 2: 'nd', 3: 'rd' } as Record<number, string>)[day % 10] ?? 'th'
  return `${day}${tail}`
}

/** "Oct 17, 10:00" or, outside this year, "Oct 17, 2027, 10:00". */
export function whenAt(at: number): string {
  const date = new Date(at)
  const sameYear = date.getFullYear() === new Date().getFullYear()
  return `${date.toLocaleDateString('en', { month: 'short', day: 'numeric', ...(sameYear ? {} : { year: 'numeric' }) })}, ${clock(date)}`
}

/**
 * The last minute a window is on. The end is exclusive, so three days from Jan 1, 00:00 end at
 * Jan 4, 00:00 and the last minute anybody plays in is Jan 3, 23:59, which is how a person
 * reads "until" and how every date on these screens is written.
 */
export const lastMinute = (occurrence: Occurrence) => occurrence.end - MINUTE

/** A window in words: "Fri Sep 18, 18:00 to Sun Sep 20, 23:59", "Thu Sep 24, 20:00 to 21:59". */
export function windowWords(occurrence: Occurrence): string {
  const start = new Date(occurrence.start)
  const last = new Date(lastMinute(occurrence))
  const day = (date: Date) => date.toLocaleDateString('en', { weekday: 'short', month: 'short', day: 'numeric' })
  if (start.toDateString() === last.toDateString()) return `${day(start)}, ${clock(start)} to ${clock(last)}`
  return `${day(start)}, ${clock(start)} to ${day(last)}, ${clock(last)}`
}

/** A window in the fewest words, for a chip: "Sep 18 to 20", "Oct 17 to Nov 2", "Sep 24". */
export function windowShort(occurrence: Occurrence): string {
  const start = new Date(occurrence.start)
  const last = new Date(lastMinute(occurrence))
  const month = (date: Date) => date.toLocaleDateString('en', { month: 'short' })
  if (start.toDateString() === last.toDateString()) return `${month(start)} ${start.getDate()}`
  if (start.getMonth() === last.getMonth()) return `${month(start)} ${start.getDate()} to ${last.getDate()}`
  return `${month(start)} ${start.getDate()} to ${month(last)} ${last.getDate()}`
}

/**
 * The schedule as a sentence, which is how anybody checks they set it right: "Every Friday at
 * 18:00 for 2 days 6 hours", "The 1st of every month at 00:00 for 5 days", "Once, Oct 17 at
 * 10:00 for 16 days".
 */
export function scheduleWords(schedule: LiveOpsSchedule): string {
  const anchor = new Date(Date.parse(schedule.anchor))
  const length = durationWords(parseDuration(schedule.duration))
  const every = Math.max(1, schedule.interval ?? 1)
  const at = clock(anchor)
  let text: string
  switch (schedule.mode) {
    case 'once':
      text = `Once, ${whenAt(anchor.getTime()).replace(', ', ' at ')} for ${length}`
      break
    case 'hourly': {
      // A repeat counted from the end of the last one is stored as a period of hours: the
      // length and the gap together. Said back the way it was set.
      const gap = every * HOUR - parseDuration(schedule.duration)
      text = every >= 24 && gap > 0
        ? `From ${whenAt(anchor.getTime()).replace(', ', ' at ')}, on for ${length} then off for ${durationWords(gap)}, over and over`
        : `Every ${every === 1 ? 'hour' : `${every} hours`} for ${length}`
      break
    }
    case 'daily':
      text = `${every === 1 ? 'Every day' : `Every ${every} days`} at ${at} for ${length}`
      break
    case 'weekly': {
      const days = (schedule.weekdays?.length ? schedule.weekdays : [anchor.getDay()]).map(day => WEEKDAYS[day]!)
      const list = days.length > 1 ? `${days.slice(0, -1).join(', ')} and ${days.at(-1)}` : days[0]
      text = `Every ${every === 2 ? 'other ' : every > 2 ? `${every} weeks on ` : ''}${list} at ${at} for ${length}`
      break
    }
    case 'monthly':
      text = `The ${ordinal(anchor.getDate())} of ${every === 1 ? 'every month' : every === 3 ? 'every quarter' : `every ${every} months`} at ${at} for ${length}`
      break
    case 'yearly':
      text = `Every ${anchor.toLocaleDateString('en', { month: 'long', day: 'numeric' })} at ${at} for ${length}`
      break
  }
  if (schedule.mode !== 'once' && schedule.until) text += `, until ${whenAt(Date.parse(schedule.until)).split(',')[0]}`
  return text
}

export const SCHEDULE_MODES: { value: ScheduleMode, title: string, note: string }[] = [
  { value: 'once', title: 'Once', note: 'A single window: a holiday event, a season.' },
  { value: 'hourly', title: 'Hourly', note: 'Every few hours: a rotating shop, a raid boss.' },
  { value: 'daily', title: 'Daily', note: 'At the same time each day: a happy hour.' },
  { value: 'weekly', title: 'Weekly', note: 'On chosen weekdays: a weekend event.' },
  { value: 'monthly', title: 'Monthly', note: 'On a day of the month: a login calendar.' },
  { value: 'yearly', title: 'Yearly', note: 'On a date: an anniversary.' }
]

export const TIMEZONES = ['UTC', 'Europe/London', 'Europe/Istanbul', 'Europe/Berlin', 'America/New_York', 'America/Los_Angeles', 'America/Sao_Paulo', 'Asia/Tokyo', 'Asia/Seoul', 'Asia/Shanghai', 'Asia/Kolkata', 'Australia/Sydney']

// --- State ----------------------------------------------------------------------------------

/**
 * What an event is doing now. Only draft, active, paused and archived are stored; live,
 * scheduled and ended are what an active event's calendar says at this moment, worked out
 * rather than kept, so nothing has to run at midnight to keep a word true.
 */
export type LiveOpsState = 'live' | 'scheduled' | 'ended' | 'draft' | 'paused' | 'archived'

export const LIVEOPS_STATE: Record<LiveOpsState, { label: string, tone: BadgeTone, note: string }> = {
  live: { label: 'Live', tone: 'success', note: 'On for its players right now.' },
  scheduled: { label: 'Scheduled', tone: 'neutral', note: 'Active, and waiting for its next window.' },
  ended: { label: 'Ended', tone: 'neutral', note: 'Active, but its schedule has no window left.' },
  draft: { label: 'Draft', tone: 'neutral', note: 'Not published to players. Activate it to schedule it.' },
  paused: { label: 'Paused', tone: 'warning', note: 'Off for everybody, whatever its schedule says, until it is resumed.' },
  archived: { label: 'Archived', tone: 'neutral', note: 'Kept for its history. Never served.' }
}

export const STATE_ORDER: LiveOpsState[] = ['live', 'scheduled', 'paused', 'draft', 'ended', 'archived']

export function stateOf(event: LiveOpsEventDef, now: number): { state: LiveOpsState, current: Occurrence | null, next: Occurrence | null } {
  if (event.status !== 'active') return { state: event.status, current: null, next: event.status === 'archived' ? null : nextOccurrence(event.schedule, now) }
  const next = nextOccurrence(event.schedule, now)
  if (next && next.start <= now) return { state: 'live', current: next, next: nextOccurrence(event.schedule, next.end) }
  return next ? { state: 'scheduled', current: null, next } : { state: 'ended', current: null, next: null }
}

/** The state as a line under the badge: "Ends in 2 days", "Starts in 5 hours", "Ended Sep 20". */
export function stateWords(event: LiveOpsEventDef, now: number): string {
  const { state, current, next } = stateOf(event, now)
  if (state === 'live' && current) return `Ends ${relativeWords(current.end, now)}`
  if (state === 'scheduled' && next) return `Starts ${relativeWords(next.start, now)}`
  if (state === 'ended') {
    const last = pastOccurrences(event.schedule, now, 1)[0]
    return last ? `Ended ${windowShort({ start: lastMinute(last), end: last.end })}` : 'Ended'
  }
  if (state === 'draft' && next) return `Would start ${relativeWords(next.start, now)}`
  if (state === 'paused') return 'Off until resumed'
  return ''
}
