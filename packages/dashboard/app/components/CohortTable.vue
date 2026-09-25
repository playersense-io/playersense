<script setup lang="ts">
/**
 * The cohort triangle, for any metric: one row per cohort, one column per day since it
 * arrived, and in each cell what a `CohortMode` reads from it, shaded so the decay reads
 * down and across at a glance. The first row is every cohort together, and it stays under
 * the header, as the cohort and its size stay at the left, however far the table scrolls.
 *
 * Three kinds of cell, never drawn alike: a figure, a cohort too young for the day
 * (nothing to know yet), and a cell withheld because it counts too few players to show.
 * Neither of the last two is a zero, and a zero would be the one wrong way to draw them.
 *
 * Pointing at a cell, or moving to it with the arrow keys, lights its row and its column
 * and opens a card that says the cell in words. The table can hold ninety cohorts by
 * thirty-five days, so the light is kept off the rows: the row is `:hover` and
 * `:focus-within`, the column is one `<col>`, and each row is a `CohortGridRow`, which Vue
 * leaves alone unless its own props changed — so moving the pointer redraws the header
 * and nothing under it. A component rather than `v-memo`: the skip is Vue's own props
 * check, with nothing cached by hand to go stale when the list is replaced whole.
 */
import {
  type CohortColor,
  type CohortGrain,
  type CohortMode,
  type CohortDrawnCell,
  type CohortShade,
  type CohortTableRow,
  cellAt,
  differenceOf,
  divergingShade,
  formatDifference,
  heatDomain,
  heatShade,
  lastDayOf,
  offsetLabel,
  readyOn
} from '../utils/cohort-table'

const props = withDefaults(defineProps<{
  rows: readonly CohortTableRow[]
  offsets: readonly number[]
  mode: CohortMode
  color?: CohortColor
  grain?: CohortGrain
  /** The suppression threshold the answer came with. */
  threshold: number
  /** What the table is, for a screen reader. */
  label?: string
  /** The head of the first column: "Cohort", or the dimension segments are named by. */
  rowLabel?: string
  /** The head of the size column. */
  sizeLabel?: string
  /** What a cohort is made of, in the card: "1,204 of 9,830 players". */
  unit?: string
  maxHeight?: string
  /**
   * While the answer is on its way: the header stays, since the days are the reader's and
   * already known, and every row below it is bones in the shape of the cells to come.
   */
  loading?: boolean
  /** How many rows of bones to draw while loading: about as many as are coming. */
  loadingRows?: number
}>(), {
  color: 'heat',
  grain: 'day',
  label: 'Cohorts',
  rowLabel: 'Cohort',
  sizeLabel: 'Players',
  unit: 'players',
  maxHeight: '34rem',
  loading: false,
  loadingRows: 8
})

// --- Words --------------------------------------------------------------------------------

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const weekdayFormat = new Intl.DateTimeFormat('en', { weekday: 'short', timeZone: 'UTC' })
const monthFormat = new Intl.DateTimeFormat('en', { month: 'short', year: 'numeric', timeZone: 'UTC' })

function cohortLabel(row: CohortTableRow): string {
  if (row.label) return row.label
  if (props.grain === 'day') return dayFormat.format(row.start)
  if (props.grain === 'month') return monthFormat.format(row.start)
  return `${dayFormat.format(row.start)} – ${dayFormat.format(lastDayOf(row, 'week'))}`
}

/** The card's name for a row: a segment by its name, a stretch of time in full. */
function cohortTitle(row: CohortTableRow): string {
  if (row.label) return row.label
  if (props.grain === 'week') return `Week of ${cohortLabel(row)}`
  if (props.grain === 'month') return cohortLabel(row)
  return `${weekdayFormat.format(row.start)} ${cohortLabel(row)}`
}

/** Named rows and week ranges want the wider label column. */
const labelWidth = computed(() => (props.grain === 'week' || props.rows.some(row => row.label) ? '9.25rem' : '7.5rem'))

const sizeText = (size: number | null) => (size === null ? `<${props.threshold}` : writeFigure(size))

// --- Figures and shades -------------------------------------------------------------------

const summary = computed(() => props.offsets.map(offset => props.mode.summarize(props.rows, offset)))

const summaryDetails = computed(() => props.offsets.map(offset => detailText(props.mode.detail?.summarize(props.rows, offset) ?? null)))

function detailText(value: number | null): string {
  return value === null || !props.mode.detail ? '' : props.mode.detail.format(value)
}

const reference = computed(() => props.offsets.map(offset => (props.mode.reference ?? props.mode.summarize)(props.rows, offset)))

const domain = computed(() => heatDomain(props.rows, props.offsets, props.mode))

/**
 * How far a column's cohorts usually stray from its reference, which the average colouring
 * is drawn against: the 90th percentile of the gaps rather than the widest. A cohort of
 * sixty players strays furthest of all, and drawn against it every other cell goes pale.
 */
const spreads = computed(() => props.offsets.map((offset, index) => {
  const against = reference.value[index]
  if (against === null || against === undefined) return 0
  const gaps: number[] = []
  for (const row of props.rows) {
    const value = props.mode.read(cellAt(row, offset), row)
    const difference = value === null ? null : differenceOf(props.mode, value, against)
    if (difference !== null) gaps.push(Math.abs(difference))
  }
  gaps.sort((a, b) => a - b)
  return gaps[Math.floor((gaps.length - 1) * 0.9)] ?? 0
}))

function shadeOf(value: number | null, index: number): CohortShade | null {
  if (value === null) return null
  if (props.color === 'heat') return domain.value ? heatShade(value, domain.value) : null
  const against = reference.value[index]
  if (against === null || against === undefined) return null
  const difference = differenceOf(props.mode, value, against)
  return difference === null ? null : divergingShade(difference, spreads.value[index] ?? 0)
}

type DrawnCell = CohortDrawnCell

const NO_CELLS: readonly DrawnCell[] = []

/**
 * Every cell as it is drawn, worked out once per change of data, days, mode or colouring
 * rather than once per render. Each row's memo is this, so a row redraws only when its
 * own cells have.
 */
const drawn = computed(() => new Map(props.rows.map(row => [row.key, props.offsets.map((offset, index): DrawnCell => {
  const cell = cellAt(row, offset)
  if (cell.state !== 'ok') return { offset, state: cell.state, text: '', detail: '', shade: null }
  const value = props.mode.read(cell, row)
  return {
    offset,
    state: 'ok',
    text: value === null ? '–' : props.mode.format(value),
    detail: detailText(props.mode.detail?.read(cell, row) ?? null),
    shade: shadeOf(value, index)
  }
})])))

/**
 * The reference row is shaded in the heat only: against the average, it is the average.
 * Nor when it is a sum, which is not on the cells' scale and would read as the darkest.
 */
const summaryShades = computed(() => summary.value.map(value => (
  props.color === 'heat' && !props.mode.reference && value !== null && domain.value ? heatShade(value, domain.value) : null
)))

const totalSize = computed(() => props.rows.reduce((sum, row) => sum + (row.size ?? 0), 0))

const anyWithheldCohort = computed(() => props.rows.some(row => row.size === null))

// --- Where the reader is ------------------------------------------------------------------

/**
 * Rows are counted as the grid counts them: 0 is "All cohorts", 1 the newest cohort. The
 * focus is the one cell the Tab key lands on; the active cell is the one lit, which is the
 * pointer's while it is over the table and the focus's otherwise.
 */
interface Place { row: number, offset: number }
interface ActiveCell extends Place { el: HTMLElement }

const table = useTemplateRef<HTMLTableElement>('table')
const scroller = useTemplateRef<HTMLElement>('scroller')

const focus = ref<Place>({ row: 0, offset: props.offsets[0] ?? 0 })
const active = shallowRef<ActiveCell | null>(null)

/** The focus, kept on a cell that exists when the days or the cohorts change under it. */
const tabStop = computed<Place>(() => ({
  row: Math.min(focus.value.row, props.rows.length),
  offset: props.offsets.includes(focus.value.offset) ? focus.value.offset : (props.offsets[0] ?? 0)
}))

const tabIndexOf = (row: number, offset: number) => (tabStop.value.row === row && tabStop.value.offset === offset ? 0 : -1)

/** The one thing a row's memo needs to know about the focus: which of its cells has it. */
const stopIn = (row: number) => (tabStop.value.row === row ? tabStop.value.offset : -1)

function placeOf(target: EventTarget | null): ActiveCell | null {
  const el = (target as HTMLElement | null)?.closest<HTMLElement>('td[data-offset]')
  if (!el) return null
  return { row: Number(el.dataset.row), offset: Number(el.dataset.offset), el }
}

function onPointer(event: PointerEvent) {
  const place = placeOf(event.target)
  if (place && (place.el !== active.value?.el)) active.value = place
}

function onLeave() {
  active.value = placeOf(document.activeElement)
}

function onFocus(event: FocusEvent) {
  const place = placeOf(event.target)
  if (!place) return
  focus.value = { row: place.row, offset: place.offset }
  active.value = place
}

function onBlur(event: FocusEvent) {
  if (!table.value?.contains(event.relatedTarget as Node | null) && !table.value?.matches(':hover')) active.value = null
}

const PAGE = 10

/** A grid's keys: arrows by one, Home and End along the row, with Ctrl to the corners. */
function onKey(event: KeyboardEvent) {
  const last = props.rows.length
  const at = props.offsets.indexOf(tabStop.value.offset)
  let row = tabStop.value.row
  let column = at
  switch (event.key) {
    case 'ArrowUp':
      row -= 1
      break
    case 'ArrowDown':
      row += 1
      break
    case 'ArrowLeft':
      column -= 1
      break
    case 'ArrowRight':
      column += 1
      break
    case 'PageUp':
      row -= PAGE
      break
    case 'PageDown':
      row += PAGE
      break
    case 'Home':
      column = 0
      if (event.ctrlKey || event.metaKey) row = 0
      break
    case 'End':
      column = props.offsets.length - 1
      if (event.ctrlKey || event.metaKey) row = last
      break
    case 'Escape':
      active.value = null
      return
    default:
      return
  }
  event.preventDefault()
  row = Math.max(0, Math.min(last, row))
  column = Math.max(0, Math.min(props.offsets.length - 1, column))
  const offset = props.offsets[column]
  if (offset === undefined) return
  focus.value = { row, offset }
  void nextTick(() => table.value?.querySelector<HTMLElement>(`td[data-row="${row}"][data-offset="${offset}"]`)?.focus())
}

// Past the fixed columns' edge, a shadow says the table goes on under them.
const { x: scrolledX } = useScroll(scroller)

// --- The card -----------------------------------------------------------------------------

interface Card {
  title: string
  day: string
  figure?: string
  lines: string[]
  difference?: { text: string, tone: 'up' | 'down' | 'level' }
}

const card = computed<Card | null>(() => {
  const place = active.value
  if (!place) return null
  const index = props.offsets.indexOf(place.offset)
  if (index === -1) return null
  const day = `Day ${place.offset}`

  if (place.row === 0) {
    const value = summary.value[index] ?? null
    const counted = props.rows.filter(row => cellAt(row, place.offset).state === 'ok' && row.size !== null).length
    return {
      title: 'All cohorts',
      day,
      figure: value === null ? undefined : props.mode.format(value),
      lines: value === null
        ? [`No cohort is old enough for day ${place.offset} yet`]
        : [`${props.mode.reference ? 'Added up' : 'Weighted by size'} over the ${counted} ${counted === 1 ? 'cohort' : 'cohorts'} old enough`]
    }
  }

  const row = props.rows[place.row - 1]
  if (!row) return null
  const cell = cellAt(row, place.offset)
  const title = cohortTitle(row)

  if (cell.state === 'immature') {
    return { title, day, lines: [`Not old enough for day ${place.offset} yet`, `Known from ${dayFormat.format(readyOn(row, props.grain, place.offset))}`] }
  }
  if (cell.state === 'withheld' || row.size === null) {
    return { title, day, lines: [`Fewer than ${props.threshold} ${props.unit}: withheld, so no one can be singled out`] }
  }

  const value = props.mode.read(cell, row)
  const lines = cell.players === null ? [] : [`${writeFigure(cell.players)} of ${writeFigure(row.size)} ${props.unit}`]
  const against = reference.value[index] ?? null
  const difference = value === null || against === null ? null : differenceOf(props.mode, value, against)
  return {
    title,
    day,
    figure: value === null ? undefined : props.mode.format(value),
    lines,
    difference: difference === null
      ? undefined
      : {
          text: `${formatDifference(props.mode, difference)} vs ${props.mode.reference ? 'the average cohort' : 'all cohorts'} (${props.mode.format(against!)})`,
          tone: Math.abs(difference) < 0.0005 ? 'level' : difference > 0 ? 'up' : 'down'
        }
  }
})

/** The card's words, for a screen reader on the cell itself. */
function spoken(row: number, offset: number): string | undefined {
  if (tabStop.value.row !== row || tabStop.value.offset !== offset) return undefined
  const words = card.value && active.value?.row === row && active.value.offset === offset ? card.value : null
  if (!words) return undefined
  return [words.title, words.day, words.figure, ...words.lines, words.difference?.text].filter(Boolean).join(', ')
}

const GLYPH = { up: '▲', down: '▼', level: '●' } as const
const GLYPH_CLASS = { up: 'cohort-card__glyph--up', down: 'cohort-card__glyph--down', level: 'cohort-card__glyph--level' } as const
</script>

<template>
  <div class="cohort-table">
    <div
      ref="scroller"
      :class="{ 'cohort-table__scroller--scrolled': scrolledX > 0 }"
      :style="{ 'maxHeight': maxHeight, '--cohort-label': labelWidth }"
      :aria-busy="loading || undefined"
      class="cohort-table__scroller"
    >
      <table v-if="loading && offsets.length" :aria-label="`${label}, loading`" class="cohort-grid">
        <colgroup>
          <col class="cohort-grid__label-col">
          <col class="cohort-grid__size-col">
          <col v-for="offset in offsets" :key="offset">
        </colgroup>
        <thead>
          <tr class="cohort-grid__head">
            <th class="cohort-grid__label" scope="col">
              {{ rowLabel }}
            </th>
            <th class="cohort-grid__size" scope="col">
              {{ sizeLabel }}
            </th>
            <th
              v-for="offset in offsets"
              :key="offset"
              class="cohort-grid__day"
              scope="col"
            >
              <span>{{ offsetLabel(offset) }}</span>
            </th>
          </tr>
        </thead>
        <tbody aria-hidden="true">
          <tr v-for="line in loadingRows" :key="line">
            <th class="cohort-grid__label" scope="row">
              <span :style="{ width: `${55 + (line * 37) % 30}%` }" class="cohort-grid__bone" />
            </th>
            <td class="cohort-grid__size">
              <span class="cohort-grid__bone ml-auto w-3/5" />
            </td>
            <td v-for="offset in offsets" :key="offset">
              <span class="cohort-grid__cell cohort-grid__cell--bone" />
            </td>
          </tr>
        </tbody>
      </table>

      <table
        v-else-if="rows.length && offsets.length"
        ref="table"
        :aria-colcount="offsets.length + 2"
        :aria-label="label"
        :aria-rowcount="rows.length + 2"
        class="cohort-grid"
        role="grid"
        @focusin="onFocus"
        @focusout="onBlur"
        @keydown="onKey"
        @pointerleave="onLeave"
        @pointerover="onPointer"
      >
        <colgroup>
          <col class="cohort-grid__label-col">
          <col class="cohort-grid__size-col">
          <col
            v-for="offset in offsets"
            :key="offset"
            :class="{ 'cohort-grid__col--lit': active?.offset === offset }"
          >
        </colgroup>

        <thead>
          <tr class="cohort-grid__head">
            <th class="cohort-grid__label" scope="col">
              {{ rowLabel }}
            </th>
            <th class="cohort-grid__size" scope="col">
              {{ sizeLabel }}
            </th>
            <th
              v-for="offset in offsets"
              :key="offset"
              :class="{ 'cohort-grid__day--lit': active?.offset === offset }"
              class="cohort-grid__day"
              scope="col"
            >
              <span>{{ offsetLabel(offset) }}</span>
            </th>
          </tr>

          <tr class="cohort-grid__summary" :class="{ 'cohort-grid__summary--lit': active?.row === 0 }">
            <th class="cohort-grid__label" scope="row">
              All cohorts
            </th>
            <td class="cohort-grid__size">
              {{ writeFigure(totalSize, { compact: totalSize >= 100_000 }) }}
            </td>
            <td
              v-for="(value, index) in summary"
              :key="offsets[index]"
              :aria-label="spoken(0, offsets[index]!)"
              :data-offset="offsets[index]"
              :tabindex="tabIndexOf(0, offsets[index]!)"
              data-row="0"
              role="gridcell"
            >
              <span :style="summaryShades[index] ?? undefined" class="cohort-grid__cell" :class="{ 'cohort-grid__cell--plain': !summaryShades[index] }">
                <template v-if="value !== null">
                  <span class="cohort-grid__figure">{{ mode.format(value) }}</span>
                  <span v-if="summaryDetails[index]" class="cohort-grid__detail">{{ summaryDetails[index] }}</span>
                </template>
                <span v-else class="cohort-grid__empty">–</span>
              </span>
            </td>
          </tr>
        </thead>

        <tbody>
          <CohortGridRow
            v-for="(row, index) in rows"
            :key="row.key"
            :cells="drawn.get(row.key) ?? NO_CELLS"
            :mark="row.mark"
            :index="index + 1"
            :label="cohortLabel(row)"
            :size="sizeText(row.size)"
            :spoken="stopIn(index + 1) === -1 ? undefined : spoken(index + 1, stopIn(index + 1))"
            :tab-stop="stopIn(index + 1)"
            :threshold="threshold"
            :weekday="grain === 'day' && !row.label ? weekdayFormat.format(row.start) : ''"
            :withheld-size="row.size === null"
          />
        </tbody>
      </table>

      <div v-else class="p-4">
        <NoDataPlaceholder :height="160" :seed="7" label="No cohorts in this range yet" />
      </div>
    </div>

    <div v-if="!loading" class="flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-line px-4 py-3 text-[11px] text-ink/55">
      <span v-if="color === 'heat'" class="flex items-center gap-2">
        <span class="font-mono">{{ domain ? mode.format(domain[0]) : '' }}</span>
        <span aria-hidden="true" class="cohort-legend__ramp cohort-legend__ramp--heat" />
        <span class="font-mono">{{ domain ? mode.format(domain[1]) : '' }}</span>
      </span>
      <span v-else class="flex items-center gap-2">
        below
        <span aria-hidden="true" class="cohort-legend__ramp cohort-legend__ramp--average" />
        above {{ mode.reference ? 'the average cohort' : 'all cohorts' }} on the same day
      </span>
      <span class="flex items-center gap-1.5">
        <span aria-hidden="true" class="h-3 w-5 rounded-[3px] bg-ink/2 ring-1 ring-ink/6 ring-inset" /> not old enough yet
      </span>
      <span class="flex items-center gap-1.5">
        <span class="font-mono text-ink/40">&lt;{{ threshold }}</span>
        too few {{ unit }} to show{{ anyWithheldCohort ? ', for a cell or a whole cohort' : '' }}
      </span>
      <span class="ml-auto hidden text-ink/40 lg:inline">Arrow keys move between cells</span>
    </div>

    <v-tooltip
      :model-value="card !== null"
      :offset="8"
      :target="active?.el"
      content-class="ps-figure-card"
      location="top"
      :transition="false"
    >
      <template v-if="card">
        <p class="m-0 flex items-baseline gap-2 text-[11px] text-ink/60">
          <span class="text-ink/80">{{ card.title }}</span>
          <span class="font-mono">{{ card.day }}</span>
        </p>
        <p v-if="card.figure" class="m-0 mt-0.5 font-mono text-base leading-6 text-ink">
          {{ card.figure }}
        </p>
        <p v-for="line in card.lines" :key="line" class="m-0 text-xs text-ink/70">
          {{ line }}
        </p>
        <p v-if="card.difference" class="m-0 mt-1 flex items-center gap-1.5 text-xs text-ink/70">
          <span :class="GLYPH_CLASS[card.difference.tone]" aria-hidden="true" class="cohort-card__glyph">{{ GLYPH[card.difference.tone] }}</span>
          {{ card.difference.text }}
        </p>
      </template>
    </v-tooltip>
  </div>
</template>

<!--
  Not scoped: the body's rows are CohortGridRow's elements, which a scoped sheet would not
  reach. Every class here is the table's own — cohort-grid__, cohort-table__, cohort-legend__ — so nothing
  else on a page is drawn by it.
-->
<style>
/*
 * Fixed heights make the sticky rows exact: the header is one line and every row below it
 * one cell of two, so "All cohorts" sticks at the header's height rather than a measured
 * one, and the scroll padding keeps a focused cell out from under both.
 */
.cohort-table__scroller {
  --cohort-head: 2.25rem;
  --cohort-row: 2.75rem;
  --cohort-size: 5.25rem;

  overflow: auto;
  overscroll-behavior-x: contain;
  scroll-padding-left: calc(var(--cohort-label) + var(--cohort-size));
  scroll-padding-top: calc(var(--cohort-head) + var(--cohort-row));
}

.cohort-grid {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 13px;
  min-width: 100%;
}

.cohort-grid__label-col {
  width: var(--cohort-label);
}

.cohort-grid__size-col {
  width: var(--cohort-size);
}

/* Weightless, so the fixed columns' own padding wins over it. */
:where(.cohort-grid) th,
:where(.cohort-grid) td {
  height: var(--cohort-row);
  padding: 2px;
}

/* The fixed columns: opaque, so what scrolls under them is hidden rather than showing through. */
.cohort-grid__label,
.cohort-grid__size {
  background: rgb(var(--v-theme-surface));
  left: 0;
  position: sticky;
  white-space: nowrap;
  z-index: 1;
}

.cohort-grid__label {
  color: rgba(var(--v-theme-on-surface), 0.8);
  font-weight: 400;
  max-width: var(--cohort-label);
  min-width: var(--cohort-label);
  padding-inline: 16px 8px;
  text-align: left;
}

.cohort-grid__size {
  color: rgba(var(--v-theme-on-surface), 0.6);
  font-family: var(--font-mono);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  left: var(--cohort-label);
  max-width: var(--cohort-size);
  min-width: var(--cohort-size);
  padding-inline: 8px 12px;
  text-align: right;
}

.cohort-grid__size--withheld {
  color: rgba(var(--v-theme-on-surface), 0.4);
}

/* The edge the table scrolls under, drawn only once something is under it. */
.cohort-table__scroller--scrolled .cohort-grid__size {
  box-shadow:
    inset -1px 0 0 rgba(var(--v-border-color), var(--v-border-opacity)),
    8px 0 8px -8px rgba(0, 0, 0, 0.2);
}

/* A long segment name — United Kingdom — ends in an ellipsis rather than under the size. */
.cohort-grid__name {
  display: inline-block;
  font-size: 0.75rem;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: middle;
  white-space: nowrap;
}

.cohort-grid__weekday {
  color: rgba(var(--v-theme-on-surface), 0.4);
  float: right;
  font-size: 11px;
}

/* --- The header and the reference row, both held at the top --------------------------- */

.cohort-grid thead th,
.cohort-grid thead td {
  position: sticky;
  z-index: 2;
}

.cohort-grid__head > * {
  background: rgb(var(--v-theme-surface));
  height: var(--cohort-head);
  top: 0;
}

.cohort-grid__head th {
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-size: 12px;
  font-weight: 400;
}

.cohort-grid__summary > * {
  background: rgb(var(--v-theme-surface));
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  top: var(--cohort-head);
}

.cohort-grid__summary .cohort-grid__label {
  color: rgb(var(--v-theme-on-surface));
  font-weight: 500;
}

.cohort-grid__summary .cohort-grid__cell {
  font-weight: 500;
}

/* The corners stay above both the header and the fixed columns as either scrolls. */
.cohort-grid thead .cohort-grid__label,
.cohort-grid thead .cohort-grid__size {
  z-index: 3;
}

.cohort-grid__day {
  min-width: 3.5rem;
  text-align: center;
}

.cohort-grid__day > span {
  border-radius: var(--ps-radius-x-small);
  display: inline-block;
  font-family: var(--font-mono);
  padding: 1px 6px;
  transition: background-color 120ms, color 120ms;
}

.cohort-grid__day--lit > span {
  background: rgba(var(--v-theme-on-surface), 0.08);
  color: rgb(var(--v-theme-on-surface));
}

/* --- Cells ---------------------------------------------------------------------------- */

/* The figure over the players it is of: the second smaller and quieter, in the cell's ink. */
.cohort-grid__cell {
  align-items: center;
  border-radius: var(--ps-radius-x-small);
  color: rgb(var(--v-theme-on-surface));
  display: flex;
  flex-direction: column;
  font-family: var(--font-mono);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  gap: 1px;
  height: 2.5rem;
  justify-content: center;
  padding-inline: 4px;
  white-space: nowrap;
}

.cohort-grid__figure {
  font-size: 12px;
  line-height: 14px;
}

.cohort-grid__detail {
  font-size: 10px;
  font-weight: 400;
  line-height: 12px;
  opacity: 0.7;
}

.cohort-grid__cell--plain {
  background: rgba(var(--v-theme-on-surface), 0.03);
}

.cohort-grid__cell--immature {
  background: rgba(var(--v-theme-on-surface), 0.02);
}

/* The dash a cell with nothing to know yet holds: there, so the cell is not read as
   missing, and faint, so it is not read as a figure. */
.cohort-grid__empty {
  color: rgba(var(--v-theme-on-surface), 0.25);
}

.cohort-grid__cell--withheld {
  background: rgba(var(--v-theme-on-surface), 0.05);
  color: rgba(var(--v-theme-on-surface), 0.4);
}

.cohort-grid td[data-offset] {
  cursor: default;
  outline: none;
}

.cohort-grid td[data-offset]:hover > .cohort-grid__cell,
.cohort-grid td[data-offset]:focus-visible > .cohort-grid__cell {
  box-shadow: 0 0 0 2px rgb(var(--v-theme-on-surface));
}

/* --- Loading ------------------------------------------------------------------------- */

/* Still, in the panel's skeleton colours: a label's line, and a well where each cell goes. */
.cohort-grid__bone {
  background: var(--ps-skeleton-bone);
  border-radius: var(--ps-radius-x-small);
  display: block;
  height: 10px;
}

.cohort-grid__cell--bone {
  background: var(--ps-skeleton-well);
}

/* --- The crosshair -------------------------------------------------------------------- */

/* The column: its `<col>`, washed, which shows through the gutter around every cell in it. */
.cohort-grid__col--lit {
  background: rgba(var(--v-theme-on-surface), 0.05);
}

/*
 * The row: the pointer's while the pointer is on the table, the focus's otherwise. The
 * fixed cells take the wash mixed into the surface, since they have to stay opaque.
 */
.cohort-grid tbody tr:hover > td,
.cohort-grid:not(:hover) tbody tr:focus-within > td {
  background-color: rgba(var(--v-theme-on-surface), 0.04);
}

.cohort-grid tbody tr:hover > .cohort-grid__label,
.cohort-grid tbody tr:hover > .cohort-grid__size,
.cohort-grid:not(:hover) tbody tr:focus-within > .cohort-grid__label,
.cohort-grid:not(:hover) tbody tr:focus-within > .cohort-grid__size {
  background-color: color-mix(in srgb, rgb(var(--v-theme-on-surface)) 4%, rgb(var(--v-theme-surface)));
}

.cohort-grid tbody tr:hover > .cohort-grid__label,
.cohort-grid:not(:hover) tbody tr:focus-within > .cohort-grid__label {
  color: rgb(var(--v-theme-on-surface));
}

.cohort-grid__summary--lit > .cohort-grid__label {
  text-decoration: underline;
  text-decoration-color: rgba(var(--v-theme-on-surface), 0.3);
  text-underline-offset: 3px;
}

/* --- The legend ----------------------------------------------------------------------- */

.cohort-legend__ramp {
  border-radius: 3px;
  display: inline-block;
  height: 10px;
  width: 96px;
}

.cohort-legend__ramp--heat {
  background: linear-gradient(to right, rgba(var(--v-theme-primary), 0.06), rgb(var(--v-theme-primary)));
}

.cohort-legend__ramp--average {
  background: linear-gradient(
    to right,
    rgba(var(--v-theme-error), 0.9),
    rgba(var(--v-theme-error), 0.12) 44%,
    rgba(var(--v-theme-on-surface), 0.04) 50%,
    rgba(var(--v-theme-success), 0.12) 56%,
    rgba(var(--v-theme-success), 0.9)
  );
}
</style>

<style>
/* The card's glyph. Unscoped, because the overlay is teleported out of this component. */
.cohort-card__glyph {
  font-size: 9px;
}

.cohort-card__glyph--up {
  color: rgb(var(--v-theme-success));
}

.cohort-card__glyph--down {
  color: rgb(var(--v-theme-error));
}

.cohort-card__glyph--level {
  color: rgba(var(--v-theme-on-surface), 0.4);
}
</style>
