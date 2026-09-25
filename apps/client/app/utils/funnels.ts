// Funnels as the Funnels page draws them: a definition (ordered steps with conditions, or a
// progression over a numeric property), the window its players have to convert in, and the
// shapes the API's answers are read into (composables/funnels.ts).

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

interface FunnelBase {
  id: string
  name: string
  window: FunnelWindow
}

/** Steps chosen one by one: session_start, then tutorial_step where step = 6, then… */
export interface StepFunnel extends FunnelBase {
  kind: 'steps'
  steps: FunnelStep[]
}

/**
 * One step per value of a number property of one event, in order: level_complete at level
 * 1, then at level 2, … up to `to`. The level funnel a game is read by, too long to build
 * by hand and too long to draw as bars, so it is its own kind.
 */
export interface ProgressionFunnel extends FunnelBase {
  kind: 'progression'
  event: string
  property: string
  from: number
  to: number
  /** Narrows every value's occurrences alike: result = win. */
  where: FunnelCondition[]
}

export type FunnelDefinition = StepFunnel | ProgressionFunnel

export const MIN_STEPS = 2

export const MAX_STEPS = 10

/** The most values a progression steps through. */
export const MAX_VALUES = 200

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

export const BREAKDOWN_LABELS: Record<Exclude<FunnelBreakdown, 'none'>, string> = {
  install_platform: 'Platform',
  install_country: 'Country',
  first_app_version: 'First app version'
}

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

export interface FunnelResult {
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

export interface ProgressionRow {
  value: number
  /** Players who reached this value in order from the first. Null when too few to show. */
  players: number | null
  /** How often each of them did the event at this value: attempts, for a level. */
  tries: number | null
  /** Median seconds from this value to the next, for the players who went on. */
  toNext: number | null
}

export interface ProgressionResult {
  rows: ProgressionRow[]
  /** The first value's and the last value's players in the range before. */
  previous: { first: number | null, last: number | null }
  /** Median seconds from the first value to the last, for the players who got there. */
  medianToLast: number | null
}

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
