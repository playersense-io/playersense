// Sample data for the Cohorts page, generated in the browser. Shaped after what it will
// query once the API client is wired in:
//
//   POST …/analytics/production/cohorts/query  metric retention | playtime | revenue |
//     ltv | payer_conversion, basis=install, retention_basis for retention (never
//     inferred — project.md §29.10), cohort { from, to, grain day | week | month }, the
//     offsets on screen, at most one breakdown dimension (§57.1: never two at once), and
//     filters on the others. Rows are (cohort, offset[, dimension value]) with the
//     cohort's size, players, playtime_ms, revenue_micros and its running total, and
//     payers; a cell under the suppression threshold is dropped, not zeroed.
//
// One set of players underneath every metric — the same cohorts retain, play, spend and
// convert — so switching metric reads the same game another way rather than a new one.
// Seeded, so a reload draws the same cohorts.

export type CohortMetric = 'retention' | 'playtime' | 'revenue' | 'payers'
export type CohortRange = '30d' | '90d' | '180d'
export type CohortBasis = 'classic' | 'rolling'
export type CohortDimension = 'install_platform' | 'install_country' | 'first_app_version'
export type CohortSplit = 'none' | CohortDimension
export type CohortFilters = Record<CohortDimension, string[]>

/** Cells counting fewer players than this are withheld by the API: its default k (§32.2). */
export const COHORT_THRESHOLD = 5

export const RANGE_DAYS: Record<CohortRange, number> = { '30d': 30, '90d': 90, '180d': 180 }

/**
 * The sticky install dimensions the API breaks down and filters by: each value's share of
 * installs, and how well its players retain and spend against the average.
 */
export const COHORT_DIMENSIONS: Record<CohortDimension, { label: string, values: readonly (readonly [string, number, number])[] }> = {
  install_platform: { label: 'Platform', values: [['iOS', 0.46, 1.1], ['Android', 0.43, 0.9], ['Steam', 0.11, 1.18]] },
  install_country: { label: 'Country', values: [['US', 0.24, 1.14], ['BR', 0.14, 0.82], ['DE', 0.09, 1.05], ['JP', 0.07, 1.22], ['TR', 0.06, 0.86], ['Other', 0.4, 0.96]] },
  first_app_version: { label: 'First app version', values: [['2.5.0', 0.38, 1.06], ['2.4.1', 0.41, 1], ['2.4.0', 0.21, 0.9]] }
}

export const DIMENSION_KEYS = Object.keys(COHORT_DIMENSIONS) as CohortDimension[]

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

/** The share of a cohort active on day `day`: a power-law decay, rolling above classic. */
function active(day: number, basis: CohortBasis, quality: number): number {
  if (day === 0) return 1
  const [scale, decay] = basis === 'classic' ? [0.42, 0.47] : [0.55, 0.4]
  return Math.min(1, scale * quality * day ** -decay)
}

/** Minutes an active player plays on day `day`: the ones who stay, play longer. */
const minutesPerActive = (day: number, quality: number) => 26 * (1 + 0.12 * Math.log(1 + day)) * quality ** 0.5

/** Dollars an active player spends on day `day`: starter packs on day 0, then a slow climb. */
const dollarsPerActive = (day: number, quality: number) => (day === 0 ? 0.3 : 0.11 * (1 + 0.25 * Math.log(1 + day) / Math.log(31))) * quality ** 2

export interface CohortSampleOptions {
  metric: CohortMetric
  basis: CohortBasis
  range: CohortRange
  grain: CohortGrain
  split: CohortSplit
  filters: CohortFilters
}

export interface CohortSample {
  /** Newest cohort first; or, split, one row per segment over the whole range. */
  rows: CohortTableRow[]
  /** The same cohorts over the range before, unsplit: what the headline figures move from. */
  previous: CohortTableRow[]
}

/** A population the day-by-day model runs over: how many installs, and how good they are. */
interface Population { share: number, quality: number, name: string }

/** The filtered mix of one dimension: the share of installs it keeps, and its quality. */
function mixOf(dimension: CohortDimension, chosen: readonly string[]): Population {
  const values = COHORT_DIMENSIONS[dimension].values.filter(([value]) => chosen.length === 0 || chosen.includes(value))
  const share = values.reduce((sum, [, weight]) => sum + weight, 0)
  const quality = share ? values.reduce((sum, [, weight, better]) => sum + weight * better, 0) / share : 1
  return { share: chosen.length === 0 ? 1 : share, quality, name: '' }
}

/** Sums over the daily cohorts that make up one row, per offset. */
interface Tally {
  size: number
  mature: number[]
  players: number[]
  value: number[]
  total: number[]
}

const emptyTally = (): Tally => ({
  size: 0,
  mature: COHORT_OFFSETS.map(() => 0),
  players: COHORT_OFFSETS.map(() => 0),
  value: COHORT_OFFSETS.map(() => 0),
  total: COHORT_OFFSETS.map(() => 0)
})

interface Drawn { start: number, label?: string, tally: Tally }

/**
 * Every daily cohort of the range, day by day, into the rows it belongs to: its week or
 * month, or its segment. A week or month is known at an offset only once all its days are;
 * a segment, over whichever of its cohorts are old enough.
 */
function draw(options: CohortSampleOptions, populations: readonly Population[], today: number, salt: string): Map<string, Drawn> {
  const days = RANGE_DAYS[options.range]
  const random = seeded(`cohorts:${salt}:${options.range}:${JSON.stringify(options.filters)}`)
  const rows = new Map<string, Drawn>()
  const last = COHORT_OFFSETS[COHORT_OFFSETS.length - 1]!

  for (let index = 0; index < days; index += 1) {
    const at = today - (days - index) * DAY
    const age = Math.floor((today - at) / DAY) - 1
    const weekend = [0, 6].includes(new Date(at).getUTCDay()) ? 1.25 : 1
    const installs = 2_600 * weekend * (0.85 + random() * 0.3)
    const lift = 0.94 + random() * 0.12 + index / days * 0.04

    for (const population of populations) {
      const size = Math.round(installs * population.share)
      if (!size) continue
      const quality = population.quality * lift

      const key = population.name || bucketKey(at, options.grain)
      const row = rows.get(key) ?? { start: population.name ? at : bucketStart(at, options.grain), label: population.name || undefined, tally: emptyTally() }
      if (population.name) row.start = Math.min(row.start, at)
      rows.set(key, row)
      row.tally.size += size

      // Day by day to the last offset old enough, so running totals add every day.
      let playedSoFar = 0
      let spentSoFar = 0
      let activeDays = 0
      let offsetAt = 0
      const until = Math.min(age, last)
      for (let day = 0; day <= until; day += 1) {
        const classic = active(day, 'classic', quality)
        activeDays += classic
        const playedToday = classic * minutesPerActive(day, quality)
        const spentToday = classic * dollarsPerActive(day, quality)
        playedSoFar += playedToday
        spentSoFar += spentToday
        if (day !== COHORT_OFFSETS[offsetAt]) continue

        const noise = 0.94 + random() * 0.12
        const retained = active(day, options.basis, quality) * noise
        const payers = Math.min(0.6, 0.004 * quality + 1 - Math.exp(-0.0055 * quality ** 2 * activeDays)) * noise
        let players = size * retained
        let value = retained
        let total = 0
        if (options.metric === 'playtime') {
          value = playedToday * noise
          total = playedSoFar * noise
        } else if (options.metric === 'revenue') {
          value = spentToday * noise
          total = spentSoFar * noise
        } else if (options.metric === 'payers') {
          players = size * payers
          value = payers
        }
        row.tally.mature[offsetAt]! += size
        row.tally.players[offsetAt]! += players
        row.tally.value[offsetAt]! += value * size
        row.tally.total[offsetAt]! += total * size
        offsetAt += 1
      }
      // A week or month is known at an offset only once every day in it is: one day too
      // young marks the offset unknown for the whole row, and NaN stays NaN as days add.
      if (!population.name) {
        for (let position = offsetAt; position < COHORT_OFFSETS.length; position += 1) row.tally.mature[position] = Number.NaN
      }
    }
  }
  return rows
}

function bucketStart(at: number, grain: CohortGrain): number {
  if (grain === 'day') return at
  const date = new Date(at)
  if (grain === 'month') return Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), 1)
  // Weeks start on Monday, as the API's week grain does.
  return at - ((date.getUTCDay() + 6) % 7) * DAY
}

const bucketKey = (at: number, grain: CohortGrain) => String(bucketStart(at, grain))

/** A tally written out as table cells: a figure where every day under it is old enough. */
function toRow(key: string, entry: Drawn): CohortTableRow {
  const { tally } = entry
  const cells: Record<number, CohortTableCell> = {}
  COHORT_OFFSETS.forEach((offset, position) => {
    const mature = tally.mature[position]!
    if (!mature || Number.isNaN(mature)) return
    const players = Math.round(tally.players[position]!)
    if (mature < COHORT_THRESHOLD || (players > 0 && players < COHORT_THRESHOLD)) {
      cells[offset] = { state: 'withheld', players: null, value: null, cumulative: null }
      return
    }
    cells[offset] = {
      state: 'ok',
      players,
      value: tally.value[position]! / mature,
      cumulative: tally.total[position]! / mature
    }
  })
  return { key, label: entry.label, start: entry.start, size: tally.size < COHORT_THRESHOLD ? null : tally.size, cells }
}

export function sampleCohorts(options: CohortSampleOptions): CohortSample {
  const today = Math.floor(Date.now() / DAY) * DAY

  // The filtered install mix, one dimension at a time, and the split's values within it.
  const filtered = DIMENSION_KEYS.filter(dimension => dimension !== options.split).map(dimension => mixOf(dimension, options.filters[dimension]))
  const base: Population = {
    name: '',
    share: filtered.reduce((product, mix) => product * mix.share, 1),
    quality: filtered.reduce((product, mix) => product * mix.quality, 1)
  }

  let populations: Population[] = [base]
  if (options.split !== 'none') {
    const chosen = options.filters[options.split]
    populations = COHORT_DIMENSIONS[options.split].values
      .filter(([value]) => chosen.length === 0 || chosen.includes(value))
      .map(([value, share, quality]) => ({ name: value, share: base.share * share, quality: base.quality * quality }))
  }

  const current = draw(options, populations, today, 'now')
  const rows = [...current.entries()].map(([key, entry]) => toRow(key, entry))
  if (options.split === 'none') rows.sort((a, b) => b.start - a.start)

  const unsplit = options.split === 'none'
    ? populations
    : [{ ...base, share: populations.reduce((sum, population) => sum + population.share, 0) }]
  const before = draw(options, unsplit, today - RANGE_DAYS[options.range] * DAY, 'before')
  const previous = [...before.entries()].map(([key, entry]) => toRow(key, entry))

  return { rows, previous }
}

// --- How each metric reads ----------------------------------------------------------------

const minutes = (value: number) => `${writeFigure(value, { decimals: value < 10 ? 1 : 0 })}m`
const dollars = (value: number) => `$${value.toFixed(value < 10 ? 2 : 1)}`

/**
 * How the table, the curves and the figures read a metric: per day, or as the running
 * total up to the day for the ones that have one (minutes played, dollars spent). The
 * detail under each cell is the players it is about.
 */
export function cohortModeOf(metric: CohortMetric, accumulated: boolean): CohortMode {
  if (metric === 'retention') return rateMode('Retention')
  if (metric === 'payers') return rateMode('Payers')
  const format = metric === 'playtime' ? minutes : dollars
  const label = metric === 'playtime'
    ? (accumulated ? 'Playtime to date' : 'Playtime that day')
    : (accumulated ? 'Revenue to date' : 'Revenue that day')
  return accumulated ? cumulativeMode({ label, format }) : valueMode({ label, format })
}

export const METRIC_LABELS: Record<CohortMetric, string> = {
  retention: 'Retention',
  playtime: 'Playtime per install',
  revenue: 'Revenue per install',
  payers: 'Payer conversion'
}
