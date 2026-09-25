<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { PlanRecord } from '~/stores/billing'

// Billing > Plan: the plan the organization is on, how much of its allowance this month has
// used (GET …/usage), and the others it could move to (GET /pricing). A plan is bought on
// Stripe Checkout and changed or canceled in the customer portal.
const store = useBillingStore()
const { billing, plans } = storeToRefs(store)
const workspace = useWorkspaceStore()

const usage = useApiGet<Schema<'UsageOut'>>(() => workspace.orgPath && `${workspace.orgPath}/usage`)

/* Binary, as writeFigure writes bytes, so 25 included reads as 25 GB. */
const GB = 1024 ** 3

const entitlements = computed<Record<string, number>>(() => ({ ...(billing.value?.plan.entitlements ?? {}) }))
const month = computed(() => billing.value?.month as { tracked_players: number, included_players: number } | undefined)
const subscription = computed(() => billing.value?.subscription as { status: string, interval?: string, period_end?: string, cancel_at_period_end: boolean } | undefined)

/* This month's usage, or null (no data) until a day of it has been counted. */
const counted = computed(() => (usage.data.value?.days?.length ? usage.data.value.totals : null))

const METERS = computed(() => [
  {
    key: 'included_mau',
    label: 'Tracked players',
    format: 'number' as const,
    info: 'Players with qualifying activity this month, counted once however many days they played.',
    used: month.value?.tracked_players ?? null,
    included: month.value?.included_players ?? entitlements.value.included_mau ?? 0
  },
  {
    key: 'included_events',
    label: 'Events',
    format: 'number' as const,
    info: 'Events accepted by ingestion this month, every project and environment.',
    used: counted.value?.events ?? null,
    included: entitlements.value.included_events ?? 0
  },
  {
    key: 'included_storage_gb_month',
    label: 'Retained storage',
    format: 'bytes' as const,
    info: 'Raw events kept for replay and export, inside the retention window today.',
    used: counted.value?.retained_bytes ?? null,
    included: (entitlements.value.included_storage_gb_month ?? 0) * GB
  }
])

const limits = computed<Fact[]>(() => [
  { key: 'retention', label: 'Raw events kept', value: `Up to ${entitlements.value.max_raw_retention_days ?? 30} days`, to: '/settings/data-retention' },
  { key: 'projects', label: 'Projects', value: String(entitlements.value.max_projects ?? 1), mono: true },
  { key: 'webhooks', label: 'Webhook destinations', value: String(entitlements.value.max_webhook_endpoints ?? 1), mono: true },
  {
    key: 'renews',
    label: subscription.value?.cancel_at_period_end ? 'Ends' : 'Renews',
    value: subscription.value?.period_end ? formatDate(subscription.value.period_end) : 'Not billed'
  }
])

const price = (entry: PlanRecord, interval: 'month' | 'year') => (interval === 'year' ? entry.yearly_price_micros : entry.monthly_price_micros)

// --- Changing plan ---------------------------------------------------------------------------

const INTERVALS: SegmentedOption[] = [
  { value: 'month', label: 'Monthly' },
  { value: 'year', label: 'Yearly' }
]
const interval = ref<'month' | 'year'>(subscription.value?.interval === 'year' ? 'year' : 'month')

/* The plans on sale, and the one the organization is on even when it is not. */
const offered = computed(() => plans.value.filter(entry => entry.active || entry.key === billing.value?.plan.key))

const choosing = ref<PlanRecord | null>(null)
const handoff = computed({
  get: () => choosing.value !== null,
  set: (open: boolean) => {
    if (!open) choosing.value = null
  }
})

const isPlan = (entry: PlanRecord) => entry.key === billing.value?.plan.key
const current = (entry: PlanRecord) => isPlan(entry) && (!entry.monthly_price_micros || !subscription.value || interval.value === (subscription.value.interval ?? 'month'))
const canBill = (entry: PlanRecord) => (entry.intervals ?? ['month', 'year']).includes(interval.value) && price(entry, interval.value) > 0

const switchTo = () => store.subscribeUrl(choosing.value!.key, interval.value)

/* A cheaper plan than this one: offered, not pushed. Free is reached by canceling, in the portal. */
const lower = (entry: PlanRecord) => entry.monthly_price_micros < (billing.value?.plan.monthly_price_micros ?? 0)

const managing = ref(false)
const portal = () => store.portalUrl()
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard v-if="billing" :description="`${store.plan?.description ?? ''} Usage past an allowance is taken from the wallet.`" :title="`${billing.plan.name} plan`">
      <template #actions>
        <v-btn
          v-if="subscription"
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
            :included="meter.included"
            :info="meter.info"
            :label="meter.label"
            :used="meter.used"
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
          v-for="entry in offered"
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
            <template v-if="!entry.monthly_price_micros && !entry.yearly_price_micros && entry.subscribable">
              <span class="font-mono text-xl text-ink">$0</span>
            </template>
            <template v-else-if="entry.subscribable && canBill(entry)">
              <span class="font-mono text-xl text-ink">{{ writeMicros(price(entry, interval)) }}</span>
              <span class="text-[11px] text-ink/50">a {{ interval }}</span>
            </template>
            <span v-else-if="entry.subscribable" class="text-sm text-ink/60">Not billed {{ interval }}ly</span>
            <span v-else class="text-xl text-ink">By contract</span>
          </p>

          <ul class="m-0 flex flex-1 list-none flex-col gap-1.5 p-0 text-xs text-ink/75">
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              {{ writeFigure(entry.entitlements?.included_mau ?? 0, { compact: true }) }} tracked players a month
            </li>
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              {{ writeFigure(entry.entitlements?.included_events ?? 0, { compact: true }) }} events a month
            </li>
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              Raw events kept up to {{ entry.entitlements?.max_raw_retention_days ?? 30 }} days
            </li>
            <li class="flex items-center gap-2">
              <Icon aria-hidden="true" class="flex-none text-sm text-success" name="material-symbols:check" />
              {{ entry.entitlements?.max_projects ?? 1 }} {{ (entry.entitlements?.max_projects ?? 1) === 1 ? 'project' : 'projects' }}
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
            :color="lower(entry) ? 'neutral' : 'primary'"
            :disabled="current(entry) || (!canBill(entry) && !(subscription && !entry.monthly_price_micros))"
            :variant="current(entry) || lower(entry) ? 'outlined' : 'flat'"
            class="text-xs font-medium"
            size="small"
            @click="canBill(entry) ? choosing = entry : managing = true"
          >
            {{ current(entry) ? 'Your plan' : isPlan(entry) ? `Bill ${interval}ly` : !entry.monthly_price_micros ? 'Cancel to move here' : `Switch to ${entry.name}` }}
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
      :run="portal"
      action="Open the customer portal"
      intro="Canceling keeps the plan to the end of the period you have paid for, then moves the organization to Free."
      place="portal"
      title="Manage the subscription"
    />
  </div>
</template>
