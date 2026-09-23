// Sample data for the Retention page, generated in the browser. Shaped after what it will
// query once the API client is wired in:
//
//   POST …/analytics/production/cohorts/query  metric=retention, basis=install,
//     retention_basis classic | rolling (never inferred — project.md §29.10), cohort
//     { from, to, grain day | week }, offsets, and at most one sticky breakdown
//     (install_platform, install_source …, §29.13). Rows are (cohort, offset, players,
//     rate) with the cohort's size; cells younger than the offset are absent, and so are
//     cells under the suppression threshold — `pivotCohortRows` tells the two apart.
//
// Seeded, so a reload draws the same cohorts.

export type RetentionBasis = 'classic' | 'rolling'
export type RetentionRange = '30d' | '90d'
export type RetentionGrain = 'day' | 'week'
export type RetentionBreakdown = 'none' | 'platform' | 'source'

/** How many days the retention curve runs. */
export const CURVE_DAYS = 30

/** Cells counting fewer players than this are withheld by the API: its default k (§32.2). */
export const SUPPRESSION_THRESHOLD = 5

const DAY = 86_400_000
const RANGE_DAYS: Record<RetentionRange, number> = { '30d': 30, '90d': 90 }

export const BREAKDOWN_VALUES: Record<Exclude<RetentionBreakdown, 'none'>, readonly (readonly [string, number, number])[]> = {
  // value, share of installs, how much better or worse it retains
  platform: [['iOS', 0.46, 1.12], ['Android', 0.43, 0.9], ['Steam', 0.11, 1.28]],
  source: [['Organic', 0.52, 1.18], ['Meta Ads', 0.17, 0.84], ['Google Ads', 0.14, 0.9], ['TikTok Ads', 0.11, 0.72], ['Referral', 0.06, 1.35]]
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

/** The share of a cohort active at day `offset`: a power-law decay, rolling above classic. */
function curve(offset: number, basis: RetentionBasis, quality = 1): number {
  if (offset === 0) return 1
  const [scale, decay] = basis === 'classic' ? [0.42, 0.47] : [0.55, 0.4]
  return Math.min(1, scale * quality * offset ** -decay)
}

export interface RetentionSample {
  /** Newest first, a cell for every offset the API answers by default. */
  cohorts: CohortTableRow[]
  /** Day 0 … CURVE_DAYS, weighted over the cohorts old enough for each day. */
  curve: (number | null)[]
  previousCurve: (number | null)[]
  /** Per breakdown value, when one is asked for. */
  breakdown: { value: string, curve: (number | null)[] }[]
  newPlayers: number
  previousNewPlayers: number
}

export function sampleRetention(
  basis: RetentionBasis,
  range: RetentionRange,
  grain: RetentionGrain,
  breakdown: RetentionBreakdown
): RetentionSample {
  const days = RANGE_DAYS[range]
  const today = Math.floor(Date.now() / DAY) * DAY
  const random = seeded(`retention:${basis}:${range}:${grain}`)

  // One daily cohort per day of the range, newest last, each with its own quality.
  const daily = Array.from({ length: days }, (_, index) => {
    const at = today - (days - index) * DAY
    const weekend = [0, 6].includes(new Date(at).getUTCDay()) ? 1.25 : 1
    // Now and then a day brings almost nobody (a store outage, a holiday), so its later
    // cells fall under the suppression threshold; once in a long while so few that the
    // cohort itself cannot be shown.
    const roll = random()
    const size = roll < 0.02
      ? Math.round(2 + random() * 2)
      : roll < 0.09 ? Math.round(25 + random() * 45) : Math.round(2_600 * weekend * (0.85 + random() * 0.3))
    return { at, size, quality: 0.92 + random() * 0.18 + index / days * 0.05 }
  })

  const ageOf = (at: number) => Math.floor((today - at) / DAY) - 1

  const cellsFor = (size: number, age: number, quality: number) => {
    const cells: Record<number, CohortTableCell> = {}
    for (const offset of COHORT_OFFSETS) {
      if (offset > age) continue
      const rate = offset === 0 ? 1 : curve(offset, basis, quality) * (0.94 + random() * 0.12)
      const players = Math.round(size * rate)
      cells[offset] = size < SUPPRESSION_THRESHOLD || (players > 0 && players < SUPPRESSION_THRESHOLD)
        ? { state: 'withheld', players: null, value: null }
        : { state: 'ok', players, value: players / size }
    }
    return cells
  }

  const cohortOf = (at: number, size: number, age: number, quality: number): CohortTableRow => ({
    key: String(at),
    start: at,
    size: size < SUPPRESSION_THRESHOLD ? null : size,
    cells: cellsFor(size, age, quality)
  })

  let cohorts: CohortTableRow[]
  if (grain === 'day') {
    cohorts = daily.map(cohort => cohortOf(cohort.at, cohort.size, ageOf(cohort.at), cohort.quality)).reverse()
  } else {
    cohorts = []
    for (let start = 0; start < daily.length; start += 7) {
      const week = daily.slice(start, start + 7)
      const size = week.reduce((sum, cohort) => sum + cohort.size, 0)
      const quality = week.reduce((sum, cohort) => sum + cohort.quality, 0) / week.length
      // A week is mature at an offset only once its youngest day is.
      cohorts.push(cohortOf(week[0]!.at, size, ageOf(week[week.length - 1]!.at), quality))
    }
    cohorts.reverse()
  }

  const weighted = (quality: (offset: number) => number) => Array.from({ length: CURVE_DAYS + 1 }, (_, offset) => {
    const mature = daily.filter(cohort => ageOf(cohort.at) >= offset)
    if (!mature.length) return null
    const size = mature.reduce((sum, cohort) => sum + cohort.size, 0)
    return mature.reduce((sum, cohort) => sum + cohort.size * curve(offset, basis, cohort.quality * quality(offset)), 0) / size
  })

  // The range before: early days a little weaker, later ones closer, so each day moved by
  // its own amount rather than all by one.
  const before = Array.from({ length: CURVE_DAYS + 1 }, (_, offset) => 0.9 + Math.min(offset, 20) * 0.004 + random() * 0.03)

  const newPlayers = daily.reduce((sum, cohort) => sum + cohort.size, 0)

  return {
    cohorts,
    curve: weighted(() => 1),
    previousCurve: weighted(offset => before[offset]!),
    breakdown: breakdown === 'none'
      ? []
      : BREAKDOWN_VALUES[breakdown].map(([value, , quality]) => ({ value, curve: weighted(() => quality) })),
    newPlayers,
    previousNewPlayers: Math.round(newPlayers / 1.08)
  }
}
