<script setup lang="ts">
// Billing > Payment methods: the cards on file, as the metadata Stripe's webhook hands over
// (brand, last four digits, expiry). A card is added on Stripe Checkout; which one is the
// default and removing one are the customer portal. The default card is the one auto top-up
// charges, so the list says so, and says when a card is about to stop working.
const { billing, defaultCard, saveCard, setDefault, removeCard } = useBilling()

defineEmits<{ autoTopUp: [] }>()

const now = Date.now()

const cards = computed(() => [...billing.value.cards].sort((a, b) => Number(b.is_default) - Number(a.is_default) || Date.parse(a.added_at) - Date.parse(b.added_at)))

const expiryText = (card: PaymentMethod) => `${String(card.exp_month).padStart(2, '0')}/${card.exp_year}`

// --- Stripe --------------------------------------------------------------------------------

const adding = ref(false)

function added() {
  const card = saveCard()
  return `Saved. ${CARD_BRANDS[card.brand].label} ending ${card.last4}, expiring ${expiryText(card)}, is on file${card.is_default ? ' and is the default' : ''}.`
}

/* Making a card the default and removing one both go through the portal. */
const portal = ref<{ card: PaymentMethod, action: 'default' | 'remove' } | null>(null)
const portalOpen = computed({
  get: () => portal.value !== null,
  set: (open: boolean) => {
    if (!open) portal.value = null
  }
})

const cardName = (card: PaymentMethod) => `${CARD_BRANDS[card.brand].label} ending ${card.last4}`

const lastWithAuto = computed(() => billing.value.cards.length === 1 && billing.value.auto.enabled)

function portalRun() {
  const { card, action } = portal.value!
  if (action === 'default') {
    setDefault(card.id)
    return `Done. ${cardName(card)} is the default now: top-ups and the plan are charged to it.`
  }
  const wasAuto = lastWithAuto.value
  removeCard(card.id)
  return `Removed. ${cardName(card)} is off file at Stripe and here.${wasAuto ? ' Auto top-up is off, with no card left to charge.' : ''}`
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard :padded="!cards.length" description="Cards are entered on Stripe and stay there. PlayerSense keeps a card's brand, last four digits and expiry, to show it here." title="Payment methods">
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          variant="flat"
          @click="adding = true"
        >
          Add payment method
        </v-btn>
      </template>

      <ul v-if="cards.length" class="m-0 flex list-none flex-col divide-y divide-line p-0">
        <li v-for="card in cards" :key="card.id" class="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3.5">
          <span class="flex min-w-56 flex-1 items-center gap-3.5">
            <span aria-hidden="true" class="flex h-8 w-12 flex-none items-center justify-center rounded-(--ps-radius-small) border border-line bg-white">
              <Icon :name="CARD_BRANDS[card.brand].icon" class="text-2xl" />
            </span>
            <span class="flex min-w-0 flex-col">
              <span class="flex items-center gap-2 text-sm text-ink">
                {{ CARD_BRANDS[card.brand].label }}
                <span class="font-mono text-xs text-ink/70">•••• {{ card.last4 }}</span>
              </span>
              <span class="text-[11px] text-ink/55">Added {{ formatDate(card.added_at) }}</span>
            </span>
          </span>

          <span class="flex w-44 items-center gap-1.5 text-xs">
            <template v-if="cardExpiry(card, now) === 'expired'">
              <Icon aria-hidden="true" class="text-sm text-error" name="material-symbols:error-outline" />
              <span class="text-error">Expired {{ expiryText(card) }}</span>
            </template>
            <template v-else-if="cardExpiry(card, now) === 'soon'">
              <Icon aria-hidden="true" class="text-sm text-warning" name="material-symbols:schedule-outline" />
              <span class="text-ink/80">Expires <span class="font-mono">{{ expiryText(card) }}</span></span>
            </template>
            <span v-else class="text-ink/60">Expires <span class="font-mono">{{ expiryText(card) }}</span></span>
          </span>

          <span class="flex w-24 justify-end">
            <StatusBadge v-if="card.is_default" label="Default" tone="success" />
          </span>

          <v-btn
            :aria-label="`Actions for ${cardName(card)}`"
            icon
            size="small"
            variant="text"
          >
            <Icon aria-hidden="true" class="text-lg" name="material-symbols:more-vert" />
            <v-menu activator="parent" location="bottom end" :offset="4">
              <v-list min-width="200" nav>
                <v-list-item
                  :disabled="card.is_default"
                  prepend-icon="material-symbols:star-outline"
                  title="Make default"
                  @click="portal = { card, action: 'default' }"
                />
                <v-list-item
                  base-color="error"
                  prepend-icon="material-symbols:delete-outline"
                  title="Remove"
                  @click="portal = { card, action: 'remove' }"
                />
              </v-list>
            </v-menu>
          </v-btn>
        </li>
      </ul>

      <EmptyState
        v-else
        description="Add a card to top up in one step and to turn on auto top-up. It is entered on Stripe, never on this page."
        icon="material-symbols:credit-card-outline"
        title="No payment method yet."
      >
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="adding = true"
          >
            Add payment method
          </v-btn>
        </template>
      </EmptyState>
    </SectionCard>

    <SectionCard v-if="defaultCard" title="What the default card pays for">
      <div class="flex flex-col divide-y divide-line">
        <SettingRow description="Every plan renewal, and upgrades the moment they are made." label="The plan" />
        <SettingRow description="When the balance falls under the amount you set, up to three times a day." label="Auto top-up">
          <template #actions>
            <button class="flex cursor-pointer items-center gap-1 text-xs text-ink/60 transition-colors hover:text-ink" type="button" @click="$emit('autoTopUp')">
              {{ billing.auto.enabled ? 'On' : 'Off' }}
              <Icon aria-hidden="true" class="text-sm" name="material-symbols:chevron-right" />
            </button>
          </template>
        </SettingRow>
        <SettingRow description="Offered first on Stripe Checkout. Another card can be chosen there." label="Top-ups by hand" />
      </div>
    </SectionCard>

    <StripeHandoff
      v-if="adding"
      v-model="adding"
      :points="[
        'Stripe Checkout opens to save a card. Nothing is charged.',
        'Stripe checks the card with the bank, and may ask you to confirm it.',
        'Back here, the card is listed by its brand, last four digits and expiry.'
      ]"
      :run="added"
      action="Continue to Stripe Checkout"
      intro="For top-ups, the plan and auto top-up. The first card saved becomes the default."
      title="Add a payment method"
    />

    <StripeHandoff
      v-if="portal"
      v-model="portalOpen"
      :points="portal.action === 'default'
        ? [`The Stripe customer portal opens on your payment methods.`, `Choose ${cardName(portal.card)} as the default.`, 'Back here, it is marked Default.']
        : [`The Stripe customer portal opens on your payment methods.`, `Remove ${cardName(portal.card)}. Stripe deletes it.`, 'Back here, it is gone from the list.']"
      :run="portalRun"
      :action="'Open the customer portal'"
      :intro="portal.action === 'default'
        ? 'The default card pays for the plan and for auto top-up.'
        : lastWithAuto ? 'This is the last card, so auto top-up turns off with it: there would be nothing to charge.' : 'Credit already bought stays in the wallet.'"
      :title="portal.action === 'default' ? 'Make the default card' : 'Remove the card'"
      place="portal"
    />
  </div>
</template>
