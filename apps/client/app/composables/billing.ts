/**
 * The organization's billing, kept in this browser until the dashboard is wired to
 * GET /organizations/{org}/billing and its routes. Every action that involves a card stands in
 * for a round trip to Stripe: a top-up and a new card are Stripe Checkout, the default card,
 * removing one and the invoice details are the customer portal. The wallet is credited by
 * Stripe's webhook, never by the browser coming back, so a top-up lands a moment after the
 * return here, as it will for real.
 */
let shared: Ref<BillingSample> | null = null

function stored(): Ref<BillingSample> {
  shared ??= effectScope(true).run(() => useLocalStorage<BillingSample>('playersense.billing.v1', seedBilling, { deep: false }))!
  return shared
}

export function useBilling() {
  const store = stored()

  const billing = computed<BillingSample>(() => {
    const value = store.value
    return value && Array.isArray(value.cards) && Array.isArray(value.lots) ? value : seedBilling()
  })

  const balance = computed(() => billing.value.lots.reduce((sum, lot) => sum + lot.remaining_micros, 0))
  const plan = computed(() => SAMPLE_PLANS.find(entry => entry.key === billing.value.plan) ?? SAMPLE_PLANS[0]!)
  const defaultCard = computed(() => billing.value.cards.find(card => card.is_default) ?? billing.value.cards[0] ?? null)

  function write(change: (value: BillingSample) => BillingSample) {
    store.value = change(billing.value)
  }

  const id = (prefix: string) => `${prefix}_${Math.random().toString(36).slice(2, 10)}`

  /** A top-up paid on Stripe Checkout: a new lot, a ledger line and a receipt. */
  function creditTopUp(amount: number, card: PaymentMethod | null, automatic = false) {
    const at = new Date().toISOString()
    const label = card ? `${CARD_BRANDS[card.brand].label} •••• ${card.last4}` : 'card'
    write(value => ({
      ...value,
      lots: [{ id: id('lot'), kind: 'purchase', amount_micros: amount, remaining_micros: amount, granted_at: at, description: automatic ? 'Automatic top-up' : 'Top-up' }, ...value.lots],
      ledger: [{ id: id('led'), kind: 'topup', amount_micros: amount, balance_after_micros: balance.value + amount, description: `${automatic ? 'Automatic top-up' : 'Top-up by card'}, ${label}`, at }, ...value.ledger],
      invoices: [{ id: id('in'), number: `PS-2026-${String(13 + value.invoices.length).padStart(4, '0')}`, status: 'paid', kind: 'top-up', description: automatic ? 'Automatic top-up' : 'Top-up', total_micros: amount, issued_at: at, paid_at: at, hosted_url: 'https://invoice.stripe.com/i/…', pdf_url: 'https://pay.stripe.com/invoice/…/pdf' }, ...value.invoices]
    }))
  }

  /** A card saved on Stripe Checkout: its metadata, and the default when it is the first. */
  function saveCard(): PaymentMethod {
    const brands: CardBrand[] = ['visa', 'mastercard', 'amex']
    const brand = brands[Math.floor(Math.random() * brands.length)]!
    const card: PaymentMethod = {
      id: id('pm'),
      brand,
      last4: String(1000 + Math.floor(Math.random() * 9000)),
      exp_month: 1 + Math.floor(Math.random() * 12),
      exp_year: 2027 + Math.floor(Math.random() * 4),
      is_default: !billing.value.cards.length,
      added_at: new Date().toISOString()
    }
    write(value => ({ ...value, cards: [...value.cards, card] }))
    return card
  }

  function setDefault(cardId: string) {
    write(value => ({ ...value, cards: value.cards.map(card => ({ ...card, is_default: card.id === cardId })) }))
  }

  /** A card removed. The last one takes auto top-up with it: nothing is left to charge. */
  function removeCard(cardId: string) {
    write((value) => {
      const cards = value.cards.filter(card => card.id !== cardId)
      if (cards.length && !cards.some(card => card.is_default)) cards[0] = { ...cards[0]!, is_default: true }
      return { ...value, cards, auto: cards.length ? value.auto : { ...value.auto, enabled: false } }
    })
  }

  function saveAutoTopUp(enabled: boolean, threshold: number, amount: number) {
    write(value => ({ ...value, auto: { ...value.auto, enabled: enabled && value.cards.length > 0, threshold_micros: threshold, amount_micros: amount } }))
  }

  /** A plan bought on Stripe Checkout. */
  function subscribe(planKey: string, interval: 'month' | 'year') {
    write(value => ({ ...value, plan: planKey, interval, renews_at: new Date(Date.now() + (interval === 'year' ? 365 : 30) * 86_400_000).toISOString() }))
  }

  return { billing, balance, plan, defaultCard, creditTopUp, saveCard, setDefault, removeCard, saveAutoTopUp, subscribe }
}
