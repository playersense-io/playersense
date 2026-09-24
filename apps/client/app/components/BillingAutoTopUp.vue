<script setup lang="ts">
// Billing > Auto top-up: charge the default card when the balance runs low, so collection
// never stops on an empty wallet. It needs a card on file, which the backend enforces too
// (PUT /billing/auto-topup refuses without one), so with no card the tab is the way to add
// one and nothing else. The rules are the backend's: a top-up of $5 to $5,000, more than the
// balance it starts at, at most three a day.
const { billing, defaultCard, saveCard, saveAutoTopUp } = useBilling()

defineEmits<{ cards: [] }>()

const enabled = ref(billing.value.auto.enabled)
const threshold = ref(billing.value.auto.threshold_micros)
const amount = ref(billing.value.auto.amount_micros)

const problem = computed(() => (enabled.value ? autoTopUpProblem(threshold.value, amount.value) : null))
const dirty = computed(() => enabled.value !== billing.value.auto.enabled
  || (enabled.value && (threshold.value !== billing.value.auto.threshold_micros || amount.value !== billing.value.auto.amount_micros)))

const saved = ref(false)

function save() {
  saveAutoTopUp(enabled.value, threshold.value, amount.value)
  saved.value = true
  setTimeout(() => (saved.value = false), 2400)
}

function discard() {
  enabled.value = billing.value.auto.enabled
  threshold.value = billing.value.auto.threshold_micros
  amount.value = billing.value.auto.amount_micros
}

const cardName = computed(() => (defaultCard.value ? `${CARD_BRANDS[defaultCard.value.brand].label} ending ${defaultCard.value.last4}` : ''))

// --- No card yet -----------------------------------------------------------------------------

const adding = ref(false)

function added() {
  const card = saveCard()
  return `Saved. ${CARD_BRANDS[card.brand].label} ending ${card.last4} is on file, and auto top-up can be turned on.`
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard v-if="!billing.cards.length" title="Auto top-up">
      <EmptyState
        description="Auto top-up charges a saved card when the balance runs low, so there has to be one first. Cards are added on Stripe, never on this page."
        icon="material-symbols:lock-outline"
        title="Add a payment method to turn this on."
      >
        <template #actions>
          <div class="flex items-center gap-2">
            <v-btn
              class="text-xs font-medium"
              color="primary"
              size="small"
              variant="flat"
              @click="adding = true"
            >
              Add payment method
            </v-btn>
          </div>
        </template>
      </EmptyState>
    </SectionCard>

    <template v-else>
      <SectionCard description="Charges the default card when the balance runs low, so events keep being collected past the plan's allowance." title="Auto top-up">
        <template #badge>
          <StatusBadge :label="billing.auto.enabled ? 'On' : 'Off'" :tone="billing.auto.enabled ? 'success' : 'neutral'" />
        </template>

        <div class="flex max-w-xl flex-col gap-5">
          <SwitchRow v-model="enabled" label="Top up automatically" note="Off, the wallet is only topped up by hand." />

          <div :class="enabled ? '' : 'pointer-events-none opacity-45'" class="grid gap-4 transition-opacity duration-150 sm:grid-cols-2">
            <label class="flex flex-col gap-1.5">
              <span class="text-xs text-ink/60">When the balance falls under</span>
              <MoneyField v-model="threshold" :error="Boolean(problem)" label="When the balance falls under" />
            </label>
            <label class="flex flex-col gap-1.5">
              <span class="text-xs text-ink/60">Add</span>
              <MoneyField v-model="amount" :error="Boolean(problem)" label="Add" />
            </label>
          </div>

          <p v-if="problem" class="m-0 text-xs text-error">
            {{ problem }}.
          </p>
          <p v-else-if="enabled" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius) bg-ink/4 px-3.5 py-3 text-xs leading-5 text-ink/75">
            <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-primary" name="material-symbols:autorenew" />
            <span>
              When the balance falls under <span class="font-mono text-ink">{{ writeMicros(threshold) }}</span>,
              {{ cardName }} is charged <span class="font-mono text-ink">{{ writeMicros(amount) }}</span>.
              At most {{ billing.auto.daily_limit }} times a day, so a runaway day costs at most
              <span class="font-mono text-ink">{{ writeMicros(amount * billing.auto.daily_limit) }}</span>.
            </span>
          </p>

          <div class="flex items-center justify-end gap-2 border-t border-line pt-4">
            <span v-if="saved" class="mr-auto flex items-center gap-1 text-xs text-success">
              <Icon aria-hidden="true" class="text-sm" name="material-symbols:check" />
              Saved
            </span>
            <v-btn
              :disabled="!dirty"
              class="text-xs font-medium"
              color="neutral"
              size="small"
              variant="text"
              @click="discard"
            >
              Discard
            </v-btn>
            <v-btn
              :disabled="!dirty || Boolean(problem)"
              class="text-xs font-medium"
              color="primary"
              size="small"
              variant="flat"
              @click="save"
            >
              Save
            </v-btn>
          </div>
        </div>
      </SectionCard>

      <SectionCard title="How it charges">
        <div class="flex flex-col divide-y divide-line">
          <SettingRow description="Auto top-up always charges the default card. Change it under Payment methods." label="Card">
            <template #actions>
              <button class="flex cursor-pointer items-center gap-1 text-xs text-ink/60 transition-colors hover:text-ink" type="button" @click="$emit('cards')">
                <span class="font-mono">{{ cardName }}</span>
                <Icon aria-hidden="true" class="text-sm" name="material-symbols:chevron-right" />
              </button>
            </template>
          </SettingRow>
          <SettingRow :description="`${billing.auto.used_today} of ${billing.auto.daily_limit} used today. The count starts again at midnight UTC.`" fixed="Fixed" label="Three charges a day, at most" />
          <SettingRow description="A declined charge turns nothing off. Stripe's reason is shown here, and the next check tries again." label="When a charge fails" />
        </div>
        <p v-if="billing.auto.last_error" class="m-0 mt-4 flex items-start gap-2 rounded-(--ps-radius) bg-error/8 px-3.5 py-3 text-xs leading-5 text-ink/80 ring-1 ring-error/20 ring-inset">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-error" name="material-symbols:error-outline" />
          The last charge failed: {{ billing.auto.last_error }}
        </p>
      </SectionCard>
    </template>

    <StripeHandoff
      v-if="adding"
      v-model="adding"
      :points="[
        'Stripe Checkout opens to save a card. Nothing is charged.',
        'Stripe checks the card with the bank, and may ask you to confirm it.',
        'Back here, auto top-up can be turned on against it.'
      ]"
      :run="added"
      action="Continue to Stripe Checkout"
      intro="The card auto top-up charges. It becomes the default, as the first card on file."
      title="Add a payment method"
    />
  </div>
</template>
