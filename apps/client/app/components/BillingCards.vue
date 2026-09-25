<script setup lang="ts">
// Billing > Payment methods: the cards on file, as the metadata Stripe hands over (brand, last
// four digits, expiry). A card is added on Stripe Checkout; making one the default and removing
// one are asked of the API, which asks Stripe. The default card is the one auto top-up charges,
// so the list says so, and says when a card is about to stop working.
const store = useBillingStore()
const { cards: onFile, cardsProblem, defaultCard, auto } = storeToRefs(store)

defineEmits<{ autoTopUp: [] }>()

const now = Date.now()

const cards = computed(() => [...onFile.value].sort((a, b) => Number(b.is_default) - Number(a.is_default) || Date.parse(a.added_at) - Date.parse(b.added_at)))

const expiryText = (card: PaymentMethod) => `${String(card.exp_month).padStart(2, '0')}/${card.exp_year}`
const cardName = (card: PaymentMethod) => `${cardBrand(card.brand).label} ending ${card.last4}`

// --- Adding, on Stripe ----------------------------------------------------------------------

const adding = ref(false)
const addCard = () => store.addCardUrl()

// --- The default, and removing ----------------------------------------------------------------

const problem = ref('')
const busy = ref('')

async function makeDefault(card: PaymentMethod) {
  busy.value = card.id
  problem.value = ''
  try {
    await store.setDefaultCard(card)
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    busy.value = ''
  }
}

const removing = ref<PaymentMethod | null>(null)
const removeOpen = computed({
  get: () => removing.value !== null,
  set: (open: boolean) => {
    if (!open) removing.value = null
  }
})

const lastWithAuto = computed(() => onFile.value.length === 1 && Boolean(auto.value?.enabled))

async function remove(card: PaymentMethod) {
  busy.value = card.id
  problem.value = ''
  try {
    await store.removeCard(card)
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    busy.value = ''
  }
}

const refreshing = ref(false)

async function refresh() {
  refreshing.value = true
  try {
    await store.loadCards(true)
  } finally {
    refreshing.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard :padded="!cards.length" description="Cards are entered on Stripe and stay there. PlayerSense keeps a card's brand, last four digits and expiry, to show it here." title="Payment methods">
      <template #actions>
        <v-btn
          :loading="refreshing"
          aria-label="Read the cards from Stripe again"
          class="text-xs"
          color="neutral"
          icon="material-symbols:refresh"
          size="small"
          variant="text"
          @click="refresh"
        />
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

      <p v-if="problem || cardsProblem" class="m-0 border-b border-line px-5 py-3 text-xs text-error">
        {{ problem || cardsProblem }}
      </p>

      <ul v-if="cards.length" class="m-0 flex list-none flex-col divide-y divide-line p-0">
        <li v-for="card in cards" :key="card.id" class="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3.5">
          <span class="flex min-w-56 flex-1 items-center gap-3.5">
            <span aria-hidden="true" class="flex h-8 w-12 flex-none items-center justify-center rounded-(--ps-radius-small) border border-line bg-white">
              <Icon :name="cardBrand(card.brand).icon" class="text-2xl" />
            </span>
            <span class="flex min-w-0 flex-col">
              <span class="flex items-center gap-2 text-sm text-ink">
                {{ cardBrand(card.brand).label }}
                <span class="font-mono text-xs text-ink/70">•••• {{ card.last4 }}</span>
              </span>
              <span class="text-[11px] text-ink/55">{{ card.funding ? `${card.funding[0]!.toUpperCase()}${card.funding.slice(1)} card, added` : 'Added' }} {{ formatDate(card.added_at) }}</span>
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
            :loading="busy === card.id"
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
                  @click="makeDefault(card)"
                />
                <v-list-item
                  base-color="error"
                  prepend-icon="material-symbols:delete-outline"
                  title="Remove"
                  @click="removing = card"
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
              {{ auto?.enabled ? 'On' : 'Off' }}
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
      :run="addCard"
      action="Continue to Stripe Checkout"
      intro="For top-ups, the plan and auto top-up. The first card saved becomes the default."
      title="Add a payment method"
    />

    <ConfirmDialog
      v-if="removing"
      v-model="removeOpen"
      :description="lastWithAuto
        ? `This is the last card, so auto top-up turns off with it: there would be nothing to charge. Credit already bought stays in the wallet.`
        : 'Stripe deletes it. Credit already bought stays in the wallet.'"
      :title="`Remove ${cardName(removing)}?`"
      action="Remove card"
      tone="error"
      @confirm="remove(removing!)"
    />
  </div>
</template>
