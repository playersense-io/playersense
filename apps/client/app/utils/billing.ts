// Billing as the organization sees it, in the shapes the API serves (BillingOut and its parts):
// the wallet and its lots, the ledger, auto top-up, the plan, and receipts. The figures live in
// stores/billing.ts. Money is micros throughout, as the API's is: $1 is 1,000,000.
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

const BRANDS: Record<string, { label: string, icon: string }> = {
  visa: { label: 'Visa', icon: 'selfhst:visa' },
  mastercard: { label: 'Mastercard', icon: 'selfhst:mastercard' },
  amex: { label: 'American Express', icon: 'selfhst:american-express' },
  discover: { label: 'Discover', icon: 'material-symbols:credit-card-outline' },
  jcb: { label: 'JCB', icon: 'material-symbols:credit-card-outline' },
  unionpay: { label: 'UnionPay', icon: 'material-symbols:credit-card-outline' },
  diners: { label: 'Diners Club', icon: 'material-symbols:credit-card-outline' }
}

/** A card brand as Stripe names it, in words and a mark. */
export const cardBrand = (brand: string) => BRANDS[brand] ?? { label: brand ? brand[0]!.toUpperCase() + brand.slice(1) : 'Card', icon: 'material-symbols:credit-card-outline' }

/** A saved card's metadata, from Stripe: the card itself stays at Stripe. */
export interface PaymentMethod {
  id: string
  brand: string
  last4: string
  exp_month: number
  exp_year: number
  is_default: boolean
  added_at: string
  /** credit, debit or prepaid. */
  funding?: string
}

/** Expired, expiring within three months, or fine. */
export function cardExpiry(card: PaymentMethod, now: number): 'expired' | 'soon' | 'ok' {
  const end = Date.UTC(card.exp_year, card.exp_month, 1)
  if (end <= now) return 'expired'
  return end - now < 92 * 86_400_000 ? 'soon' : 'ok'
}

// --- Auto top-up ------------------------------------------------------------------------------

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

export const ENTITLEMENTS: Record<string, { label: string, format: 'number' | 'bytes' | 'days' }> = {
  included_mau: { label: 'Monthly players', format: 'number' },
  included_events: { label: 'Events a month', format: 'number' },
  included_storage_gb_month: { label: 'Storage', format: 'bytes' },
  max_raw_retention_days: { label: 'Raw events kept', format: 'days' },
  max_projects: { label: 'Projects', format: 'number' },
  max_webhook_endpoints: { label: 'Webhook destinations', format: 'number' }
}

/** What a plan's features unlock, in words. */
export const FEATURES: Record<string, string> = {
  slack_reports: 'Scheduled reports to Slack',
  premium_support: 'Support within a working day',
  mcp_access: 'MCP access for your agents'
}

export type InvoiceStatus = 'paid' | 'open' | 'void' | 'uncollectible'

export const INVOICE_STATUS: Record<InvoiceStatus, { label: string, tone: BadgeTone }> = {
  paid: { label: 'Paid', tone: 'success' },
  open: { label: 'Open', tone: 'warning' },
  void: { label: 'Void', tone: 'neutral' },
  uncollectible: { label: 'Uncollectible', tone: 'error' }
}

/** A Stripe receipt: a top-up's or the plan's (GET …/billing/receipts). */
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
