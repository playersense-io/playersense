<script setup lang="ts">
// The right-hand end of the app bar: what the organization has to spend and the way to
// add to it, the docs, notifications and the theme.
//
// The balance is the organization's, because the wallet is, and it links to Billing: the
// next thing somebody wants after reading a low balance is the screen that explains it.
// When the organization owes, that is the figure shown, in the warning ink. Balance and
// Docs are desktop only — on a phone the breadcrumb needs the row.
const { mdAndUp } = useDisplay()

// The sample wallet Billing keeps in this browser, so the bar and Billing agree until both
// read GET /v1/users/me's `balance` (LiveBalanceOut).
const { balance: available } = useBilling()
const balance = computed(() => (import.meta.client ? { available_micros: available.value, owed_micros: 0 } : null))

const dollars = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })

const figure = computed(() => {
  if (!balance.value) return null
  const owes = balance.value.owed_micros > 0
  return {
    label: owes ? 'Owed' : 'Balance',
    amount: dollars.format((owes ? balance.value.owed_micros : balance.value.available_micros) / 1_000_000),
    owes
  }
})

const DOCS_URL = 'https://docs.playersense.io'
</script>

<template>
  <div class="flex items-center gap-1">
    <template v-if="mdAndUp && figure">
      <!-- Monospaced and tabular so the digits sit still as the figure changes, and the
           word a shade quieter than the number. -->
      <NuxtLink
        class="
          mr-1 flex h-7 items-baseline gap-1.5 rounded-(--ps-radius-small) px-2 pt-1.5
          text-xs no-underline transition-colors duration-150
          hover:bg-ink/5
          focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary
        "
        to="/settings/billing"
      >
        <span class="text-ink/55">{{ figure.label }}</span>
        <span :class="figure.owes ? 'text-warning' : 'text-ink'" class="font-mono tabular-nums">
          {{ figure.amount }}
        </span>
      </NuxtLink>
    </template>

    <!-- Funding lives on Billing, where a top-up opens Stripe Checkout
         (POST /v1/organizations/{org}/billing/top-ups); this is the way there. -->
    <v-btn
      class="text-xs"
      color="primary"
      prepend-icon="material-symbols:add"
      size="small"
      to="/settings/billing?topup=1"
      variant="flat"
    >
      Add funds
    </v-btn>

    <span aria-hidden="true" class="mx-1.5 h-4 w-px bg-shell-line" />

    <v-btn
      v-if="mdAndUp"
      :href="DOCS_URL"
      class="
        text-xs text-ink/70
        hover:text-ink
      "
      rel="noreferrer"
      size="small"
      target="_blank"
      variant="text"
    >
      Docs
    </v-btn>

    <HeaderNotifications />
    <ThemeToggle />
  </div>
</template>
