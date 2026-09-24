<script setup lang="ts">
// Billing: what the organization has to spend and how it gets more. A strip of the figures that
// matter at a glance (the balance, this month's spend, auto top-up, the plan), then a tab each:
// the wallet and its ledger, the plan, the cards, auto top-up, and invoices.
//
// Nothing here takes a card. A top-up and a new card are Stripe Checkout, the default card and
// the invoice details are Stripe's customer portal (StripeHandoff); this page keeps only a
// card's metadata. Sample data, in this browser (composables/billing.ts).
const { billing, balance, plan, defaultCard, creditTopUp } = useBilling()

const TABS = computed<PageTab[]>(() => [
  { value: 'wallet', label: 'Wallet' },
  { value: 'plan', label: 'Plan' },
  { value: 'cards', label: 'Payment methods', count: billing.value.cards.length },
  { value: 'auto', label: 'Auto top-up' },
  { value: 'invoices', label: 'Invoices', count: billing.value.invoices.length }
])
const tab = useQueryChoice('tab', ['wallet', 'plan', 'cards', 'auto', 'invoices'], 'wallet')

const monthStart = (() => {
  const now = new Date()
  return Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1)
})()
const spent = computed(() => -billing.value.ledger
  .filter(entry => Date.parse(entry.at) >= monthStart && (entry.kind === 'usage' || entry.kind === 'subscription'))
  .reduce((sum, entry) => sum + entry.amount_micros, 0))

const state = computed(() => BILLING_STATE[billing.value.state])
const renews = computed(() => new Date(billing.value.renews_at).toLocaleDateString('en', { month: 'short', day: 'numeric' }))

// --- Topping up ---------------------------------------------------------------------------

const PRESETS = [50, 100, 250, 500, 1_000].map(dollars => dollars * MICROS)
const toppingUp = ref(false)
const amount = ref(250 * MICROS)
const amountProblem = computed(() => (amount.value < TOP_UP_MIN ? 'At least $5' : amount.value > TOP_UP_MAX ? 'Up to $50,000 at a time' : null))

/* The bar's "Add funds" lands here with ?topup=1: open the dialog, and drop the flag so a
   reload does not open it again. */
const route = useRoute()
watch(() => route.query.topup, (flag) => {
  if (!flag) return
  toppingUp.value = true
  const { topup: _, ...query } = route.query
  navigateTo({ query }, { replace: true })
}, { immediate: true })

function topUp() {
  const paid = amount.value
  creditTopUp(paid, defaultCard.value)
  return `Paid. Stripe confirmed ${writeMicros(paid)} and it is in the wallet, with a receipt under Invoices.`
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Billing">
      <template #description>
        What the organization has to spend, what it has spent it on, and how it gets more. Cards are entered on Stripe; this page keeps only their brand, last four digits and expiry.
      </template>
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          variant="flat"
          @click="toppingUp = true"
        >
          Top up
        </v-btn>
      </template>
    </PageHeader>

    <InfoNote text="Billing is sample data in this browser until this page reads GET /organizations/{org}/billing. Stripe is simulated: no card is charged." />

    <!-- The figures, at a glance. -->
    <section aria-label="At a glance" class="grid overflow-hidden rounded-(--ps-radius-large) border border-line bg-surface sm:grid-cols-2 xl:grid-cols-4">
      <div class="flex flex-col gap-1 border-b border-line p-4 sm:border-r xl:border-b-0">
        <span class="flex items-center justify-between gap-2 text-[11px] text-ink/50">
          Balance
          <StatusBadge :label="state.label" :tone="state.tone" />
        </span>
        <span class="font-mono text-2xl text-ink">{{ writeMicros(balance) }}</span>
        <span class="text-[11px] text-ink/50">Usage is taken from it every day</span>
      </div>
      <div class="flex flex-col gap-1 border-b border-line p-4 xl:border-r xl:border-b-0">
        <span class="text-[11px] text-ink/50">Spent this month</span>
        <span class="font-mono text-2xl text-ink">{{ writeMicros(spent) }}</span>
        <span class="text-[11px] text-ink/50">Usage and the plan, since the 1st</span>
      </div>
      <button class="flex cursor-pointer flex-col gap-1 border-b border-line p-4 text-left transition-colors duration-150 hover:bg-ink/2 sm:border-r sm:border-b-0" type="button" @click="tab = 'auto'">
        <span class="text-[11px] text-ink/50">Auto top-up</span>
        <span :class="billing.auto.enabled ? 'text-ink' : 'text-ink/45'" class="text-2xl">{{ billing.auto.enabled ? 'On' : 'Off' }}</span>
        <span class="text-[11px] text-ink/50">{{ billing.auto.enabled ? `Adds ${writeMicros(billing.auto.amount_micros)} under ${writeMicros(billing.auto.threshold_micros)}` : billing.cards.length ? 'The wallet is only topped up by hand' : 'Needs a payment method' }}</span>
      </button>
      <button class="flex cursor-pointer flex-col gap-1 p-4 text-left transition-colors duration-150 hover:bg-ink/2" type="button" @click="tab = 'plan'">
        <span class="text-[11px] text-ink/50">Plan</span>
        <span class="text-2xl text-ink">{{ plan.name }}</span>
        <span class="text-[11px] text-ink/50">{{ plan.monthly_price_micros ? `${writeMicros(billing.interval === 'year' ? plan.yearly_price_micros ?? 0 : plan.monthly_price_micros)} a ${billing.interval}, renews ${renews}` : 'No charge' }}</span>
      </button>
    </section>

    <p v-if="billing.state !== 'active'" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius-large) bg-warning/8 px-4 py-3 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset">
      <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-warning" name="material-symbols:warning-outline" />
      {{ state.note }}
    </p>

    <div class="flex flex-col gap-5">
      <PageTabs v-model="tab" :tabs="TABS" label="Billing views" />

      <BillingWallet v-if="tab === 'wallet'" @top-up="toppingUp = true" />
      <BillingPlan v-else-if="tab === 'plan'" />
      <BillingCards v-else-if="tab === 'cards'" @auto-top-up="tab = 'auto'" />
      <BillingAutoTopUp v-else-if="tab === 'auto'" @cards="tab = 'cards'" />
      <BillingInvoices v-else />
    </div>

    <StripeHandoff
      v-if="toppingUp"
      v-model="toppingUp"
      :disabled="Boolean(amountProblem)"
      :points="[
        `Stripe Checkout opens for ${writeMicros(amount)}, with your saved cards offered first.`,
        'Pay, and Stripe sends you back here. The card is saved for next time and for auto top-up.',
        'The wallet is credited when Stripe confirms the payment, usually within seconds.'
      ]"
      :run="topUp"
      action="Continue to Stripe Checkout"
      intro="Adds to the wallet. Usage is taken from it every day, and credit bought never expires."
      title="Top up the wallet"
    >
      <div class="flex flex-col gap-2">
        <span class="text-xs text-ink/60">Amount</span>
        <div class="grid grid-cols-5 gap-1.5" role="radiogroup" aria-label="Amount">
          <button
            v-for="preset in PRESETS"
            :key="preset"
            :aria-checked="amount === preset"
            :class="amount === preset ? 'border-primary bg-primary/6 text-ink' : 'border-line text-ink/75 hover:bg-ink/3'"
            class="h-9 cursor-pointer rounded-(--ps-radius-small) border font-mono text-xs transition-colors duration-150"
            role="radio"
            type="button"
            @click="amount = preset"
          >
            ${{ (preset / MICROS).toLocaleString('en') }}
          </button>
        </div>
        <MoneyField v-model="amount" :error="Boolean(amountProblem)" label="Another amount" />
        <span :class="amountProblem ? 'text-error' : 'text-ink/50'" class="text-[11px]">{{ amountProblem ?? 'Any amount from $5 to $50,000.' }}</span>
      </div>
    </StripeHandoff>
  </div>
</template>
