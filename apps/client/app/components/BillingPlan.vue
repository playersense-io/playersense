<script setup lang="ts">
// Billing > Plan: the plan the organization is on, how much of its allowance this month has
// used, and the others it could move to. A plan is bought on Stripe Checkout and changed or
// canceled in the customer portal; the list is GET /pricing's (sample plans here), never
// written into the dashboard.
const { billing, plan, subscribe } = useBilling()

/* Binary, as writeFigure writes bytes, so 25 included reads as 25 GB. */
const GB = 1024 ** 3

/* This month so far, across every project: the sample figures the overview's meters draw. */
const USED = { included_mau: 18_420, included_events: 31_400_000, included_storage_gb_month: 14.2 * GB }

const METERS = [
  { key: 'included_mau', label: 'Tracked players', format: 'number', info: 'Players with qualifying activity this month, counted once however many days they played.' },
  { key: 'included_events', label: 'Events', format: 'number', info: 'Events accepted by ingestion this month, every project and environment.' },
  { key: 'included_storage_gb_month', label: 'Retained storage', format: 'bytes', info: 'Raw events kept for replay and export, averaged over the month.' }
] as const

const included = (key: string) => (plan.value.entitlements[key] ?? 0) * (key === 'included_storage_gb_month' ? GB : 1)

const limits = computed<Fact[]>(() => [
  { key: 'retention', label: 'Raw events kept', value: `Up to ${plan.value.entitlements.max_raw_retention_days ?? 30} days`, to: '/settings/data-retention' },
  { key: 'projects', label: 'Projects', value: String(plan.value.entitlements.max_projects ?? 1), mono: true },
  { key: 'webhooks', label: 'Webhook destinations', value: String(plan.value.entitlements.max_webhook_endpoints ?? 1), mono: true },
  { key: 'renews', label: plan.value.monthly_price_micros ? 'Renews' : 'Billed', value: plan.value.monthly_price_micros ? formatDate(billing.value.renews_at) : 'Never' }
])

const price = (entry: Plan, interval: 'month' | 'year') => (interval === 'year' ? entry.yearly_price_micros ?? 0 : entry.monthly_price_micros)

// --- Changing plan ---------------------------------------------------------------------------

const INTERVALS: SegmentedOption[] = [
  { value: 'month', label: 'Monthly' },
  { value: 'year', label: 'Yearly, 2 months free' }
]
const interval = ref<'month' | 'year'>(billing.value.interval)

const FEATURES: Record<string, string> = {
  slack_reports: 'Scheduled reports to Slack',
  premium_support: 'Support within a working day',
  mcp_access: 'MCP access for your agents'
}

const choosing = ref<Plan | null>(null)
const handoff = computed({
  get: () => choosing.value !== null,
  set: (open: boolean) => {
    if (!open) choosing.value = null
  }
})

const current = (entry: Plan) => entry.key === billing.value.plan && (!entry.monthly_price_micros || interval.value === billing.value.interval)

function switchTo() {
  const next = choosing.value!
  subscribe(next.key, interval.value)
  return `Subscribed. ${next.name} starts today, ${writeMicros(price(next, interval.value))} a ${interval.value}, with the unused part of ${plan.value.name} credited on the first invoice.`
}

/* A cheaper plan than this one: offered, not pushed. Free is reached by canceling, in the portal. */
const lower = (entry: Plan) => entry.monthly_price_micros < plan.value.monthly_price_micros

const managing = ref(false)
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard :description="`${plan.description} Usage past an allowance is taken from the wallet.`" :title="`${plan.name} plan`">
      <template #actions>
        <v-btn
          v-if="plan.monthly_price_micros"
          append-icon="material-symbols:open-in-new"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="managing = true"
        >
          Manage on Stripe
        </v-btn>
      </template>

      <div class="flex flex-col gap-6">
        <div class="grid gap-6 md:grid-cols-3">
          <UsageMeter
            v-for="meter in METERS"
            :key="meter.key"
            :format="meter.format"
            :included="included(meter.key)"
            :info="meter.info"
            :label="meter.label"
            :used="USED[meter.key]"
          />
        </div>
        <FactGrid :facts="limits" label="What the plan includes" />
      </div>
    </SectionCard>

    <SectionCard description="Moving up takes effect at once, and the rest of this period is credited. Moving down takes effect when the period ends." title="Plans">
      <template #actions>
        <SegmentedControl v-model="interval" :options="INTERVALS" label="Billed" />
      </template>

      <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <article
          v-for="entry in SAMPLE_PLANS"
          :key="entry.key"
          :class="current(entry) ? 'border-primary ring-1 ring-primary ring-inset' : 'border-line'"
          class="flex flex-col gap-4 rounded-(--ps-radius-large) border p-4"
        >
          <header class="flex flex-col gap-1">
            <span class="flex items-center justify-between gap-2">
              <h3 class="m-0 text-sm font-medium text-ink">{{ entry.name }}</h3>
              <Tag v-if="current(entry)" label="Current" tone="primary" />
            </span>
            <p class="m-0 min-h-10 text-xs leading-5 text-ink/60">
              {{ entry.description }}
            </p>
          </header>

          <p class="m-0 flex items-baseline gap-1">
            <template v-if="entry.subscribable">
              <span class="font-mono text-xl text-ink">{{ writeMicros(price(entry, interval)) }}</span>
              <span class="text-[11px] text-ink/50">a {{ interval }}</span>
            </template>
            <span v-else class="text-xl text-ink">By contract</span>
          </p>

          <ul class="m-0 flex flex-1 list-none flex-col gap-1.5 p-0 text-xs text-ink/75">
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              {{ writeFigure(entry.entitlements.included_mau ?? 0, { compact: true }) }} tracked players a month
            </li>
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              {{ writeFigure(entry.entitlements.included_events ?? 0, { compact: true }) }} events a month
            </li>
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              Raw events kept up to {{ entry.entitlements.max_raw_retention_days }} days
            </li>
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              {{ entry.entitlements.max_projects }} {{ entry.entitlements.max_projects === 1 ? 'project' : 'projects' }}
            </li>
            <li v-for="feature in entry.features" :key="feature" class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              {{ FEATURES[feature] ?? feature }}
            </li>
          </ul>

          <v-btn
            v-if="!entry.subscribable"
            class="text-xs font-medium"
            color="neutral"
            href="mailto:sales@playersense.dev"
            size="small"
            variant="outlined"
          >
            Talk to us
          </v-btn>
          <v-btn
            v-else
            :disabled="current(entry)"
            :color="lower(entry) ? 'neutral' : 'primary'"
            :variant="current(entry) || lower(entry) ? 'outlined' : 'flat'"
            class="text-xs font-medium"
            size="small"
            @click="entry.monthly_price_micros ? choosing = entry : managing = true"
          >
            {{ current(entry) ? 'Your plan' : entry.key === billing.plan ? `Bill ${interval}ly` : !entry.monthly_price_micros ? 'Cancel to move here' : `Switch to ${entry.name}` }}
          </v-btn>
        </article>
      </div>
    </SectionCard>

    <StripeHandoff
      v-if="choosing"
      v-model="handoff"
      :points="[
        `Stripe Checkout opens for ${choosing.name}, ${writeMicros(price(choosing, interval))} a ${interval}.`,
        'Pay with a saved card or a new one, and Stripe sends you back here.',
        'The plan changes when Stripe confirms the subscription, usually within seconds.'
      ]"
      :run="switchTo"
      :title="`Switch to ${choosing.name}`"
      action="Continue to Stripe Checkout"
      intro="Allowances change the moment the plan does. Usage already past the old allowance stays billed as it was."
    />

    <StripeHandoff
      v-if="managing"
      v-model="managing"
      :points="[
        'The Stripe customer portal opens on your subscription.',
        'Change how often you are billed, or cancel at the end of the period.',
        'Come back here and the plan shows what you chose.'
      ]"
      :run="() => `Back from Stripe. ${plan.name} is unchanged and renews on ${formatDate(billing.renews_at)}.`"
      action="Open the customer portal"
      intro="Canceling keeps the plan to the end of the period you have paid for, then moves the organization to Free."
      place="portal"
      title="Manage the subscription"
    />
  </div>
</template>
