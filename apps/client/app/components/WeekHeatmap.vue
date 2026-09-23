<script setup lang="ts">
// A week as a grid, drawn by Vuetify's VHeatmap (labs): one row per weekday, one square
// per hour, the hours grouped into the four parts of a day. Above it, what the grid says in
// words — the busiest and quietest hour, weekends against weekdays, the busiest part of the
// day — so nobody has to read shades to get the answer.
//
// Five shades rather than a gradient: a bucket has a range that can be written down, and
// VHeatmap lets each bucket be switched off from its legend, which a gradient cannot. The
// legend is ours (VHeatmap's `legend` slot) so the ranges sit beside their shades and the
// row starts where the grid does. Hours are the viewer's local time.
const props = defineProps<{
  /** Seven rows, Monday first, of 24 counts each. */
  values: readonly (readonly number[])[]
  /** What a cell counts: "session starts". */
  unit: string
}>()

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
const WEEKDAY_NAMES = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday']
const HOURS = Array.from({ length: 24 }, (_, hour) => String(hour).padStart(2, '0'))
const PARTS = [
  { label: 'Night', from: 0 },
  { label: 'Morning', from: 6 },
  { label: 'Afternoon', from: 12 },
  { label: 'Evening', from: 18 }
]

const partOf = (hour: number) => PARTS[Math.floor(hour / 6)]!.label

// Grouped by part of the day, so each group takes its six hours from its own items; a
// `columns` list would be laid out whole in every group.
const items = computed(() => props.values.flatMap((row, weekday) =>
  row.map((value, hour) => ({ row: WEEKDAYS[weekday]!, column: HOURS[hour]!, part: partOf(hour), value }))))

// --- Shades -------------------------------------------------------------------------------

const largest = computed(() => Math.max(1, ...props.values.flat()))

const SHADES = [0.12, 0.3, 0.5, 0.72, 1]

const thresholds = computed(() => SHADES.map((alpha, index) => ({
  min: Math.round(largest.value * index / SHADES.length),
  color: `rgba(var(--v-theme-primary), ${alpha})`
})))

/** "Under 8.2k", "8.2k–16.4k", "32.8k+": what each shade stands for. */
function rangeOf(index: number): string {
  const stops = thresholds.value
  const from = writeFigure(stops[index]!.min, { compact: true })
  if (index === 0) return `Under ${writeFigure(stops[1]!.min, { compact: true })}`
  if (index === stops.length - 1) return `${from}+`
  return `${from}–${writeFigure(stops[index + 1]!.min, { compact: true })}`
}

// --- Size ---------------------------------------------------------------------------------

const GAP = 4
const GROUP_GAP = 14
const ROW_HEADER = 40
/** A square that would make the grid taller than a glance is capped here. */
const LARGEST_CELL = 40

// VHeatmap takes a fixed cell size, so the squares are sized from the width they are given:
// what is left after the weekday column and the gaps, shared across 24 hours.
const box = useTemplateRef<HTMLElement>('box')
const { width } = useElementSize(box)
const cellSize = computed(() => Math.min(LARGEST_CELL,
  Math.max(10, Math.floor((width.value - ROW_HEADER - GAP * 23 - GROUP_GAP * (PARTS.length - 1)) / 24))))

function cellProps(cell: { row: string, column: string, value: number }) {
  return { title: `${cell.row} ${cell.column}:00 — ${writeFigure(cell.value)} ${props.unit}` }
}

// --- In words -----------------------------------------------------------------------------

const facts = computed<Fact[]>(() => {
  const cells = props.values.flatMap((row, weekday) => row.map((value, hour) => ({ weekday, hour, value })))
  const busiest = cells.reduce((best, cell) => (cell.value > best.value ? cell : best))
  const quietest = cells.reduce((least, cell) => (cell.value < least.value ? cell : least))

  const perDay = props.values.map(row => row.reduce((sum, value) => sum + value, 0))
  const weekday = perDay.slice(0, 5).reduce((sum, value) => sum + value, 0) / 5
  const weekend = perDay.slice(5).reduce((sum, value) => sum + value, 0) / 2

  const byPart = PARTS.map(part => ({
    label: part.label,
    total: props.values.reduce((sum, row) => sum + row.slice(part.from, part.from + 6).reduce((a, b) => a + b, 0), 0)
  }))
  const everything = byPart.reduce((sum, part) => sum + part.total, 0)
  const peak = byPart.reduce((best, part) => (part.total > best.total ? part : best))

  const at = (cell: { weekday: number, hour: number }) => `${WEEKDAY_NAMES[cell.weekday]} ${HOURS[cell.hour]}:00`
  return [
    { key: 'busiest', label: 'Busiest hour', value: at(busiest), note: `${writeFigure(busiest.value)} ${props.unit}` },
    { key: 'quietest', label: 'Quietest hour', value: at(quietest), note: `${writeFigure(quietest.value)} ${props.unit}` },
    {
      key: 'weekend',
      label: 'Weekends',
      value: `${formatChange((weekend - weekday) / weekday)} vs weekdays`,
      note: 'A weekend day against an average weekday'
    },
    {
      key: 'part',
      label: 'Busiest part of the day',
      value: peak.label,
      note: `${writeFigure(peak.total / everything * 100, { decimals: 0, unit: '%' })} of ${props.unit}`
    }
  ]
})
</script>

<template>
  <div class="flex flex-col gap-5">
    <FactGrid :columns="4" :facts="facts" label="When players play, in words" />

    <div ref="box" class="ps-week-heatmap min-w-0">
      <v-heatmap
        :cell-size="cellSize"
        :gap="GAP"
        :group-gap="GROUP_GAP"
        :item-props="cellProps"
        :items="items"
        :rows="WEEKDAYS"
        :thresholds="thresholds"
        empty-color="rgba(var(--v-theme-on-surface), 0.04)"
        group-by="part"
        hover
        legend
        rounded="5"
      >
        <template #group-header="{ group }">
          <span class="text-[11px] text-ink/60">{{ group.label }}</span>
        </template>
        <template #column-header="{ column }">
          <span class="font-mono text-[10px] text-ink/40">{{ Number(column) % 2 === 0 ? column : '' }}</span>
        </template>
        <template #row-header="{ row }">
          <span class="text-[11px] text-ink/55">{{ row }}</span>
        </template>

        <template #legend="{ activeBuckets, toggle }">
          <div class="flex flex-wrap items-center gap-x-4 gap-y-2 pt-4 pl-10">
            <span class="text-[11px] text-ink/50">{{ unit }} per hour</span>
            <button
              v-for="(stop, index) in thresholds"
              :key="index"
              :aria-pressed="activeBuckets.includes(index)"
              :class="activeBuckets.includes(index) ? 'text-ink/70' : 'text-ink/35 line-through'"
              class="
                inline-flex items-center gap-1.5 rounded-(--ps-radius-x-small) px-1 text-[11px]
                transition-colors
                hover:bg-ink/5
              "
              type="button"
              @click="toggle(index)"
            >
              <span
                :style="{ background: activeBuckets.includes(index) ? stop.color : 'rgba(var(--v-theme-on-surface), 0.06)' }"
                aria-hidden="true"
                class="size-3 rounded-[3px]"
              />
              <span class="font-mono">{{ rangeOf(index) }}</span>
            </button>
            <span class="ml-auto text-[11px] text-ink/40">Select a shade to hide it</span>
          </div>
        </template>
      </v-heatmap>
    </div>
  </div>
</template>

<style scoped>
/*
 * VHeatmap is an inline block with its own frame: 12px around it, 12px under the grid and
 * 24px after the weekday labels. Inside a card that frame doubles the card's padding and
 * leaves the grid short of the edge it should reach, so it is taken back to nothing and
 * the weekday column to a gap of its own. Unlayered, so it wins over Vuetify's layer.
 */
.ps-week-heatmap :deep(.v-heatmap) {
  --v-heatmap-row-header-padding: 12px;
  --v-heatmap-column-header-margin: 0 0 6px;

  display: block;
  padding: 0;
}

.ps-week-heatmap :deep(.v-heatmap__body) {
  padding: 0;
}

.ps-week-heatmap :deep(.v-heatmap__group-labels) {
  margin-bottom: 2px;
}
</style>
