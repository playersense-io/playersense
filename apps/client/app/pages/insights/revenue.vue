<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// What the game earned, and everything between the price and the money in the bank: gross, less
// refunds, less the stores' share, to an estimated net; who paid; each package's part in it; the
// refunds and why; and what purchase verification turned away before any of it counted. Over 7,
// 30 or 90 days, narrowed by store, country and package, against the range before.
//
// One answer from the API (…/analytics/{env}/revenue). Money is in US dollars at each day's rate
// when the deployment has exchange rates, and as reported otherwise, and the page says which.
// Every choice is in the URL.
const RANGES: SegmentedOption[] = [
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' }
]
const RANGE_DAYS: Record<string, number> = { '7d': 7, '30d': 30, '90d': 90 }
const RANGE_WORDS: Record<string, string> = { '7d': 'the last 7 days', '30d': 'the last 30 days', '90d': 'the last 90 days' }

const BASES: SegmentedOption[] = [
  { value: 'gross', label: 'Gross', title: 'What players paid' },
  { value: 'net', label: 'Net', title: 'What is left after refunds and the stores\' share, estimated' }
]

const range = useQueryChoice<'7d' | '30d' | '90d'>('range', ['7d', '30d', '90d'], '30d')
const basis = useQueryChoice<'gross' | 'net'>('basis', ['gross', 'net'], 'gross')

const DAY = 86_400_000
const span = computed(() => {
  const today = Math.floor(Date.now() / DAY) * DAY
  return { from: isoDate(today - (RANGE_DAYS[range.value]! - 1) * DAY), to: isoDate(today) }
})

// --- Filters ------------------------------------------------------------------------------

const { stores, countries, products, filters, query, remove: removeFilter, clear: clearFilters } = useRevenueFilters()

const { data, pending, loading, error, reload } = useRevenue(span, query)

const money = computed(() => data.value?.money ?? null)
const usd = computed(() => Boolean(money.value))

/* The options are what the answer names, and whatever is chosen already, so a choice never vanishes. */
const optionsOf = (slices: readonly RevenueSlice[] | undefined, chosen: readonly string[]) => [...new Set([...(slices ?? []).map(slice => slice.key), ...chosen])]

const FILTER_GROUPS = computed<FilterGroup[]>(() => [
  {
    key: 'store',
    label: 'Store',
    noun: 'store',
    options: optionsOf(data.value?.stores, stores.value).map(store => ({ value: store, label: storeLook(store).label, mark: { kind: 'platform', code: storeLook(store).platform } }))
  },
  {
    key: 'country',
    label: 'Country',
    noun: 'country',
    options: optionsOf(data.value?.countries, countries.value).map(code => ({ value: code, label: countryName(code), note: code, mark: { kind: 'country', code } }))
  },
  {
    key: 'product',
    label: 'Package',
    noun: 'package',
    options: optionsOf(data.value?.products, products.value).map(id => ({ value: id, label: id }))
  }
])

const activeFilters = computed<ActiveFilter[]>(() => [
  ...stores.value.map(store => ({ key: `store:${store}`, label: storeLook(store).label, mark: { kind: 'platform' as const, code: storeLook(store).platform } })),
  ...countries.value.map(code => ({ key: `country:${code}`, label: countryName(code), mark: { kind: 'country' as const, code } })),
  ...products.value.map(id => ({ key: `product:${id}`, label: `Package: ${id}` }))
])

// --- The figures --------------------------------------------------------------------------

/* What is behind the answer: an empty range comes back as zeros the API filled in, which are
   drawn as dashes and the no-data mark, not as a measured nothing. */
const spread = computed(() => revenueSpread(data.value))

const moneyBoard = computed<Kpi[]>(() => {
  const revenue = data.value
  if (!revenue) return []
  const { totals } = revenue
  const { any, bought, comparable } = spread.value
  const m = revenue.money
  const gross = m ? m.gross : totals.revenue_micros / 1_000_000
  const previousGross = m ? m.previous_gross : totals.previous_revenue_micros / 1_000_000
  const refunded = m ? m.refunded : totals.refunded_micros / 1_000_000
  const average = totals.purchases ? gross / totals.purchases : 0
  const previousAverage = totals.previous_purchases ? previousGross / totals.previous_purchases : 0
  const board: Kpi[] = [
    { key: 'gross', label: 'Gross revenue', value: any ? writeMoney(gross, usd.value) : null, exact: any ? writeExactMoney(gross, usd.value) : undefined, trend: comparable ? trendOf(gross, previousGross) : undefined, info: `What players paid, ${currencyWords(revenue)}, before refunds and the stores' share.` }
  ]
  if (m) board.push({ key: 'net', label: 'Net revenue', value: any ? writeMoney(m.net) : null, exact: any ? writeExactMoney(m.net) : undefined, trend: comparable ? trendOf(m.net, m.previous_net) : undefined, info: `Estimated: gross, less refunds and each store's share. ${m.note}` })
  board.push(
    { key: 'refunds', label: 'Refunded', value: any ? writeMoney(refunded, usd.value) : null, exact: any ? writeExactMoney(refunded, usd.value) : undefined, info: `${writeFigure(totals.refunds)} refunds the stores granted in the range.` },
    { key: 'purchases', label: 'Purchases', value: any ? writeFigure(totals.purchases, { compact: true }) : null, exact: any ? writeFigure(totals.purchases) : undefined, trend: comparable ? trendOf(totals.purchases, totals.previous_purchases) : undefined, info: 'Purchases that count: test purchases and receipts turned away are not.' },
    { key: 'average', label: 'Average purchase', value: bought ? writeExactMoney(average, usd.value) : null, trend: comparable ? trendOf(average, previousAverage) : undefined, info: 'Gross over purchases.' }
  )
  return board
})

/* New payers are not narrowed by the filters, so they have data of their own: a day held back is
   null, and a range where every day is null or empty has none. */
const newPayers = computed<number | null>(() => {
  const days = data.value?.new_payers ?? []
  const counted = days.filter(day => day.payers != null)
  if (!counted.length) return null
  const total = counted.reduce((sum, day) => sum + (day.payers ?? 0), 0)
  return total || spread.value.any ? total : null
})

const payerBoard = computed<Kpi[]>(() => {
  const revenue = data.value
  if (!revenue) return []
  const { totals } = revenue
  const { bought, comparable } = spread.value
  const gross = revenue.money ? revenue.money.gross : totals.revenue_micros / 1_000_000
  const previousGross = revenue.money ? revenue.money.previous_gross : totals.previous_revenue_micros / 1_000_000
  /* Buyers are held back when too few to show: no figure then, and the note says why. */
  const buyers = bought ? totals.buyers ?? null : null
  const previousBuyers = totals.previous_buyers ?? null
  const arppu = buyers ? gross / buyers : null
  const previousArppu = previousBuyers ? previousGross / previousBuyers : null
  const held = bought && totals.buyers == null ? ` Held back: fewer than ${revenue.suppression_threshold} players, too few to show.` : ''
  return [
    { key: 'payers', label: 'Paying players', value: buyers === null ? null : writeFigure(buyers, { compact: true }), exact: buyers === null ? undefined : writeFigure(buyers), trend: comparable && buyers !== null && previousBuyers !== null ? trendOf(buyers, previousBuyers) : undefined, info: `Players with at least one purchase in the range${revenue.approximate ? ', counted approximately' : ''}.${held}` },
    { key: 'new', label: 'New payers', value: newPayers.value === null ? null : writeFigure(newPayers.value, { compact: true }), exact: newPayers.value === null ? undefined : writeFigure(newPayers.value), info: 'Players whose first purchase ever was in the range. Not narrowed by the filters.' },
    { key: 'arppu', label: 'ARPPU', value: arppu === null ? null : writeExactMoney(arppu, usd.value), trend: comparable && arppu !== null && previousArppu !== null ? trendOf(arppu, previousArppu) : undefined, info: `Gross over paying players: what a payer spends.${held}` }
  ]
})

// --- Views --------------------------------------------------------------------------------

const tabs = computed<PageTab[]>(() => [
  { value: 'overview', label: 'Overview' },
  { value: 'packages', label: 'Packages', count: data.value?.products.length },
  { value: 'refunds', label: 'Refunds' },
  { value: 'payers', label: 'Payers' },
  { value: 'verification', label: 'Verification' }
])

const tab = useQueryChoice('tab', ['overview', 'packages', 'refunds', 'payers', 'verification'], 'overview')

const categories = computed(() => (data.value?.days ?? []).map(day => dayLabel(day.day)))

/* Gross by store, stacked; net as one line, since the stores' shares are estimated for the whole.
   A day held back, and every day of an empty range, is a gap: all gaps draw the no-data mark. */
const chartSeries = computed<MetricSeries[]>(() => {
  const revenue = data.value
  if (!revenue) return []
  const { any } = spread.value
  const withheld = (index: number) => revenue.days[index]?.withheld
  if (basis.value === 'net' && revenue.money) {
    return [{ name: 'Net, estimated', data: revenue.money.days.map((day, index) => dayPoint(day.net, withheld(index), any)), type: 'area', tone: 1 }]
  }
  const days = revenue.money ? revenue.money.days : revenue.days
  const keys = [...new Set(days.flatMap(day => Object.keys(day.by_store ?? {})))]
  if (!keys.length) {
    const values = revenue.money ? revenue.money.days.map(day => day.gross) : revenue.days.map(day => day.revenue_micros / 1_000_000)
    return [{ name: 'Gross', data: values.map((value, index) => dayPoint(value, withheld(index), any)), type: 'area', tone: 1 }]
  }
  return keys.map((store, index) => ({
    name: storeLook(store).label,
    data: days.map((day, at) => {
      const value = (day.by_store as Record<string, number> | undefined)?.[store] ?? 0
      return dayPoint(revenue.money ? value : value / 1_000_000, withheld(at), any)
    }),
    type: 'area',
    stack: 'stores',
    tone: ((index % 6) + 1) as 1 | 2 | 3 | 4 | 5 | 6
  }))
})

const countryLines = computed(() => linesOf(data.value?.countries ?? []))
const storeLines = computed(() => linesOf(data.value?.stores ?? [], money.value))
const productLines = computed(() => linesOf(data.value?.products ?? []))

const describeCountry = (line: RevenueLine) => ({ label: countryName(line.key), mark: { kind: 'country' as const, code: line.key } })
const describeStore = (line: RevenueLine) => ({ label: storeLook(line.key).label, mark: { kind: 'platform' as const, code: storeLook(line.key).platform } })

const withheld = computed(() => (data.value?.suppressed_cells ?? 0) > 0)

// --- Packages -----------------------------------------------------------------------------

const openPackage = ref<string | null>(null)
const packageOpen = computed({
  get: () => openPackage.value !== null,
  set: (value) => {
    if (!value) openPackage.value = null
  }
})

function downloadPackages() {
  const header = ['package', 'purchases', 'buyers', usd.value ? 'gross (USD)' : 'gross (as reported)', 'trend']
  const lines = productLines.value.map(line => [line.key, line.purchases, line.buyers, Math.round(line.gross * 100) / 100, line.trend === null ? '' : Math.round(line.trend * 10_000) / 10_000])
  downloadCsv(`revenue-packages-${range.value}`, [header, ...lines])
}

/* No project yet: nothing to ask the API about, so the page says so instead of drawing zeros. */
const workspace = useWorkspaceStore()
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader description="What the game earned, package by package, and what was left after refunds and the stores." title="Revenue">
      <template #actions>
        <SegmentedControl
          v-if="money"
          v-model="basis"
          :options="BASES"
          label="Revenue"
        />
        <SegmentedControl v-model="range" :options="RANGES" label="Range" />
      </template>
    </PageHeader>

    <!-- The filters stay in reach down the whole page, figures and tabs alike. -->
    <StickyBar>
      <v-card border flat>
        <TableToolbar
          :filters="activeFilters"
          :ruled="false"
          @clear="clearFilters"
          @remove="removeFilter"
        >
          <FilterDialog v-model="filters" :groups="FILTER_GROUPS" title="Filter revenue" />
        </TableToolbar>
      </v-card>
    </StickyBar>

    <LoadState
      :error="error"
      :loading="loading"
      what="revenue"
      @retry="reload"
    >
      <LayerCard v-if="!workspace.projectPath" :padded="false">
        <EmptyState
          description="Make a project from the project menu and send events with its SDK key: its revenue shows here."
          icon="material-symbols:sports-esports-outline"
          title="No project yet"
        />
      </LayerCard>
      <template v-else-if="data">
        <p v-if="!money" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius-large) bg-ink/4 px-4 py-3 text-xs leading-5 text-ink/75">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/50" name="material-symbols:currency-exchange" />
          <span>No exchange rates on this deployment, so revenue is shown {{ currencyWords(data) }}, and net cannot be estimated.</span>
        </p>
        <p v-else-if="money.unconverted.length || !money.exact" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius-large) bg-warning/8 px-4 py-3 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-warning" name="material-symbols:currency-exchange" />
          <span>
            <template v-if="money.unconverted.length">{{ money.unconverted.join(', ') }} had no exchange rate and {{ money.unconverted.length === 1 ? 'is' : 'are' }} left out of every dollar figure. </template>
            <template v-if="!money.exact">Some days had no rates of their own and were converted at the nearest day's.</template>
          </span>
        </p>

        <KpiBoard
          :description="`${RANGE_WORDS[range]}, ${currencyWords(data)}, against the range before.`"
          :items="moneyBoard"
          :loading="pending"
          title="Revenue"
        />

        <KpiBoard
          :items="payerBoard"
          :loading="pending"
          description="The players behind it."
          title="Payers"
        />

        <div class="flex flex-col gap-5">
          <PageTabs v-model="tab" :tabs="tabs" label="Revenue views" />

          <template v-if="tab === 'overview'">
            <MetricCard
              :categories="categories"
              :decimals="2"
              :description="basis === 'net' && money ? 'What was left each day after refunds and the stores\' share, estimated.' : 'What players paid each day, by the store they paid through.'"
              :loading="pending"
              :prefix="usd ? '$' : ''"
              :series="chartSeries"
              :title="basis === 'net' && money ? 'Net revenue' : 'Gross revenue by store'"
              category-label="Day"
              type="area"
            />

            <SectionCard
              :padded="false"
              description="Where the money comes from: each country's revenue and share, how it moved, and what its buyers spend."
              title="By country"
            >
              <RevenueBreakdown
                :describe="describeCountry"
                :limit="10"
                :lines="countryLines"
                :usd="usd"
                noun="Country"
              />
            </SectionCard>

            <SectionCard :padded="false" description="Each store's part, the share it is taken to keep, and what was left." title="By store">
              <RevenueBreakdown
                :describe="describeStore"
                :fees="Boolean(money)"
                :lines="storeLines"
                :usd="usd"
                noun="Store"
              />
            </SectionCard>

            <p v-if="withheld" class="m-0 text-[11px] leading-4 text-ink/50">
              {{ data.suppressed_cells }} {{ data.suppressed_cells === 1 ? 'row is' : 'rows are' }} left out: bought by fewer than {{ data.suppression_threshold }} players, too few to show without pointing at someone.
            </p>

            <RevenueFeeSettings @saved="reload" />
          </template>

          <SectionCard
            v-else-if="tab === 'packages'"
            :padded="false"
            description="Every package sold in the range, by the product id the game sent. Open one for its day-by-day revenue, stores, countries and refunds."
            title="Packages"
          >
            <template #actions>
              <v-btn
                aria-label="Download as CSV"
                color="neutral"
                icon="material-symbols:download"
                size="small"
                variant="text"
                @click="downloadPackages"
              />
            </template>
            <RevenueProducts :lines="productLines" :usd="usd" @open="openPackage = $event" />
          </SectionCard>

          <RevenueRefunds
            v-else-if="tab === 'refunds'"
            :filters="query"
            :loading="pending"
            :range="span"
            :revenue="data"
          />
          <RevenuePayers
            v-else-if="tab === 'payers'"
            :categories="categories"
            :loading="pending"
            :revenue="data"
          />
          <RevenueVerification
            v-else
            :loading="pending"
            :revenue="data"
          />
        </div>
      </template>
    </LoadState>

    <RevenueProductDialog
      v-model="packageOpen"
      :filters="query"
      :product="openPackage"
      :range="span"
    />
  </div>
</template>
