<script setup lang="ts">
// The step out to Stripe and back, for everything that touches a card: a top-up and a new card
// are Stripe Checkout, the default card and the invoice details are the customer portal. It
// says what happens there and what PlayerSense keeps, sends the reader on, and says what
// happened when they return. Whatever the dialog asks first (an amount, a plan) is its slot.
//
// Stripe is simulated here: "Continue" waits as the redirect would, and `run` stands in for
// what the return and Stripe's webhook do, returning the sentence that says so.
const props = withDefaults(defineProps<{
  title: string
  intro: string
  /** What happens on Stripe, a line each. */
  points: readonly string[]
  /** The button: "Continue to Stripe Checkout". */
  action: string
  /** Nothing to continue with yet: an amount out of bounds. */
  disabled?: boolean
  /** What coming back does. Returns what to tell the reader. */
  run: () => string
  /** Where Stripe opens: Checkout, or the portal. */
  place?: 'checkout' | 'portal'
}>(), {
  disabled: false,
  place: 'checkout'
})

const open = defineModel<boolean>({ required: true })

const phase = ref<'ready' | 'away' | 'back'>('ready')
const outcome = ref('')

async function go() {
  phase.value = 'away'
  await new Promise(resolve => setTimeout(resolve, 1400))
  outcome.value = props.run()
  phase.value = 'back'
}
</script>

<template>
  <v-dialog v-model="open" :persistent="phase === 'away'" max-width="520">
    <v-card border flat>
      <header class="flex items-start gap-3 border-b border-line p-5">
        <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-primary/10 text-lg text-primary">
          <Icon :name="place === 'portal' ? 'material-symbols:manage-accounts-outline' : 'material-symbols:lock-outline'" />
        </span>
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ title }}
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            {{ intro }}
          </p>
        </div>
      </header>

      <div v-if="phase === 'ready'" class="flex flex-col gap-4 p-5">
        <slot />
        <ol class="m-0 flex list-none flex-col gap-2 p-0">
          <li v-for="(point, index) in points" :key="index" class="flex items-start gap-2.5 text-xs leading-5 text-ink/75">
            <span class="flex size-5 flex-none items-center justify-center rounded-full bg-ink/6 font-mono text-[10px] text-ink/65">{{ index + 1 }}</span>
            {{ point }}
          </li>
        </ol>
        <p class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-[11px] leading-4 text-ink/65">
          <Icon aria-hidden="true" class="mt-px flex-none text-sm text-ink/45" name="material-symbols:shield-outline" />
          The card is entered on Stripe's page and stays at Stripe. PlayerSense keeps its brand, last four digits and expiry, and nothing else.
        </p>
      </div>

      <div v-else-if="phase === 'away'" class="flex flex-col items-center gap-3 px-5 py-10 text-center">
        <v-progress-circular
          color="primary"
          indeterminate
          size="28"
          width="3"
        />
        <span class="text-xs text-ink/65">Opening Stripe {{ place === 'portal' ? 'customer portal' : 'Checkout' }}…</span>
      </div>

      <div v-else class="flex flex-col items-center gap-3 px-5 py-8 text-center">
        <span aria-hidden="true" class="flex size-10 items-center justify-center rounded-full bg-success/12 text-xl text-success">
          <Icon name="material-symbols:check" />
        </span>
        <span class="max-w-sm text-xs leading-5 text-ink/75">{{ outcome }}</span>
      </div>

      <footer class="flex justify-end gap-2 border-t border-line px-5 py-3">
        <template v-if="phase === 'ready'">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Cancel
          </v-btn>
          <v-btn
            :disabled="disabled"
            append-icon="material-symbols:open-in-new"
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="go"
          >
            {{ action }}
          </v-btn>
        </template>
        <v-btn
          v-else
          :disabled="phase === 'away'"
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="open = false"
        >
          Done
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>
