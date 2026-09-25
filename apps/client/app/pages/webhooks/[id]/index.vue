<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// One destination, read top to bottom as the questions come: is it working (the warning, if it
// is not, and the day's figures), what was sent and what came back (the log, which is where most
// visits end), what it listens to, how its server checks a delivery is ours (the secret and a
// handler that verifies it), and who changed what.
const route = useRoute()

const id = computed(() => String(route.params.id ?? ''))
const { destination, loading, error, reload } = useWebhook(id)
const { setEnabled, remove, resendFailed } = useWebhookActions()
const { health: figures, loading: pending, reload: reloadHealth } = useWebhookHealth(() => (destination.value ? id.value : null))

usePageCrumb(() => destination.value?.name ?? 'Destination')

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const health = computed<WebhookHealth>(() => destination.value?.health ?? 'disabled')

// --- The log --------------------------------------------------------------------------------

const status = ref<'all' | DeliveryStatus>((DELIVERY_STATUSES as string[]).includes(String(route.query.status)) ? route.query.status as DeliveryStatus : 'all')
const log = useWebhookDeliveries(() => (destination.value ? [id.value] : []), () => ({ status: status.value }))
const deliveries = computed(() => log.deliveries.value)

/* Something was sent or resent: the log, the day's figures and the destination move with it. */
async function refresh() {
  await Promise.all([log.reload(), reloadHealth(), reload()])
}

// --- The day's figures --------------------------------------------------------------------

/* No health read is no data (a dash); a figure that needs a delivery is a dash without one. */
const board = computed<Kpi[]>(() => {
  const known = Boolean(figures.value)
  const current = figures.value?.current ?? NO_DAY
  const previous = figures.value?.previous ?? NO_DAY
  const sent = known && current.deliveries > 0
  const move = (value: number, before: number, positive: 'up' | 'down' = 'up') => (before > 0 ? trendOf(value, before, positive) : undefined)
  return [
    { key: 'deliveries', label: 'Events sent', value: known ? writeFigure(current.deliveries, { compact: true }) : null, exact: known ? writeFigure(current.deliveries) : undefined, trend: known ? move(current.deliveries, previous.deliveries) : undefined, info: 'Events sent here, one each. A delivery tried three times counts once.' },
    { key: 'first', label: 'Delivered first try', value: sent ? `${(current.firstTry * 100).toFixed(current.firstTry > 0.995 ? 2 : 1)}%` : null, trend: sent && previous.deliveries ? move(current.firstTry, previous.firstTry) : undefined, info: 'Of the events sent, how many the server took on the first try.' },
    { key: 'retrying', label: 'Retrying now', value: writeFigure(figures.value?.retrying ?? destination.value?.retrying ?? 0), info: 'Not delivered yet, and due another try.' },
    { key: 'p50', label: 'Median answer', value: sent ? msWords(current.p50) : null, trend: sent && previous.deliveries ? move(current.p50, previous.p50, 'down') : undefined, info: 'Half of the tries were answered faster than this.' },
    { key: 'p95', label: 'Slowest 5%', value: sent ? msWords(current.p95) : null, trend: sent && previous.deliveries ? move(current.p95, previous.p95, 'down') : undefined, info: 'One try in twenty took at least this long. At 10 seconds a try times out.' }
  ]
})

const hours = computed(() => figures.value?.hours ?? [])
const categories = computed(() => hours.value.map(hour => new Date(hour.at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })))
const trySeries = computed<MetricSeries[]>(() => [
  { name: 'Delivered', data: hours.value.map(hour => hour.ok), stack: 'tries', tone: 3 },
  { name: 'Failed', data: hours.value.map(hour => hour.failed), stack: 'tries', tone: 2 }
])

/* Nothing sent in the day, nothing in the log: the next step, rather than figures of nothing. */
const quiet = computed(() => !pending.value && !log.loading.value && !(figures.value?.current.deliveries) && !deliveries.value.length && status.value === 'all')

// --- About it -----------------------------------------------------------------------------

const stamp = (iso: string) => new Date(iso).toLocaleString('en', { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

const facts = computed<Fact[]>(() => {
  const entry = destination.value
  if (!entry) return []
  return [
    { key: 'url', label: 'Endpoint URL', value: entry.url, mono: true, copy: entry.url },
    { key: 'environments', label: 'From', value: entry.environments.map(value => ENVIRONMENT_LABELS[value]).join(', ') },
    { key: 'version', label: 'Payload version', value: entry.version, mono: true },
    { key: 'headers', label: 'Custom headers', value: entry.headers.length ? entry.headers.map(header => header.name).join(', ') : 'None', mono: entry.headers.length > 0 },
    { key: 'created', label: 'Created', value: stamp(entry.created_at), note: `by ${entry.created_by}` },
    { key: 'id', label: 'Destination id', value: entry.id, mono: true, copy: entry.id }
  ]
})

// --- Tabs ---------------------------------------------------------------------------------

const TABS = computed<PageTab[]>(() => [
  { value: 'deliveries', label: 'Deliveries' },
  { value: 'events', label: 'Listening to', count: eventsOf(destination.value?.events ?? []).length },
  { value: 'signing', label: 'Signing' },
  { value: 'history', label: 'History', count: destination.value?.history.length ?? 0 }
])
const tab = useQueryChoice('tab', ['deliveries', 'events', 'signing', 'history'], 'deliveries')

const listened = computed(() => {
  const types = eventsOf(destination.value?.events ?? [])
  return WEBHOOK_GROUPS.map(group => ({ group, events: WEBHOOK_EVENTS.filter(entry => entry.group === group.key && types.includes(entry.type)) })).filter(entry => entry.events.length)
})

const previewing = ref<WebhookEventType | null>(null)
const previewOpen = computed({
  get: () => previewing.value !== null,
  set: (value) => {
    if (!value) previewing.value = null
  }
})

// --- Actions ------------------------------------------------------------------------------

const testing = ref(false)
const disabling = ref(false)
const deleting = ref(false)
const resendingFailed = ref(false)
const busy = ref(false)
const problem = ref('')
const notice = ref('')

async function attempt(change: () => Promise<unknown>) {
  busy.value = true
  problem.value = ''
  notice.value = ''
  try {
    await change()
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    busy.value = false
  }
}

const turn = (enabled: boolean) => attempt(async () => {
  await setEnabled(destination.value!, enabled)
  await reload()
})

const confirmDelete = () => attempt(async () => {
  await remove(destination.value!)
  await navigateTo('/webhooks')
})

const confirmResendFailed = () => attempt(async () => {
  const queued = await resendFailed(destination.value!)
  notice.value = queued
    ? `${queued} failed ${queued === 1 ? 'delivery is' : 'deliveries are'} queued to go again, now.`
    : 'Nothing failed in the last three days, so nothing was queued.'
  await refresh()
})
</script>

<template>
  <LoadState
    v-if="loading || (error && error.status !== 404)"
    :error="error"
    :loading="loading"
    what="the destination"
    @retry="reload"
  />

  <div v-else-if="!destination" class="flex flex-col gap-8">
    <PageHeader title="Destination not found" />
    <LayerCard>
      <EmptyState description="There is no webhook destination with this id in the project. It may have been deleted." icon="material-symbols:webhook" title="No such destination">
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            to="/webhooks"
            variant="flat"
          >
            All destinations
          </v-btn>
        </template>
      </EmptyState>
    </LayerCard>
  </div>

  <div v-else class="flex flex-col gap-8">
    <PageHeader :title="destination.name">
      <template #badge>
        <StatusBadge :label="WEBHOOK_HEALTH[health].label" :tone="WEBHOOK_HEALTH[health].tone" />
      </template>
      <template #description>
        <span class="flex flex-col gap-1">
          <span class="font-mono text-xs text-ink/70">{{ destination.url }}</span>
          <span v-if="destination.description">{{ destination.description }}</span>
        </span>
      </template>
      <template #actions>
        <v-btn
          :disabled="destination.status === 'disabled'"
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:send-outline"
          size="small"
          variant="outlined"
          @click="testing = true"
        >
          Send test event
        </v-btn>
        <v-btn
          :to="`/webhooks/${destination.id}/edit`"
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:edit-outline"
          size="small"
          variant="outlined"
        >
          Edit
        </v-btn>
        <v-btn
          v-if="destination.status === 'disabled'"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:play-arrow-outline"
          size="small"
          variant="flat"
          :loading="busy"
          @click="turn(true)"
        >
          Enable
        </v-btn>
        <v-menu location="bottom end">
          <template #activator="{ props: menu }">
            <v-btn
              v-bind="menu"
              aria-label="More"
              color="neutral"
              icon="material-symbols:more-vert"
              size="small"
              variant="text"
            />
          </template>
          <v-list density="compact" nav>
            <v-list-item
              :disabled="destination.status === 'disabled'"
              prepend-icon="material-symbols:replay"
              title="Resend failed deliveries"
              @click="resendingFailed = true"
            />
            <v-list-item
              v-if="destination.status === 'enabled'"
              prepend-icon="material-symbols:pause-outline"
              title="Disable"
              @click="disabling = true"
            />
            <v-list-item
              base-color="error"
              prepend-icon="material-symbols:delete-outline"
              title="Delete"
              @click="deleting = true"
            />
          </v-list>
        </v-menu>
      </template>
    </PageHeader>

    <p v-if="problem" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ problem }}
    </p>
    <p v-else-if="notice" class="m-0 flex items-center gap-2 rounded-(--ps-radius) bg-ink/4 px-3.5 py-3 text-xs text-ink/75">
      <Icon :aria-hidden="true" class="flex-none text-sm text-primary" name="material-symbols:replay" />
      {{ notice }}
    </p>

    <!-- Failing: said first, with what happens next. -->
    <section v-if="health === 'failing'" class="flex flex-wrap items-start gap-4 rounded-(--ps-radius-x-large) bg-error/6 p-4 ring-1 ring-error/20 ring-inset">
      <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-error/12 text-lg text-error">
        <Icon name="material-symbols:error-outline" />
      </span>
      <div class="flex min-w-0 flex-1 basis-80 flex-col gap-0.5">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ destination.failing_since ? `Every try has failed for ${relativeWords(Date.parse(destination.failing_since), time).replace(' ago', '')}` : 'Every try is failing' }}
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/70">
          <template v-if="deliveries[0]?.attempts">
            The last answer was <span class="font-mono">{{ answerWords({ code: deliveries[0].lastCode, error: deliveries[0].lastError }) }}</span>.
          </template>
          Nothing is lost yet: {{ destination.retrying }} {{ destination.retrying === 1 ? 'event is' : 'events are' }} kept and retried.
          <template v-if="destination.auto_disable_at">
            Unless a try succeeds, the destination is turned off {{ relativeWords(Date.parse(destination.auto_disable_at), time) }}.
          </template>
          Once the server is fixed, resend the failed ones so they do not wait for their next try.
        </p>
      </div>
    </section>

    <section v-else-if="destination.status === 'disabled'" class="flex items-start gap-3 rounded-(--ps-radius-x-large) bg-ink/4 p-4 ring-1 ring-ink/8 ring-inset">
      <Icon :aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/50" name="material-symbols:pause-circle-outline" />
      <p class="m-0 text-xs leading-5 text-ink/70">
        Disabled {{ destination.disabled_reason === 'failing' ? 'by PlayerSense after three days of nothing but failures' : 'by hand' }}{{ destination.disabled_at ? ` ${relativeWords(Date.parse(destination.disabled_at), time)}` : '' }}. Nothing is sent to it, and events that happen meanwhile are not kept for it: enabling it sends what happens from then on.
      </p>
    </section>

    <FactGrid :columns="3" :facts="facts" label="About this destination" />

    <!-- Nothing sent yet: the next step, rather than figures of nothing. -->
    <LayerCard v-if="quiet">
      <EmptyState
        :description="destination.status === 'disabled' ? 'It is disabled, so nothing is sent to it.' : 'Events are sent here the moment one it listens to happens. Send a test event now to see your server answer, and check the signature on the Signing tab.'"
        icon="material-symbols:send-outline"
        title="Nothing sent yet"
      >
        <template v-if="destination.status === 'enabled'" #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            prepend-icon="material-symbols:send-outline"
            size="small"
            variant="flat"
            @click="testing = true"
          >
            Send test event
          </v-btn>
        </template>
      </EmptyState>
    </LayerCard>

    <template v-else>
      <KpiBoard
        :items="board"
        :loading="pending"
        description="The last 24 hours, to the hour, against the 24 before."
        title="Delivery"
      />

      <MetricCard
        :categories="categories"
        :decimals="0"
        :height="180"
        :loading="pending"
        :series="trySeries"
        category-label="Hour"
        description="Every try in the last 24 hours, hour by hour. A retry is a try of its own."
        title="Tries"
        type="bar"
      />
    </template>

    <div class="flex flex-col gap-6">
      <PageTabs v-model="tab" :tabs="TABS" label="Destination views" />

      <SectionCard
        v-if="tab === 'deliveries'"
        :padded="false"
        description="Every event sent here in the last 30 days, newest first. Open one for its tries, the request and the answer; tick several to resend them together."
        title="Deliveries"
      >
        <p v-if="log.error.value" class="m-0 border-b border-line px-5 py-3 text-xs text-error">
          {{ log.error.value.message }}
        </p>
        <DeliveryTable
          v-model:status="status"
          :deliveries="deliveries"
          :destinations="[destination]"
          :has-more="log.hasMore.value"
          :loading="log.loading.value"
          :loading-more="log.loadingMore.value"
          @changed="refresh"
          @more="log.loadMore"
        />
      </SectionCard>

      <SectionCard
        v-else-if="tab === 'events'"
        :padded="false"
        :description="destination.events.includes(EVERY_EVENT) ? 'Every event, and new ones as they are added.' : 'The events sent here, in their groups.'"
        title="Listening to"
      >
        <template #actions>
          <v-btn
            :to="`/webhooks/${destination.id}/edit#section-what`"
            class="text-xs"
            color="neutral"
            prepend-icon="material-symbols:edit-outline"
            size="small"
            variant="outlined"
          >
            Change events
          </v-btn>
        </template>
        <section v-for="{ group, events } in listened" :key="group.key" class="border-t border-line first:border-t-0">
          <header class="flex items-center gap-2.5 bg-ink/2 px-5 py-2.5">
            <Icon :name="group.icon" :aria-hidden="true" class="text-base text-ink/55" />
            <h3 class="m-0 text-xs font-medium text-ink">
              {{ group.label }}
            </h3>
          </header>
          <ul class="m-0 list-none p-0">
            <li v-for="entry in events" :key="entry.type" class="grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto_auto] items-center gap-4 border-t border-line/60 px-5 py-2.5">
              <span class="truncate font-mono text-xs text-ink">{{ entry.type }}</span>
              <span class="text-xs text-ink/65">{{ entry.summary }}</span>
              <span class="text-right text-[11px] whitespace-nowrap text-ink/50">{{ entry.emitted ? '' : 'Not sent yet' }}</span>
              <v-btn
                :aria-label="`See the ${entry.type} payload`"
                class="text-[11px]"
                color="neutral"
                prepend-icon="material-symbols:data-object"
                size="x-small"
                variant="text"
                @click="previewing = entry"
              >
                Payload
              </v-btn>
            </li>
          </ul>
        </section>
      </SectionCard>

      <SectionCard v-else-if="tab === 'signing'" description="How your server knows a delivery came from PlayerSense and not from somebody who found the URL." title="Signing">
        <WebhookSigning :destination="destination" @changed="reload" />
      </SectionCard>

      <SectionCard
        v-else
        :padded="false"
        description="Every change to this destination, newest first."
        title="History"
      >
        <ol class="m-0 flex list-none flex-col p-0">
          <li v-if="!destination.history.length" class="px-5 py-4 text-xs text-ink/50">
            No change recorded yet.
          </li>
          <li v-for="(entry, index) in destination.history" :key="index" class="flex items-start gap-3 border-t border-line/70 px-5 py-3 first:border-t-0">
            <span aria-hidden="true" class="mt-1.5 size-1.5 flex-none rounded-full bg-ink/30" />
            <span class="flex min-w-0 flex-1 flex-col gap-0.5">
              <span class="text-xs text-ink">{{ entry.text }}</span>
              <span class="text-[11px] text-ink/45">{{ entry.actor }} · {{ stamp(entry.at) }}</span>
            </span>
          </li>
        </ol>
      </SectionCard>
    </div>

    <TestEventDialog
      v-if="testing"
      v-model="testing"
      :destination="destination"
      @sent="refresh"
    />

    <WebhookEventDialog
      v-if="previewing"
      v-model="previewOpen"
      :event-type="previewing"
    />

    <ConfirmDialog
      v-if="resendingFailed"
      v-model="resendingFailed"
      :description="`Every delivery to ${destination.name} that gave up in the last three days goes again now, with the same event id, so a server that keeps the ids it has handled sees a repeat. Test events are left out.`"
      action="Resend failed"
      title="Resend what failed?"
      @confirm="confirmResendFailed"
    />

    <ConfirmDialog
      v-if="disabling"
      v-model="disabling"
      :description="`Nothing is sent to ${destination.name} while it is disabled, and events that happen meanwhile are not kept for it. Deliveries retrying now stop.`"
      action="Disable"
      title="Disable this destination?"
      @confirm="turn(false)"
    />

    <ConfirmDialog
      v-if="deleting"
      v-model="deleting"
      :description="`${destination.name} is deleted with its secret and its delivery log. Its server stops getting events at once. This cannot be undone: to stop it for a while, disable it instead.`"
      action="Delete destination"
      title="Delete this destination?"
      tone="error"
      @confirm="confirmDelete"
    />
  </div>
</template>
