<script setup lang="ts">
// Who pays, and how unevenly: how many players paid, how many for the first time, and the spender
// tiers from the smallest spenders to the largest, each tier's share of payers against its share of
// revenue, and first-time payers day by day.
//
// Tiers are by what a payer has spent in their whole life, in the reported currency, in fixed
// bands, so a player moves between them as they spend rather than as the bands move. Neither the
// tiers nor the new payers follow the filters: both are about the payer, not about one product or
// store. A tier or a day with too few payers to disclose is left blank.
const props = defineProps<{
  revenue: RevenueRecord | null
  categories: readonly string[]
  loading?: boolean
}>()

const newPayers = computed(() => (props.revenue?.new_payers ?? []).reduce((sum, day) => sum + (day.payers ?? 0), 0))

const facts = computed<Fact[]>(() => {
  const totals = props.revenue?.totals
  if (!totals) return []
  const buyers = totals.buyers ?? null
  return [
    { key: 'payers', label: 'Paying players', value: buyers === null ? 'Too few to show' : writeFigure(buyers), mono: true, note: buyers ? `${writeFigure(totals.purchases / buyers, { decimals: 1 })} purchases each` : undefined },
    { key: 'new', label: 'New payers', value: writeFigure(newPayers.value), mono: true, note: buyers ? `${formatShare(newPayers.value / Math.max(buyers, 1))} paid for the first time` : 'Paid for the first time' },
    { key: 'previous', label: 'The range before', value: totals.previous_buyers == null ? 'Too few to show' : writeFigure(totals.previous_buyers), mono: true, note: 'Paying players' }
  ]
})

const band = (from: number, to?: number) => {
  const write = (micros: number) => writeFigure(micros / 1_000_000)
  return to ? `${write(from)} to ${write(to)}` : `${write(from)} and more`
}

const tiers = computed(() => {
  const rows = props.revenue?.spender_tiers ?? []
  const payers = rows.reduce((sum, tier) => sum + (tier.payers ?? 0), 0) || 1
  const revenue = rows.reduce((sum, tier) => sum + (tier.revenue_micros ?? 0), 0) || 1
  return rows.map(tier => ({
    key: tier.from_micros,
    band: band(tier.from_micros, tier.to_micros),
    payers: tier.payers ?? null,
    revenue: tier.revenue_micros == null ? null : tier.revenue_micros / 1_000_000,
    payerShare: (tier.payers ?? 0) / payers,
    revenueShare: (tier.revenue_micros ?? 0) / revenue
  }))
})

const series = computed<MetricSeries[]>(() => [
  { name: 'New payers', data: (props.revenue?.new_payers ?? []).map(day => day.payers ?? 0), type: 'bar', tone: 3 }
])
</script>

<template>
  <div class="flex flex-col gap-5">
    <SectionCard description="The players who paid in the range." title="Payers">
      <FactGrid :columns="3" :facts="facts" label="Payers in figures" />
    </SectionCard>

    <SectionCard :padded="false" description="Everybody who bought in the range, by what they have spent in their life, as reported. The gap between a tier's share of payers and its share of revenue is how the game is paid for." title="Spender tiers">
      <table class="ps-revenue-tiers">
        <thead>
          <tr>
            <th>Spent in their life</th>
            <th>Payers</th>
            <th class="w-2/5">
              Share of payers · of revenue
            </th>
            <th>Revenue</th>
            <th>Per payer</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="tier in tiers" :key="tier.key">
            <td class="text-ink">
              {{ tier.band }}
            </td>
            <td class="font-mono">
              {{ tier.payers === null ? 'Too few' : writeFigure(tier.payers) }}
            </td>
            <td>
              <span class="flex flex-col gap-1">
                <span class="flex items-center gap-2">
                  <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6"><span :style="{ width: `${tier.payerShare * 100}%` }" class="block h-full rounded-full bg-ink/35" /></span>
                  <span class="w-12 text-right font-mono text-[11px] text-ink/55">{{ formatShare(tier.payerShare) }}</span>
                </span>
                <span class="flex items-center gap-2">
                  <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6"><span :style="{ width: `${tier.revenueShare * 100}%` }" class="block h-full rounded-full bg-primary" /></span>
                  <span class="w-12 text-right font-mono text-[11px] text-ink">{{ formatShare(tier.revenueShare) }}</span>
                </span>
              </span>
            </td>
            <td class="font-mono">
              {{ tier.revenue === null ? '' : writeMoney(tier.revenue, false) }}
            </td>
            <td class="font-mono text-ink/70">
              {{ tier.revenue === null || !tier.payers ? '' : writeExactMoney(tier.revenue / tier.payers, false) }}
            </td>
          </tr>
        </tbody>
      </table>
      <p class="m-0 flex items-center gap-4 border-t border-line px-5 py-3 text-[11px] text-ink/55">
        <span class="flex items-center gap-1.5"><span class="h-1.5 w-4 rounded-full bg-ink/35" /> share of payers</span>
        <span class="flex items-center gap-1.5"><span class="h-1.5 w-4 rounded-full bg-primary" /> share of revenue</span>
      </p>
    </SectionCard>

    <MetricCard
      :categories="categories"
      :decimals="0"
      :loading="loading"
      :series="series"
      category-label="Day"
      description="Players who paid for the first time ever, each day. A day with too few to disclose shows none."
      title="New payers"
      type="bar"
    />
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
