<script setup lang="ts">
// A week as a grid, drawn by Vuetify's VHeatmap (labs): one row per weekday, one cell per
// hour, shaded along a scale from a faint accent to the full one. This wrapper turns a
// weekday × hour matrix into the heatmap's items, sizes the cells to the width it is given
// — VHeatmap takes a fixed cell size — and names each cell's figure in its title, since a
// shade on its own says only "more" or "less".
const props = defineProps<{
  /** Seven rows, Monday first, of 24 counts each. Hours in the viewer's local time. */
  values: readonly (readonly number[])[]
  /** What a cell counts, for its title: "session starts". */
  unit: string
}>()

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
const HOURS = Array.from({ length: 24 }, (_, hour) => String(hour).padStart(2, '0'))

const GAP = 3
// The weekday column plus VHeatmap's own spacing beside it.
const ROW_HEADER = 56
const HEIGHT = 22

const items = computed(() => props.values.flatMap((row, weekday) =>
  row.map((value, hour) => ({ row: WEEKDAYS[weekday]!, column: HOURS[hour]!, value }))))

const largest = computed(() => Math.max(1, ...props.values.flat()))

const thresholds = computed(() => ({
  from: { min: 0, color: 'rgba(var(--v-theme-primary), 0.06)' },
  to: { min: largest.value, color: 'rgb(var(--v-theme-primary))' }
}))

// The cells fill the width: what is left after the weekday column, shared across 24.
const box = useTemplateRef<HTMLElement>('box')
const { width } = useElementSize(box)
const cellWidth = computed(() => Math.max(10, Math.floor((width.value - ROW_HEADER - GAP * 23) / 24)))

function cellProps(cell: { row: string, column: string, value: number }) {
  return { title: `${cell.row} ${cell.column}:00 — ${writeFigure(cell.value)} ${props.unit}` }
}
</script>

<template>
  <div ref="box" class="min-w-0">
    <v-heatmap
      :cell-size="[cellWidth, HEIGHT]"
      :columns="HOURS"
      :gap="GAP"
      :item-props="cellProps"
      :items="items"
      :rows="WEEKDAYS"
      :thresholds="thresholds"
      hover
      legend
      rounded="3"
    >
      <template #column-header="{ column }">
        <span class="font-mono text-[10px] text-ink/45">{{ Number(column) % 3 === 0 ? column : '' }}</span>
      </template>
      <template #row-header="{ row }">
        <span class="text-[11px] text-ink/55">{{ row }}</span>
      </template>
    </v-heatmap>
  </div>
</template>
