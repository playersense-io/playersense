import type { Schema } from '@playersense/contracts'
import type { MaybeRefOrGetter } from 'vue'

// Revenue and purchases from the API, for Insights > Revenue and Purchases: the filters both keep
// in the URL, the revenue answer (…/analytics/{env}/revenue), the purchases ledger a page at a
// time (…/purchases), how net is estimated (the organization's revenue settings), and the
// project's store connections that make purchases verified.

type Range = { from: string, to: string }

/** A value safe to carry in a query list: a store, a country code, a product id. */
const SAFE = /^[\w.:-]{1,120}$/

/**
 * The store, country and package filters, kept in the URL as ?store=&country=&package=. Any
 * value the API could name is kept: the lists come from the answers, not from the page.
 */
export function useRevenueFilters() {
  const route = useRoute()
  const accepted = (key: string) => () => String(route.query[key] ?? '').split(',').filter(value => SAFE.test(value))

  const stores = useQueryList('store', accepted('store'))
  const countries = useQueryList('country', accepted('country'))
  const products = useQueryList('package', accepted('package'))

  const filters = computed<Record<string, string[]>>({
    get: () => ({ store: stores.value, country: countries.value, product: products.value }),
    set: (value) => {
      stores.value = value.store ?? []
      countries.value = value.country ?? []
      products.value = value.product ?? []
    }
  })

  /** The filters as the API's query takes them. */
  const query = computed(() => ({ store: stores.value, country: countries.value, product: products.value }))

  function remove(key: string) {
    const split = key.indexOf(':')
    const group = key.slice(0, split)
    const value = key.slice(split + 1)
    filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(entry => entry !== value) }
  }

  function clear() {
    filters.value = { store: [], country: [], product: [] }
  }

  return { stores, countries, products, filters, query, remove, clear }
}

/**
 * The revenue answer for a range and filters, read again whenever either changes. `when` holds
 * the read back until it is true: a dialog reads only once it has something to show.
 */
export function useRevenue(range: MaybeRefOrGetter<Range>, filters: MaybeRefOrGetter<Record<string, readonly string[]>>, when: MaybeRefOrGetter<boolean> = true) {
  const workspace = useWorkspaceStore()
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
  const answer = useApiGet<RevenueRecord>(() => toValue(when) && workspace.analyticsPath
    ? withQuery(`${workspace.analyticsPath}/revenue`, { ...toValue(range), ...toValue(filters), tz })
    : null)

  return {
    data: answer.data,
    /** Reading, the first time or again: the figures draw as bones. */
    pending: computed(() => answer.isFetching.value),
    /** Nothing to show yet. */
    loading: computed(() => answer.isFetching.value && !answer.data.value),
    error: computed(() => answer.error.value as ApiError | null),
    reload: () => answer.execute()
  }
}

/**
 * The purchases in a range, newest first, 100 at a time: the first page is read with the page and
 * again when the range, the filters or the statuses change; older pages are added on asking.
 */
export function usePurchaseLedger(range: MaybeRefOrGetter<Range>, filters: MaybeRefOrGetter<Record<string, readonly string[]>>, statuses: MaybeRefOrGetter<readonly string[]>) {
  const workspace = useWorkspaceStore()
  const url = computed(() => workspace.analyticsPath && withQuery(`${workspace.analyticsPath}/purchases`, { ...toValue(range), ...toValue(filters), status: toValue(statuses), limit: 100 }))

  const first = useApiGet<Schema<'PurchasesOut'>>(url)
  const older = ref<PurchaseRow[]>([])
  const next = ref<string | undefined>()
  const loadingMore = ref(false)

  watch(first.data, (out) => {
    older.value = []
    next.value = out?.next || undefined
  })

  const purchases = computed(() => [...(first.data.value?.purchases ?? []), ...older.value])

  async function loadMore() {
    if (!url.value || !next.value || loadingMore.value) return
    loadingMore.value = true
    try {
      const out = await apiRequest<Schema<'PurchasesOut'>>('GET', `${url.value}&before=${encodeURIComponent(next.value)}`)
      older.value = [...older.value, ...(out.purchases ?? [])]
      next.value = out.next || undefined
    } finally {
      loadingMore.value = false
    }
  }

  return {
    purchases,
    pending: computed(() => first.isFetching.value),
    error: computed(() => first.error.value as ApiError | null),
    hasMore: computed(() => Boolean(next.value)),
    loadingMore,
    loadMore,
    reload: () => first.execute()
  }
}

/** How net revenue is estimated: each store's share, the organization's to set. */
export function useRevenueSettings() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.orgPath && `${workspace.orgPath}/revenue-settings`)
  const answer = useApiGet<Schema<'RevenueSettingsOut'>>(path)

  /** Each store's share in percent; null puts a store back on its usual rate. */
  async function save(fees: Record<string, number | null>) {
    if (!path.value) return
    await apiRequest('PUT', path.value, { store_fees: fees })
    await answer.execute()
  }

  return {
    settings: answer.data,
    loading: computed(() => answer.isFetching.value && !answer.data.value),
    error: computed(() => answer.error.value as ApiError | null),
    reload: () => answer.execute(),
    save
  }
}

export type StoreConnection = Schema<'StoreOut'>

/** The project's App Store, Google Play and Steam connections, and every change to them. */
export function useStoreConnections() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.projectPath && `${workspace.projectPath}/stores`)
  const answer = useApiGet<Schema<'StoreListOut'>>(path)

  const stores = computed<StoreConnection[]>(() => [...(answer.data.value?.stores ?? [])])

  async function reload() {
    await answer.execute()
  }

  /** Connects a store or changes how it is reached. A secret left out is kept. */
  async function save(store: string, body: Record<string, unknown>) {
    await apiRequest('PUT', `${path.value}/${store}`, body)
    await reload()
  }

  async function disconnect(store: string) {
    await apiRequest('DELETE', `${path.value}/${store}`)
    await reload()
  }

  /** A new notification URL; the old one stops being accepted. */
  async function rotateUrl(store: string) {
    await apiRequest('POST', `${path.value}/${store}/notification-url`)
    await reload()
  }

  async function test(store: string) {
    const out = await apiRequest<Schema<'StoreTestOut'>>('POST', `${path.value}/${store}/test`)
    await reload()
    return out
  }

  return {
    stores,
    loading: computed(() => answer.isFetching.value && !answer.data.value),
    error: computed(() => answer.error.value as ApiError | null),
    reload,
    save,
    disconnect,
    rotateUrl,
    test
  }
}
