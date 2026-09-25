<script setup lang="ts">
// Every package the game sold in the range, and what it made: how many sold and to how many
// players, gross, what a purchase of it averages, its share of revenue, and its trend against the
// range before. The table arrives ordered by gross, the question most asked; any column sorts it,
// and a row opens the package's detail.
//
// A package is its product id, as the game sent it: the API keeps no catalogue of names or prices.
const props = withDefaults(defineProps<{
  lines: readonly RevenueLine[]
  /** Whether the figures are dollars. */
  usd?: boolean
}>(), { usd: true })

const emit = defineEmits<{ open: [id: string] }>()

const sort = ref<TableSort | null>(null)

type Key = 'name' | 'sold' | 'buyers' | 'gross' | 'average' | 'share' | 'trend'

const total = computed(() => props.lines.reduce((sum, line) => sum + line.gross, 0) || 1)

const VALUES: Record<Key, (line: RevenueLine) => number | string> = {
  name: line => line.key,
  sold: line => line.purchases,
  buyers: line => line.buyers,
  gross: line => line.gross,
  average: line => line.gross / Math.max(line.purchases, 1),
  share: line => line.gross,
  trend: line => line.trend ?? -Infinity
}

const rows = computed(() => {
  const current = sort.value
  if (!current) return props.lines
  const read = VALUES[current.key as Key]
  return [...props.lines].sort((a, b) => {
    const left = read(a)
    const right = read(b)
    const compared = typeof left === 'string' ? left.localeCompare(String(right)) : left - (right as number)
    return applyDirection(compared, current.direction)
  })
})

const COLUMNS: { key: Key, label: string, align: 'start' | 'end' }[] = [
  { key: 'name', label: 'Package', align: 'start' },
  { key: 'sold', label: 'Sold', align: 'end' },
  { key: 'buyers', label: 'Buyers', align: 'end' },
  { key: 'gross', label: 'Gross', align: 'end' },
  { key: 'average', label: 'Average', align: 'end' },
  { key: 'share', label: 'Share', align: 'start' },
  { key: 'trend', label: 'Trend', align: 'end' }
]

const count = (value: number) => writeFigure(Math.round(value), { compact: value >= 100_000 })
</script>

<template>
  <!-- No package sold in the range: the no-data mark, at the height of a few rows. -->
  <div v-if="!lines.length" class="p-5">
    <NoDataPlaceholder :height="120" :seed="9" label="No data yet" />
  </div>
  <div v-else class="overflow-x-auto">
    <table class="ps-revenue-products">
      <thead>
        <tr>
          <th v-for="column in COLUMNS" :key="column.key" :class="column.align === 'end' ? 'text-right' : 'text-left'">
            <SortHeader
              :align="column.align"
              :direction="directionOf(sort, column.key)"
              :label="column.label"
              @toggle="sort = cycleSort(sort, column.key)"
            />
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="line in rows"
          :key="line.key"
          class="cursor-pointer"
          tabindex="0"
          @click="emit('open', line.key)"
          @keydown.enter="emit('open', line.key)"
        >
          <td>
            <span class="font-mono text-ink">{{ line.key }}</span>
          </td>
          <td class="font-mono">
            {{ count(line.purchases) }}
          </td>
          <td class="font-mono text-ink/70">
            {{ count(line.buyers) }}
          </td>
          <td class="font-mono">
            {{ writeMoney(line.gross, usd) }}
          </td>
          <td class="font-mono text-ink/70">
            {{ writeExactMoney(line.gross / Math.max(line.purchases, 1), usd) }}
          </td>
          <td>
            <span class="flex items-center gap-2">
              <span class="h-1.5 w-20 overflow-hidden rounded-full bg-ink/6">
                <span :style="{ width: `${line.gross / total * 100}%` }" class="block h-full rounded-full bg-primary" />
              </span>
              <span class="w-12 text-right font-mono text-ink/70">{{ formatShare(line.gross / total) }}</span>
            </span>
          </td>
          <td>
            <span
              :class="line.trend === null ? 'text-ink/40' : line.trend >= 0 ? 'text-success' : 'text-error'"
              class="font-mono text-[11px]"
            >{{ line.trend === null ? 'new' : formatChange(line.trend) }}</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.ps-revenue-products {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-revenue-products th {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  white-space: nowrap;
}

.ps-revenue-products td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 8px 12px;
  text-align: right;
  white-space: nowrap;
}

.ps-revenue-products td:first-child,
.ps-revenue-products td:nth-child(6) {
  text-align: left;
}

.ps-revenue-products :is(th, td):first-child {
  padding-left: 20px;
}

.ps-revenue-products :is(th, td):last-child {
  padding-right: 20px;
}

.ps-revenue-products tbody tr:last-child td {
  border-bottom: 0;
}

.ps-revenue-products tbody tr:hover td,
.ps-revenue-products tbody tr:focus-visible td {
  background: rgba(var(--v-theme-on-surface), 0.03);
}

.ps-revenue-products tbody tr:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: -2px;
}
</style>
