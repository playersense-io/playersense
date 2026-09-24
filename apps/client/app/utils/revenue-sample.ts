// Sample data for the Revenue page, generated in the browser. What it stands in for:
//
//   Revenue, purchases and payers over time, split by platform, country or version, are
//   what POST …/analytics/production/metrics/query answers today (metric revenue,
//   purchases, payers; project.md §3.5, revenue_1d). Everything by package and store, the
//   refunds, the store fees and the verification outcomes need the verified purchase
//   record (§46–48: platform, store_transaction_id, product_id, amount, currency), which is
//   a schema without a handler yet; this draws what it will hold. Amounts are USD, as they
//   will be once each purchase is converted at the day's rate — which is not built either.
//
// Seeded by range and filters, so a reload draws the same money.

export type RevenueRange = '7d' | '30d' | '90d'
export type RevenueStore = 'app_store' | 'google_play' | 'steam' | 'web_shop'
export type ProductKind = 'bundle' | 'consumable' | 'season_pass' | 'subscription' | 'non_consumable'
export type RevenueDimension = 'store' | 'country' | 'product'
export type RevenueFilters = Record<RevenueDimension, string[]>

export const REVENUE_RANGE_DAYS: Record<RevenueRange, number> = { '7d': 7, '30d': 30, '90d': 90 }

/** Any stretch of whole days: how many, and the start of the day after the last one (UTC). */
export interface RevenueWindow {
  days: number
  end: number
}

/** A named range as the days it covers, ending today. */
const windowOf = (range: RevenueRange | RevenueWindow, today: number): RevenueWindow =>
  typeof range === 'string' ? { days: REVENUE_RANGE_DAYS[range], end: today } : range

/**
 * Where a purchase is made: the store's name, the platform its mark is drawn for, what it
 * keeps of each sale, its share of transactions, and how readily it refunds: Google Play
 * lets a player refund themselves for two days, Apple decides for them, and a game's own
 * web shop refunds only what it chooses to. The web shop keeps little more than the card
 * processor's cut.
 */
export const STORES: Record<RevenueStore, { label: string, platform: string, fee: number, share: number, verifyMs: number, refunds: number }> = {
  app_store: { label: 'App Store', platform: 'iOS', fee: 0.3, share: 0.47, verifyMs: 420, refunds: 0.8 },
  google_play: { label: 'Google Play', platform: 'Android', fee: 0.3, share: 0.41, verifyMs: 310, refunds: 1.3 },
  steam: { label: 'Steam', platform: 'Steam', fee: 0.3, share: 0.09, verifyMs: 650, refunds: 1.1 },
  web_shop: { label: 'Web shop', platform: 'web', fee: 0.05, share: 0.03, verifyMs: 120, refunds: 0.5 }
}

export const STORE_KEYS = Object.keys(STORES) as RevenueStore[]

export const KIND_LABELS: Record<ProductKind, string> = {
  bundle: 'Bundle',
  consumable: 'Consumable',
  season_pass: 'Season pass',
  subscription: 'Subscription',
  non_consumable: 'Unlock'
}

export interface RevenueProduct {
  id: string
  name: string
  kind: ProductKind
  price: number
  /** Its share of transactions, before the others are normalised against it. */
  weight: number
  /** The share of its sales refunded. */
  refundRate: number
  /** Of refunds, the share that are chargebacks: the costly kind, with a fee on top. */
  chargebacks: number
  /** Its share of first purchases: which package turns a player into a payer. */
  first: number
  /** On sale only between these days ago, for a limited offer. */
  window?: readonly [number, number]
}

/** The shop of a free-to-play game: a starter pack, gems in tiers, a pass, a club, unlocks, an event offer. */
export const PRODUCTS: readonly RevenueProduct[] = [
  { id: 'starter_pack', name: 'Starter pack', kind: 'bundle', price: 1.99, weight: 0.15, refundRate: 0.006, chargebacks: 0.05, first: 0.44 },
  { id: 'gems_small', name: 'Pouch of gems', kind: 'consumable', price: 4.99, weight: 0.22, refundRate: 0.018, chargebacks: 0.08, first: 0.17 },
  { id: 'gems_medium', name: 'Sack of gems', kind: 'consumable', price: 9.99, weight: 0.13, refundRate: 0.022, chargebacks: 0.1, first: 0.07 },
  { id: 'gems_large', name: 'Chest of gems', kind: 'consumable', price: 19.99, weight: 0.08, refundRate: 0.028, chargebacks: 0.14, first: 0.03 },
  { id: 'gems_vault', name: 'Vault of gems', kind: 'consumable', price: 99.99, weight: 0.012, refundRate: 0.045, chargebacks: 0.35, first: 0.004 },
  { id: 'season_pass', name: 'Season pass', kind: 'season_pass', price: 9.99, weight: 0.12, refundRate: 0.012, chargebacks: 0.06, first: 0.09 },
  { id: 'vip_monthly', name: 'VIP club, monthly', kind: 'subscription', price: 4.99, weight: 0.1, refundRate: 0.015, chargebacks: 0.05, first: 0.06 },
  { id: 'remove_ads', name: 'No ads', kind: 'non_consumable', price: 2.99, weight: 0.06, refundRate: 0.01, chargebacks: 0.04, first: 0.08 },
  { id: 'coin_doubler', name: 'Coin doubler', kind: 'non_consumable', price: 6.99, weight: 0.03, refundRate: 0.012, chargebacks: 0.05, first: 0.02 },
  { id: 'halloween_bundle', name: 'Halloween bundle', kind: 'bundle', price: 14.99, weight: 0.09, refundRate: 0.02, chargebacks: 0.08, first: 0.04, window: [12, 5] }
]

export const REFUND_REASONS = ['Customer request', 'Accidental purchase', 'Item not received', 'Chargeback', 'Fraud'] as const
export type RefundReason = typeof REFUND_REASONS[number]

/** Why the SDK's verification turned a receipt away. None of these reach revenue. */
export const REJECTIONS = [
  { reason: 'Invalid receipt', note: 'Forged or malformed: the store does not recognise it', rate: 0.009 },
  { reason: 'Replayed transaction', note: 'A transaction id already granted once, sent again', rate: 0.005 },
  { reason: 'Signature mismatch', note: 'The receipt was altered after the store signed it', rate: 0.002 },
  { reason: 'Store unreachable', note: 'Retried until the store answered; counted once it did', rate: 0.0015 }
] as const

/** Test purchases — TestFlight, license testers — verified, and kept out of revenue. */
const SANDBOX_RATE = 0.011

/** Spend over the range, in bands a monetisation team talks in. */
export const SPENDER_TIERS = [
  { tier: 'Minnows', band: 'Under $5', payers: 0.52, revenue: 0.09 },
  { tier: 'Dolphins', band: '$5 – $50', payers: 0.38, revenue: 0.33 },
  { tier: 'Whales', band: '$50 – $500', payers: 0.09, revenue: 0.38 },
  { tier: 'Top whales', band: 'Over $500', payers: 0.01, revenue: 0.2 }
] as const

const DAY = 86_400_000

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

/** A country's pull on revenue: its installs, and how much more its payers spend. */
const COUNTRIES = COHORT_DIMENSIONS.install_country.values.map(([code, share, quality]) => ({ code, share, spend: share * quality ** 2 }))
const SPEND_TOTAL = COUNTRIES.reduce((sum, country) => sum + country.spend, 0)

export interface Money {
  gross: number
  refunds: number
  fees: number
  net: number
  transactions: number
  refunded: number
}

const zero = (): Money => ({ gross: 0, refunds: 0, fees: 0, net: 0, transactions: 0, refunded: 0 })

function add(into: Money, gross: number, transactions: number, refundRate: number, fee: number) {
  const refunded = transactions * refundRate
  const refunds = refunded * (gross / Math.max(transactions, 1e-9))
  const fees = (gross - refunds) * fee
  into.gross += gross
  into.transactions += transactions
  into.refunded += refunded
  into.refunds += refunds
  into.fees += fees
  into.net += gross - refunds - fees
}

export interface ProductLine extends Money {
  product: RevenueProduct
  buyers: number
  firstPurchases: number
  previousGross: number
  daily: number[]
  byStore: Record<RevenueStore, number>
  byCountry: { code: string, gross: number }[]
  reasons: { reason: RefundReason, count: number }[]
}

/** One store's or country's part: its money, the players who paid there, and its gross the range before. */
export interface BreakdownLine extends Money {
  key: string
  payers: number
  previousGross: number
}

/**
 * Where refunds run above or below the average: chargebacks and card fraud cluster in some
 * markets, and players in others rarely ask for their money back.
 */
const REFUND_BIAS: Record<string, number> = { BR: 1.6, ID: 1.8, VN: 1.9, PH: 1.5, IN: 1.7, MX: 1.4, RU: 1.5, CO: 1.4, AR: 1.3, JP: 0.6, KR: 0.7, DE: 0.8 }

export interface RevenueSample {
  days: number[]
  total: Money
  previous: Money
  daily: (Money & { byStore: Record<RevenueStore, Money> })[]
  products: ProductLine[]
  /** Each store's money, payers and the range before. */
  stores: BreakdownLine[]
  /** Each country's, the biggest first. */
  countries: BreakdownLine[]
  payers: { total: number, newPayers: number, previousTotal: number, daily: { new: number, returning: number }[] }
  /** Player-days in the range: what ARPDAU divides by. */
  activePlayers: number
  previousActivePlayers: number
  /** Different players active in the range: what payer conversion divides by. */
  uniquePlayers: number
  refunds: {
    reasons: { reason: RefundReason, count: number, amount: number }[]
    recent: { id: string, product: string, store: RevenueStore, country: string, amount: number, purchasedAt: number, refundedAt: number, reason: RefundReason }[]
    medianDays: number
  }
  verification: {
    verified: number
    sandbox: number
    rejected: { reason: string, note: string, count: number }[]
    daily: { verified: number, rejected: number }[]
    byStore: { store: RevenueStore, verified: number, rejected: number, medianMs: number }[]
  }
}

function keep<T extends string>(all: readonly T[], chosen: readonly string[]): T[] {
  return chosen.length ? all.filter(entry => chosen.includes(entry)) : [...all]
}

/**
 * One range of the shop, ending on `end`. Limited offers are placed against the real
 * today, so a Halloween bundle on sale this month is new against the month before rather
 * than on sale in both; `scale` sets how the range compares, for a game that is growing.
 */
function draw(range: RevenueRange | RevenueWindow, filters: RevenueFilters, end: number, today: number, salt: string, scale = 1) {
  const days = windowOf(range, today).days
  const random = seeded(`revenue:${salt}:${typeof range === 'string' ? range : `${range.days}@${range.end}`}:${JSON.stringify(filters)}`)
  const stores = keep(STORE_KEYS, filters.store)
  const products = PRODUCTS.filter(product => !filters.product.length || filters.product.includes(product.id))
  const countries = COUNTRIES.filter(country => !filters.country.length || filters.country.includes(country.code))
  const countryPull = countries.reduce((sum, country) => sum + country.spend, 0) / SPEND_TOTAL
  const installPull = countries.reduce((sum, country) => sum + country.share, 0)
  const storePull = stores.reduce((sum, store) => sum + STORES[store].share, 0)
  const weightTotal = PRODUCTS.reduce((sum, product) => sum + product.weight, 0)

  const starts = Array.from({ length: days }, (_, index) => end - (days - index) * DAY)
  const daily: (Money & { byStore: Record<RevenueStore, Money> })[] = []
  const lines = new Map(products.map(product => [product.id, {
    money: zero(),
    daily: starts.map(() => 0),
    byStore: Object.fromEntries(STORE_KEYS.map(store => [store, 0])) as Record<RevenueStore, number>
  }]))

  starts.forEach((at, index) => {
    const ago = Math.round((today - at) / DAY)
    const weekend = [0, 6].includes(new Date(at).getUTCDay()) ? 1.25 : 1
    const base = 1_150 * scale * weekend * (0.9 + random() * 0.2) * (1 + index / days * 0.06) * countryPull * storePull
    const day = { ...zero(), byStore: Object.fromEntries(STORE_KEYS.map(store => [store, zero()])) as Record<RevenueStore, Money> }
    for (const product of products) {
      const onSale = !product.window || (ago <= product.window[0] && ago >= product.window[1])
      if (!onSale) continue
      const line = lines.get(product.id)!
      // The event offer draws buyers to the shop: everything sells a little more while it runs.
      const eventLift = PRODUCTS.some(entry => entry.window && ago <= entry.window[0] && ago >= entry.window[1]) ? 1.1 : 1
      for (const store of stores) {
        const transactions = base * (product.weight / weightTotal) * (STORES[store].share / storePull) * eventLift * (0.85 + random() * 0.3)
        const gross = transactions * product.price
        const refundRate = product.refundRate * STORES[store].refunds
        add(day, gross, transactions, refundRate, STORES[store].fee)
        add(day.byStore[store], gross, transactions, refundRate, STORES[store].fee)
        add(line.money, gross, transactions, refundRate, STORES[store].fee)
        line.daily[index]! += gross
        line.byStore[store] += gross
      }
    }
    daily.push(day)
  })

  const total = daily.reduce((sum, day) => {
    for (const key of ['gross', 'refunds', 'fees', 'net', 'transactions', 'refunded'] as const) sum[key] += day[key]
    return sum
  }, zero())

  return { starts, daily, lines, total, products, countries, stores, installPull, storePull, random, days }
}

export function sampleRevenue(range: RevenueRange | RevenueWindow, filters: RevenueFilters): RevenueSample {
  const today = Math.floor(Date.now() / DAY) * DAY
  const window = windowOf(range, today)
  const now = draw(window, filters, window.end, today, 'now')
  const before = draw(window, filters, window.end - window.days * DAY, today, 'before', 0.93)
  const { random, days } = now

  // Payers repeat, and more over a longer range: about 2.7 purchases each in a week, 3.8 in
  // a month — whales buying again pull the average up, so payers are a few percent of
  // players, as in a game that earns this.
  const perPayer = 1.1 + 0.8 * Math.log(days)
  const payersTotal = now.total.transactions / perPayer
  const newPayers = payersTotal * (0.34 + random() * 0.06)

  const products: ProductLine[] = now.products.map((product) => {
    const line = now.lines.get(product.id)!
    const previous = before.lines.get(product.id)
    const reasons = REFUND_REASONS.map((reason) => {
      const share = reason === 'Chargeback'
        ? product.chargebacks
        : reason === 'Fraud' ? product.chargebacks * 0.4 : (1 - product.chargebacks * 1.4) * ({ 'Customer request': 0.62, 'Accidental purchase': 0.24, 'Item not received': 0.14 } as Record<string, number>)[reason]!
      return { reason, count: line.money.refunded * share }
    })
    // Big packs skew to the markets whose payers spend most.
    const skew = product.price >= 19.99 ? 1.25 : 1
    const byCountry = now.countries
      .map(country => ({ code: country.code, gross: line.money.gross * (country.spend ** skew) }))
    const skewTotal = byCountry.reduce((sum, entry) => sum + entry.gross, 0) || 1
    return {
      ...line.money,
      product,
      buyers: line.money.transactions / (product.kind === 'consumable' ? perPayer : 1.02),
      firstPurchases: newPayers * product.first,
      previousGross: previous?.money.gross ?? 0,
      daily: line.daily,
      byStore: line.byStore,
      byCountry: byCountry.map(entry => ({ code: entry.code, gross: entry.gross / skewTotal * line.money.gross })).sort((a, b) => b.gross - a.gross),
      reasons
    }
  }).sort((a, b) => b.gross - a.gross)

  // Each country's part: its spend share of the money, its installs' share of the payers,
  // its own refund rate, and the range before with a little movement of its own.
  const countryTotal = now.countries.reduce((sum, country) => sum + country.spend, 0) || 1
  const refundRate = now.total.transactions ? now.total.refunded / now.total.transactions : 0
  const averagePrice = now.total.transactions ? now.total.gross / now.total.transactions : 0
  const countries: BreakdownLine[] = now.countries
    .map((country) => {
      const gross = now.total.gross * country.spend / countryTotal
      const transactions = gross / Math.max(averagePrice * (0.85 + 0.3 * Math.min(1, country.spend / country.share / 1.3)), 0.01)
      const refunded = transactions * refundRate * (REFUND_BIAS[country.code] ?? 1)
      const refunds = refunded * (transactions ? gross / transactions : 0)
      const fees = (gross - refunds) * (now.total.gross ? now.total.fees / (now.total.gross - now.total.refunds) : 0.3)
      return {
        key: country.code,
        gross,
        refunds,
        fees,
        net: gross - refunds - fees,
        transactions,
        refunded,
        payers: payersTotal * (country.share / now.installPull) * (0.7 + 0.3 * country.spend / country.share),
        previousGross: before.total.gross * country.spend / countryTotal * (0.92 + random() * 0.16)
      }
    })
    .sort((a, b) => b.gross - a.gross)

  const stores: BreakdownLine[] = now.stores.map((store) => {
    const money = now.daily.reduce((sum, day) => {
      for (const key of ['gross', 'refunds', 'fees', 'net', 'transactions', 'refunded'] as const) sum[key] += day.byStore[store][key]
      return sum
    }, zero())
    const previousGross = before.daily.reduce((sum, day) => sum + day.byStore[store].gross, 0)
    return { key: store, ...money, payers: money.transactions / perPayer, previousGross }
  }).sort((a, b) => b.gross - a.gross)

  const reasons = REFUND_REASONS.map(reason => ({
    reason,
    count: products.reduce((sum, line) => sum + (line.reasons.find(entry => entry.reason === reason)?.count ?? 0), 0),
    amount: products.reduce((sum, line) => sum + (line.reasons.find(entry => entry.reason === reason)?.count ?? 0) * line.product.price, 0)
  }))

  const recent = Array.from({ length: 12 }, (_, index) => {
    const line = products[Math.floor(random() ** 1.6 * products.length)] ?? products[0]!
    // A store by its share of sales, so the list looks like the stores' mix.
    let pick = random() * now.storePull
    const store = now.stores.find(entry => (pick -= STORES[entry].share) < 0) ?? now.stores[0] ?? 'app_store'
    const country = now.countries[Math.floor(random() ** 2 * now.countries.length)]?.code ?? 'US'
    const refundedAt = today - Math.floor(index * 0.6 * DAY + random() * 0.5 * DAY)
    const reason = REFUND_REASONS[Math.min(REFUND_REASONS.length - 1, Math.floor(random() ** (line.product.price > 50 ? 0.5 : 1.8) * REFUND_REASONS.length))]!
    const digits = () => Math.floor(random() * 10_000).toString().padStart(4, '0')
    const id = store === 'google_play' ? `GPA.${digits()}-${digits()}-…-${digits()}` : store === 'app_store' ? `2000000${digits()}…${digits()}` : `${store === 'steam' ? 'STM' : 'WEB'}-${digits()}…${digits()}`
    return { id, product: line.product.name, store, country, amount: line.product.price, purchasedAt: refundedAt - Math.floor((1 + random() * 9) * DAY), refundedAt, reason }
  })

  const sandbox = now.total.transactions * SANDBOX_RATE
  const rejected = REJECTIONS.map(entry => ({ reason: entry.reason, note: entry.note, count: now.total.transactions * entry.rate * (0.8 + random() * 0.4) }))
  const rejectedShare = rejected.reduce((sum, entry) => sum + entry.count, 0) / Math.max(now.total.transactions, 1)

  const activePlayers = 31_000 * now.installPull * now.storePull * days
  const previousActivePlayers = activePlayers * (0.94 + random() * 0.06)
  const uniquePlayers = 31_000 * (1 + 0.9 * Math.log(days)) * now.installPull * now.storePull

  return {
    days: now.starts,
    total: now.total,
    previous: before.total,
    daily: now.daily,
    products,
    stores,
    countries,
    payers: {
      total: payersTotal,
      newPayers,
      previousTotal: before.total.transactions / perPayer,
      daily: now.daily.map(day => ({ new: day.transactions / 1.15 * 0.32, returning: day.transactions / 1.15 * 0.68 }))
    },
    activePlayers,
    previousActivePlayers,
    uniquePlayers,
    refunds: { reasons, recent, medianDays: 3 },
    verification: {
      verified: now.total.transactions,
      sandbox,
      rejected,
      daily: now.daily.map(day => ({ verified: day.transactions, rejected: day.transactions * rejectedShare * (0.7 + random() * 0.6) })),
      byStore: now.stores.map(store => ({
        store,
        verified: now.daily.reduce((sum, day) => sum + day.byStore[store].transactions, 0),
        rejected: now.daily.reduce((sum, day) => sum + day.byStore[store].transactions, 0) * rejectedShare * (store === 'google_play' ? 1.3 : store === 'web_shop' ? 0.4 : 0.9),
        medianMs: STORES[store].verifyMs * (0.9 + random() * 0.2)
      }))
    }
  }
}

/** "$1.2m", "$84.3k", "$912": money at a glance. */
export const formatMoney = (value: number) => (Math.abs(value) >= 10_000 ? writeFigure(value, { compact: true, prefix: '$' }) : `$${writeFigure(value, { decimals: value < 100 ? 2 : 0 })}`)

/** Every cent, for a price or a table's exact figure. */
export const formatExactMoney = (value: number) => `$${value.toLocaleString('en', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
