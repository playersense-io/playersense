<script setup lang="ts">
import type { ShareRow } from '~/components/ShareList.vue'

// The money that came back: how much, how often, and why — a customer's request, an
// accidental tap, a chargeback, fraud — which packages are refunded most, day by day, and
// the latest refunds one by one, each with the transaction the store knows it by. A
// refund rate that climbs is a pack that disappoints, or a card thief who found the game.
const props = defineProps<{
  sample: RevenueSample
  categories: readonly string[]
  loading?: boolean
}>()

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

function daysBefore(refund: { purchasedAt: number, refundedAt: number }) {
  const days = Math.round((refund.refundedAt - refund.purchasedAt) / 86_400_000)
  return days === 1 ? '1 day before' : `${days} days before`
}

const chargebacks = computed(() => props.sample.refunds.reasons.find(entry => entry.reason === 'Chargeback'))

const facts = computed<Fact[]>(() => {
  const { total, previous } = props.sample
  const rate = total.transactions ? total.refunded / total.transactions : 0
  const was = previous.transactions ? previous.refunded / previous.transactions : 0
  return [
    { key: 'amount', label: 'Refunded', value: formatExactMoney(total.refunds), mono: true, note: `${formatShare(total.gross ? total.refunds / total.gross : 0)} of gross` },
    { key: 'rate', label: 'Refund rate', value: formatShare(rate), mono: true, note: `${formatShare(was)} the range before` },
    { key: 'chargebacks', label: 'Chargebacks', value: writeFigure(Math.round(chargebacks.value?.count ?? 0)), mono: true, note: `${formatExactMoney(chargebacks.value?.amount ?? 0)}, and a fee each` },
    { key: 'days', label: 'Median days to refund', value: `${props.sample.refunds.medianDays} days`, mono: true, note: 'From purchase to refund' }
  ]
})

const series = computed<MetricSeries[]>(() => [{
  name: 'Refunded',
  data: props.sample.daily.map(day => Math.round(day.refunds * 100) / 100),
  type: 'bar',
  tone: 2
}])

const reasons = computed<ShareRow[]>(() => props.sample.refunds.reasons
  .map(entry => ({ key: entry.reason, label: entry.reason, value: entry.amount, display: formatMoney(entry.amount), note: `${writeFigure(Math.round(entry.count))} refunds` }))
  .sort((a, b) => b.value - a.value))

const byPackage = computed<ShareRow[]>(() => props.sample.products
  .map(line => ({ key: line.product.id, label: line.product.name, value: line.transactions ? line.refunded / line.transactions : 0, display: formatShare(line.transactions ? line.refunded / line.transactions : 0), note: formatMoney(line.refunds) }))
  .sort((a, b) => b.value - a.value))
</script>

<template>
  <div class="flex flex-col gap-5">
    <SectionCard description="Refunds granted by the stores in the range, and chargebacks raised through a player's bank." title="Refunds">
      <FactGrid :columns="4" :facts="facts" label="Refunds in figures" />
    </SectionCard>

    <MetricCard
      :categories="categories"
      :decimals="2"
      :loading="loading"
      :series="series"
      category-label="Day"
      description="Revenue refunded each day, taken off net revenue on the day the store refunded it."
      prefix="$"
      title="Refunded by day"
      type="bar"
    />

    <div class="grid gap-5 lg:grid-cols-2">
      <SectionCard description="What the store or the bank gave as the reason." title="Why">
        <ShareList :rows="reasons" label="Refunds by reason" tone="error" />
      </SectionCard>
      <SectionCard description="The share of each package's sales refunded. Above 3% is worth a look." title="By package">
        <ShareList :rows="byPackage" label="Refund rate by package" tone="warning" />
      </SectionCard>
    </div>

    <SectionCard :padded="false" description="The latest refunds, with the transaction the store knows each by." title="Latest refunds">
      <div class="overflow-x-auto">
        <table class="ps-revenue-refunds">
          <thead>
            <tr>
              <th>Refunded</th>
              <th>Package</th>
              <th>Store</th>
              <th>Country</th>
              <th>Amount</th>
              <th>Bought</th>
              <th>Reason</th>
              <th>Transaction</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="refund in sample.refunds.recent" :key="refund.id">
              <td class="font-mono">
                {{ dayFormat.format(refund.refundedAt) }}
              </td>
              <td>{{ refund.product }}</td>
              <td>
                <span class="inline-flex items-center gap-1.5">
                  <PlatformIcon :platform="STORES[refund.store].platform" />{{ STORES[refund.store].label }}
                </span>
              </td>
              <td>
                <span class="inline-flex items-center gap-1.5">
                  <CountryFlag :code="refund.country" />{{ segmentName('install_country', refund.country) }}
                </span>
              </td>
              <td class="font-mono">
                {{ formatExactMoney(refund.amount) }}
              </td>
              <td class="font-mono text-ink/60">
                {{ daysBefore(refund) }}
              </td>
              <td>
                <span
                  :class="refund.reason === 'Chargeback' || refund.reason === 'Fraud' ? 'bg-error/12 text-error' : 'bg-ink/5 text-ink/70'"
                  class="rounded-(--ps-radius-x-small) px-1.5 py-0.5 text-[11px]"
                >{{ refund.reason }}</span>
              </td>
              <td class="font-mono text-[11px] text-ink/55">
                {{ refund.id }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </SectionCard>
  </div>
</template>

<style scoped>
.ps-revenue-refunds {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-revenue-refunds th {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  text-align: left;
  white-space: nowrap;
}

.ps-revenue-refunds td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 8px 12px;
  white-space: nowrap;
}

.ps-revenue-refunds :is(th, td):first-child {
  padding-left: 20px;
}

.ps-revenue-refunds :is(th, td):last-child {
  padding-right: 20px;
}

.ps-revenue-refunds tbody tr:last-child td {
  border-bottom: 0;
}
</style>
