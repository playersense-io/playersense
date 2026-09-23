<script setup lang="ts">
import type { ShareRow } from '~/components/ShareList.vue'

// Who pays, and how unevenly: how many players paid, how many for the first time, the
// spender tiers from minnows to top whales with each tier's share of payers against its
// share of revenue, how much of it the top few bring, new against returning payers day by
// day, and the package that turns a player into a payer.
//
// Tiers are by spend over the range, in the fixed bands of the spend buckets (project.md
// §29.11), so a player moves between them as they spend rather than as the bands move.
const props = defineProps<{
  sample: RevenueSample
  categories: readonly string[]
  loading?: boolean
}>()

const tiers = computed(() => SPENDER_TIERS.map(tier => ({
  ...tier,
  players: props.sample.payers.total * tier.payers,
  amount: props.sample.total.gross * tier.revenue
})))

const top = (count: number) => SPENDER_TIERS.slice(-count).reduce((sum, tier) => sum + tier.revenue, 0)

const facts = computed<Fact[]>(() => {
  const { payers, total } = props.sample
  return [
    { key: 'payers', label: 'Paying players', value: writeFigure(Math.round(payers.total)), mono: true, note: `${writeFigure(total.transactions / Math.max(payers.total, 1), { decimals: 1 })} purchases each` },
    { key: 'new', label: 'New payers', value: writeFigure(Math.round(payers.newPayers)), mono: true, note: `${formatShare(payers.newPayers / Math.max(payers.total, 1))} paid for the first time` },
    { key: 'top1', label: 'Top 1% of payers', value: formatShare(top(1)), mono: true, note: 'of revenue' },
    { key: 'top10', label: 'Top 10% of payers', value: formatShare(top(2)), mono: true, note: 'of revenue' }
  ]
})

const series = computed<MetricSeries[]>(() => [
  { name: 'Returning payers', data: props.sample.payers.daily.map(day => Math.round(day.returning)), type: 'bar', stack: 'payers', tone: 1 },
  { name: 'New payers', data: props.sample.payers.daily.map(day => Math.round(day.new)), type: 'bar', stack: 'payers', tone: 3 }
])

const first = computed<ShareRow[]>(() => props.sample.products
  .map(line => ({ key: line.product.id, label: line.product.name, value: line.firstPurchases, display: writeFigure(Math.round(line.firstPurchases)), note: formatShare(line.firstPurchases / Math.max(props.sample.payers.newPayers, 1)) }))
  .sort((a, b) => b.value - a.value))
</script>

<template>
  <div class="flex flex-col gap-5">
    <SectionCard description="The players who paid in the range, and how much of the money the biggest spenders bring." title="Payers">
      <FactGrid :columns="4" :facts="facts" label="Payers in figures" />
    </SectionCard>

    <SectionCard :padded="false" description="Payers by what they spent in the range. The gap between a tier's share of payers and its share of revenue is how the game is paid for." title="Spender tiers">
      <table class="ps-revenue-tiers">
        <thead>
          <tr>
            <th>Tier</th>
            <th>Spent</th>
            <th>Payers</th>
            <th class="w-2/5">
              Share of payers · of revenue
            </th>
            <th>Revenue</th>
            <th>Per payer</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="tier in tiers" :key="tier.tier">
            <td class="text-ink">
              {{ tier.tier }}
            </td>
            <td class="text-ink/60">
              {{ tier.band }}
            </td>
            <td class="font-mono">
              {{ writeFigure(Math.round(tier.players)) }}
            </td>
            <td>
              <span class="flex flex-col gap-1">
                <span class="flex items-center gap-2">
                  <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6"><span :style="{ width: `${tier.payers * 100}%` }" class="block h-full rounded-full bg-ink/35" /></span>
                  <span class="w-12 text-right font-mono text-[11px] text-ink/55">{{ formatShare(tier.payers) }}</span>
                </span>
                <span class="flex items-center gap-2">
                  <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6"><span :style="{ width: `${tier.revenue * 100}%` }" class="block h-full rounded-full bg-primary" /></span>
                  <span class="w-12 text-right font-mono text-[11px] text-ink">{{ formatShare(tier.revenue) }}</span>
                </span>
              </span>
            </td>
            <td class="font-mono">
              {{ formatMoney(tier.amount) }}
            </td>
            <td class="font-mono text-ink/70">
              {{ formatExactMoney(tier.amount / Math.max(tier.players, 1)) }}
            </td>
          </tr>
        </tbody>
      </table>
      <p class="m-0 flex items-center gap-4 border-t border-line px-5 py-3 text-[11px] text-ink/55">
        <span class="flex items-center gap-1.5"><span class="h-1.5 w-4 rounded-full bg-ink/35" /> share of payers</span>
        <span class="flex items-center gap-1.5"><span class="h-1.5 w-4 rounded-full bg-primary" /> share of revenue</span>
      </p>
    </SectionCard>

    <div class="grid gap-5 lg:grid-cols-[3fr_2fr]">
      <MetricCard
        :categories="categories"
        :decimals="0"
        :loading="loading"
        :series="series"
        category-label="Day"
        description="Players who paid each day: those paying for the first time, over those who had paid before."
        title="New and returning payers"
        type="bar"
      />
      <SectionCard description="The package bought first by the players who became payers in the range." title="What makes a payer">
        <ShareList :rows="first" label="First purchases by package" tone="success" />
      </SectionCard>
    </div>
  </div>
</template>

<style scoped>
.ps-revenue-tiers {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-revenue-tiers th {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  text-align: left;
  white-space: nowrap;
}

.ps-revenue-tiers td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 10px 12px;
  white-space: nowrap;
}

.ps-revenue-tiers :is(th, td):first-child {
  padding-left: 20px;
}

.ps-revenue-tiers :is(th, td):last-child {
  padding-right: 20px;
}

.ps-revenue-tiers tbody tr:last-child td {
  border-bottom: 0;
}
</style>
