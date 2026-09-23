// Sample data for the Funnels page, generated in the browser. No funnel query exists yet
// (project.md §31); what this stands in for:
//
//   A funnel is a canonical definition — ordered steps, each an event with optional
//   property conditions, and a conversion window — hashed so its answer can be cached.
//   A player enters on their first step 1 in the range and converts through step N if
//   they then do each step in order, other events in between allowed, all within the
//   window of that step 1. Counts are unique players; a count under the suppression
//   threshold is withheld rather than drawn as zero (§32.2). "Same session" keeps a
//   funnel answerable for a Kids-tier project, where cross-day ones are not
//   (docs/compliance/04-children-and-platform-rules.md).
//
// Seeded by the definition, the range and the breakdown, so a reload draws the same page.

import type { EventDefinition } from './events-sample'

export type FunnelWindow = 'session' | '1h' | '1d' | '7d' | '30d'
export type FunnelRange = '7d' | '30d' | '90d'
export type FunnelBreakdown = 'none' | 'install_platform' | 'install_country' | 'first_app_version'

export interface FunnelCondition {
  property: string
  value: string
}

export interface FunnelStep {
  event: string
  /** All must hold. */
  where: FunnelCondition[]
}

export interface FunnelDefinition {
  id: string
  name: string
  steps: FunnelStep[]
  window: FunnelWindow
}

export const MIN_STEPS = 2
export const MAX_STEPS = 10

/** Counts under this are withheld: the API's default k (§32.2). */
export const FUNNEL_THRESHOLD = 5

export const WINDOW_LABELS: Record<FunnelWindow, string> = {
  'session': 'the same session',
  '1h': '1 hour',
  '1d': '1 day',
  '7d': '7 days',
  '30d': '30 days'
}

/** "within 1 day", "within the same session": how a sentence says the window. */
export const windowPhrase = (window: FunnelWindow) => `within ${WINDOW_LABELS[window]}`

const MINUTE = 60
const HOUR = 3_600
const DAY_S = 86_400
const DAY = 86_400_000

/** How long each window lets a player take. A session is bounded by the 30 minutes away that end one. */
const WINDOW_SECONDS: Record<FunnelWindow, number> = {
  'session': 45 * MINUTE,
  '1h': HOUR,
  '1d': DAY_S,
  '7d': 7 * DAY_S,
  '30d': 30 * DAY_S
}

const RANGE_DAYS: Record<FunnelRange, number> = { '7d': 7, '30d': 30, '90d': 90 }

export const BREAKDOWN_LABELS: Record<Exclude<FunnelBreakdown, 'none'>, string> = {
  install_platform: 'Platform',
  install_country: 'Country',
  first_app_version: 'First app version'
}

/** Value, share of players, how much better or worse it converts. The sticky install dimensions. */
const BREAKDOWN_VALUES: Record<Exclude<FunnelBreakdown, 'none'>, readonly (readonly [string, number, number])[]> = {
  install_platform: [['iOS', 0.46, 1.08], ['Android', 0.43, 0.92], ['Steam', 0.11, 1.2]],
  // Five and the rest: the chart has six colours, and a seventh would repeat the first.
  install_country: [['US', 0.24, 1.1], ['BR', 0.14, 0.88], ['DE', 0.09, 1.05], ['JP', 0.07, 1.15], ['TR', 0.06, 0.9], ['Other', 0.4, 0.97]],
  first_app_version: [['2.5.0', 0.38, 1.06], ['2.4.1', 0.41, 1], ['2.4.0', 0.21, 0.9]]
}

/**
 * How an event behaves as a step: the share of players who, having done the step before,
 * go on to do it at all, and how long they typically take to.
 */
const BEHAVIOUR: Record<string, { reach: number, after: number }> = {
  session_start: { reach: 0.92, after: 20 * HOUR },
  session_end: { reach: 0.96, after: 15 * MINUTE },
  level_complete: { reach: 0.82, after: 8 * MINUTE },
  match_end: { reach: 0.58, after: 12 * MINUTE },
  tutorial_step: { reach: 0.88, after: 70 },
  purchase: { reach: 0.07, after: 2.5 * DAY_S },
  currency_spend: { reach: 0.46, after: 3 * HOUR },
  ad_impression: { reach: 0.55, after: 25 * MINUTE },
  item_equip: { reach: 0.5, after: 40 * MINUTE },
  daily_reward_claim: { reach: 0.62, after: 18 * HOUR },
  guild_join: { reach: 0.14, after: 3 * DAY_S }
}

const FALLBACK = { reach: 0.4, after: 2 * HOUR }

/** Saved funnels a new project starts with. */
export const SAMPLE_FUNNELS: readonly FunnelDefinition[] = [
  {
    id: 'onboarding',
    name: 'Onboarding',
    window: '1d',
    steps: [
      { event: 'session_start', where: [{ property: 'is_first_session', value: 'true' }] },
      { event: 'tutorial_step', where: [{ property: 'step', value: '1' }] },
      { event: 'tutorial_step', where: [{ property: 'step', value: '6' }] },
      { event: 'level_complete', where: [] }
    ]
  },
  {
    id: 'first-purchase',
    name: 'First purchase',
    window: '7d',
    steps: [
      { event: 'session_start', where: [] },
      { event: 'level_complete', where: [{ property: 'result', value: 'win' }] },
      { event: 'currency_spend', where: [] },
      { event: 'purchase', where: [] }
    ]
  },
  {
    id: 'social',
    name: 'Into a guild',
    window: '30d',
    steps: [
      { event: 'match_end', where: [] },
      { event: 'daily_reward_claim', where: [] },
      { event: 'guild_join', where: [] }
    ]
  }
]

// --- Answers ------------------------------------------------------------------------------

export interface FunnelSeries {
  /** "All players", or the breakdown value. */
  name: string
  /** Players at each step. Null when too few to show. */
  players: (number | null)[]
  /** Median seconds from the step before to this one; null for step 1 and withheld steps. */
  medians: (number | null)[]
}

export interface FunnelBucket {
  label: string
  players: number
}

export interface FunnelSample {
  total: FunnelSeries
  previous: FunnelSeries
  /** One series per breakdown value, when a breakdown is asked for. */
  breakdown: FunnelSeries[]
  /** Overall conversion per day (per week over 90 days), by series: total first. */
  trend: {
    categories: string[]
    titles: string[]
    /** Per point: whether its players have had the whole window to convert yet. */
    settled: boolean[]
    series: { name: string, rates: (number | null)[] }[]
  }
  /** How long converting players took from step 1 to the last, in buckets up to the window. */
  timeToConvert: FunnelBucket[]
  /** Median seconds from step 1 to the last, for the players who got there. */
  medianToConvert: number | null
}

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

/** The standard normal CDF (Abramowitz and Stegun 7.1.26). */
function normal(x: number): number {
  const t = 1 / (1 + 0.3275911 * Math.abs(x) / Math.SQRT2)
  const erf = 1 - ((((1.061405429 * t - 1.453152027) * t + 1.421413741) * t - 0.284496736) * t + 0.254829592) * t * Math.exp(-(x * x) / 2)
  return x >= 0 ? (1 + erf) / 2 : (1 - erf) / 2
}

/** The share of a log-normal time with median `median` that is under `seconds`. */
const within = (seconds: number, median: number, spread = 1.2) => normal(Math.log(seconds / median) / spread)

/** How much a step's conditions narrow it: the first listed value is the commonest. */
function narrowing(step: FunnelStep, catalogue: readonly EventDefinition[]): number {
  const event = catalogue.find(entry => entry.name === step.event)
  let factor = 1
  for (const condition of step.where) {
    const property = event?.properties.find(entry => entry.key === condition.property)
    if (!property) continue
    const index = Math.max(0, property.values.indexOf(condition.value))
    factor *= property.fill * (0.35 + 0.65 * (property.values.length - index) / property.values.length)
  }
  return factor
}

/** Players active at some point in the range: more days, more of them, but not in proportion. */
const activeIn = (days: number) => 31_000 * (1 + 0.9 * Math.log(days))

interface Shape {
  entered: number
  rates: number[]
  medians: number[]
  /**
   * Each step's seed: the window and the steps up to it. A step's figures are drawn from
   * its own, so adding a step, or changing one, never moves the figures of those before it.
   */
  keys: string[]
}

/** The funnel's true shape before any breakdown: who enters, and each step's odds and pace. */
function shapeOf(definition: FunnelDefinition, range: FunnelRange, catalogue: readonly EventDefinition[]): Shape {
  const days = RANGE_DAYS[range]
  const window = WINDOW_SECONDS[definition.window]
  const keys = definition.steps.map((_, index) => `${range}:${JSON.stringify([definition.window, definition.steps.slice(0, index + 1)])}`)
  const [first, ...rest] = definition.steps
  const firstEvent = catalogue.find(entry => entry.name === first?.event)
  const reach = Math.min(0.97, 1 - Math.exp(-(firstEvent?.rate ?? 1) * days * 0.6))
  const entered = activeIn(days) * reach * (first ? narrowing(first, catalogue) : 1)

  const rates: number[] = [1]
  const medians: number[] = [0]
  let left = window
  for (const [index, step] of rest.entries()) {
    const random = seeded(`shape:${keys[index + 1]}`)
    const { reach: odds, after } = BEHAVIOUR[step.event] ?? FALLBACK
    // A new session cannot start inside the one the funnel began in.
    const impossible = definition.window === 'session' && step.event === 'session_start'
    const inTime = 0.12 + 0.88 * within(Math.max(left, 1), after)
    const rate = impossible ? 0.004 : Math.min(0.99, odds * inTime * narrowing(step, catalogue) * (0.92 + random() * 0.1))
    const median = Math.min(after, left * 0.35) * (0.8 + random() * 0.4)
    rates.push(rate)
    medians.push(median)
    left = Math.max(window * 0.05, left - median)
  }
  return { entered, rates, medians, keys }
}

/** A shape counted out for a population, with the thin counts withheld. */
function countOut(name: string, shape: Shape, scale: number, quality: number): FunnelSeries {
  const players: (number | null)[] = []
  const medians: (number | null)[] = []
  let count = shape.entered * scale
  shape.rates.forEach((rate, index) => {
    const random = seeded(`count:${name}:${shape.keys[index]}`)
    if (index > 0) count *= Math.min(0.995, rate ** (1 / quality) * (0.97 + random() * 0.06))
    const whole = Math.round(count)
    const shown = whole > 0 && whole < FUNNEL_THRESHOLD ? null : whole
    players.push(shown)
    medians.push(index === 0 || shown === null ? null : shape.medians[index]! / quality)
  })
  return { name, players, medians }
}

const BUCKETS: readonly (readonly [number, string])[] = [
  [MINUTE, '<1m'],
  [5 * MINUTE, '1–5m'],
  [15 * MINUTE, '5–15m'],
  [30 * MINUTE, '15–30m'],
  [HOUR, '30m–1h'],
  [6 * HOUR, '1–6h'],
  [DAY_S, '6–24h'],
  [3 * DAY_S, '1–3d'],
  [7 * DAY_S, '3–7d'],
  [30 * DAY_S, '7–30d']
]

const dayLabel = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

export function sampleFunnel(
  definition: FunnelDefinition,
  range: FunnelRange,
  breakdown: FunnelBreakdown,
  catalogue: readonly EventDefinition[]
): FunnelSample {
  const days = RANGE_DAYS[range]
  const key = JSON.stringify([definition.steps, definition.window])
  const shape = shapeOf(definition, range, catalogue)

  const total = countOut('All players', shape, 1, 1)
  const before = seeded(`previous:${shape.keys[0]}`)
  const previous = countOut('The range before', shape, 0.9 + before() * 0.12, 0.94 + before() * 0.1)

  const breakdownSeries = breakdown === 'none'
    ? []
    : BREAKDOWN_VALUES[breakdown].map(([value, share, quality]) => countOut(value, shape, share, quality))

  // --- Over time: the conversion of the players who entered on each day or week.
  const today = Math.floor(Date.now() / DAY) * DAY
  const step = range === '90d' ? 7 : 1
  const points = Math.ceil(days / step)
  const overall = (series: FunnelSeries) => {
    const first = series.players[0]
    const last = series.players[series.players.length - 1]
    return first && last !== null && last !== undefined ? last / first : null
  }
  const windowDays = WINDOW_SECONDS[definition.window] / DAY_S
  const trendRandom = seeded(`funnel:${key}:${range}:trend`)
  const starts = Array.from({ length: points }, (_, index) => today - (points - index) * step * DAY)
  const settled = starts.map(at => (today - (at + step * DAY)) / DAY >= windowDays)
  const wobble = starts.map(at => ([0, 6].includes(new Date(at).getUTCDay()) && step === 1 ? 1.05 : 1) * (0.92 + trendRandom() * 0.16))
  const lineOf = (series: FunnelSeries) => {
    const rate = overall(series)
    return starts.map((at, index) => {
      if (rate === null) return null
      // Players who entered too recently are still inside their window: fewer of them yet.
      const elapsed = Math.max(0, (today - at) / DAY - step / 2)
      const ripening = settled[index] ? 1 : Math.min(1, 0.35 + 0.65 * elapsed / Math.max(windowDays, 0.01))
      return Math.min(1, rate * wobble[index]! * ripening * (0.97 + trendRandom() * 0.06))
    })
  }
  const trend = {
    categories: starts.map(at => dayLabel.format(at)),
    titles: starts.map(at => (step === 1 ? dayLabel.format(at) : `Week of ${dayLabel.format(at)}`)),
    settled,
    series: [total, ...breakdownSeries].map(series => ({ name: series.name, rates: lineOf(series) }))
  }

  // --- Time to convert: the sum of the steps' paces, spread log-normally, cut at the window.
  const converted = total.players[total.players.length - 1] ?? 0
  const medianToConvert = converted ? shape.medians.reduce((sum, median) => sum + median, 0) : null
  const limit = WINDOW_SECONDS[definition.window]
  const buckets = BUCKETS.filter((_, index) => (BUCKETS[index - 1]?.[0] ?? 0) < limit)
  const mass = buckets.map(([upper], index) => {
    const lower = BUCKETS[index - 1]?.[0] ?? 0
    const top = Math.min(upper, limit)
    if (!medianToConvert) return 0
    return within(top, medianToConvert, 0.9) - (lower ? within(lower, medianToConvert, 0.9) : 0)
  })
  const whole = mass.reduce((sum, value) => sum + value, 0) || 1
  const timeToConvert = buckets.map(([, label], index) => ({ label, players: Math.round(converted * mass[index]! / whole) }))

  return { total, previous, breakdown: breakdownSeries, trend, timeToConvert, medianToConvert }
}

// --- Words --------------------------------------------------------------------------------

const shares = new Intl.NumberFormat('en', { minimumFractionDigits: 1, maximumFractionDigits: 1 })

/** "31.0%": always one decimal, so a column of them lines up and none reads as rounder. */
export const formatShare = (rate: number) => `${shares.format(rate * 100)}%`

/** "45s", "4m", "1.5h", "2.3d": short enough for the gap between two bars. */
export function formatSeconds(seconds: number): string {
  if (seconds < MINUTE) return `${Math.max(1, Math.round(seconds))}s`
  if (seconds < HOUR) return `${Math.round(seconds / MINUTE)}m`
  if (seconds < DAY_S) return `${writeFigure(seconds / HOUR, { decimals: 1 })}h`
  return `${writeFigure(seconds / DAY_S, { decimals: 1 })}d`
}

/** "tutorial_step · step = 6". */
export function describeStep(step: FunnelStep): string {
  return [step.event, ...step.where.map(condition => `${condition.property} = ${condition.value}`)].join(' · ')
}
