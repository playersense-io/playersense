<script setup lang="ts">
// Revenue split by where it came from (a country, a store) as a table with one figure to a
// column, so each can be read down and compared: the revenue with a bar against the largest,
// its share, how it moved on the range before, the buyers there and what each spent, the
// purchases and their average, and for stores the share each keeps and what was left. Ranked by
// revenue; any column sorts it.
const props = withDefaults(defineProps<{
  lines: readonly RevenueLine[]
  /** What a row is: "Country", "Store". */
  noun: string
  /** A row's name and mark. */
  describe: (line: RevenueLine) => { label: string, mark?: SegmentMarkValue }
  /** Whether to show what each row's store kept, and what was left. */
  fees?: boolean
  /** Whether the figures are dollars. */
  usd?: boolean
  /** Rows shown before "Show all". Every row when absent. */
  limit?: number
}>(), {
  fees: false,
  usd: true,
  limit: undefined
})

type Key = 'name' | 'revenue' | 'trend' | 'buyers' | 'arppu' | 'purchases' | 'average' | 'fee' | 'net'

const sort = ref<TableSort | null>(null)
const showAll = ref(false)

const total = computed(() => props.lines.reduce((sum, line) => sum + line.gross, 0) || 1)
const largest = computed(() => Math.max(0, ...props.lines.map(line => line.gross)) || 1)

const VALUES: Record<Key, (line: RevenueLine) => number | string> = {
  name: line => props.describe(line).label,
  revenue: line => line.gross,
  trend: line => line.trend ?? -Infinity,
  buyers: line => line.buyers,
  arppu: line => line.gross / Math.max(line.buyers, 1),
  purchases: line => line.purchases,
  average: line => line.gross / Math.max(line.purchases, 1),
  fee: line => line.feePercent ?? 0,
  net: line => line.net ?? 0
}

const ranked = computed(() => {
  const current = sort.value
  const lines = current
    ? [...props.lines].sort((a, b) => {
        const read = VALUES[current.key as Key]
        const left = read(a)
        const right = read(b)
        return applyDirection(typeof left === 'string' ? left.localeCompare(String(right)) : left - (right as number), current.direction)
      })
    : props.lines
  return props.limit && !showAll.value ? lines.slice(0, props.limit) : lines
})

const columns = computed(() => [
  { key: 'name' as const, label: props.noun, align: 'start' as const },
  { key: 'revenue' as const, label: 'Gross', align: 'start' as const },
  { key: 'trend' as const, label: 'Trend', align: 'end' as const },
  { key: 'buyers' as const, label: 'Buyers', align: 'end' as const },
  { key: 'arppu' as const, label: 'Per buyer', align: 'end' as const },
  { key: 'purchases' as const, label: 'Purchases', align: 'end' as const },
  { key: 'average' as const, label: 'Average', align: 'end' as const },
  ...(props.fees
    ? [
        { key: 'fee' as const, label: 'Store keeps', align: 'end' as const },
        { key: 'net' as const, label: 'Net, estimated', align: 'end' as const }
      ]
    : [])
])

const count = (value: number) => writeFigure(Math.round(value), { compact: value >= 100_000 })
</script>

<template>
  <div>
    <div class="overflow-x-auto">
      <table class="ps-revenue-breakdown">
        <thead>
          <tr>
            <th class="w-8">
              #
            </th>
            <th v-for="column in columns" :key="column.key" :class="column.align === 'end' ? 'text-right' : 'text-left'">
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
          <tr v-for="(line, index) in ranked" :key="line.key">
            <td class="font-mono text-ink/40">
              {{ index + 1 }}
            </td>
            <td>
              <span class="flex items-center gap-2 text-ink">
                <SegmentMark v-if="describe(line).mark" :mark="describe(line).mark!" />
                <span class="truncate">{{ describe(line).label }}</span>
              </span>
            </td>
            <td class="ps-revenue-breakdown__revenue">
              <span class="flex items-center gap-3">
                <span class="w-16 text-right font-mono text-ink">{{ writeMoney(line.gross, usd) }}</span>
                <span class="h-1.5 min-w-16 flex-1 overflow-hidden rounded-full bg-ink/6">
                  <span :style="{ width: `${line.gross / largest * 100}%` }" class="block h-full rounded-full bg-primary" />
                </span>
                <span class="w-12 text-right font-mono text-[11px] text-ink/55">{{ formatShare(line.gross / total) }}</span>
              </span>
            </td>
            <td class="font-mono">
              <span
                :class="line.trend === null ? 'text-ink/40' : line.trend >= 0 ? 'text-success' : 'text-error'"
              >{{ line.trend === null ? 'new' : formatChange(line.trend) }}</span>
            </td>
            <td class="font-mono text-ink/80">
              {{ count(line.buyers) }}
            </td>
            <td class="font-mono">
              {{ writeExactMoney(line.gross / Math.max(line.buyers, 1), usd) }}
            </td>
            <td class="font-mono text-ink/80">
              {{ count(line.purchases) }}
            </td>
            <td class="font-mono text-ink/80">
              {{ writeExactMoney(line.gross / Math.max(line.purchases, 1), usd) }}
            </td>
            <template v-if="fees">
              <td class="font-mono text-ink/70">
                {{ line.feePercent === undefined ? '' : `${line.feePercent}%` }}
              </td>
              <td class="font-mono text-ink/70">
                {{ line.net === undefined ? '' : writeMoney(line.net, usd) }}
              </td>
            </template>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="limit && lines.length > limit" class="flex items-center justify-between border-t border-line px-5 py-2.5 text-xs text-ink/55">
      <span>{{ showAll ? `All ${lines.length}` : `The top ${limit} of ${lines.length}` }}</span>
      <button class="font-medium text-ink/70 hover:text-ink" type="button" @click="showAll = !showAll">
        {{ showAll ? `Top ${limit}` : `Show all ${lines.length}` }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.ps-revenue-breakdown {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-revenue-breakdown th {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  white-space: nowrap;
}

.ps-revenue-breakdown td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 9px 12px;
  text-align: right;
  white-space: nowrap;
}

/* The rank, the name and the revenue read from the left; the rest from the right. */
.ps-revenue-breakdown td:nth-child(-n + 3) {
  text-align: left;
}

/* The revenue column takes the room left over, so the bars are long enough to compare. */
.ps-revenue-breakdown__revenue {
  width: 34%;
}

.ps-revenue-breakdown :is(th, td):first-child {
  padding-left: 20px;
}

.ps-revenue-breakdown :is(th, td):last-child {
  padding-right: 20px;
}

.ps-revenue-breakdown tbody tr:last-child td {
  border-bottom: 0;
}

.ps-revenue-breakdown tbody tr:hover td {
  background: rgba(var(--v-theme-on-surface), 0.03);
}
</style>
