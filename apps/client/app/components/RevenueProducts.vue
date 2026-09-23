<script setup lang="ts">
// Every package in the shop, and what it made: how many sold and to how many players,
// gross and net, how often it was refunded, its share of revenue, how many players it
// turned into payers, and its trend against the range before, with its daily line. The
// table arrives ordered by gross, the question most asked; any column sorts it, and a row
// opens the package's detail.
//
// A refund rate above 3% is marked: the rate at which a store starts to look at a
// developer's refunds, and usually a package that promises more than it gives.
const props = defineProps<{
  lines: readonly ProductLine[]
  /** Gross or net: which of the two the share and the sort by revenue read. */
  basis: 'gross' | 'net'
}>()

const emit = defineEmits<{ open: [id: string] }>()

const sort = ref<TableSort | null>(null)

type Key = 'name' | 'price' | 'sold' | 'buyers' | 'gross' | 'net' | 'refunds' | 'share' | 'first' | 'trend'

const total = computed(() => props.lines.reduce((sum, line) => sum + line[props.basis], 0) || 1)

const trendOf = (line: ProductLine) => (line.previousGross ? (line.gross - line.previousGross) / line.previousGross : null)
const refundRate = (line: ProductLine) => (line.transactions ? line.refunded / line.transactions : 0)

const VALUES: Record<Key, (line: ProductLine) => number | string> = {
  name: line => line.product.name,
  price: line => line.product.price,
  sold: line => line.transactions,
  buyers: line => line.buyers,
  gross: line => line.gross,
  net: line => line.net,
  refunds: refundRate,
  share: line => line[props.basis],
  first: line => line.firstPurchases,
  trend: line => trendOf(line) ?? -Infinity
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
  { key: 'price', label: 'Price', align: 'end' },
  { key: 'sold', label: 'Sold', align: 'end' },
  { key: 'buyers', label: 'Buyers', align: 'end' },
  { key: 'gross', label: 'Gross', align: 'end' },
  { key: 'net', label: 'Net', align: 'end' },
  { key: 'refunds', label: 'Refunded', align: 'end' },
  { key: 'share', label: 'Share', align: 'start' },
  { key: 'first', label: 'First buys', align: 'end' },
  { key: 'trend', label: 'Trend', align: 'end' }
]

const count = (value: number) => writeFigure(Math.round(value), { compact: value >= 100_000 })
</script>

<template>
  <div class="overflow-x-auto">
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
          :key="line.product.id"
          class="cursor-pointer"
          tabindex="0"
          @click="emit('open', line.product.id)"
          @keydown.enter="emit('open', line.product.id)"
        >
          <td>
            <span class="flex flex-col">
              <span class="flex items-center gap-2">
                <span class="text-ink">{{ line.product.name }}</span>
                <span class="rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 text-[10px] text-ink/60">{{ KIND_LABELS[line.product.kind] }}</span>
              </span>
              <span class="font-mono text-[11px] text-ink/45">{{ line.product.id }}</span>
            </span>
          </td>
          <td class="font-mono">
            {{ formatExactMoney(line.product.price) }}
          </td>
          <td class="font-mono">
            {{ count(line.transactions) }}
          </td>
          <td class="font-mono text-ink/70">
            {{ count(line.buyers) }}
          </td>
          <td class="font-mono">
            {{ formatMoney(line.gross) }}
          </td>
          <td class="font-mono text-ink/70">
            {{ formatMoney(line.net) }}
          </td>
          <td class="font-mono">
            <span :class="refundRate(line) > 0.03 ? 'bg-error/12 text-error' : 'text-ink/70'" class="rounded-(--ps-radius-x-small) px-1.5 py-0.5">
              {{ formatShare(refundRate(line)) }}
            </span>
          </td>
          <td>
            <span class="flex items-center gap-2">
              <span class="h-1.5 w-20 overflow-hidden rounded-full bg-ink/6">
                <span :style="{ width: `${line[basis] / total * 100}%` }" class="block h-full rounded-full bg-primary" />
              </span>
              <span class="w-12 text-right font-mono text-ink/70">{{ formatShare(line[basis] / total) }}</span>
            </span>
          </td>
          <td class="font-mono text-ink/70">
            {{ count(line.firstPurchases) }}
          </td>
          <td>
            <span class="flex items-center justify-end gap-2">
              <span class="w-16">
                <Sparkline :height="20" :values="line.daily" />
              </span>
              <span
                :class="trendOf(line) === null ? 'text-ink/40' : trendOf(line)! >= 0 ? 'text-success' : 'text-error'"
                class="w-14 text-right font-mono text-[11px]"
              >{{ trendOf(line) === null ? 'new' : formatChange(trendOf(line)!) }}</span>
            </span>
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
.ps-revenue-products td:nth-child(8) {
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
