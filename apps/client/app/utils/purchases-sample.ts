// Sample data for Data & commerce > Purchases, generated in the browser. What it stands in for:
//
//   Every row is a verified purchase record (project.md §46–48: platform, store transaction id,
//   product id, amount and currency, the store's answer), which the backend has a schema for and
//   no handler yet. The totals are drawn from the same model as Insights > Revenue
//   (utils/revenue-sample.ts), so a range and a filter give the same purchases on both pages;
//   Revenue reads them as money over time, this page as the purchases themselves.
//
// Seeded by range and filters, so a reload draws the same purchases.

const DAY = 86_400_000
const HOUR = 3_600_000

function seeded(text: string): () => number {
  let state = 0
  for (const char of text) state = (Math.imul(state, 31) + char.charCodeAt(0)) >>> 0
  return () => {
    state = (state + 0x6D2B79F5) >>> 0
    let t = state
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

function pick<T>(entries: readonly (readonly [T, number])[], random: () => number): T {
  const total = entries.reduce((sum, [, weight]) => sum + weight, 0)
  let roll = random() * total
  for (const [value, weight] of entries) {
    roll -= weight
    if (roll <= 0) return value
  }
  return entries[entries.length - 1]![0]
}

const hex = (random: () => number, length: number) => Array.from({ length }, () => Math.floor(random() * 16).toString(16)).join('')

// --- Records ------------------------------------------------------------------------------

/**
 * What happened to a purchase. Verified is counted; refunded was counted and given back;
 * a test purchase was verified and kept out of revenue; a rejected receipt never counted.
 */
export type PurchaseStatus = 'verified' | 'refunded' | 'sandbox' | 'rejected'

export const PURCHASE_STATUS: Record<PurchaseStatus, { label: string, tone: BadgeTone, note: string }> = {
  verified: { label: 'Verified', tone: 'success', note: 'The store confirmed it, and it counts as revenue.' },
  refunded: { label: 'Refunded', tone: 'warning', note: 'Verified, then given back by the store or the player\'s bank.' },
  sandbox: { label: 'Test', tone: 'neutral', note: 'A TestFlight or license-tester purchase: verified, and kept out of revenue.' },
  rejected: { label: 'Rejected', tone: 'error', note: 'The receipt did not check out with the store. Nothing was granted or counted.' }
}

export const PURCHASE_STATUSES = Object.keys(PURCHASE_STATUS) as PurchaseStatus[]

export interface PurchaseRecord {
  id: string
  at: number
  playerId: string
  store: RevenueStore
  country: string
  productId: string
  /** What the player paid, in USD at the day's rate. */
  usd: number
  /** What the player paid, in their own currency. */
  local: number
  currency: string
  status: PurchaseStatus
  /** The store's own id for it: what makes a retry idempotent. */
  storeTransactionId: string
  /** How long the store took to answer the verification. */
  verifyMs: number
  /** The player's first purchase ever. */
  first: boolean
  refundedAt?: number
  refundReason?: RefundReason
  rejection?: string
}

/** A country's currency, and how many of it a US dollar buys, near enough to draw prices in. */
const CURRENCY: Record<string, readonly [string, number]> = {
  US: ['USD', 1], JP: ['JPY', 150], BR: ['BRL', 5.2], DE: ['EUR', 0.92], FR: ['EUR', 0.92], IT: ['EUR', 0.92], ES: ['EUR', 0.92], NL: ['EUR', 0.92],
  GB: ['GBP', 0.79], IN: ['INR', 83], TR: ['TRY', 32], KR: ['KRW', 1350], MX: ['MXN', 17], ID: ['IDR', 15700], CA: ['CAD', 1.36], AU: ['AUD', 1.52],
  RU: ['RUB', 92], PH: ['PHP', 56], VN: ['VND', 24500], TH: ['THB', 36], PL: ['PLN', 4], SA: ['SAR', 3.75], AR: ['ARS', 870], CO: ['COP', 3900]
}

export const currencyOf = (country: string) => CURRENCY[country] ?? ['USD', 1] as const

/** A price in a currency, the way a store's receipt writes it: "¥1,500", "R$25.90". */
export function writeLocal(amount: number, currency: string): string {
  const whole = ['JPY', 'KRW', 'IDR', 'VND', 'COP', 'ARS'].includes(currency)
  return new Intl.NumberFormat('en', { style: 'currency', currency, maximumFractionDigits: whole ? 0 : 2, minimumFractionDigits: whole ? 0 : 2 }).format(amount)
}

/* Stores set local prices on tiers, not by converting: a price ends on .99, or rounds for the big currencies. */
function localPrice(usd: number, rate: number, currency: string): number {
  const raw = usd * rate
  if (['JPY', 'KRW', 'IDR', 'VND', 'COP', 'ARS'].includes(currency)) {
    const step = raw >= 10_000 ? 1000 : raw >= 1000 ? 100 : 10
    return Math.max(step, Math.round(raw / step) * step)
  }
  return Math.max(0.99, Math.floor(raw) + 0.99)
}

/** Purchases in each hour of the week, by the player's own clock: evenings, and more at weekends. */
const HOUR_PROFILE = Array.from({ length: 24 }, (_, hour) => 0.25 + Math.exp(-((hour - 20.5) ** 2) / 14) + 0.45 * Math.exp(-((hour - 13) ** 2) / 8))

// --- The sample ---------------------------------------------------------------------------

export interface PackageWeight {
  product: RevenueProduct
  purchases: number
  gross: number
  /** Its share of every purchase. */
  purchaseShare: number
  /** Its share of every dollar. */
  revenueShare: number
  buyers: number
}

export interface CountrySales {
  code: string
  purchases: number
  gross: number
  buyers: number
  share: number
  /** Its most bought package there. */
  topProduct: string
}

export interface PurchasesSample {
  total: { purchases: number, buyers: number, gross: number, refunded: number, sandbox: number, rejected: number, firstPurchases: number, repeatBuyers: number }
  previous: { purchases: number, gross: number }
  packages: PackageWeight[]
  countries: CountrySales[]
  /** Seven rows, Monday first, of 24 hours each. */
  week: number[][]
  pricePoints: { price: number, purchases: number, gross: number }[]
  /** The newest purchases, newest first: the ledger's page of records. */
  ledger: PurchaseRecord[]
}

export function samplePurchases(window: RevenueWindow, filters: RevenueFilters): PurchasesSample {
  const revenue = sampleRevenue(window, filters)
  const random = seeded(`purchases:${window.days}@${window.end}:${JSON.stringify(filters)}`)
  // The ledger runs back from the end of the window, or from now when the window reaches today.
  const now = Math.min(Date.now(), window.end)
  const days = window.days

  const purchases = revenue.total.transactions
  const gross = revenue.total.gross
  const packages: PackageWeight[] = revenue.products
    .filter(line => line.transactions > 0)
    .map(line => ({
      product: line.product,
      purchases: line.transactions,
      gross: line.gross,
      purchaseShare: line.transactions / Math.max(purchases, 1),
      revenueShare: line.gross / Math.max(gross, 1),
      buyers: line.buyers
    }))
    .sort((a, b) => b.gross - a.gross)

  const countries: CountrySales[] = revenue.countries.map((line) => {
    const byProduct = revenue.products.map(product => ({ id: product.product.id, gross: product.byCountry.find(entry => entry.code === line.key)?.gross ?? 0 }))
    const top = byProduct.sort((a, b) => b.gross - a.gross)[0]
    return { code: line.key, purchases: line.transactions, gross: line.gross, buyers: line.payers, share: line.gross / Math.max(gross, 1), topProduct: top?.id ?? '' }
  })

  const week = Array.from({ length: 7 }, (_, weekday) => HOUR_PROFILE.map(weight => weight * (weekday >= 5 ? 1.3 : weekday === 4 ? 1.1 : 1)))
  const weekTotal = week.flat().reduce((sum, value) => sum + value, 0)
  const scaledWeek = week.map(row => row.map(value => Math.round(value / weekTotal * purchases * (0.9 + random() * 0.2))))

  const prices = new Map<number, { price: number, purchases: number, gross: number }>()
  for (const line of revenue.products) {
    const entry = prices.get(line.product.price) ?? { price: line.product.price, purchases: 0, gross: 0 }
    entry.purchases += line.transactions
    entry.gross += line.gross
    prices.set(line.product.price, entry)
  }

  const sandbox = Math.round(revenue.verification.sandbox)
  const rejected = revenue.verification.rejected.reduce((sum, entry) => sum + entry.count, 0)

  // The ledger: the newest purchases, walking back from now, drawn from the same shares.
  const payers = samplePlayers().filter(player => player.purchases > 0)
  const products = revenue.products.filter(line => line.transactions > 0).map(line => [line.product, line.transactions] as const)
  const stores = STORE_KEYS.filter(store => !filters.store.length || filters.store.includes(store)).map(store => [store, STORES[store].share] as const)
  const countryShares = revenue.countries.filter(line => line.key !== 'Other').map(line => [line.key, line.transactions] as const)
  const rejectionShares = revenue.verification.rejected.map(entry => [entry.reason, Math.max(entry.count, 0.1)] as const)
  const perHour = Math.max(purchases / (days * 24), 0.5)
  const count = Math.min(600, Math.round(purchases))
  const ledger: PurchaseRecord[] = []
  let at = now - random() * HOUR / perHour
  for (let index = 0; index < count && products.length && stores.length && countryShares.length; index += 1) {
    const hour = new Date(at).getHours()
    at -= (HOUR / perHour) * (0.3 + random() * 1.4) / (HOUR_PROFILE[hour]! / 0.9)
    if (at < window.end - days * DAY) break
    const product = pick(products, random)
    const store = pick(stores, random)
    const country = pick(countryShares, random)
    const [currency, rate] = currencyOf(country)
    const roll = random()
    const refundRate = product.refundRate * STORES[store].refunds
    const status: PurchaseStatus = roll < 0.0175 ? 'rejected' : roll < 0.0175 + 0.011 ? 'sandbox' : roll < 0.0285 + refundRate * 2.5 ? 'refunded' : 'verified'
    const player = payers[Math.floor(random() * payers.length)]
    const refundedAt = status === 'refunded' ? Math.min(now, at + (0.2 + random() * 6) * DAY) : undefined
    ledger.push({
      id: `pur_${hex(random, 10)}`,
      at,
      playerId: player?.id ?? `u_${100_000 + Math.floor(random() * 900_000)}`,
      store,
      country,
      productId: product.id,
      usd: product.price,
      local: localPrice(product.price, rate, currency),
      currency,
      status,
      storeTransactionId: store === 'app_store'
        ? `2000000${Math.floor(random() * 1e9).toString().padStart(9, '0')}`
        : store === 'google_play'
          ? `GPA.33${Math.floor(random() * 90 + 10)}-${Math.floor(random() * 9000 + 1000)}-${Math.floor(random() * 9000 + 1000)}-${Math.floor(random() * 90000 + 10000)}`
          : store === 'steam'
            ? `${Math.floor(random() * 9e15 + 1e15)}`
            : `pi_${hex(random, 24)}`,
      verifyMs: Math.round(STORES[store].verifyMs * (0.5 + random() * 1.3)),
      first: random() < product.first * 1.6,
      ...(refundedAt ? { refundedAt, refundReason: pick(REFUND_REASONS.map(reason => [reason, reason === 'Customer request' ? 4 : reason === 'Accidental purchase' ? 3 : reason === 'Chargeback' ? product.chargebacks * 10 : 1] as const), random) } : {}),
      ...(status === 'rejected' ? { rejection: pick(rejectionShares, random) } : {})
    })
  }

  return {
    total: {
      purchases,
      buyers: revenue.payers.total,
      gross,
      refunded: revenue.total.refunded,
      sandbox,
      rejected,
      firstPurchases: revenue.payers.newPayers,
      // Buyers who came back: the more purchases a buyer makes on average, the fewer stop at one.
      repeatBuyers: revenue.payers.total * (1 - 1 / Math.max(purchases / Math.max(revenue.payers.total, 1), 1) ** 0.8)
    },
    previous: { purchases: revenue.previous.transactions, gross: revenue.previous.gross },
    packages,
    countries,
    week: scaledWeek,
    pricePoints: [...prices.values()].sort((a, b) => a.price - b.price),
    ledger
  }
}

/**
 * A purchase as the API will hand it over: in the purchases list, and in the body of the
 * `purchase.verified` webhook (§49). The shape a studio's own backend reads to grant an item.
 */
export function purchaseDocument(record: PurchaseRecord, product: RevenueProduct | undefined) {
  return {
    id: record.id,
    status: record.status,
    player: { id: record.playerId, country: record.country },
    product: { id: record.productId, name: product?.name ?? record.productId, kind: product?.kind ?? null },
    price: { amount: record.local, currency: record.currency, usd: record.usd },
    store: { name: record.store, transaction_id: record.storeTransactionId, verified_in_ms: record.verifyMs },
    environment: record.status === 'sandbox' ? 'sandbox' : 'production',
    first_purchase: record.first,
    purchased_at: new Date(record.at).toISOString(),
    ...(record.refundedAt ? { refund: { at: new Date(record.refundedAt).toISOString(), reason: record.refundReason } } : {}),
    ...(record.rejection ? { rejection: record.rejection } : {})
  }
}
