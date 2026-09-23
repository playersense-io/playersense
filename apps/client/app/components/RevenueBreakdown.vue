<script setup lang="ts">
// Revenue split by where it came from — a country, a store — as a table with one figure to
// a column, so each can be read down and compared: the revenue with a bar against the
// largest, its share, how it moved on the range before, the payers there and what each
// spent, the purchases and their average, how much was refunded, and for stores what the
// store kept. Ranked by revenue; any column sorts it.
//
// A refund rate above 3% is marked, as in the packages table: where chargebacks and card
// fraud cluster, the money made there is worth less than it looks.
const props = withDefaults(defineProps<{
  lines: readonly BreakdownLine[]
  basis: 'gross' | 'net'
  /** What a row is: "Country", "Store". */
  noun: string
  /** A row's name and mark. */
  describe: (line: BreakdownLine) => { label: string, mark?: SegmentMarkValue }
  /** Whether to show what each row's store kept. */
  fees?: boolean
  /** Rows shown before "Show all". Every row when absent. */
  limit?: number
}>(), {
  fees: false,
  limit: undefined
})

type Key = 'name' | 'revenue' | 'trend' | 'payers' | 'arppu' | 'purchases' | 'average' | 'refunds' | 'fees'

const sort = ref<TableSort | null>(null)
const showAll = ref(false)

const total = computed(() => props.lines.reduce((sum, line) => sum + line[props.basis], 0) || 1)
const largest = computed(() => Math.max(0, ...props.lines.map(line => line[props.basis])) || 1)

const trendOf = (line: BreakdownLine) => (line.previousGross ? (line.gross - line.previousGross) / line.previousGross : null)
const refundRate = (line: BreakdownLine) => (line.transactions ? line.refunded / line.transactions : 0)

const VALUES: Record<Key, (line: BreakdownLine) => number | string> = {
  name: line => props.describe(line).label,
  revenue: line => line[props.basis],
  trend: line => trendOf(line) ?? -Infinity,
  payers: line => line.payers,
  arppu: line => line.gross / Math.max(line.payers, 1),
  purchases: line => line.transactions,
  average: line => line.gross / Math.max(line.transactions, 1),
  refunds: refundRate,
  fees: line => line.fees
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
  { key: 'revenue' as const, label: props.basis === 'gross' ? 'Gross' : 'Net', align: 'start' as const },
  { key: 'trend' as const, label: 'Trend', align: 'end' as const },
  { key: 'payers' as const, label: 'Payers', align: 'end' as const },
  { key: 'arppu' as const, label: 'Per payer', align: 'end' as const },
  { key: 'purchases' as const, label: 'Purchases', align: 'end' as const },
  { key: 'average' as const, label: 'Average', align: 'end' as const },
  { key: 'refunds' as const, label: 'Refunded', align: 'end' as const },
  ...(props.fees ? [{ key: 'fees' as const, label: 'Store fees', align: 'end' as const }] : [])
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
                <span class="w-16 text-right font-mono text-ink">{{ formatMoney(line[basis]) }}</span>
                <span class="h-1.5 min-w-16 flex-1 overflow-hidden rounded-full bg-ink/6">
                  <span :style="{ width: `${line[basis] / largest * 100}%` }" class="block h-full rounded-full bg-primary" />
                </span>
                <span class="w-12 text-right font-mono text-[11px] text-ink/55">{{ formatShare(line[basis] / total) }}</span>
              </span>
            </td>
            <td class="font-mono">
              <span
                :class="trendOf(line) === null ? 'text-ink/40' : trendOf(line)! >= 0 ? 'text-success' : 'text-error'"
              >{{ trendOf(line) === null ? 'new' : formatChange(trendOf(line)!) }}</span>
            </td>
            <td class="font-mono text-ink/80">
              {{ count(line.payers) }}
            </td>
            <td class="font-mono">
              {{ formatExactMoney(line.gross / Math.max(line.payers, 1)) }}
            </td>
            <td class="font-mono text-ink/80">
              {{ count(line.transactions) }}
            </td>
            <td class="font-mono text-ink/80">
              {{ formatExactMoney(line.gross / Math.max(line.transactions, 1)) }}
            </td>
            <td class="font-mono">
              <span :class="refundRate(line) > 0.03 ? 'bg-error/12 text-error' : 'text-ink/70'" class="rounded-(--ps-radius-x-small) px-1.5 py-0.5">
                {{ formatShare(refundRate(line)) }}
              </span>
            </td>
            <td v-if="fees" class="font-mono text-ink/70">
              {{ formatMoney(line.fees) }}
            </td>
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
