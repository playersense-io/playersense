<script setup lang="ts">
import type { ShareRow } from '~/components/ShareList.vue'

// One package, in detail: what it made and where it went, gross, less refunds, less the stores'
// share, to an estimated net; how many sold and to how many players; its revenue day by day;
// which stores and countries bought it; and why the refunded ones were refunded. Read from the
// revenue answer narrowed to this package, over the page's range and its other filters.
const props = defineProps<{
  /** The package's product id; null while none is open. */
  product: string | null
  range: { from: string, to: string }
  /** The page's store and country filters, which the package's figures keep. */
  filters: Record<string, readonly string[]>
}>()

const open = defineModel<boolean>({ required: true })

const answer = useRevenue(
  () => props.range,
  () => ({ store: props.filters.store ?? [], country: props.filters.country ?? [], product: props.product ? [props.product] : [] }),
  () => Boolean(props.product)
)
const data = computed(() => (props.product ? answer.data.value : null))
const money = computed(() => data.value?.money ?? null)
const usd = computed(() => Boolean(money.value))

const gross = computed(() => money.value?.gross ?? (data.value?.totals.revenue_micros ?? 0) / 1_000_000)

/* A package with nothing sold or refunded in the range comes back as zeros the API filled in:
   no data, drawn as dashes and the no-data mark. */
const spread = computed(() => revenueSpread(data.value))
const NONE = '–'

const facts = computed<Fact[]>(() => {
  const revenue = data.value
  if (!revenue) return []
  const { totals } = revenue
  const { any, bought, comparable } = spread.value
  const trend = comparable && totals.previous_revenue_micros ? (totals.revenue_micros - totals.previous_revenue_micros) / totals.previous_revenue_micros : null
  const out: Fact[] = [
    { key: 'gross', label: 'Gross', value: any ? writeExactMoney(gross.value, usd.value) : NONE, mono: true, note: trend !== null ? `${formatChange(trend)} on the range before` : bought ? 'New this range' : 'Nothing sold in the range' },
    { key: 'sold', label: 'Sold', value: any ? writeFigure(totals.purchases) : NONE, mono: true, note: bought && totals.buyers ? `to ${writeFigure(totals.buyers)} players` : undefined },
    { key: 'refunds', label: 'Refunded', value: any ? writeFigure(totals.refunds) : NONE, mono: true, note: !any ? undefined : money.value ? `${writeExactMoney(money.value.refunded)} given back` : `${formatShare(totals.purchases ? totals.refunds / totals.purchases : 0)} of sales` }
  ]
  if (money.value) {
    out.push(
      { key: 'fees', label: 'Store fees', value: any ? writeExactMoney(money.value.fees) : NONE, mono: true, note: 'Estimated' },
      { key: 'net', label: 'Net', value: any ? writeExactMoney(money.value.net) : NONE, mono: true, note: any && money.value.gross ? `Estimated, ${formatShare(money.value.net / money.value.gross)} of gross` : 'Estimated' }
    )
  }
  return out
})

const categories = computed(() => (data.value?.days ?? []).map(day => dayLabel(day.day)))
const series = computed<MetricSeries[]>(() => {
  const revenue = data.value
  if (!revenue) return []
  const { any } = spread.value
  const values = revenue.money
    ? revenue.money.days.map((day, index) => dayPoint(day.gross, revenue.days[index]?.withheld, any))
    : revenue.days.map(day => dayPoint(day.revenue_micros / 1_000_000, day.withheld, any))
  return [{ name: 'Gross', data: values, type: 'bar', tone: 1 }]
})

const shareRows = (slices: readonly RevenueSlice[], describe: (key: string) => { label: string, mark?: SegmentMarkValue }, limit = 6): ShareRow[] => {
  const total = slices.reduce((sum, slice) => sum + grossOf(slice), 0) || 1
  return slices.slice(0, limit).map(slice => ({
    key: slice.key,
    ...describe(slice.key),
    value: grossOf(slice),
    display: writeMoney(grossOf(slice), usd.value),
    note: formatShare(grossOf(slice) / total)
  }))
}

const stores = computed(() => shareRows(data.value?.stores ?? [], key => ({ label: storeLook(key).label, mark: { kind: 'platform', code: storeLook(key).platform } })))
const countries = computed(() => shareRows(data.value?.countries ?? [], key => ({ label: countryName(key), mark: { kind: 'country', code: key } })))

const reasons = computed<ShareRow[]>(() => (data.value?.refunds?.reasons ?? [])
  .map(entry => ({ key: entry.reason, label: entry.reason || 'No reason given', value: entry.refunds, display: writeFigure(entry.refunds), note: entry.usd ? writeMoney(entry.usd) : undefined }))
  .sort((a, b) => b.value - a.value))
</script>

<template>
  <v-dialog v-model="open" max-width="880" scrollable>
    <v-card v-if="product" border flat>
      <header class="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
        <div class="flex flex-col gap-1">
          <h2 class="m-0 font-mono text-base font-medium text-ink">
            {{ product }}
          </h2>
          <p class="m-0 text-xs text-ink/55">
            {{ range.from === range.to ? range.from : `${range.from} to ${range.to}` }}, {{ currencyWords(data) }}
          </p>
        </div>
        <v-btn
          aria-label="Close"
          color="neutral"
          icon="material-symbols:close"
          size="small"
          variant="text"
          @click="open = false"
        />
      </header>

      <div class="overflow-y-auto">
        <LoadState
          :error="answer.error.value"
          :loading="answer.loading.value"
          what="the package"
          @retry="answer.reload"
        >
          <div class="flex flex-col gap-6 p-5">
            <FactGrid :columns="3" :facts="facts" :label="`${product} in figures`" />

            <MetricCard
              :categories="categories"
              :decimals="2"
              :height="180"
              :loading="answer.pending.value"
              :prefix="usd ? '$' : ''"
              :series="series"
              category-label="Day"
              description="Gross revenue, day by day."
              title="Revenue"
              type="bar"
            />

            <div class="grid gap-6 md:grid-cols-2">
              <section class="flex flex-col gap-2">
                <h3 class="m-0 text-xs font-medium text-ink/70">
                  By store
                </h3>
                <ShareList v-if="stores.length" :rows="stores" label="Revenue by store" />
                <NoDataPlaceholder
                  v-else
                  :height="96"
                  :seed="3"
                  label="No data yet"
                />
              </section>
              <section class="flex flex-col gap-2">
                <h3 class="m-0 text-xs font-medium text-ink/70">
                  Top countries
                </h3>
                <ShareList v-if="countries.length" :rows="countries" label="Revenue by country" />
                <NoDataPlaceholder
                  v-else
                  :height="96"
                  :seed="5"
                  label="No data yet"
                />
              </section>
            </div>

            <section v-if="reasons.length" class="flex flex-col gap-2">
              <h3 class="m-0 text-xs font-medium text-ink/70">
                Why it was refunded
              </h3>
              <ShareList :rows="reasons" label="Refunds by reason" tone="error" />
            </section>
          </div>
        </LoadState>
      </div>
    </v-card>
  </v-dialog>
</template>
