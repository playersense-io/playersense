<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// What the game earned, and everything between the price and the money in the bank:
// gross verified purchases, less refunds, less the stores' fees, to net; who paid and how
// much; each package's part in it; the refunds and why; and what the SDK's verification
// turned away before any of it counted. Over 7, 30 or 90 days, narrowed by store, country
// and package, against the range before.
//
// Sample data (app/utils/revenue-sample.ts says what stands in for what): packages,
// stores, refunds, fees and verification need the verified purchase record, which the
// backend has a schema for and no handler yet. Every choice is in the URL.
const { project } = useWorkspace()

const RANGES: SegmentedOption[] = [
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' }
]

const BASES: SegmentedOption[] = [
  { value: 'gross', label: 'Gross', title: 'What players paid' },
  { value: 'net', label: 'Net', title: 'What is left after refunds and the stores\' fees' }
]

const range = useQueryChoice<RevenueRange>('range', ['7d', '30d', '90d'], '30d')
const basis = useQueryChoice<'gross' | 'net'>('basis', ['gross', 'net'], 'gross')

// --- Filters ------------------------------------------------------------------------------

const COUNTRY_CODES = COHORT_DIMENSIONS.install_country.values.map(([code]) => code)

const stores = useQueryList('store', STORE_KEYS)
const countries = useQueryList('country', COUNTRY_CODES)
const packages = useQueryList('package', PRODUCTS.map(product => product.id))

const filters = computed<Record<string, string[]>>({
  get: () => ({ store: stores.value, country: countries.value, product: packages.value }),
  set: (value) => {
    stores.value = value.store ?? []
    countries.value = value.country ?? []
    packages.value = value.product ?? []
  }
})

const FILTER_GROUPS: FilterGroup[] = [
  {
    key: 'store',
    label: 'Store',
    noun: 'store',
    options: STORE_KEYS.map(store => ({ value: store, label: STORES[store].label, note: `${formatShare(STORES[store].fee)} fee`, mark: { kind: 'platform', code: STORES[store].platform } }))
  },
  {
    key: 'country',
    label: 'Country',
    noun: 'country',
    options: COHORT_DIMENSIONS.install_country.values.map(([code]) => ({
      value: code,
      label: segmentName('install_country', code),
      note: code === 'Other' ? undefined : code,
      mark: { kind: 'country', code }
    }))
  },
  {
    key: 'product',
    label: 'Package',
    noun: 'package',
    options: PRODUCTS.map(product => ({ value: product.id, label: product.name, note: formatExactMoney(product.price) }))
  }
]

const activeFilters = computed<ActiveFilter[]>(() => [
  ...stores.value.map(store => ({ key: `store:${store}`, label: STORES[store as RevenueStore].label, mark: { kind: 'platform' as const, code: STORES[store as RevenueStore].platform } })),
  ...countries.value.map(code => ({ key: `country:${code}`, label: segmentName('install_country', code), mark: { kind: 'country' as const, code } })),
  ...packages.value.map(id => ({ key: `product:${id}`, label: `Package: ${PRODUCTS.find(product => product.id === id)?.name ?? id}` }))
])

function removeFilter(key: string) {
  const [group, value] = key.split(':') as [string, string]
  filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(entry => entry !== value) }
}

function clearFilters() {
  filters.value = { store: [], country: [], product: [] }
}

// --- The answer ---------------------------------------------------------------------------

const { data, pending } = useSampleQuery(
  () => ({ range: range.value, filters: { store: stores.value, country: countries.value, product: packages.value } }),
  question => sampleRevenue(question.range, question.filters)
)

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const categories = computed(() => data.value.days.map(at => dayFormat.format(at)))

const RANGE_WORDS: Record<RevenueRange, string> = { '7d': 'the last 7 days', '30d': 'the last 30 days', '90d': 'the last 90 days' }

const moneyBoard = computed<Kpi[]>(() => {
  const { total, previous } = data.value
  const average = total.transactions ? total.gross / total.transactions : 0
  const previousAverage = previous.transactions ? previous.gross / previous.transactions : 0
  return [
    { key: 'gross', label: 'Gross revenue', value: formatMoney(total.gross), exact: formatExactMoney(total.gross), trend: trendOf(total.gross, previous.gross), info: 'Verified purchases, in USD, before refunds and the stores\' fees.' },
    { key: 'net', label: 'Net revenue', value: formatMoney(total.net), exact: formatExactMoney(total.net), trend: trendOf(total.net, previous.net), info: 'Gross, less refunds and the stores\' fees: what reaches the studio.' },
    { key: 'refunds', label: 'Refunded', value: formatMoney(total.refunds), exact: formatExactMoney(total.refunds), trend: trendOf(total.refunds, previous.refunds, 'down'), info: 'Refunds granted by the stores and chargebacks through players\' banks.' },
    { key: 'transactions', label: 'Purchases', value: writeFigure(Math.round(total.transactions), { compact: true }), exact: writeFigure(Math.round(total.transactions)), trend: trendOf(total.transactions, previous.transactions), info: 'Verified purchases. Receipts turned away and test purchases are not counted.' },
    { key: 'average', label: 'Average purchase', value: formatExactMoney(average), trend: trendOf(average, previousAverage), info: 'Gross over purchases.' }
  ]
})

const payerBoard = computed<Kpi[]>(() => {
  const { total, payers, activePlayers, previousActivePlayers, uniquePlayers, previous } = data.value
  const conversion = payers.total / Math.max(uniquePlayers, 1)
  const arppu = total.gross / Math.max(payers.total, 1)
  const previousArppu = previous.gross / Math.max(payers.previousTotal, 1)
  const arpdau = total.gross / Math.max(activePlayers, 1)
  const previousArpdau = previous.gross / Math.max(previousActivePlayers, 1)
  return [
    { key: 'payers', label: 'Paying players', value: writeFigure(Math.round(payers.total), { compact: true }), exact: writeFigure(Math.round(payers.total)), trend: trendOf(payers.total, payers.previousTotal), info: 'Players with at least one verified purchase in the range.' },
    { key: 'conversion', label: 'Payer conversion', value: formatShare(conversion), info: 'Paying players over every player active in the range.' },
    { key: 'new', label: 'New payers', value: writeFigure(Math.round(payers.newPayers), { compact: true }), info: 'Players whose first purchase ever was in the range.' },
    { key: 'arppu', label: 'ARPPU', value: formatExactMoney(arppu), trend: trendOf(arppu, previousArppu), info: 'Gross over paying players: what a payer spends.' },
    { key: 'arpdau', label: 'ARPDAU', value: formatExactMoney(arpdau), trend: trendOf(arpdau, previousArpdau), info: 'Gross over daily active players, summed over the range: what a player is worth a day.' }
  ]
})

// --- Views --------------------------------------------------------------------------------

const tabs = computed<PageTab[]>(() => [
  { value: 'overview', label: 'Overview' },
  { value: 'packages', label: 'Packages', count: data.value.products.length },
  { value: 'refunds', label: 'Refunds' },
  { value: 'payers', label: 'Payers' },
  { value: 'verification', label: 'Verification' }
])

const tab = useQueryChoice('tab', ['overview', 'packages', 'refunds', 'payers', 'verification'], 'overview')

const byStoreSeries = computed<MetricSeries[]>(() => STORE_KEYS
  .filter(store => data.value.daily.some(day => day.byStore[store].gross > 0))
  .map((store, index) => ({
    name: STORES[store].label,
    data: data.value.daily.map(day => Math.round(day.byStore[store][basis.value] * 100) / 100),
    type: 'area',
    stack: 'stores',
    tone: ((index % 6) + 1) as 1 | 2 | 3 | 4 | 5 | 6
  })))

/** A country's or a store's name and mark, for the breakdown tables. */
const describeCountry = (line: BreakdownLine) => ({ label: segmentName('install_country', line.key), mark: { kind: 'country' as const, code: line.key } })
const describeStore = (line: BreakdownLine) => ({ label: STORES[line.key as RevenueStore].label, mark: { kind: 'platform' as const, code: STORES[line.key as RevenueStore].platform } })

// --- Packages -----------------------------------------------------------------------------

const openPackage = ref<string | null>(null)
const packageOpen = computed({
  get: () => openPackage.value !== null,
  set: (value) => {
    if (!value) openPackage.value = null
  }
})
const openLine = computed(() => data.value.products.find(line => line.product.id === openPackage.value) ?? null)

function downloadPackages() {
  const header = ['package', 'id', 'kind', 'price', 'purchases', 'buyers', 'gross', 'refunds', 'store fees', 'net', 'refund rate', 'first purchases']
  const round = (value: number, places = 2) => Math.round(value * 10 ** places) / 10 ** places
  const lines = data.value.products.map(line => [
    line.product.name,
    line.product.id,
    line.product.kind,
    line.product.price,
    Math.round(line.transactions),
    Math.round(line.buyers),
    round(line.gross),
    round(line.refunds),
    round(line.fees),
    round(line.net),
    round(line.transactions ? line.refunded / line.transactions : 0, 4),
    Math.round(line.firstPurchases)
  ])
  downloadCsv(`revenue-packages-${range.value}`, [header, ...lines])
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Revenue">
      <template #description>
        What <span class="text-ink">{{ project.name }}</span> earned, package by package, and what
        was left after refunds and the stores.
      </template>
      <template #actions>
        <SegmentedControl v-model="basis" :options="BASES" label="Revenue" />
        <SegmentedControl v-model="range" :options="RANGES" label="Range" />
      </template>
    </PageHeader>

    <InfoNote text="Packages, stores, refunds, fees and verification are sample data until verified purchases are recorded server-side. Revenue, purchases and payers over time are what the metrics query answers today." />

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

    <KpiBoard
      :description="`Verified purchases in ${RANGE_WORDS[range]}, in USD, against the range before.`"
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
          :description="basis === 'gross' ? 'What players paid each day, by the store they paid through.' : 'What was left each day after refunds and each store\'s fee, by store.'"
          :loading="pending"
          :series="byStoreSeries"
          category-label="Day"
          prefix="$"
          :title="basis === 'gross' ? 'Gross revenue by store' : 'Net revenue by store'"
          type="area"
        />

        <SectionCard
          :padded="false"
          description="Where the money comes from: each country's revenue and share, how it moved, and what its payers spend."
          title="By country"
        >
          <RevenueBreakdown
            :basis="basis"
            :describe="describeCountry"
            :limit="10"
            :lines="data.countries"
            noun="Country"
          />
        </SectionCard>

        <SectionCard :padded="false" description="Each store's part, and what it kept of it." title="By store">
          <RevenueBreakdown
            :basis="basis"
            :describe="describeStore"
            :lines="data.stores"
            fees
            noun="Store"
          />
        </SectionCard>
      </template>

      <SectionCard
        v-else-if="tab === 'packages'"
        :padded="false"
        description="Every package in the shop. Open one for its day-by-day revenue, stores, countries and refunds."
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
        <RevenueProducts :basis="basis" :lines="data.products" @open="openPackage = $event" />
      </SectionCard>

      <RevenueRefunds
        v-else-if="tab === 'refunds'"
        :categories="categories"
        :loading="pending"
        :sample="data"
      />
      <RevenuePayers
        v-else-if="tab === 'payers'"
        :categories="categories"
        :loading="pending"
        :sample="data"
      />
      <RevenueVerification
        v-else
        :categories="categories"
        :loading="pending"
        :sample="data"
      />
    </div>

    <RevenueProductDialog v-model="packageOpen" :days="data.days" :line="openLine" />
  </div>
</template>
