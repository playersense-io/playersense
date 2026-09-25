<script setup lang="ts">
// How much each package weighs in the shop, two ways at once: its share of every purchase, and
// its share of every dollar. The two are rarely alike, and the difference is the point: a
// starter pack is bought often and earns little, a vault of gems is bought rarely and earns a
// great deal, and a shop is read by which packages carry the purchases and which the money.
//
// One bar across the top shows the mix by the measure chosen; below it, every package with both
// shares side by side and how far apart they are. A package is its product id, as the game sent it.
const props = withDefaults(defineProps<{
  lines: readonly RevenueLine[]
  /** Whether the figures are dollars. */
  usd?: boolean
  /** While the figures are on their way: the sentence, the bar and the rows as bones. */
  loading?: boolean
}>(), { usd: true, loading: false })

interface PackageWeight {
  id: string
  purchases: number
  gross: number
  purchaseShare: number
  revenueShare: number
}

const packages = computed<PackageWeight[]>(() => {
  const purchases = props.lines.reduce((sum, line) => sum + line.purchases, 0) || 1
  const gross = props.lines.reduce((sum, line) => sum + line.gross, 0) || 1
  return props.lines.map(line => ({ id: line.key, purchases: line.purchases, gross: line.gross, purchaseShare: line.purchases / purchases, revenueShare: line.gross / gross }))
})

const MEASURES: SegmentedOption[] = [
  { value: 'purchases', label: 'By purchases' },
  { value: 'revenue', label: 'By revenue' }
]
const measure = ref<'purchases' | 'revenue'>('revenue')
const measureModel = computed({
  get: () => measure.value as string,
  set: (value: string) => {
    measure.value = value === 'purchases' ? 'purchases' : 'revenue'
  }
})

const shareOf = (entry: PackageWeight) => (measure.value === 'purchases' ? entry.purchaseShare : entry.revenueShare)

/* The biggest six by the measure chosen each get a colour; the rest share one, as "Others". */
const ranked = computed(() => [...packages.value].sort((a, b) => shareOf(b) - shareOf(a)))
const coloured = computed(() => new Map(ranked.value.slice(0, 6).map((entry, index) => [entry.id, index + 1])))

const segments = computed(() => {
  const top = ranked.value.slice(0, 6).map(entry => ({ key: entry.id, label: entry.id, share: shareOf(entry), colour: toneColour(coloured.value.get(entry.id)!) }))
  const rest = ranked.value.slice(6).reduce((sum, entry) => sum + shareOf(entry), 0)
  return rest > 0.001 ? [...top, { key: 'others', label: 'Others', share: rest, colour: 'rgba(var(--v-theme-on-surface), 0.2)' }] : top
})

const colourOf = (id: string) => (coloured.value.has(id) ? toneColour(coloured.value.get(id)!) : 'rgba(var(--v-theme-on-surface), 0.2)')

const percent = (share: number) => `${(share * 100).toFixed(share < 0.1 ? 1 : 0)}%`

/* How a package's money compares with how often it is bought: "earns 9× its share". */
function pull(entry: PackageWeight) {
  const ratio = entry.revenueShare / Math.max(entry.purchaseShare, 1e-9)
  if (ratio >= 1.25) return { text: `${ratio.toFixed(1)}× its share of purchases`, tone: 'text-success' }
  if (ratio <= 0.8) return { text: `${ratio.toFixed(1)}× its share of purchases`, tone: 'text-ink/50' }
  return { text: 'About its share', tone: 'text-ink/50' }
}

const largest = computed(() => Math.max(0.01, ...packages.value.flatMap(entry => [entry.purchaseShare, entry.revenueShare])))

/* The contrast in a sentence: the most bought, and the one that earns most for how rarely it is bought. */
const summary = computed(() => {
  if (packages.value.length < 2) return ''
  const mostBought = [...packages.value].sort((a, b) => b.purchaseShare - a.purchaseShare)[0]!
  const heaviest = [...packages.value].sort((a, b) => b.revenueShare / Math.max(b.purchaseShare, 1e-9) - a.revenueShare / Math.max(a.purchaseShare, 1e-9))[0]!
  if (mostBought.id === heaviest.id) return ''
  return `${mostBought.id} is the most bought, ${percent(mostBought.purchaseShare)} of purchases, and brings ${percent(mostBought.revenueShare)} of revenue. ${heaviest.id} is ${percent(heaviest.purchaseShare)} of purchases and ${percent(heaviest.revenueShare)} of revenue.`
})
</script>

<template>
  <!-- Loading: every part in its own place and at its own size, so the card does not jump. -->
  <div
    v-if="loading"
    aria-busy="true"
    aria-label="Loading package weights"
    class="flex flex-col"
  >
    <div aria-hidden="true" class="flex flex-col gap-3 border-b border-line px-5 py-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <span class="flex flex-col gap-1.5">
          <span class="ps-bone h-3 w-[28rem] max-w-full" />
          <span class="ps-bone h-3 w-64" />
        </span>
        <SegmentedControl
          v-model="measureModel"
          :options="MEASURES"
          disabled
          label="Weigh packages"
        />
      </div>
      <span class="ps-bone h-3 w-full rounded-full!" />
      <span class="flex flex-wrap gap-x-4 gap-y-1">
        <span
          v-for="index in 6"
          :key="index"
          :style="{ width: `${4 + (index * 3) % 4}rem` }"
          class="ps-bone h-2.5"
        />
      </span>
    </div>
    <ul aria-hidden="true" class="m-0 flex list-none flex-col p-0">
      <li v-for="index in 6" :key="index" class="grid grid-cols-[minmax(0,1fr)_14rem_14rem_10rem] items-center gap-3 border-b border-line/70 px-5 py-3.5 last:border-b-0">
        <span class="flex items-center gap-2.5">
          <span class="ps-bone size-2.5" />
          <span class="flex flex-col gap-1.5">
            <span :style="{ width: `${6 + (index * 5) % 5}rem` }" class="ps-bone h-3" />
            <span class="ps-bone h-2.5 w-24" />
          </span>
        </span>
        <span :style="{ width: `${Math.max(20, 90 - index * 13)}%` }" class="ps-bone h-1.5 rounded-full!" />
        <span :style="{ width: `${Math.max(20, 80 - index * 9)}%` }" class="ps-bone h-1.5 rounded-full!" />
        <span class="ps-bone ml-auto h-3 w-28" />
      </li>
    </ul>
  </div>

  <div v-else class="flex flex-col">
    <div class="flex flex-col gap-3 border-b border-line px-5 py-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="m-0 max-w-2xl text-xs leading-5 text-ink/65">
          {{ summary }}
        </p>
        <SegmentedControl v-model="measureModel" :options="MEASURES" label="Weigh packages" />
      </div>

      <!-- The mix, as one bar. -->
      <div class="flex h-3 w-full overflow-hidden rounded-full bg-ink/6" role="img" :aria-label="`Packages ${measure === 'purchases' ? 'by purchases' : 'by revenue'}: ${segments.map(segment => `${segment.label} ${percent(segment.share)}`).join(', ')}`">
        <span
          v-for="segment in segments"
          :key="segment.key"
          :style="{ width: `${segment.share * 100}%`, background: segment.colour }"
          class="h-full border-r-2 border-surface last:border-r-0"
        />
      </div>
      <ul class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0">
        <li v-for="segment in segments" :key="segment.key" class="flex items-center gap-1.5 text-[11px] text-ink/70">
          <span :style="{ background: segment.colour }" aria-hidden="true" class="size-2 rounded-[2px]" />
          {{ segment.label }}
          <span class="font-mono text-ink/45">{{ percent(segment.share) }}</span>
        </li>
      </ul>
    </div>

    <!-- Every package, both weights side by side. -->
    <div class="overflow-x-auto">
      <table class="w-full min-w-[720px] border-collapse text-left">
        <thead>
          <tr class="border-b border-line text-[11px] text-ink/45">
            <th class="px-5 py-2.5 font-normal">
              Package
            </th>
            <th class="w-56 px-3 py-2.5 font-normal">
              Share of purchases
            </th>
            <th class="w-56 px-3 py-2.5 font-normal">
              Share of revenue
            </th>
            <th class="px-5 py-2.5 text-right font-normal">
              Weight
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in ranked" :key="entry.id" class="border-b border-line/70 last:border-b-0">
            <td class="px-5 py-2.5">
              <span class="flex min-w-0 items-center gap-2.5">
                <span :style="{ background: colourOf(entry.id) }" aria-hidden="true" class="size-2.5 flex-none rounded-[3px]" />
                <span class="flex min-w-0 flex-col">
                  <span class="truncate font-mono text-[13px] text-ink">{{ entry.id }}</span>
                  <span class="truncate text-[11px] text-ink/45">{{ writeExactMoney(entry.gross / Math.max(entry.purchases, 1), usd) }} a purchase</span>
                </span>
              </span>
            </td>
            <td class="px-3 py-2.5">
              <span class="flex items-center gap-2">
                <span class="relative h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6">
                  <span :style="{ width: `${entry.purchaseShare / largest * 100}%` }" class="absolute inset-y-0 left-0 rounded-full bg-ink/40" />
                </span>
                <span class="w-12 flex-none text-right font-mono text-xs text-ink/80">{{ percent(entry.purchaseShare) }}</span>
              </span>
              <span class="font-mono text-[10px] text-ink/40">{{ writeFigure(Math.round(entry.purchases), { compact: true }) }} purchases</span>
            </td>
            <td class="px-3 py-2.5">
              <span class="flex items-center gap-2">
                <span class="relative h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6">
                  <span :style="{ width: `${entry.revenueShare / largest * 100}%` }" class="absolute inset-y-0 left-0 rounded-full bg-primary" />
                </span>
                <span class="w-12 flex-none text-right font-mono text-xs text-ink">{{ percent(entry.revenueShare) }}</span>
              </span>
              <span class="font-mono text-[10px] text-ink/40">{{ writeMoney(entry.gross, usd) }}</span>
            </td>
            <td :class="pull(entry).tone" class="px-5 py-2.5 text-right text-[11px] whitespace-nowrap">
              {{ pull(entry).text }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
