import type { Schema } from '@playersense/contracts'

// Revenue and purchases as the API serves them (…/analytics/{env}/revenue and /purchases): the
// stores by name and mark, what can happen to a purchase, why a receipt is turned away, and the
// few sums both pages draw from the same answer.
//
// Money comes two ways. `money` is every figure in US dollars at each day's rate, with the
// stores' fees and net estimated; it is absent when the deployment has no exchange rates. Without
// it the figures are what the game reported, summed across whatever currencies it reported in, and
// the page says which.

export type RevenueRecord = Schema<'RevenueOut'>
export type RevenueSlice = Schema<'RevenueSliceOut'>
export type RevenueMoney = Schema<'RevenueMoneyOut'>
export type PurchaseRow = Schema<'PurchaseOut'>

const STORE_LOOKS: Record<string, { label: string, platform: string }> = {
  app_store: { label: 'App Store', platform: 'iOS' },
  google_play: { label: 'Google Play', platform: 'Android' },
  steam: { label: 'Steam', platform: 'Steam' },
  web_shop: { label: 'Web shop', platform: 'web' }
}

/** A store as a reader names it, with the platform its mark is drawn from. Unknown stores are what the game called them. */
export const storeLook = (key: string) => STORE_LOOKS[key] ?? { label: key || 'Unknown store', platform: key }

/** What can happen to a purchase: reported by the game, or checked with its store and answered. */
export type PurchaseState = 'reported' | 'verified' | 'refunded' | 'sandbox' | 'rejected'

export const PURCHASE_STATES: Record<PurchaseState, { label: string, tone: BadgeTone, note: string }> = {
  reported: { label: 'Reported', tone: 'neutral', note: 'The game reported it. Its store is not verified, so it counts as the game said.' },
  verified: { label: 'Verified', tone: 'success', note: 'The store confirmed it, and it counts as revenue.' },
  refunded: { label: 'Refunded', tone: 'warning', note: 'Verified, then given back by the store or the player\'s bank.' },
  sandbox: { label: 'Test', tone: 'neutral', note: 'A TestFlight or license-tester purchase: verified, and kept out of revenue.' },
  rejected: { label: 'Rejected', tone: 'error', note: 'The receipt did not check out with the store. Nothing was granted or counted.' }
}

export const PURCHASE_STATE_KEYS = Object.keys(PURCHASE_STATES) as PurchaseState[]

export const purchaseState = (status: string) => PURCHASE_STATES[(status || 'reported') as PurchaseState] ?? PURCHASE_STATES.reported

/** Why a store turned a receipt away, by the code the API records. */
export const REJECTION_CODES: Record<string, { label: string, note: string }> = {
  invalid_receipt: { label: 'Not a receipt', note: 'Malformed, or unknown to the store.' },
  signature_mismatch: { label: 'Forged or altered', note: 'The signature does not check out against the store\'s certificates.' },
  wrong_app: { label: 'Another app\'s receipt', note: 'A real receipt, for a different app.' },
  product_mismatch: { label: 'Another product', note: 'A real receipt, for a different product than the game said.' },
  not_purchased: { label: 'Not paid', note: 'Pending, canceled or never finalized.' },
  replayed: { label: 'Replayed', note: 'A transaction already granted to another player, sent again.' }
}

export const rejectionLook = (code: string) => REJECTION_CODES[code] ?? { label: code.replace(/_/g, ' ') || 'Turned away', note: '' }

/** An amount in its own currency, the way a store's receipt writes it: "¥1,500", "R$25.90". */
export function writeAmount(micros: number, currency: string): string {
  const amount = micros / 1_000_000
  try {
    return new Intl.NumberFormat('en', { style: 'currency', currency: currency || 'USD', currencyDisplay: 'narrowSymbol' }).format(amount)
  } catch {
    return `${amount.toLocaleString('en', { maximumFractionDigits: 2 })} ${currency}`
  }
}

/** What a slice earned, in dollars when the API converted it, or as reported when it could not. */
export const grossOf = (slice: Pick<RevenueSlice, 'revenue_usd' | 'revenue_micros'>) => slice.revenue_usd ?? slice.revenue_micros / 1_000_000

/** How a slice moved on the range before: the reported amounts, so no rate changes it. Null when it is new. */
export const sliceTrend = (slice: Pick<RevenueSlice, 'revenue_micros' | 'previous_revenue_micros'>) => (slice.previous_revenue_micros
  ? (slice.revenue_micros - slice.previous_revenue_micros) / slice.previous_revenue_micros
  : null)

/** One row of a breakdown: a country, a store or a package. */
export interface RevenueLine {
  key: string
  gross: number
  /** Only for stores, with dollars: the fee the store was taken to keep, and what was left. */
  refunded?: number
  fees?: number
  net?: number
  feePercent?: number
  purchases: number
  buyers: number
  trend: number | null
}

/** Slices as breakdown rows, each store joined to its dollars when there are any. */
export function linesOf(slices: readonly RevenueSlice[], money?: RevenueMoney | null): RevenueLine[] {
  return slices.map((slice) => {
    const store = money?.stores?.find(entry => entry.store === slice.key)
    return {
      key: slice.key,
      gross: store?.gross ?? grossOf(slice),
      refunded: store?.refunded,
      fees: store?.fees,
      net: store?.net,
      feePercent: store?.fee_percent,
      purchases: slice.purchases,
      buyers: slice.buyers,
      trend: sliceTrend(slice)
    }
  })
}

/** The figures' unit, in a clause: "in USD at each day's rate", or the currencies they were reported in. */
export function currencyWords(revenue: Pick<RevenueRecord, 'money' | 'currencies'> | null | undefined): string {
  if (revenue?.money) return 'in USD at each day\'s rate'
  const currencies = revenue?.currencies ?? []
  if (!currencies.length) return 'as reported'
  return currencies.length === 1 ? `in ${currencies[0]}` : `as reported, summed across ${currencies.join(', ')}`
}

/** A day of the API's, "2026-09-24T00:00:00Z", as a chart names it. */
const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
export const dayLabel = (iso: string) => dayFormat.format(Date.parse(iso))

/** YYYY-MM-DD, in UTC, as the API's ranges take it. */
export const isoDate = (at: number) => new Date(at).toISOString().slice(0, 10)

/** Money for a figure: "$1,240", "$48.5k". Without dollars, the reported amounts with no sign. */
export function writeMoney(value: number, usd = true): string {
  const prefix = usd ? '$' : ''
  if (Math.abs(value) >= 10_000) return writeFigure(value, { compact: true, prefix })
  return `${prefix}${writeFigure(value, { decimals: Math.abs(value) < 100 ? 2 : 0 })}`
}

/** Money to the cent: "$1,240.50". */
export const writeExactMoney = (value: number, usd = true) => `${usd ? '$' : ''}${value.toLocaleString('en', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
