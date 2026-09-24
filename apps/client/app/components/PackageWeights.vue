<script setup lang="ts">
// How much each package weighs in the shop, two ways at once: its share of every purchase, and
// its share of every dollar. The two are rarely alike, and the difference is the point: a
// starter pack is bought often and earns little, a vault of gems is bought rarely and earns a
// great deal, and a shop is read by which packages carry the purchases and which the money.
//
// One bar across the top shows the mix by the measure chosen; below it, every package with both
// shares side by side and how far apart they are.
const props = defineProps<{
  packages: readonly PackageWeight[]
}>()

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
const ranked = computed(() => [...props.packages].sort((a, b) => shareOf(b) - shareOf(a)))
const coloured = computed(() => new Map(ranked.value.slice(0, 6).map((entry, index) => [entry.product.id, index + 1])))

const segments = computed(() => {
  const top = ranked.value.slice(0, 6).map(entry => ({ key: entry.product.id, label: entry.product.name, share: shareOf(entry), colour: toneColour(coloured.value.get(entry.product.id)!) }))
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

const largest = computed(() => Math.max(0.01, ...props.packages.flatMap(entry => [entry.purchaseShare, entry.revenueShare])))

/* The contrast in a sentence: the most bought, and the one that earns most for how rarely it is bought. */
const summary = computed(() => {
  if (props.packages.length < 2) return ''
  const mostBought = [...props.packages].sort((a, b) => b.purchaseShare - a.purchaseShare)[0]!
  const heaviest = [...props.packages].sort((a, b) => b.revenueShare / b.purchaseShare - a.revenueShare / a.purchaseShare)[0]!
  if (mostBought.product.id === heaviest.product.id) return ''
  return `${mostBought.product.name} is the most bought, ${percent(mostBought.purchaseShare)} of purchases, and brings ${percent(mostBought.revenueShare)} of revenue. ${heaviest.product.name} is ${percent(heaviest.purchaseShare)} of purchases and ${percent(heaviest.revenueShare)} of revenue.`
})
</script>

<template>
  <div class="flex flex-col">
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
          <tr v-for="entry in ranked" :key="entry.product.id" class="border-b border-line/70 last:border-b-0">
            <td class="px-5 py-2.5">
              <span class="flex min-w-0 items-center gap-2.5">
                <span :style="{ background: colourOf(entry.product.id) }" aria-hidden="true" class="size-2.5 flex-none rounded-[3px]" />
                <span class="flex min-w-0 flex-col">
                  <span class="truncate text-[13px] text-ink">{{ entry.product.name }}</span>
                  <span class="truncate text-[11px] text-ink/45">{{ KIND_LABELS[entry.product.kind] }} · {{ formatExactMoney(entry.product.price) }}</span>
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
              <span class="font-mono text-[10px] text-ink/40">{{ formatMoney(entry.gross) }}</span>
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
