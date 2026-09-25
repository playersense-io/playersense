<script setup lang="ts">
// Billing: what the organization has to spend and how it gets more. A strip of the figures that
// matter at a glance (the balance, this month's charges, auto top-up, the plan), then a tab
// each: the wallet and its ledger, the plan, the cards, auto top-up, and receipts.
//
// Nothing here takes a card. A top-up and a new card are Stripe Checkout, the plan's changes and
// the invoice details are Stripe's customer portal (StripeHandoff); this page keeps only a
// card's metadata (stores/billing.ts).
const store = useBillingStore()
const { billing, plan, cards, receipts, auto, loading, error } = storeToRefs(store)

onMounted(() => void store.load())
watch(() => useWorkspaceStore().orgPath, () => void store.load())

const TABS = computed<PageTab[]>(() => [
  { value: 'wallet', label: 'Wallet' },
  { value: 'plan', label: 'Plan' },
  { value: 'cards', label: 'Payment methods', count: cards.value.length },
  { value: 'auto', label: 'Auto top-up' },
  { value: 'invoices', label: store.invoiced ? 'Invoices' : 'Receipts', count: store.invoiced ? store.invoices.length : receipts.value.length }
])
const tab = useQueryChoice('tab', ['wallet', 'plan', 'cards', 'auto', 'invoices'], 'wallet')

const state = computed(() => BILLING_STATE[(billing.value?.state ?? 'active') as BillingState] ?? BILLING_STATE.active)
const subscription = computed(() => billing.value?.subscription as { status: string, interval?: string, period_end?: string, cancel_at_period_end: boolean } | undefined)
const month = computed(() => billing.value?.month as { charged_micros: number, tracked_players: number, included_players: number } | undefined)

// --- Coming back from Stripe ----------------------------------------------------------------

/* Stripe sends the browser back with ?checkout=success or cancel. The success page proves
   nothing, so the page reads the wallet again a few times until the webhook's credit shows. */
const route = useRoute()
const returned = ref<'success' | 'cancel' | null>(null)

watch(() => route.query.checkout, async (flag) => {
  if (flag !== 'success' && flag !== 'cancel') return
  returned.value = flag
  const { checkout: _, ...query } = route.query
  await navigateTo({ query }, { replace: true })
  if (flag !== 'success') return
  const before = billing.value?.live.available_micros
  for (let attempt = 0; attempt < 6; attempt++) {
    await new Promise(resolve => setTimeout(resolve, 2_000))
    await Promise.all([store.refresh(), store.loadCards()])
    if (billing.value?.live.available_micros !== before) break
  }
}, { immediate: true })

// --- Topping up ---------------------------------------------------------------------------

const PRESETS = [50, 100, 250, 500, 1_000].map(dollars => dollars * MICROS)
const toppingUp = ref(false)
const amount = ref(250 * MICROS)
const amountProblem = computed(() => (amount.value < TOP_UP_MIN ? 'At least $5' : amount.value > TOP_UP_MAX ? 'Up to $50,000 at a time' : null))

/* The bar's "Add funds" lands here with ?topup=1: open the dialog, and drop the flag so a
   reload does not open it again. */
watch(() => route.query.topup, (flag) => {
  if (!flag) return
  toppingUp.value = true
  const { topup: _, ...query } = route.query
  navigateTo({ query }, { replace: true })
}, { immediate: true })

const topUp = () => store.topUpUrl(amount.value)

const planWords = computed(() => {
  const price = plan.value?.monthly_price_micros ?? billing.value?.plan.monthly_price_micros ?? 0
  const sub = subscription.value
  if (!price && !sub) return 'No charge'
  if (!sub) return `${writeMicros(price)} a month`
  const yearly = sub.interval === 'year'
  const amountWords = writeMicros(yearly ? plan.value?.yearly_price_micros ?? 0 : price)
  const end = sub.period_end ? formatDate(sub.period_end) : ''
  if (sub.cancel_at_period_end) return `Ends ${end}`
  return `${amountWords} a ${yearly ? 'year' : 'month'}${end ? `, renews ${end}` : ''}`
})
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

    <p v-if="returned" :class="returned === 'success' ? 'bg-success/8 ring-success/20' : 'bg-ink/4 ring-line'" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius-large) px-4 py-3 text-xs leading-5 text-ink/80 ring-1 ring-inset">
      <Icon
        :class="returned === 'success' ? 'text-success' : 'text-ink/50'"
        :name="returned === 'success' ? 'material-symbols:check-circle-outline' : 'material-symbols:info-outline'"
        aria-hidden="true"
        class="mt-0.5 flex-none text-base"
      />
      <span class="flex-1">{{ returned === 'success' ? 'Back from Stripe. What you paid for shows here as soon as Stripe confirms it, usually within seconds.' : 'Back from Stripe. Nothing was paid or saved.' }}</span>
      <button
        aria-label="Dismiss"
        class="cursor-pointer text-ink/45 hover:text-ink"
        type="button"
        @click="returned = null"
      >
        <Icon aria-hidden="true" name="material-symbols:close" />
      </button>
    </p>

    <LoadState
      :error="error"
      :loading="loading && !billing"
      what="billing"
      @retry="store.load(true)"
    >
      <template v-if="billing">
        <!-- The figures, at a glance. -->
        <section aria-label="At a glance" class="grid overflow-hidden rounded-(--ps-radius-large) border border-line bg-surface sm:grid-cols-2 xl:grid-cols-4">
          <div class="flex flex-col gap-1 border-b border-line p-4 sm:border-r xl:border-b-0">
            <span class="flex items-center justify-between gap-2 text-[11px] text-ink/50">
              Balance
              <StatusBadge :label="state.label" :tone="state.tone" />
            </span>
            <span class="font-mono text-2xl text-ink">{{ writeMicros(billing.live.available_micros) }}</span>
            <span class="text-[11px] text-ink/50">{{ billing.live.pending_micros ? `${writeMicros(billing.live.pending_micros)} of usage about to be taken` : 'Usage is taken from it as it happens' }}</span>
          </div>
          <div class="flex flex-col gap-1 border-b border-line p-4 xl:border-r xl:border-b-0">
            <span class="text-[11px] text-ink/50">Charged this month</span>
            <span class="font-mono text-2xl text-ink">{{ writeMicros(month?.charged_micros ?? 0) }}</span>
            <span class="text-[11px] text-ink/50">{{ month ? `${writeFigure(month.tracked_players)} tracked players, ${writeFigure(month.included_players)} included` : 'Usage past the plan\'s allowance' }}</span>
          </div>
          <button class="flex cursor-pointer flex-col gap-1 border-b border-line p-4 text-left transition-colors duration-150 hover:bg-ink/2 sm:border-r sm:border-b-0" type="button" @click="tab = 'auto'">
            <span class="text-[11px] text-ink/50">Auto top-up</span>
            <span :class="auto?.enabled ? 'text-ink' : 'text-ink/45'" class="text-2xl">{{ auto?.enabled ? 'On' : 'Off' }}</span>
            <span class="text-[11px] text-ink/50">{{ auto?.enabled ? `Adds ${writeMicros(auto.amount_micros)} under ${writeMicros(auto.threshold_micros)}` : cards.length ? 'The wallet is only topped up by hand' : 'Needs a payment method' }}</span>
          </button>
          <button class="flex cursor-pointer flex-col gap-1 p-4 text-left transition-colors duration-150 hover:bg-ink/2" type="button" @click="tab = 'plan'">
            <span class="text-[11px] text-ink/50">Plan</span>
            <span class="text-2xl text-ink">{{ billing.plan.name }}</span>
            <span class="text-[11px] text-ink/50">{{ planWords }}</span>
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
      </template>
    </LoadState>

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
      intro="Adds to the wallet. Usage is taken from it as it happens, and credit bought never expires."
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
