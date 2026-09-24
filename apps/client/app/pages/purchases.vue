<script setup lang="ts">
import type { DayRange } from '~/components/DateRangePicker.vue'
import type { Kpi } from '~/components/KpiBoard.vue'
import type { ShareRow } from '~/components/ShareList.vue'

// Every purchase the game's players made, as purchases rather than as money: how many, by whom,
// which packages carry the shop (by how often they are bought and by what they earn), where in
// the world they are bought, when in the week, at which prices, and then every purchase itself,
// each openable to what happened to it and the record the studio's backend receives.
//
// Insights > Revenue reads the same purchases as money over time: gross to net, fees, refunds,
// payers. This page is the ledger, and the shape of the shop. Both draw from one model, so a
// range and a filter agree across them.
//
// Sample data (app/utils/purchases-sample.ts): the verified purchase record has a schema and no
// handler yet. Every choice is in the URL.
const { project } = useWorkspace()

// --- The days -----------------------------------------------------------------------------

const route = useRoute()
const router = useRouter()

const DAY = 86_400_000
const isoDay = (at: number) => new Date(at).toISOString().slice(0, 10)
const dayStart = (iso: string) => Date.parse(`${iso}T00:00:00Z`)
const todayStart = () => Math.floor(Date.now() / DAY) * DAY

/* The days in view, in the URL as ?from=&to=; the last 30 days when it names none, or none that parse. */
const period = computed<DayRange>({
  get: () => {
    const from = String(route.query.from ?? '')
    const to = String(route.query.to ?? '')
    const valid = /^\d{4}-\d{2}-\d{2}$/.test(from) && /^\d{4}-\d{2}-\d{2}$/.test(to) && dayStart(from) <= dayStart(to) && dayStart(to) <= todayStart()
    return valid ? { from, to } : { from: isoDay(todayStart() - 29 * DAY), to: isoDay(todayStart()) }
  },
  set: (value) => {
    router.replace({ query: { ...route.query, from: value.from, to: value.to } })
  }
})

const span = computed<RevenueWindow>(() => ({
  days: Math.round((dayStart(period.value.to) - dayStart(period.value.from)) / DAY) + 1,
  end: dayStart(period.value.to) + DAY
}))

/* The days in a phrase for a sentence: "the last 30 days", "Sep 1 to Sep 24". */
const periodWords = computed(() => {
  const { from, to } = period.value
  if (to === isoDay(todayStart())) return span.value.days === 1 ? 'today' : `the last ${span.value.days} days`
  const day = (iso: string) => new Date(dayStart(iso)).toLocaleDateString('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
  return from === to ? day(from) : `${day(from)} to ${day(to)}`
})

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
    options: STORE_KEYS.map(store => ({ value: store, label: STORES[store].label, mark: { kind: 'platform', code: STORES[store].platform } }))
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
  () => ({ window: span.value, filters: { store: stores.value, country: countries.value, product: packages.value } }),
  question => samplePurchases(question.window, question.filters)
)

const board = computed<Kpi[]>(() => {
  const { total, previous } = data.value
  const average = total.gross / Math.max(total.purchases, 1)
  const previousAverage = previous.gross / Math.max(previous.purchases, 1)
  const attempts = total.purchases + total.rejected
  // Ten, in two rows: how much was bought and for what, then who bought it and what went wrong.
  return [
    { key: 'purchases', label: 'Purchases', value: writeFigure(Math.round(total.purchases), { compact: true }), exact: writeFigure(Math.round(total.purchases)), trend: trendOf(total.purchases, previous.purchases), info: 'Verified purchases. Test purchases and receipts turned away are not counted.' },
    { key: 'sales', label: 'Sales', value: formatMoney(total.gross), exact: formatExactMoney(total.gross), trend: trendOf(total.gross, previous.gross), info: 'What players paid for them, in USD at the day\'s rate, before refunds and the stores\' fees.' },
    { key: 'average', label: 'Average purchase', value: formatExactMoney(average), trend: trendOf(average, previousAverage), info: 'What a purchase is worth, in USD at the day\'s rate.' },
    { key: 'buyers', label: 'Buyers', value: writeFigure(Math.round(total.buyers), { compact: true }), exact: writeFigure(Math.round(total.buyers)), info: 'Different players with at least one verified purchase.' },
    { key: 'per-buyer', label: 'Purchases per buyer', value: (total.purchases / Math.max(total.buyers, 1)).toFixed(1), info: 'How often a buyer came back to the shop in the range.' },
    { key: 'first', label: 'First purchases', value: writeFigure(Math.round(total.firstPurchases), { compact: true }), exact: writeFigure(Math.round(total.firstPurchases)), info: 'Players whose first purchase ever was in the range: new buyers.' },
    { key: 'repeat', label: 'Repeat buyers', value: formatShare(total.repeatBuyers / Math.max(total.buyers, 1)), info: `${writeFigure(Math.round(total.repeatBuyers))} buyers bought more than once in the range.` },
    { key: 'refunded', label: 'Refunded', value: formatShare(total.refunded / Math.max(total.purchases, 1)), info: `${writeFigure(Math.round(total.refunded))} purchases given back by the stores or through players' banks.` },
    { key: 'rejected', label: 'Turned away', value: formatShare(total.rejected / Math.max(attempts, 1)), info: `${writeFigure(Math.round(total.rejected))} receipts that did not check out with the store: forged, replayed or altered. Nothing was granted.` },
    { key: 'sandbox', label: 'Test purchases', value: writeFigure(Math.round(total.sandbox), { compact: true }), exact: writeFigure(Math.round(total.sandbox)), info: 'Made by TestFlight and license-tester accounts: verified, granted in the game, and kept out of revenue.' }
  ]
})

// --- Where ------------------------------------------------------------------------------

const countryRows = computed<ShareRow[]>(() => data.value.countries.slice(0, 12).map(line => ({
  key: line.code,
  label: segmentName('install_country', line.code),
  value: line.gross,
  display: formatMoney(line.gross),
  note: `${formatShare(line.share)} · ${writeFigure(Math.round(line.purchases), { compact: true })} purchases · ${formatExactMoney(line.gross / Math.max(line.purchases, 1))} each`,
  mark: { kind: 'country', code: line.code }
})))

const storeRows = computed<ShareRow[]>(() => {
  const byStore = STORE_KEYS.map(store => ({ store, count: data.value.ledger.filter(record => record.store === store).length }))
  const total = byStore.reduce((sum, entry) => sum + entry.count, 0)
  return byStore.filter(entry => entry.count).map(entry => ({
    key: entry.store,
    label: STORES[entry.store].label,
    value: entry.count,
    display: formatShare(entry.count / Math.max(total, 1)),
    mark: { kind: 'platform', code: STORES[entry.store].platform }
  }))
})

// --- When, and at what price ----------------------------------------------------------------

const priceCategories = computed(() => data.value.pricePoints.map(point => formatExactMoney(point.price)))
const priceSeries = computed<MetricSeries[]>(() => [{
  name: 'Purchases',
  data: data.value.pricePoints.map(point => Math.round(point.purchases)),
  type: 'bar',
  tone: 1
}])

// --- The ledger -----------------------------------------------------------------------------

const query = ref('')
const statuses = useQueryList('status', PURCHASE_STATUSES)

const LEDGER_FILTERS: FilterGroup[] = [
  { key: 'status', label: 'What happened', options: PURCHASE_STATUSES.map(status => ({ value: status, label: PURCHASE_STATUS[status].label })) }
]

const ledgerFilters = computed<Record<string, string[]>>({
  get: () => ({ status: statuses.value }),
  set: (value) => {
    statuses.value = value.status ?? []
  }
})

const ledgerActive = computed<ActiveFilter[]>(() => [
  ...statuses.value.map(status => ({ key: `status:${status}`, label: PURCHASE_STATUS[status as PurchaseStatus].label })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeLedgerFilter(key: string) {
  if (key === 'search') query.value = ''
  else statuses.value = statuses.value.filter(status => `status:${status}` !== key)
}

function clearLedgerFilters() {
  query.value = ''
  statuses.value = []
}

const productName = (id: string) => PRODUCTS.find(product => product.id === id)?.name ?? id

const rows = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return data.value.ledger.filter(record => (!statuses.value.length || statuses.value.includes(record.status))
    && (!needle || [record.id, record.playerId, record.storeTransactionId, record.productId, productName(record.productId)].some(value => value.toLowerCase().includes(needle))))
})

const COLUMNS: DataTableColumn<PurchaseRecord>[] = [
  { key: 'productId', title: 'Package', fixed: true, minWidth: 220, value: record => productName(record.productId) },
  { key: 'at', title: 'When', minWidth: 150 },
  { key: 'playerId', title: 'Player', minWidth: 170, mono: true },
  { key: 'country', title: 'Country', minWidth: 150, value: record => segmentName('install_country', record.country) },
  { key: 'store', title: 'Store', value: record => STORES[record.store].label },
  { key: 'usd', title: 'Paid', align: 'end' },
  { key: 'status', title: 'What happened', value: record => PURCHASE_STATUSES.indexOf(record.status), skeleton: 'pill' },
  { key: 'storeTransactionId', title: 'Store transaction', mono: true, hidden: true },
  { key: 'open', title: '', sortable: false, width: 48 }
]

const columns = useLocalStorage('playersense.purchases.columns', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'at', order: 'desc' }])

const now = useNow({ interval: 60_000 })
const whenFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

const opened = shallowRef<PurchaseRecord | null>(null)
const openedModel = computed({
  get: () => opened.value !== null,
  set: (value) => {
    if (!value) opened.value = null
  }
})

function downloadLedger() {
  const header = ['id', 'purchased at', 'player', 'package', 'store', 'country', 'amount', 'currency', 'usd', 'status', 'store transaction id', 'first purchase', 'refunded at', 'refund reason', 'rejection']
  const lines = rows.value.map(record => [
    record.id,
    new Date(record.at).toISOString(),
    record.playerId,
    record.productId,
    record.store,
    record.country,
    record.local,
    record.currency,
    record.usd,
    record.status,
    record.storeTransactionId,
    record.first ? 'yes' : 'no',
    record.refundedAt ? new Date(record.refundedAt).toISOString() : '',
    record.refundReason ?? '',
    record.rejection ?? ''
  ])
  downloadCsv(`purchases-${period.value.from}-to-${period.value.to}`, [header, ...lines])
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Purchases">
      <template #description>
        Every purchase <span class="text-ink">{{ project.name }}</span>'s players made: what they bought, where, through
        which store, and what happened to each one.
      </template>
    </PageHeader>

    <InfoNote text="Purchases are sample data until verified purchases are recorded server-side. Insights > Revenue reads the same purchases as money: gross to net, fees and refunds." />

    <StickyBar>
      <v-card border flat>
        <TableToolbar
          :filters="activeFilters"
          :ruled="false"
          @clear="clearFilters"
          @remove="removeFilter"
        >
          <FilterDialog v-model="filters" :groups="FILTER_GROUPS" title="Filter purchases" />
          <DateRangePicker v-model="period" />
        </TableToolbar>
      </v-card>
    </StickyBar>

    <KpiBoard
      :description="`Verified purchases in ${periodWords}, against the same number of days before.`"
      :items="board"
      :loading="pending"
      title="Purchases"
    />

    <!-- Which packages carry the shop: by how often they are bought, and by what they earn. -->
    <SectionCard :padded="false" description="Each package's share of every purchase beside its share of every dollar. Packages bought most are rarely the ones that earn most." title="Package weights">
      <PackageWeights :loading="pending" :packages="data.packages" />
    </SectionCard>

    <!-- Where: every market in a row of its own, the list flowing into two columns on one scale. -->
    <SectionCard description="Where purchases are made, by revenue, with how many and what a purchase is worth there." title="Sales by country">
      <ShareList
        :loading="pending"
        :rows="countryRows"
        :skeleton-rows="12"
        class="ps-country-columns"
        label="Sales by country"
      />
      <p v-if="!pending && data.countries.length > 12" class="m-0 mt-2 text-[11px] text-ink/45">
        And {{ data.countries.length - 12 }} more. Insights > Revenue breaks every country down in full.
      </p>
    </SectionCard>

    <div class="grid gap-5 lg:grid-cols-2">
      <SectionCard description="Where the purchases below were made." title="By store">
        <ShareList
          :loading="pending"
          :rows="storeRows"
          :skeleton-rows="4"
          label="Purchases by store"
          tone="chart-6"
        />
      </SectionCard>
      <MetricCard
        :categories="priceCategories"
        :decimals="0"
        :height="160"
        :loading="pending"
        :series="priceSeries"
        category-label="Price"
        description="How many purchases at each price point."
        title="Price points"
        type="bar"
      />
    </div>

    <SectionCard description="Purchases in each hour of the week, by the player's own clock: when to open an offer so it is seen." title="When players buy">
      <WeekHeatmap :loading="pending" :values="data.week" unit="purchases" />
    </SectionCard>

    <!-- Every purchase, newest first. -->
    <SectionCard :padded="false" description="Every purchase in the range, newest first. Open one for what happened to it and the record your backend receives." title="All purchases">
      <template #actions>
        <v-btn
          aria-label="Download as CSV"
          color="neutral"
          icon="material-symbols:download"
          size="small"
          variant="text"
          @click="downloadLedger"
        />
      </template>
      <TableToolbar :filters="ledgerActive" @clear="clearLedgerFilters" @remove="removeLedgerFilter">
        <SearchField
          v-model="query"
          class="w-64"
          label="Search purchases"
          placeholder="Player, transaction or package"
        />
        <FilterMenu v-model="ledgerFilters" :groups="LEDGER_FILTERS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="rows"
        :loading="pending"
        empty-description="No purchase matches these filters."
        empty-title="No purchases match."
        item-value="id"
        label="Purchases"
      >
        <template #cell.productId="{ item }">
          <button class="group flex min-w-0 cursor-pointer flex-col py-1 text-left" type="button" @click="opened = item">
            <span class="flex items-center gap-1.5">
              <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ productName(item.productId) }}</span>
              <Tag v-if="item.first" label="First purchase" tone="success" />
            </span>
            <span class="truncate font-mono text-[11px] text-ink/45">{{ item.id }}</span>
          </button>
        </template>
        <template #cell.at="{ item }">
          <span class="flex flex-col">
            <span class="text-xs text-ink/80">{{ whenFormat.format(item.at) }}</span>
            <span class="text-[11px] text-ink/45">{{ sinceWords(new Date(item.at).toISOString(), now) }}</span>
          </span>
        </template>
        <template #cell.playerId="{ item }">
          <NuxtLink :to="`/analytics/players/${encodeURIComponent(item.playerId)}`" class="block max-w-40 truncate font-mono text-xs text-ink/80 no-underline hover:text-primary">
            {{ item.playerId }}
          </NuxtLink>
        </template>
        <template #cell.country="{ item }">
          <span class="flex items-center gap-2 text-xs text-ink/80">
            <CountryFlag :code="item.country" />
            {{ segmentName('install_country', item.country) }}
          </span>
        </template>
        <template #cell.store="{ item }">
          <span class="flex items-center gap-2 text-xs text-ink/80">
            <PlatformIcon :platform="STORES[item.store].platform" />
            {{ STORES[item.store].label }}
          </span>
        </template>
        <template #cell.usd="{ item }">
          <span class="flex flex-col items-end">
            <span class="font-mono text-xs text-ink">{{ writeLocal(item.local, item.currency) }}</span>
            <span v-if="item.currency !== 'USD'" class="font-mono text-[11px] text-ink/45">{{ formatExactMoney(item.usd) }}</span>
          </span>
        </template>
        <template #cell.status="{ item }">
          <span class="flex flex-col items-start gap-0.5">
            <StatusBadge :label="PURCHASE_STATUS[item.status].label" :tone="PURCHASE_STATUS[item.status].tone" />
            <span v-if="item.refundReason" class="text-[11px] text-ink/45">{{ item.refundReason }}</span>
            <span v-else-if="item.rejection" class="text-[11px] text-ink/45">{{ item.rejection }}</span>
          </span>
        </template>
        <template #cell.open="{ item }">
          <v-btn
            :aria-label="`Open ${item.id}`"
            color="neutral"
            icon="material-symbols:chevron-right"
            size="small"
            variant="text"
            @click="opened = item"
          />
        </template>
      </DataTable>
    </SectionCard>

    <PurchaseDialog v-model="openedModel" :record="opened" />
  </div>
</template>

<style scoped>
/* Twelve markets read as two columns of six on a wide screen, one list on one scale. */
@media (min-width: 1024px) {
  .ps-country-columns {
    display: block;
    column-count: 2;
    column-gap: 2.5rem;
  }

  .ps-country-columns :deep(li) {
    break-inside: avoid;
  }
}
</style>
