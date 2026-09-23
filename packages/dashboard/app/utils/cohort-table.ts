/**
 * What a cohort table is, whatever it measures: one row per cohort (the players who
 * arrived on a day or in a week), one column per day offset after they arrived, and in
 * each cell how that cohort was doing on that day. Retention, playtime, revenue, LTV and
 * payer conversion all come back from `POST …/cohorts/query` in this one shape, so the
 * table is written once and told what its cells mean by a `CohortMode`.
 *
 * Pure, so the rules — which days a reader chose, how a stored choice survives the
 * offsets changing, what an absent cell means — are tested rather than trusted.
 */

import { formatChange, writeFigure } from './format'

const DAY = 86_400_000

// --- Offsets ------------------------------------------------------------------------------

/**
 * Every offset the API answers without being asked for specific ones: each day of the
 * first month, then the four milestones after it. Mirrors `DefaultOffsets()` in
 * apps/backend/internal/query/plan.go; a column the reader can pick here is one the
 * server has.
 */
export const COHORT_OFFSETS: readonly number[] = [
  ...Array.from({ length: 31 }, (_, day) => day),
  60,
  90,
  180,
  365
]

export const offsetLabel = (offset: number) => `D${offset}`

export type CohortGrain = 'day' | 'week' | 'month'

/** Heat shades a cell by its value; average, by how far it sits from its column's. */
export type CohortColor = 'heat' | 'average'

// --- Which days are shown -----------------------------------------------------------------

export interface CohortPreset {
  key: string
  label: string
  offsets: readonly number[]
}

export const CUSTOM_PRESET = 'custom'

/** The first is the default. A page with other habits passes its own. */
export const COHORT_PRESETS: readonly CohortPreset[] = [
  { key: 'key-days', label: 'Key days', offsets: [1, 3, 7, 14, 30] },
  { key: 'first-week', label: 'First week', offsets: [0, 1, 2, 3, 4, 5, 6, 7] },
  { key: 'first-month', label: 'First month', offsets: COHORT_OFFSETS.filter(offset => offset <= 30) },
  { key: 'long-term', label: 'Long term', offsets: [1, 7, 30, 60, 90, 180, 365] }
]

/**
 * A reader's choice of columns: a preset, or `custom` with the days they ticked. The
 * custom days are kept while a preset is on, so going back to Custom finds them again.
 */
export interface CohortDays {
  preset: string
  custom: number[]
}

export function defaultCohortDays(presets: readonly CohortPreset[] = COHORT_PRESETS): CohortDays {
  return { preset: presets[0]?.key ?? CUSTOM_PRESET, custom: [] }
}

const sorted = (offsets: Iterable<number>) => [...new Set(offsets)].sort((a, b) => a - b)

function sameOffsets(a: readonly number[], b: readonly number[]) {
  return a.length === b.length && a.every((offset, index) => offset === b[index])
}

/** The columns a choice shows, in order, limited to what the server offers. */
export function shownOffsets(
  days: CohortDays,
  presets: readonly CohortPreset[] = COHORT_PRESETS,
  available: readonly number[] = COHORT_OFFSETS
): number[] {
  const preset = presets.find(entry => entry.key === days.preset)
  const offsets = preset ? preset.offsets : days.custom
  return sorted(offsets.filter(offset => available.includes(offset)))
}

/**
 * One day ticked or unticked. The result is custom unless it lands exactly on a preset,
 * so ticking Key days back together reads as Key days again. The last column cannot be
 * unticked: a table of cohorts with no days is not a table.
 */
export function toggleOffset(
  days: CohortDays,
  offset: number,
  presets: readonly CohortPreset[] = COHORT_PRESETS,
  available: readonly number[] = COHORT_OFFSETS
): CohortDays {
  const shown = shownOffsets(days, presets, available)
  const next = shown.includes(offset) ? shown.filter(entry => entry !== offset) : sorted([...shown, offset])
  if (next.length === 0) return days
  const match = presets.find(preset => sameOffsets(shownOffsets({ preset: preset.key, custom: [] }, presets, available), next))
  return { preset: match?.key ?? CUSTOM_PRESET, custom: next }
}

/** Custom picked from the list: the days last ticked, or the ones on screen if none were. */
export function choosePreset(
  days: CohortDays,
  key: string,
  presets: readonly CohortPreset[] = COHORT_PRESETS,
  available: readonly number[] = COHORT_OFFSETS
): CohortDays {
  if (key !== CUSTOM_PRESET) return { preset: key, custom: days.custom }
  const custom = days.custom.length ? days.custom : shownOffsets(days, presets, available)
  return { preset: CUSTOM_PRESET, custom }
}

/**
 * A stored choice brought back into line with the presets and offsets that exist now: a
 * preset that was renamed away falls back to the default, and days the server no longer
 * offers go quietly. Called on every read, like `reconcileColumnState`.
 */
export function reconcileCohortDays(
  saved: Partial<CohortDays> | null | undefined,
  presets: readonly CohortPreset[] = COHORT_PRESETS,
  available: readonly number[] = COHORT_OFFSETS
): CohortDays {
  const fallback = defaultCohortDays(presets)
  const custom = sorted((Array.isArray(saved?.custom) ? saved.custom : []).filter(
    (offset): offset is number => typeof offset === 'number' && available.includes(offset)
  ))
  const known = saved?.preset === CUSTOM_PRESET ? custom.length > 0 : presets.some(preset => preset.key === saved?.preset)
  return { preset: known ? saved!.preset! : fallback.preset, custom }
}

// --- Rows and cells -----------------------------------------------------------------------

/**
 * One cohort on one day. Never a zero standing in for something else:
 *
 * - `immature`: the cohort is younger than the offset, so there is nothing to know yet.
 * - `withheld`: it counts too few players to show without identifying them (§32.2).
 * - `ok`: a figure, which may genuinely be zero.
 */
export type CohortCellState = 'ok' | 'immature' | 'withheld'

export interface CohortTableCell {
  state: CohortCellState
  /** The players the cell counts: who came back, who played, who paid. */
  players: number | null
  /** The metric's own figure: a share for retention, per player for playtime. */
  value: number | null
  /** The running total up to this day, for the metrics that have one (revenue, LTV). */
  cumulative?: number | null
}

export interface CohortTableRow {
  key: string
  /**
   * What the row is called when it is not a stretch of time: a segment compared with the
   * others ("iOS"), rather than the players who arrived on a day. Dates are the table's
   * to write when this is absent.
   */
  label?: string
  /** The cohort's first day, as UTC midnight. */
  start: number
  /** Null when the cohort itself is too small to describe. */
  size: number | null
  /** By offset, so any subset of columns can be drawn. An offset not here is immature. */
  cells: Readonly<Record<number, CohortTableCell>>
}

const NOT_YET: CohortTableCell = { state: 'immature', players: null, value: null }

export function cellAt(row: CohortTableRow, offset: number): CohortTableCell {
  return row.cells[offset] ?? NOT_YET
}

/** The cohort's last day: itself for a day, the seventh for a week, the month's last for a month. */
export function lastDayOf(row: Pick<CohortTableRow, 'start'>, grain: CohortGrain): number {
  if (grain === 'week') return row.start + 6 * DAY
  if (grain === 'month') {
    const start = new Date(row.start)
    return Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + 1, 0)
  }
  return row.start
}

/** The first day of the cohort after this one. */
export function nextStart(start: number, grain: CohortGrain): number {
  if (grain === 'month') {
    const date = new Date(start)
    return Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 1)
  }
  return start + (grain === 'week' ? 7 : 1) * DAY
}

/** The first day a cell of this row can be known: its youngest player's day N, over. */
export function readyOn(row: Pick<CohortTableRow, 'start'>, grain: CohortGrain, offset: number): number {
  return lastDayOf(row, grain) + (offset + 1) * DAY
}

// --- What a cell shows --------------------------------------------------------------------

/**
 * One way of reading the cells: what number a cell stands for, how it is written, what
 * the "All cohorts" row makes of a column, and how a cell's distance from the column is
 * put — in points for a share, as a change for anything else.
 */
export interface CohortMode {
  key: string
  /** For the switch: "%", "Players". */
  label: string
  read: (cell: CohortTableCell, row: CohortTableRow) => number | null
  format: (value: number) => string
  /** The "All cohorts" figure for one column. */
  summarize: (rows: readonly CohortTableRow[], offset: number) => number | null
  /** What a cell is compared with in the average colouring. The summary, unless a sum. */
  reference?: (rows: readonly CohortTableRow[], offset: number) => number | null
  compare: 'points' | 'ratio'
  /**
   * A second, smaller figure under the first: the players behind a share. Absent, a cell
   * holds one figure.
   */
  detail?: CohortDetail
}

export interface CohortDetail {
  read: (cell: CohortTableCell, row: CohortTableRow) => number | null
  format: (value: number) => string
  /** The "All cohorts" row's. */
  summarize: (rows: readonly CohortTableRow[], offset: number) => number | null
}

/** The cells of a column that say something, with the size they speak for. */
function known(rows: readonly CohortTableRow[], offset: number, read: CohortMode['read']) {
  const out: { value: number, size: number }[] = []
  for (const row of rows) {
    const cell = cellAt(row, offset)
    if (cell.state !== 'ok' || row.size === null) continue
    const value = read(cell, row)
    if (value !== null && Number.isFinite(value)) out.push({ value, size: row.size })
  }
  return out
}

/**
 * Every cohort old enough, weighted by its size, so a young cohort never drags a column
 * down and a small one never pulls it about.
 */
function weighted(read: CohortMode['read']): CohortMode['summarize'] {
  return (rows, offset) => {
    const cells = known(rows, offset, read)
    const size = cells.reduce((sum, cell) => sum + cell.size, 0)
    return size ? cells.reduce((sum, cell) => sum + cell.value * cell.size, 0) / size : null
  }
}

/**
 * Always one decimal, where `writeFigure` drops a trailing zero: in a column of shares,
 * "26%" under "25.9%" reads as a different kind of number rather than a rounder one.
 */
const share = new Intl.NumberFormat('en', { minimumFractionDigits: 1, maximumFractionDigits: 1 })

/** The share of the cohort: retention, payer conversion. */
export function rateMode(label = '%'): CohortMode {
  const read: CohortMode['read'] = cell => (cell.state === 'ok' ? cell.value : null)
  return {
    key: 'rate',
    label,
    read,
    format: value => `${share.format(value * 100)}%`,
    summarize: weighted(read),
    compare: 'points',
    detail: playersDetail
  }
}

/** The players a share is of, added up for the summary. */
const playersDetail: CohortDetail = {
  read: cell => (cell.state === 'ok' ? cell.players : null),
  format: value => writeFigure(value, { compact: value >= 10_000 }),
  summarize: (rows, offset) => {
    const cells = known(rows, offset, cell => cell.players)
    return cells.length ? cells.reduce((sum, cell) => sum + cell.value, 0) : null
  }
}

/**
 * The players themselves. The summary is every cohort's added up; a cell is compared with
 * the column's mean cohort, since set against the total every one of them is "below".
 */
export function countMode(label = 'Players'): CohortMode {
  const read: CohortMode['read'] = cell => (cell.state === 'ok' ? cell.players : null)
  return {
    key: 'count',
    label,
    read,
    format: value => writeFigure(value, { compact: value >= 10_000 }),
    summarize: (rows, offset) => {
      const cells = known(rows, offset, read)
      return cells.length ? cells.reduce((sum, cell) => sum + cell.value, 0) : null
    },
    reference: (rows, offset) => {
      const cells = known(rows, offset, read)
      return cells.length ? cells.reduce((sum, cell) => sum + cell.value, 0) / cells.length : null
    },
    compare: 'ratio'
  }
}

/** The metric's own figure per player, on that day: playtime, revenue. */
export function valueMode(options: { key?: string, label: string, format: (value: number) => string }): CohortMode {
  const read: CohortMode['read'] = cell => (cell.state === 'ok' ? cell.value : null)
  return { key: options.key ?? 'value', label: options.label, read, format: options.format, summarize: weighted(read), compare: 'ratio' }
}

/** The running total per player up to the day: LTV, lifetime playtime. */
export function cumulativeMode(options: { key?: string, label: string, format: (value: number) => string }): CohortMode {
  const read: CohortMode['read'] = cell => (cell.state === 'ok' ? cell.cumulative ?? null : null)
  return { key: options.key ?? 'cumulative', label: options.label, read, format: options.format, summarize: weighted(read), compare: 'ratio' }
}

/** How far `value` sits from `reference`, in the mode's own terms. */
export function differenceOf(mode: CohortMode, value: number, reference: number): number | null {
  if (mode.compare === 'points') return value - reference
  return reference === 0 ? null : (value - reference) / Math.abs(reference)
}

/** "+3.1 pp", "−12%". The sign is always written: it is the part a glance reads. */
export function formatDifference(mode: CohortMode, difference: number): string {
  if (mode.compare === 'ratio') return formatChange(difference)
  const points = Math.round(difference * 1000) / 10
  if (points === 0) return '0 pp'
  return `${points > 0 ? '+' : '−'}${Math.abs(points).toFixed(1)} pp`
}

/** A cell as the table draws it: its state, its figure and the one under it, and its shade. */
export interface CohortDrawnCell {
  offset: number
  state: CohortCellState
  text: string
  /** The smaller figure under it, when the mode has one. */
  detail: string
  shade: CohortShade | null
}

// --- Colour -------------------------------------------------------------------------------

/** A type rather than an interface, so Vue takes it as a `style` binding. */
export type CohortShade = { background: string, color?: string }

/** Where the ink turns light: past this the fill is dark enough to need it. */
const INK_TURNS = 0.55

/**
 * The accent at the strength of the value within the table's range. The floor is kept
 * off zero so the lowest cell still reads as a figure rather than as an empty one.
 */
export function heatShade(value: number, domain: readonly [number, number]): CohortShade {
  const [min, max] = domain
  const t = max > min ? Math.min(1, Math.max(0, (value - min) / (max - min))) : 1
  const alpha = 0.06 + 0.94 * t ** 0.85
  return {
    background: `rgba(var(--v-theme-primary), ${alpha.toFixed(3)})`,
    color: alpha > INK_TURNS ? 'rgb(var(--v-theme-on-primary))' : undefined
  }
}

/**
 * Above the column in the success colour, below it in the error one, each by how far it
 * is against the column's widest gap. A cell within a few percent of that gap is left
 * plain: it is the average, and lighting it would only colour noise.
 */
export function divergingShade(difference: number, spread: number): CohortShade | null {
  if (!(spread > 0)) return null
  const t = Math.min(1, Math.abs(difference) / spread)
  if (t < 0.04) return null
  const tone = difference > 0 ? 'success' : 'error'
  const alpha = 0.1 + 0.8 * t ** 0.85
  return {
    background: `rgba(var(--v-theme-${tone}), ${alpha.toFixed(3)})`,
    color: alpha > INK_TURNS ? `rgb(var(--v-theme-on-${tone}))` : undefined
  }
}

/**
 * The range the heat is drawn over, across every cell shown. Day 0 is left out: it is the
 * whole cohort by definition, and in the range it would wash every later day to nothing.
 */
export function heatDomain(
  rows: readonly CohortTableRow[],
  offsets: readonly number[],
  mode: CohortMode
): [number, number] | null {
  let min = Infinity
  let max = -Infinity
  for (const offset of offsets) {
    if (offset === 0 && offsets.length > 1) continue
    for (const { value } of known(rows, offset, mode.read)) {
      if (value < min) min = value
      if (value > max) max = value
    }
  }
  return Number.isFinite(min) ? [min, max] : null
}

// --- From the API -------------------------------------------------------------------------

/** The fields of the API's `CohortRow` this reads, spelt out so the layer needs no contracts. */
export interface CohortRowLike {
  cohort_date: string
  day_offset: number
  cohort_size: number
  players: number
  rate: number
  playtime_ms?: number
  revenue_micros?: number
  revenue_micros_cumulative?: number
}

export interface PivotOptions<R extends CohortRowLike> {
  /** The requested range, as the query's `cohort.from` and `cohort.to`. */
  from: string
  to: string
  grain: CohortGrain
  /** The answer's `computed_at`: the day up to which every cohort has been counted. */
  asOf: string | number
  offsets: readonly number[]
  /** The cell's own figure. The row's `rate` unless the metric has another. */
  value?: (row: R) => number
  cumulative?: (row: R) => number
}

const utcDay = (text: string) => Date.parse(`${text.slice(0, 10)}T00:00:00Z`)

/**
 * The API's flat rows turned into the table's, newest cohort first.
 *
 * Two things about the answer shape this (apps/backend/internal/query/suppress.go):
 *
 * - A suppressed cell is dropped, not flagged. So a missing cell is `withheld` when the
 *   cohort is old enough for the offset and `immature` when it is not — which cannot tell
 *   a withheld cell from a day on which genuinely nobody came back and no row was written.
 *   Until the server says which cells it withheld, both read as withheld: the cautious one.
 * - A cohort too small to describe has no rows at all, so the cohorts are listed from the
 *   range asked for rather than from the rows, and such a cohort comes out with no size.
 */
export function pivotCohortRows<R extends CohortRowLike>(rows: readonly R[], options: PivotOptions<R>): CohortTableRow[] {
  const asOf = typeof options.asOf === 'number' ? options.asOf : Date.parse(options.asOf)
  const today = Math.floor(asOf / DAY) * DAY

  const byCohort = new Map<number, R[]>()
  for (let at = utcDay(options.from); at <= utcDay(options.to); at = nextStart(at, options.grain)) byCohort.set(at, [])
  for (const row of rows) {
    const at = utcDay(row.cohort_date)
    byCohort.set(at, [...(byCohort.get(at) ?? []), row])
  }

  return [...byCohort.entries()]
    .sort(([a], [b]) => b - a)
    .map(([start, found]) => {
      const size = found[0]?.cohort_size ?? null
      const cells: Record<number, CohortTableCell> = {}
      for (const offset of options.offsets) {
        const row = found.find(entry => entry.day_offset === offset)
        if (row) {
          cells[offset] = {
            state: 'ok',
            players: row.players,
            value: options.value ? options.value(row) : row.rate,
            cumulative: options.cumulative?.(row) ?? null
          }
        } else if (readyOn({ start }, options.grain, offset) <= today) {
          cells[offset] = { state: 'withheld', players: null, value: null }
        }
      }
      return { key: String(start), start, size, cells }
    })
}
