/**
 * Which way a figure moved since the last period, and whether that is good news.
 *
 * Pure and shared, so the statistic tile and the metric card agree on what a change of
 * 4.1% is and how it rounds, and so the rule is tested without a DOM. Direction is
 * judged on the change as it will be written rather than as it was computed: a rise of
 * 0.04% prints as "0%", and green beside a zero says the tile disagrees with itself.
 */
import { formatChange, relativeChange } from './format'

export type TrendDirection = 'up' | 'down' | 'flat'
export type TrendTone = 'good' | 'bad' | 'neutral'

export interface Trend {
  direction: TrendDirection
  tone: TrendTone
  /** "+4.1%", or a dash when there was nothing to compare against. */
  text: string
  /**
   * Whether the last period gave anything to measure against. False where it was zero and
   * a percentage has no answer, which is a different statement from a change of nothing
   * and is why the two cannot share a rendering: "0%" says the figure held still, and a
   * flat arrow beside a dash says only that the question could not be asked.
   */
  comparable: boolean
}

/* Written out in full so @nuxt/icon's scan finds them; a name assembled at runtime
   draws nothing. */
export const TREND_ICONS: Record<TrendDirection, string> = {
  up: 'material-symbols:trending-up',
  down: 'material-symbols:trending-down',
  flat: 'material-symbols:trending-flat'
}

export const TREND_TONES: Record<TrendTone, string> = {
  good: 'text-success',
  bad: 'text-error',
  neutral: 'text-ink/60'
}

/**
 * `positive` is which direction is good news: down, for anything that counts failures.
 * A comparison with no answer (a previous period of zero) is flat and neutral, with a
 * dash for its text, rather than "+∞%".
 */
export function trendOf(value: number, previous: number, positive: 'up' | 'down' = 'up'): Trend {
  const change = relativeChange(value, previous)
  if (change === null) return { direction: 'flat', tone: 'neutral', text: '–', comparable: false }

  const written = Math.round(change * 1000) / 1000
  const direction: TrendDirection = written === 0 ? 'flat' : written > 0 ? 'up' : 'down'
  const tone: TrendTone
    = direction === 'flat' ? 'neutral' : direction === positive ? 'good' : 'bad'
  return { direction, tone, text: formatChange(change), comparable: true }
}
