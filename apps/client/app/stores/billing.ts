import { defineStore } from 'pinia'
import type { Schema } from '@playersense/contracts'

// The organization's billing (GET …/billing and its parts), shared by the billing page's tabs,
// the header's balance and the pages that ask about the plan. Read once an organization is
// known, and again after anything that changes it.
//
// Nothing here takes a card. A top-up, a new card and a plan are Stripe Checkout, invoice
// details and canceling are the customer portal: the API hands back Stripe's URL and the
// browser goes there. The wallet is credited by Stripe's webhook, never by the browser coming
// back, so after a return the page reads again until the payment shows.

export type BillingRecord = Schema<'BillingOut'>
export type PlanRecord = Schema<'PlanDetailOut'>
export type ContractInvoice = Schema<'InvoiceOut'>
export type InvoiceDetailsRecord = Schema<'InvoiceDetailsOut'>

function cardOf(out: Schema<'CardOut'>): PaymentMethod {
  return {
    id: out.id,
    brand: out.brand,
    last4: out.last4,
    exp_month: out.exp_month,
    exp_year: out.exp_year,
    is_default: out.is_default,
    added_at: out.added_at,
    funding: out.funding || undefined
  }
}

function receiptOf(out: Schema<'ReceiptOut'>): Invoice {
  return {
    id: out.id,
    number: out.number ?? '',
    status: out.status as InvoiceStatus,
    kind: out.kind === 'subscription' ? 'subscription' : 'top-up',
    description: out.description ?? '',
    total_micros: out.total_micros,
    issued_at: out.issued_at,
    paid_at: out.paid_at ?? undefined,
    hosted_url: out.hosted_url ?? '',
    pdf_url: out.pdf_url ?? ''
  }
}

function lotOf(out: Schema<'CreditLotOut'>): CreditLot {
  return {
    id: out.id,
    kind: out.kind === 'promo' ? 'promo' : 'purchase',
    amount_micros: out.amount_micros,
    remaining_micros: out.remaining_micros,
    granted_at: out.granted_at,
    expires_at: out.expires_at ?? undefined,
    description: out.description || undefined
  }
}

function entryOf(out: Schema<'LedgerEntryOut'>): LedgerEntry {
  return {
    id: out.id,
    kind: out.kind as LedgerKind,
    amount_micros: out.amount_micros,
    balance_after_micros: out.balance_after_micros,
    description: out.description ?? '',
    at: out.at
  }
}

/** A read that may fail on its own (no Stripe customer yet): its error, kept beside it. */
async function settled<T>(read: Promise<T>): Promise<{ value: T | null, problem: string }> {
  try {
    return { value: await read, problem: '' }
  } catch (caught) {
    return { value: null, problem: caught instanceof Error ? caught.message : String(caught) }
  }
}

export const useBillingStore = defineStore('billing', () => {
  const workspace = useWorkspaceStore()

  const billing = ref<BillingRecord | null>(null)
  const plans = ref<PlanRecord[]>([])
  const cards = ref<PaymentMethod[]>([])
  const cardsProblem = ref('')
  const receipts = ref<Invoice[]>([])
  const receiptsNext = ref<string | undefined>()
  const invoices = ref<ContractInvoice[]>([])
  const ledger = ref<LedgerEntry[]>([])
  const ledgerNext = ref<string | undefined>()
  const details = ref<InvoiceDetailsRecord | null>(null)

  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  /** The organization the figures are for, so a switch reads again. */
  const loadedFor = ref<string | null>(null)

  const path = computed(() => workspace.orgPath && `${workspace.orgPath}/billing`)

  const plan = computed(() => {
    const key = billing.value?.plan.key
    return plans.value.find(entry => entry.key === key) ?? null
  })
  const lots = computed(() => (billing.value?.lots ?? []).map(lotOf))
  const balance = computed(() => billing.value?.live.available_micros ?? 0)
  const defaultCard = computed(() => cards.value.find(card => card.is_default) ?? cards.value[0] ?? null)
  const auto = computed(() => billing.value?.auto_topup ?? null)
  const invoiced = computed(() => billing.value?.collection === 'invoiced')

  async function loadBilling() {
    billing.value = await apiRequest<BillingRecord>('GET', path.value!)
  }

  async function loadCards(refresh = false) {
    const read = await settled(apiRequest<Schema<'CardsOut'>>('GET', withQuery(`${path.value}/cards`, { refresh: refresh || undefined })))
    cards.value = (read.value?.cards ?? []).map(cardOf)
    cardsProblem.value = read.problem
  }

  async function loadLedger() {
    const out = await apiRequest<Schema<'LedgerOut'>>('GET', withQuery(`${path.value}/ledger`, { limit: 200 }))
    ledger.value = (out.entries ?? []).map(entryOf)
    ledgerNext.value = out.next || undefined
  }

  async function moreLedger() {
    if (!ledgerNext.value) return
    const out = await apiRequest<Schema<'LedgerOut'>>('GET', withQuery(`${path.value}/ledger`, { limit: 200, before: ledgerNext.value }))
    ledger.value = [...ledger.value, ...(out.entries ?? []).map(entryOf)]
    ledgerNext.value = out.next || undefined
  }

  async function loadReceipts() {
    const read = await settled(apiRequest<Schema<'ReceiptsOut'>>('GET', withQuery(`${path.value}/receipts`, { limit: 100 })))
    receipts.value = (read.value?.receipts ?? []).map(receiptOf)
    receiptsNext.value = read.value?.next || undefined
  }

  async function moreReceipts() {
    if (!receiptsNext.value) return
    const out = await apiRequest<Schema<'ReceiptsOut'>>('GET', withQuery(`${path.value}/receipts`, { limit: 100, before: receiptsNext.value }))
    receipts.value = [...receipts.value, ...(out.receipts ?? []).map(receiptOf)]
    receiptsNext.value = out.next || undefined
  }

  async function loadInvoices() {
    const read = await settled(apiRequest<Schema<'InvoicesOut'>>('GET', `${path.value}/invoices`))
    invoices.value = [...(read.value?.invoices ?? [])]
  }

  async function loadDetails(refresh = false) {
    const read = await settled(apiRequest<InvoiceDetailsRecord>('GET', withQuery(`${path.value}/details`, { refresh: refresh || undefined })))
    details.value = read.value
  }

  async function loadPlans() {
    const out = await apiRequest<Schema<'PlansOut'>>('GET', '/v1/pricing')
    plans.value = [...(out.plans ?? [])]
  }

  /** Everything the billing page shows; once per organization unless forced. */
  async function load(force = false) {
    if (!path.value || loading.value) return
    if (!force && loadedFor.value === path.value) return
    loading.value = true
    error.value = null
    try {
      await Promise.all([loadBilling(), loadPlans()])
      await Promise.all([loadCards(), loadLedger(), loadReceipts(), loadDetails(), invoiced.value ? loadInvoices() : Promise.resolve()])
      loadedFor.value = path.value
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
    } finally {
      loading.value = false
    }
  }

  /** The wallet and what moved it, after something that changed them. */
  async function refresh() {
    await Promise.all([loadBilling(), loadLedger(), loadReceipts()])
  }

  // --- Stripe: each returns where to send the browser -------------------------------------

  async function topUpUrl(amountMicros: number) {
    return (await apiRequest<Schema<'CheckoutOut'>>('POST', `${path.value}/top-ups`, { amount_micros: amountMicros })).url
  }

  async function addCardUrl() {
    return (await apiRequest<Schema<'CheckoutOut'>>('POST', `${path.value}/cards`)).url
  }

  async function subscribeUrl(planKey: string, interval: 'month' | 'year') {
    return (await apiRequest<Schema<'CheckoutOut'>>('POST', `${path.value}/subscription`, { plan: planKey, interval })).url
  }

  async function portalUrl() {
    return (await apiRequest<Schema<'PortalOut'>>('POST', `${path.value}/portal`)).url
  }

  // --- Changes made here ------------------------------------------------------------------

  async function setDefaultCard(card: PaymentMethod) {
    const out = await apiRequest<Schema<'CardsOut'>>('PUT', `${path.value}/cards/${card.id}/default`)
    cards.value = (out.cards ?? []).map(cardOf)
  }

  async function removeCard(card: PaymentMethod) {
    const out = await apiRequest<Schema<'CardsOut'>>('DELETE', `${path.value}/cards/${card.id}`)
    cards.value = (out.cards ?? []).map(cardOf)
    await loadBilling()
  }

  async function saveAutoTopUp(enabled: boolean, thresholdMicros: number, amountMicros: number) {
    await apiRequest('PUT', `${path.value}/auto-topup`, { enabled, threshold_micros: thresholdMicros, amount_micros: amountMicros })
    await loadBilling()
  }

  function reset() {
    billing.value = null
    loadedFor.value = null
  }

  return {
    billing, plans, plan, cards, cardsProblem, receipts, receiptsNext, invoices, ledger, ledgerNext, details,
    lots, balance, defaultCard, auto, invoiced, loading, error,
    load, refresh, loadBilling, loadCards, loadDetails, moreLedger, moreReceipts,
    topUpUrl, addCardUrl, subscribeUrl, portalUrl, setDefaultCard, removeCard, saveAutoTopUp, reset
  }
})
