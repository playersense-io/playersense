// Billing as the organization sees it, in the shapes control-api serves (BillingOut and its
// parts, schemas/billing.go): the wallet and its lots, the ledger, auto top-up, the plan, and
// invoices. Money is micros throughout, as the API's is: $1 is 1,000,000.
//
// Everything that takes a card happens on Stripe: a top-up and a new payment method are
// Stripe Checkout, and a card's removal, the default card, and the invoice details (name,
// address, tax id) are the Stripe customer portal. This dashboard keeps only metadata: a card's
// brand, last four digits and expiry, never the card.

export const MICROS = 1_000_000

/** "$1,240.50" from micros. */
export const writeMicros = (micros: number) => formatExactMoney(micros / MICROS)

/** "+$500.00", "-$12.00": a ledger line, the sign before the currency. */
export const writeSignedMicros = (micros: number) => `${micros < 0 ? '-' : '+'}${writeMicros(Math.abs(micros))}`

/** The organization's standing: the backend's `state`. */
export type BillingState = 'active' | 'grace_period' | 'suspended' | 'restricted' | 'canceled'

export const BILLING_STATE: Record<BillingState, { label: string, tone: BadgeTone, note: string }> = {
  active: { label: 'Active', tone: 'success', note: 'Everything is collected and served.' },
  grace_period: { label: 'Grace period', tone: 'warning', note: 'The wallet is empty. Events are still collected until the grace period ends.' },
  suspended: { label: 'Suspended', tone: 'error', note: 'Ingestion is paused until the balance is paid.' },
  restricted: { label: 'Restricted', tone: 'error', note: 'Some features are off until the account is settled.' },
  canceled: { label: 'Canceled', tone: 'neutral', note: 'The plan was canceled.' }
}

// --- The wallet -------------------------------------------------------------------------------

/** A lot of credit: a top-up or a promotion, spent oldest first, some with an expiry. */
export interface CreditLot {
  id: string
  kind: 'purchase' | 'promo'
  amount_micros: number
  remaining_micros: number
  granted_at: string
  expires_at?: string
  description?: string
}

export type LedgerKind = 'topup' | 'promo' | 'usage' | 'subscription' | 'adjustment' | 'refund' | 'expiry'

export const LEDGER_KINDS: Record<LedgerKind, { label: string, tone: string }> = {
  topup: { label: 'Top-up', tone: 'success' },
  promo: { label: 'Promotion', tone: 'chart-6' },
  usage: { label: 'Usage', tone: 'neutral' },
  subscription: { label: 'Subscription', tone: 'chart-1' },
  adjustment: { label: 'Adjustment', tone: 'chart-4' },
  refund: { label: 'Refund', tone: 'chart-3' },
  expiry: { label: 'Expired', tone: 'warning' }
}

export interface LedgerEntry {
  id: string
  kind: LedgerKind
  amount_micros: number
  balance_after_micros: number
  description: string
  at: string
}

// --- Cards ------------------------------------------------------------------------------------

export type CardBrand = 'visa' | 'mastercard' | 'amex'

export const CARD_BRANDS: Record<CardBrand, { label: string, icon: string }> = {
  visa: { label: 'Visa', icon: 'selfhst:visa' },
  mastercard: { label: 'Mastercard', icon: 'selfhst:mastercard' },
  amex: { label: 'American Express', icon: 'selfhst:american-express' }
}

/** A saved card's metadata, from Stripe's webhook: the card itself stays at Stripe. */
export interface PaymentMethod {
  id: string
  brand: CardBrand
  last4: string
  exp_month: number
  exp_year: number
  is_default: boolean
  added_at: string
}

/** Expired, expiring within three months, or fine. */
export function cardExpiry(card: PaymentMethod, now: number): 'expired' | 'soon' | 'ok' {
  const end = Date.UTC(card.exp_year, card.exp_month, 1)
  if (end <= now) return 'expired'
  return end - now < 92 * 86_400_000 ? 'soon' : 'ok'
}

// --- Auto top-up ------------------------------------------------------------------------------

export interface AutoTopUp {
  enabled: boolean
  threshold_micros: number
  amount_micros: number
  /** Charges a day, at most: the backend's default is three. */
  daily_limit: number
  used_today: number
  last_error?: string
}

export const TOP_UP_MIN = 5 * MICROS
export const TOP_UP_MAX = 50_000 * MICROS
export const AUTO_TOP_UP_MAX = 5_000 * MICROS

/** Why an auto top-up setting cannot be saved, or null: the backend's own rules (billing/topup.go). */
export function autoTopUpProblem(threshold: number, amount: number): string | null {
  if (!Number.isFinite(amount) || amount < TOP_UP_MIN) return 'A top-up of $5 or more'
  if (amount > AUTO_TOP_UP_MAX || threshold > AUTO_TOP_UP_MAX) return 'Up to $5,000 each'
  if (!Number.isFinite(threshold) || threshold < 0) return 'A balance to top up at'
  if (threshold >= amount) return 'The top-up has to be more than the balance it starts at'
  return null
}

// --- Plans and invoices ----------------------------------------------------------------------

export interface Plan {
  key: string
  name: string
  description: string
  monthly_price_micros: number
  yearly_price_micros: number | null
  entitlements: Record<string, number>
  features: string[]
  /** Offered for sale through Checkout; Enterprise is by contract. */
  subscribable: boolean
}

export const ENTITLEMENTS: Record<string, { label: string, format: 'number' | 'bytes' | 'days' }> = {
  included_mau: { label: 'Monthly players', format: 'number' },
  included_events: { label: 'Events a month', format: 'number' },
  included_storage_gb_month: { label: 'Storage', format: 'bytes' },
  max_raw_retention_days: { label: 'Raw events kept', format: 'days' },
  max_projects: { label: 'Projects', format: 'number' },
  max_webhook_endpoints: { label: 'Webhook destinations', format: 'number' }
}

/** Sample plans, the shape GET /pricing lists. Prices and allowances are the operators', not ours. */
export const SAMPLE_PLANS: Plan[] = [
  { key: 'free', name: 'Free', description: 'For a game in development.', monthly_price_micros: 0, yearly_price_micros: 0, entitlements: { included_mau: 1_000, included_events: 1_000_000, included_storage_gb_month: 1, max_raw_retention_days: 30, max_projects: 1, max_webhook_endpoints: 1 }, features: [], subscribable: true },
  { key: 'indie', name: 'Indie', description: 'For a game that has launched.', monthly_price_micros: 49 * MICROS, yearly_price_micros: 490 * MICROS, entitlements: { included_mau: 25_000, included_events: 50_000_000, included_storage_gb_month: 25, max_raw_retention_days: 90, max_projects: 3, max_webhook_endpoints: 5 }, features: ['slack_reports'], subscribable: true },
  { key: 'studio', name: 'Studio', description: 'For a studio with several live games.', monthly_price_micros: 299 * MICROS, yearly_price_micros: 2_990 * MICROS, entitlements: { included_mau: 250_000, included_events: 500_000_000, included_storage_gb_month: 250, max_raw_retention_days: 180, max_projects: 10, max_webhook_endpoints: 10 }, features: ['slack_reports', 'premium_support'], subscribable: true },
  { key: 'enterprise', name: 'Enterprise', description: 'Invoiced monthly, with a contract and a named contact.', monthly_price_micros: 0, yearly_price_micros: null, entitlements: { included_mau: 5_000_000, included_events: 10_000_000_000, included_storage_gb_month: 5_000, max_raw_retention_days: 365, max_projects: 100, max_webhook_endpoints: 50 }, features: ['slack_reports', 'premium_support', 'mcp_access'], subscribable: false }
]

export type InvoiceStatus = 'paid' | 'open' | 'void' | 'uncollectible'

export const INVOICE_STATUS: Record<InvoiceStatus, { label: string, tone: BadgeTone }> = {
  paid: { label: 'Paid', tone: 'success' },
  open: { label: 'Open', tone: 'warning' },
  void: { label: 'Void', tone: 'neutral' },
  uncollectible: { label: 'Uncollectible', tone: 'error' }
}

/** A Stripe invoice: a top-up's receipt or the plan's, as the customer portal lists them. */
export interface Invoice {
  id: string
  number: string
  status: InvoiceStatus
  kind: 'top-up' | 'subscription'
  description: string
  total_micros: number
  issued_at: string
  paid_at?: string
  hosted_url: string
  pdf_url: string
}

/** The invoice details Stripe prints on every invoice, edited in the customer portal. */
export interface InvoiceDetails {
  name: string
  email: string
  address: string[]
  country: string
  tax_id?: string
}

// --- Sample -----------------------------------------------------------------------------------

export interface BillingSample {
  state: BillingState
  plan: string
  interval: 'month' | 'year'
  renews_at: string
  lots: CreditLot[]
  ledger: LedgerEntry[]
  cards: PaymentMethod[]
  auto: AutoTopUp
  invoices: Invoice[]
  details: InvoiceDetails
  credit_limit_micros: number
}

export function seedBilling(): BillingSample {
  const now = Date.now()
  const DAY = 86_400_000
  const ago = (days: number) => new Date(now - days * DAY).toISOString()
  const ahead = (days: number) => new Date(now + days * DAY).toISOString()

  /* Sixty days of the wallet: two top-ups, a promotion, usage every day. */
  const ledger: LedgerEntry[] = []
  let balance = 0
  const push = (kind: LedgerKind, amount: number, description: string, days: number) => {
    balance += amount
    ledger.push({ id: `led_${ledger.length}`, kind, amount_micros: amount, balance_after_micros: balance, description, at: ago(days) })
  }
  push('promo', 100 * MICROS, 'Launch credit', 60)
  push('topup', 1_000 * MICROS, 'Top-up by card, Visa •••• 4242', 58)
  push('subscription', -49 * MICROS, 'Indie plan, monthly', 55)
  for (let day = 57; day >= 1; day--) {
    const usage = Math.round((9 + Math.sin(day / 4) * 3 + (day % 7 === 0 ? 5 : 0)) * MICROS)
    push('usage', -usage, `${(1_800 + day * 7).toLocaleString('en')} tracked players over the allowance`, day)
    if (day === 31) push('topup', 500 * MICROS, 'Automatic top-up, Visa •••• 4242', day)
    if (day === 25) push('subscription', -49 * MICROS, 'Indie plan, monthly', day)
    if (day === 20) push('expiry', -12 * MICROS, 'Launch credit expired', day)
    if (day === 12) push('refund', 15 * MICROS, 'Usage refunded: ingestion outage on Sep 11', day)
  }
  ledger.reverse()

  return {
    state: 'active',
    plan: 'indie',
    interval: 'month',
    renews_at: ahead(5),
    credit_limit_micros: 50 * MICROS,
    /* Spent oldest first: the launch credit is gone, the first top-up part spent, the second whole. */
    lots: [
      { id: 'lot_3', kind: 'purchase', amount_micros: 500 * MICROS, remaining_micros: Math.min(500 * MICROS, Math.max(0, balance - 40 * MICROS)), granted_at: ago(31), description: 'Automatic top-up' },
      { id: 'lot_2', kind: 'purchase', amount_micros: 1_000 * MICROS, remaining_micros: Math.max(0, balance - 540 * MICROS), granted_at: ago(58), description: 'Top-up' },
      { id: 'lot_1', kind: 'promo', amount_micros: 100 * MICROS, remaining_micros: 0, granted_at: ago(60), expires_at: ago(20), description: 'Launch credit' },
      { id: 'lot_4', kind: 'promo', amount_micros: 40 * MICROS, remaining_micros: 40 * MICROS, granted_at: ago(12), expires_at: ahead(18), description: 'Outage credit' }
    ],
    ledger,
    cards: [
      { id: 'pm_1', brand: 'visa', last4: '4242', exp_month: 8, exp_year: 2028, is_default: true, added_at: ago(58) },
      { id: 'pm_2', brand: 'mastercard', last4: '5100', exp_month: 11, exp_year: 2026, is_default: false, added_at: ago(20) }
    ],
    auto: { enabled: true, threshold_micros: 100 * MICROS, amount_micros: 500 * MICROS, daily_limit: 3, used_today: 0 },
    invoices: [
      { id: 'in_6', number: 'PS-2026-0012', status: 'paid', kind: 'subscription', description: 'Indie plan, Sep 2026', total_micros: 49 * MICROS, issued_at: ago(25), paid_at: ago(25), hosted_url: 'https://invoice.stripe.com/i/…', pdf_url: 'https://pay.stripe.com/invoice/…/pdf' },
      { id: 'in_5', number: 'PS-2026-0011', status: 'paid', kind: 'top-up', description: 'Automatic top-up', total_micros: 500 * MICROS, issued_at: ago(31), paid_at: ago(31), hosted_url: 'https://invoice.stripe.com/i/…', pdf_url: 'https://pay.stripe.com/invoice/…/pdf' },
      { id: 'in_4', number: 'PS-2026-0009', status: 'paid', kind: 'subscription', description: 'Indie plan, Aug 2026', total_micros: 49 * MICROS, issued_at: ago(55), paid_at: ago(55), hosted_url: 'https://invoice.stripe.com/i/…', pdf_url: 'https://pay.stripe.com/invoice/…/pdf' },
      { id: 'in_3', number: 'PS-2026-0008', status: 'paid', kind: 'top-up', description: 'Top-up', total_micros: 1_000 * MICROS, issued_at: ago(58), paid_at: ago(58), hosted_url: 'https://invoice.stripe.com/i/…', pdf_url: 'https://pay.stripe.com/invoice/…/pdf' },
      { id: 'in_2', number: 'PS-2026-0004', status: 'void', kind: 'top-up', description: 'Top-up, card declined', total_micros: 250 * MICROS, issued_at: ago(59), hosted_url: 'https://invoice.stripe.com/i/…', pdf_url: 'https://pay.stripe.com/invoice/…/pdf' }
    ],
    details: { name: 'Starfall Games Ltd.', email: 'finance@starfall.games', address: ['Maslak Mah. Büyükdere Cad. 255', '34398 Sarıyer, Istanbul'], country: 'Türkiye', tax_id: 'TR 1234567890' }
  }
}
