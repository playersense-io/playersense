<script setup lang="ts">
import type { DayRange } from '~/components/DateRangePicker.vue'
import type { Kpi } from '~/components/KpiBoard.vue'
import type { ShareRow } from '~/components/ShareList.vue'

// Every purchase the game's players made, as purchases rather than as money: how many, by whom,
// which packages carry the shop (by how often they are bought and by what they earn), where in
// the world they are bought, when in the week, at which prices, and then every purchase itself,
// each openable to what happened to it.
//
// Insights > Revenue reads the same purchases as money over time: gross to net, fees, refunds,
// payers. This page is the ledger, and the shape of the shop. Both read the same answer
// (…/analytics/{env}/revenue), with the ledger a page at a time (…/purchases), so a range and a
// filter agree across them. Every choice is in the URL.

// --- The days -----------------------------------------------------------------------------

const route = useRoute()
const router = useRouter()

const DAY = 86_400_000
const dayStart = (iso: string) => Date.parse(`${iso}T00:00:00Z`)
const todayStart = () => Math.floor(Date.now() / DAY) * DAY

/* The days in view, in the URL as ?from=&to=; the last 30 days when it names none, or none that parse. */
const period = computed<DayRange>({
  get: () => {
    const from = String(route.query.from ?? '')
    const to = String(route.query.to ?? '')
    const valid = /^\d{4}-\d{2}-\d{2}$/.test(from) && /^\d{4}-\d{2}-\d{2}$/.test(to) && dayStart(from) <= dayStart(to) && dayStart(to) <= todayStart()
    return valid ? { from, to } : { from: isoDate(todayStart() - 29 * DAY), to: isoDate(todayStart()) }
  },
  set: (value) => {
    router.replace({ query: { ...route.query, from: value.from, to: value.to } })
  }
})

const days = computed(() => Math.round((dayStart(period.value.to) - dayStart(period.value.from)) / DAY) + 1)

/* The days in a phrase for a sentence: "the last 30 days", "Sep 1 to Sep 24". */
const periodWords = computed(() => {
  const { from, to } = period.value
  if (to === isoDate(todayStart())) return days.value === 1 ? 'today' : `the last ${days.value} days`
  const day = (iso: string) => new Date(dayStart(iso)).toLocaleDateString('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
  return from === to ? day(from) : `${day(from)} to ${day(to)}`
})

// --- Filters ------------------------------------------------------------------------------

const { stores, countries, products, filters, query: filterQuery, remove: removeFilter, clear: clearFilters } = useRevenueFilters()

// --- The answer ---------------------------------------------------------------------------

const { data, pending, loading, error, reload } = useRevenue(period, filterQuery)

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

/* An empty range comes back as zeros the API filled in, which are no data: dashes, not zeros.
   A purchase, a refund, a receipt turned away or a test purchase is what makes the figures real,
   and a trend needs purchases in the range before as well (revenueSpread). */
const spread = computed(() => revenueSpread(data.value))
const seen = computed(() => {
  const totals = data.value?.totals
  return spread.value.any || (totals?.rejected ?? 0) > 0 || (totals?.sandbox ?? 0) > 0
})

/* First purchases are not narrowed by the filters, so they have data of their own: none when
   every day is held back, or the range has nothing behind it. */
const firstPurchases = computed<number | null>(() => {
  const counted = (data.value?.new_payers ?? []).filter(day => day.payers != null)
  if (!counted.length) return null
  const total = counted.reduce((sum, day) => sum + (day.payers ?? 0), 0)
  return total || seen.value ? total : null
})

const board = computed<Kpi[]>(() => {
  const revenue = data.value
  if (!revenue) return []
  const { totals } = revenue
  const { any, bought, comparable } = spread.value
  const gross = money.value ? money.value.gross : totals.revenue_micros / 1_000_000
  const previousGross = money.value ? money.value.previous_gross : totals.previous_revenue_micros / 1_000_000
  const average = gross / Math.max(totals.purchases, 1)
  const previousAverage = previousGross / Math.max(totals.previous_purchases, 1)
  const attempts = totals.purchases + totals.rejected
  const first = firstPurchases.value
  /* Held back when too few to show: no figure, and the note says why. */
  const buyers = bought ? totals.buyers ?? null : null
  const held = bought && totals.buyers == null ? ` Held back: fewer than ${revenue.suppression_threshold} players, too few to show.` : ''
  // Nine, in two rows: how much was bought and for what, then who bought it and what went wrong.
  return [
    { key: 'purchases', label: 'Purchases', value: seen.value ? writeFigure(totals.purchases, { compact: true }) : null, exact: seen.value ? writeFigure(totals.purchases) : undefined, trend: comparable ? trendOf(totals.purchases, totals.previous_purchases) : undefined, info: 'Purchases that count. Test purchases and receipts turned away are not.' },
    { key: 'sales', label: 'Sales', value: any ? writeMoney(gross, usd.value) : null, exact: any ? writeExactMoney(gross, usd.value) : undefined, trend: comparable ? trendOf(gross, previousGross) : undefined, info: `What players paid for them, ${currencyWords(revenue)}, before refunds and the stores' share.` },
    { key: 'average', label: 'Average purchase', value: bought ? writeExactMoney(average, usd.value) : null, trend: comparable ? trendOf(average, previousAverage) : undefined, info: `What a purchase is worth, ${currencyWords(revenue)}.` },
    { key: 'buyers', label: 'Buyers', value: buyers === null ? null : writeFigure(buyers, { compact: true }), exact: buyers === null ? undefined : writeFigure(buyers), info: `Different players with at least one purchase${revenue.approximate ? ', counted approximately' : ''}.${held}` },
    { key: 'per-buyer', label: 'Purchases per buyer', value: buyers ? (totals.purchases / buyers).toFixed(1) : null, info: `How often a buyer came back to the shop in the range.${held}` },
    { key: 'first', label: 'First purchases', value: first === null ? null : writeFigure(first, { compact: true }), exact: first === null ? undefined : writeFigure(first), info: 'Players whose first purchase ever was in the range: new buyers. Not narrowed by the filters.' },
    { key: 'refunded', label: 'Refunded', value: bought ? formatShare(totals.refunds / totals.purchases) : null, info: `${writeFigure(totals.refunds)} purchases given back by the stores or through players' banks. Verified stores only.` },
    { key: 'rejected', label: 'Turned away', value: attempts ? formatShare(totals.rejected / attempts) : null, info: `${writeFigure(totals.rejected)} receipts that did not check out with the store: forged, replayed or altered. Nothing was granted.` },
    { key: 'sandbox', label: 'Test purchases', value: seen.value ? writeFigure(totals.sandbox, { compact: true }) : null, exact: seen.value ? writeFigure(totals.sandbox) : undefined, info: 'Made by TestFlight and license-tester accounts: verified, granted in the game, and kept out of revenue.' }
  ]
})

// --- What, where ------------------------------------------------------------------------------

const productLines = computed(() => linesOf(data.value?.products ?? []))

const countryRows = computed<ShareRow[]>(() => {
  const lines = linesOf(data.value?.countries ?? [])
  const total = lines.reduce((sum, line) => sum + line.gross, 0) || 1
  return lines.slice(0, 12).map(line => ({
    key: line.key,
    label: countryName(line.key),
    value: line.gross,
    display: writeMoney(line.gross, usd.value),
    note: `${formatShare(line.gross / total)} · ${writeFigure(line.purchases, { compact: true })} purchases · ${writeExactMoney(line.gross / Math.max(line.purchases, 1), usd.value)} each`,
    mark: { kind: 'country', code: line.key }
  }))
})

const storeRows = computed<ShareRow[]>(() => {
  const slices = data.value?.stores ?? []
  const total = slices.reduce((sum, slice) => sum + slice.purchases, 0) || 1
  return slices.filter(slice => slice.purchases).map(slice => ({
    key: slice.key,
    label: storeLook(slice.key).label,
    value: slice.purchases,
    display: formatShare(slice.purchases / total),
    note: `${writeFigure(slice.purchases, { compact: true })} purchases`,
    mark: { kind: 'platform', code: storeLook(slice.key).platform }
  }))
})

// --- When, and at what price ----------------------------------------------------------------

const pricePoints = computed(() => (data.value?.price_points ?? []).slice(0, 16))
const priceCategories = computed(() => pricePoints.value.map(point => writeAmount(point.price_micros, point.currency)))
const priceSeries = computed<MetricSeries[]>(() => [{
  name: 'Purchases',
  data: pricePoints.value.map(point => point.purchases),
  type: 'bar',
  tone: 1
}])

const week = computed(() => data.value?.heatmap ?? [])

// --- The ledger -----------------------------------------------------------------------------

const query = ref('')
const statuses = useQueryList('status', PURCHASE_STATE_KEYS)

const ledger = usePurchaseLedger(period, filterQuery, statuses)

const LEDGER_FILTERS: FilterGroup[] = [
  { key: 'status', label: 'What happened', options: PURCHASE_STATE_KEYS.map(status => ({ value: status, label: PURCHASE_STATES[status].label })) }
]

const ledgerFilters = computed<Record<string, string[]>>({
  get: () => ({ status: statuses.value }),
  set: (value) => {
    statuses.value = value.status ?? []
  }
})

const ledgerActive = computed<ActiveFilter[]>(() => [
  ...statuses.value.map(status => ({ key: `status:${status}`, label: purchaseState(status).label })),
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

/* The search narrows the purchases read so far; the filters above go to the API. */
const rows = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return ledger.purchases.value
  return ledger.purchases.value.filter(record => [record.event_id, record.player_id ?? '', record.install_id, record.store_transaction_id ?? '', record.product_id]
    .some(value => value.toLowerCase().includes(needle)))
})

const COLUMNS: DataTableColumn<PurchaseRow>[] = [
  { key: 'product_id', title: 'Package', fixed: true, minWidth: 220 },
  { key: 'at', title: 'When', minWidth: 150, value: record => Date.parse(record.at) },
  { key: 'player_id', title: 'Player', minWidth: 170, mono: true, value: record => record.player_id ?? record.install_id },
  { key: 'country', title: 'Country', minWidth: 150, value: record => (record.country ? countryName(record.country) : '') },
  { key: 'store', title: 'Store', value: record => storeLook(record.store).label },
  { key: 'usd', title: 'Paid', align: 'end', value: record => record.usd ?? record.revenue_micros / 1_000_000 },
  { key: 'status', title: 'What happened', value: record => PURCHASE_STATE_KEYS.indexOf((record.status || 'reported') as PurchaseState), skeleton: 'pill' },
  { key: 'store_transaction_id', title: 'Store transaction', mono: true, hidden: true },
  { key: 'open', title: '', sortable: false, width: 48 }
]

const columns = useLocalStorage('playersense.purchases.columns.v2', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'at', order: 'desc' }])

const now = useNow({ interval: 60_000 })
const whenFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

const opened = shallowRef<PurchaseRow | null>(null)
const openedModel = computed({
  get: () => opened.value !== null,
  set: (value) => {
    if (!value) opened.value = null
  }
})

function downloadLedger() {
  const header = ['event id', 'purchased at', 'player', 'install', 'package', 'store', 'country', 'amount', 'currency', 'usd', 'status', 'store transaction id', 'first purchase', 'refunded at', 'refund reason', 'rejection']
  const lines = rows.value.map(record => [
    record.event_id,
    record.at,
    record.player_id ?? '',
    record.install_id,
    record.product_id,
    record.store,
    record.country ?? '',
    record.revenue_micros / 1_000_000,
    record.currency,
    record.usd ?? '',
    record.status || 'reported',
    record.store_transaction_id ?? '',
    record.first_purchase ? 'yes' : 'no',
    record.refunded_at ?? '',
    record.refund_reason ?? '',
    record.rejection ?? ''
  ])
  downloadCsv(`purchases-${period.value.from}-to-${period.value.to}`, [header, ...lines])
}

/* No project yet: nothing to ask the API about, so the page says so instead of drawing zeros. */
const workspace = useWorkspaceStore()
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader description="Every purchase the players made: what they bought, where, through which store, and what happened to each one." title="Purchases" />

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

    <LoadState
      :error="error"
      :loading="loading"
      what="purchases"
      @retry="reload"
    >
      <LayerCard v-if="!workspace.projectPath" :padded="false">
        <EmptyState
          description="Make a project from the project menu and send events with its SDK key: its purchases show here."
          icon="material-symbols:sports-esports-outline"
          title="No project yet"
        />
      </LayerCard>
      <template v-else-if="data">
        <KpiBoard
          :description="`Purchases in ${periodWords}, against the same number of days before.`"
          :items="board"
          :loading="pending"
          title="Purchases"
        />

        <!-- Which packages carry the shop: by how often they are bought, and by what they earn. -->
        <SectionCard :padded="false" description="Each package's share of every purchase beside its share of every dollar. Packages bought most are rarely the ones that earn most." title="Package weights">
          <PackageWeights :lines="productLines" :loading="pending" :usd="usd" />
        </SectionCard>

        <!-- Where: every market in a row of its own, the list flowing into two columns on one scale. -->
        <SectionCard description="Where purchases are made, by revenue, with how many and what a purchase is worth there." title="Sales by country">
          <NoDataPlaceholder
            v-if="!pending && !countryRows.length"
            :height="120"
            :seed="17"
            label="No data yet"
          />
          <ShareList
            v-else
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
            <NoDataPlaceholder
              v-if="!pending && !storeRows.length"
              :height="120"
              :seed="19"
              label="No data yet"
            />
            <ShareList
              v-else
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
            description="How many purchases at each price, in the currency it was paid in."
            title="Price points"
            type="bar"
          />
        </div>

        <SectionCard description="Purchases in each hour of the week, in your own time zone: when to open an offer so it is seen. Not narrowed by the filters." title="When players buy">
          <WeekHeatmap :loading="pending" :values="week" unit="purchases" />
        </SectionCard>
      </template>
    </LoadState>

    <!-- Every purchase, newest first. -->
    <SectionCard :padded="false" description="Every purchase in the range, newest first. Open one for what happened to it." title="All purchases">
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
          placeholder="Player, install, transaction or package"
        />
        <FilterMenu v-model="ledgerFilters" :groups="LEDGER_FILTERS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="rows"
        :loading="ledger.pending.value && !rows.length"
        :empty-description="ledger.error.value ? ledger.error.value.message : 'No purchase matches these filters.'"
        :empty-title="ledger.error.value ? 'Could not load the purchases.' : 'No purchases match.'"
        item-value="event_id"
        label="Purchases"
      >
        <template #cell.product_id="{ item }">
          <button class="group flex min-w-0 cursor-pointer flex-col py-1 text-left" type="button" @click="opened = item">
            <span class="flex items-center gap-1.5">
              <span class="truncate font-mono text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.product_id }}</span>
              <Tag v-if="item.first_purchase" label="First purchase" tone="success" />
            </span>
            <span class="truncate font-mono text-[11px] text-ink/45">{{ item.store_transaction_id || item.event_id }}</span>
          </button>
        </template>
        <template #cell.at="{ item }">
          <span class="flex flex-col">
            <span class="text-xs text-ink/80">{{ whenFormat.format(Date.parse(item.at)) }}</span>
            <span class="text-[11px] text-ink/45">{{ sinceWords(item.at, now) }}</span>
          </span>
        </template>
        <template #cell.player_id="{ item }">
          <NuxtLink v-if="item.player_id" :to="`/analytics/players/${encodeURIComponent(item.player_id)}`" class="block max-w-40 truncate font-mono text-xs text-ink/80 no-underline hover:text-primary">
            {{ item.player_id }}
          </NuxtLink>
          <span v-else class="block max-w-40 truncate font-mono text-xs text-ink/50" title="No player id: the install">{{ item.install_id }}</span>
        </template>
        <template #cell.country="{ item }">
          <span v-if="item.country" class="flex items-center gap-2 text-xs text-ink/80">
            <CountryFlag :code="item.country" />
            {{ countryName(item.country) }}
          </span>
        </template>
        <template #cell.store="{ item }">
          <span class="flex items-center gap-2 text-xs text-ink/80">
            <PlatformIcon :platform="storeLook(item.store).platform" />
            {{ storeLook(item.store).label }}
          </span>
        </template>
        <template #cell.usd="{ item }">
          <span class="flex flex-col items-end">
            <span class="font-mono text-xs text-ink">{{ writeAmount(item.revenue_micros, item.currency) }}</span>
            <span v-if="item.currency !== 'USD' && item.usd != null" class="font-mono text-[11px] text-ink/45">{{ writeExactMoney(item.usd) }}</span>
          </span>
        </template>
        <template #cell.status="{ item }">
          <span class="flex flex-col items-start gap-0.5">
            <StatusBadge :label="purchaseState(item.status).label" :tone="purchaseState(item.status).tone" />
            <span v-if="item.refund_reason" class="text-[11px] text-ink/45">{{ item.refund_reason }}</span>
            <span v-else-if="item.rejection" class="text-[11px] text-ink/45">{{ rejectionLook(item.rejection).label }}</span>
          </span>
        </template>
        <template #cell.open="{ item }">
          <v-btn
            :aria-label="`Open ${item.product_id}, ${whenFormat.format(Date.parse(item.at))}`"
            color="neutral"
            icon="material-symbols:chevron-right"
            size="small"
            variant="text"
            @click="opened = item"
          />
        </template>
      </DataTable>

      <div v-if="ledger.hasMore.value" class="flex justify-center border-t border-line px-5 py-3">
        <v-btn
          :loading="ledger.loadingMore.value"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="ledger.loadMore"
        >
          Load older purchases
        </v-btn>
      </div>
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
