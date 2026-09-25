<script setup lang="ts">
// The money that came back: how much, how often, and why the stores said it was refunded, day by
// day, and the latest refunded purchases one by one, each with the transaction the store knows it
// by. Refunds are what the stores report, so they are there only for stores the project verifies
// purchases with (Settings > Stores).
import type { ShareRow } from '~/components/ShareList.vue'

const props = defineProps<{
  revenue: RevenueRecord | null
  range: { from: string, to: string }
  filters: Record<string, readonly string[]>
  loading?: boolean
}>()

const money = computed(() => props.revenue?.money ?? null)
const refunds = computed(() => props.revenue?.refunds ?? null)
const totals = computed(() => props.revenue?.totals ?? null)

const facts = computed<Fact[]>(() => {
  const all = totals.value
  if (!all) return []
  const rate = all.purchases ? all.refunds / all.purchases : 0
  const amount = money.value ? money.value.refunded : all.refunded_micros / 1_000_000
  const gross = money.value ? money.value.gross : all.revenue_micros / 1_000_000
  return [
    { key: 'amount', label: 'Refunded', value: writeExactMoney(amount, Boolean(money.value)), mono: true, note: `${formatShare(gross ? amount / gross : 0)} of gross` },
    { key: 'count', label: 'Refunds', value: writeFigure(all.refunds), mono: true, note: `${formatShare(rate)} of purchases` },
    { key: 'days', label: 'Median days to refund', value: refunds.value ? `${writeFigure(refunds.value.median_days, { decimals: 1 })} days` : 'None yet', mono: true, note: 'From purchase to refund' }
  ]
})

const categories = computed(() => (money.value?.days ?? []).map(day => dayLabel(day.day)))
const series = computed<MetricSeries[]>(() => [{
  name: 'Refunded',
  data: (money.value?.days ?? []).map(day => Math.round(day.refunded * 100) / 100),
  type: 'bar',
  tone: 2
}])

const reasons = computed<ShareRow[]>(() => (refunds.value?.reasons ?? [])
  .map(entry => ({
    key: entry.reason,
    label: entry.reason || 'No reason given',
    value: entry.refunds,
    display: writeFigure(entry.refunds),
    note: money.value ? writeMoney(entry.usd) : undefined
  }))
  .sort((a, b) => b.value - a.value))

// --- The latest, one by one ------------------------------------------------------------------

const ledger = usePurchaseLedger(() => props.range, () => props.filters, ['refunded'])
const latest = computed(() => ledger.purchases.value.slice(0, 20))

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

function daysAfter(purchase: PurchaseRow) {
  if (!purchase.refunded_at) return ''
  const days = Math.round((Date.parse(purchase.refunded_at) - Date.parse(purchase.at)) / 86_400_000)
  return days === 1 ? '1 day after' : `${days} days after`
}
</script>

<template>
  <div class="flex flex-col gap-5">
    <SectionCard description="Refunds the stores granted in the range, and chargebacks raised through a player's bank." title="Refunds">
      <FactGrid :columns="3" :facts="facts" label="Refunds in figures" />
      <p v-if="!refunds" class="m-0 mt-4 text-xs leading-5 text-ink/55">
        Refunds come from the stores themselves, so they show only for stores this project verifies purchases with. Connect App Store, Google Play or Steam in
        <NuxtLink class="text-primary no-underline hover:underline" to="/settings/stores">Settings, Stores</NuxtLink>.
      </p>
    </SectionCard>

    <MetricCard
      v-if="money"
      :categories="categories"
      :decimals="2"
      :loading="loading"
      :series="series"
      category-label="Day"
      description="Revenue refunded each day, in USD, taken off net revenue on the day the store refunded it."
      prefix="$"
      title="Refunded by day"
      type="bar"
    />

    <SectionCard description="What the store or the bank gave as the reason." title="Why">
      <ShareList
        v-if="reasons.length"
        :rows="reasons"
        label="Refunds by reason"
        tone="error"
      />
      <p v-else class="m-0 text-xs text-ink/55">
        No refund in the range.
      </p>
    </SectionCard>

    <SectionCard :padded="false" description="The latest refunded purchases bought in the range, with the transaction the store knows each by." title="Latest refunds">
      <p v-if="!ledger.pending.value && !latest.length" class="m-0 px-5 py-4 text-xs text-ink/55">
        No refunded purchase in the range.
      </p>
      <div v-else class="overflow-x-auto">
        <table class="ps-revenue-refunds">
          <thead>
            <tr>
              <th>Refunded</th>
              <th>Package</th>
              <th>Store</th>
              <th>Country</th>
              <th>Amount</th>
              <th>After</th>
              <th>Reason</th>
              <th>Transaction</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="purchase in latest" :key="purchase.event_id">
              <td class="font-mono">
                {{ purchase.refunded_at ? dayFormat.format(Date.parse(purchase.refunded_at)) : '' }}
              </td>
              <td class="font-mono">
                {{ purchase.product_id }}
              </td>
              <td>
                <span class="inline-flex items-center gap-1.5">
                  <PlatformIcon :platform="storeLook(purchase.store).platform" />{{ storeLook(purchase.store).label }}
                </span>
              </td>
              <td>
                <span v-if="purchase.country" class="inline-flex items-center gap-1.5">
                  <CountryFlag :code="purchase.country" />{{ countryName(purchase.country) }}
                </span>
              </td>
              <td class="font-mono">
                {{ writeAmount(purchase.revenue_micros, purchase.currency) }}
              </td>
              <td class="font-mono text-ink/60">
                {{ daysAfter(purchase) }}
              </td>
              <td>
                <span v-if="purchase.refund_reason" class="rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 py-0.5 text-[11px] text-ink/70">{{ purchase.refund_reason }}</span>
              </td>
              <td class="font-mono text-[11px] text-ink/55">
                {{ purchase.store_transaction_id ?? '' }}
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
