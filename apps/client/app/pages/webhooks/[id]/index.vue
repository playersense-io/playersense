<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// One destination, read top to bottom as the questions come: is it working (the warning, if it
// is not, and the day's figures), what was sent and what came back (the log, which is where most
// visits end), what it listens to, how its server checks a delivery is ours (the secret and a
// handler that verifies it), and who changed what.
//
// Sample data (app/utils/webhooks-sample.ts).
const route = useRoute()

const { destinationById, deliveriesOf, healthOfDestination, enable, disable, remove } = useWebhooks()

const id = computed(() => String(route.params.id ?? ''))
const destination = computed(() => destinationById(id.value))

usePageCrumb(() => destination.value?.name ?? 'Destination')

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const HOUR = 3_600_000

const health = computed(() => (destination.value ? healthOfDestination(destination.value, time.value) : 'disabled'))
const deliveries = computed(() => (destination.value ? deliveriesOf(destination.value, time.value) : []))

// --- The day's figures --------------------------------------------------------------------

const { data: figures, pending } = useSampleQuery(
  () => ({ id: id.value, at: Math.floor(time.value / HOUR), count: deliveries.value.length }),
  () => ({
    current: healthOf(deliveries.value, time.value - 24 * HOUR, time.value),
    previous: healthOf(deliveries.value, time.value - 48 * HOUR, time.value - 24 * HOUR)
  })
)

const board = computed<Kpi[]>(() => {
  const { current, previous } = figures.value
  const move = (value: number, before: number, positive: 'up' | 'down' = 'up') => (before > 0 ? trendOf(value, before, positive) : undefined)
  return [
    { key: 'deliveries', label: 'Events sent', value: writeFigure(current.deliveries, { compact: true }), exact: writeFigure(current.deliveries), trend: move(current.deliveries, previous.deliveries), info: 'Events sent here, one each. A delivery tried three times counts once.' },
    { key: 'first', label: 'Delivered first try', value: `${(current.firstTry * 100).toFixed(current.firstTry > 0.995 ? 2 : 1)}%`, trend: move(current.firstTry, previous.firstTry), info: 'Of the events sent, how many the server took on the first try.' },
    { key: 'retrying', label: 'Retrying now', value: writeFigure(deliveries.value.filter(entry => entry.status === 'retrying').length), info: 'Not delivered yet, and due another try.' },
    { key: 'p50', label: 'Median answer', value: current.deliveries ? msWords(current.p50) : '–', trend: current.deliveries ? move(current.p50, previous.p50, 'down') : undefined, info: 'Half of the tries were answered faster than this.' },
    { key: 'p95', label: 'Slowest 5%', value: current.deliveries ? msWords(current.p95) : '–', trend: current.deliveries ? move(current.p95, previous.p95, 'down') : undefined, info: 'One try in twenty took at least this long. At 10 seconds a try times out.' }
  ]
})

const hourStarts = computed(() => {
  const end = Math.floor(time.value / HOUR) * HOUR
  return Array.from({ length: 24 }, (_, index) => end - (23 - index) * HOUR)
})
const categories = computed(() => hourStarts.value.map(at => new Date(at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })))
const trySeries = computed<MetricSeries[]>(() => {
  const { ok, bad } = attemptSeries(deliveries.value, hourStarts.value, HOUR)
  return [
    { name: 'Delivered', data: ok, stack: 'tries', tone: 3 },
    { name: 'Failed', data: bad, stack: 'tries', tone: 2 }
  ]
})

// --- About it -----------------------------------------------------------------------------

const stamp = (iso: string) => new Date(iso).toLocaleString('en', { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

const facts = computed<Fact[]>(() => {
  const entry = destination.value
  if (!entry) return []
  return [
    { key: 'url', label: 'Endpoint URL', value: entry.url, mono: true, copy: entry.url },
    { key: 'environments', label: 'From', value: entry.environments.map(value => ENVIRONMENT_LABELS[value]).join(', ') },
    { key: 'version', label: 'Payload version', value: entry.version, mono: true, note: entry.version === LATEST_VERSION ? 'The latest' : `Latest is ${LATEST_VERSION}` },
    { key: 'headers', label: 'Custom headers', value: entry.headers.length ? entry.headers.map(header => header.name).join(', ') : 'None', mono: entry.headers.length > 0 },
    { key: 'created', label: 'Created', value: stamp(entry.created_at), note: `by ${entry.history[0]?.actor ?? entry.updated_by}` },
    { key: 'id', label: 'Destination id', value: entry.id, mono: true, copy: entry.id }
  ]
})

// --- Tabs ---------------------------------------------------------------------------------

const TABS = computed<PageTab[]>(() => [
  { value: 'deliveries', label: 'Deliveries', count: writeFigure(deliveries.value.length, { compact: true }) },
  { value: 'events', label: 'Listening to', count: eventsOf(destination.value?.events ?? []).length },
  { value: 'signing', label: 'Signing' },
  { value: 'history', label: 'History', count: destination.value?.history.length ?? 0 }
])
const tab = useQueryChoice('tab', ['deliveries', 'events', 'signing', 'history'], 'deliveries')

const initialStatus = computed(() => (DELIVERY_STATUSES as string[]).includes(String(route.query.status)) ? route.query.status as DeliveryStatus : 'all')

const listened = computed(() => {
  const types = eventsOf(destination.value?.events ?? [])
  return WEBHOOK_GROUPS.map(group => ({ group, events: WEBHOOK_EVENTS.filter(entry => entry.group === group.key && types.includes(entry.type)) })).filter(entry => entry.events.length)
})

/* How many of each event it was sent this week. */
const sentCount = computed(() => {
  const out = new Map<string, number>()
  for (const entry of deliveries.value) out.set(entry.type, (out.get(entry.type) ?? 0) + 1)
  return out
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

function confirmDelete() {
  if (!destination.value) return
  remove(destination.value.id)
  navigateTo('/webhooks')
}
</script>

<template>
  <div v-if="!destination" class="flex flex-col gap-8">
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
          @click="enable(destination.id)"
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

    <!-- Failing: said first, with what happens next. -->
    <section v-if="health === 'failing'" class="flex flex-wrap items-start gap-4 rounded-(--ps-radius-x-large) bg-error/6 p-4 ring-1 ring-error/20 ring-inset">
      <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-error/12 text-lg text-error">
        <Icon name="material-symbols:error-outline" />
      </span>
      <div class="flex min-w-0 flex-1 basis-80 flex-col gap-0.5">
        <h2 class="m-0 text-sm font-medium text-ink">
          Every try has failed for {{ relativeWords(failingSince(destination, time)!, time).replace(' ago', '') }}
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/70">
          The server answers <span class="font-mono">{{ deliveries[0]?.attempts.at(-1) ? answerWords(deliveries[0].attempts.at(-1)!) : 'nothing' }}</span>. Nothing is lost yet: every event is kept and retried.
          Unless a try succeeds, the destination is turned off {{ relativeWords(autoDisableAt(destination, time)!, time) }} and its owners are emailed. Once the server is fixed, resend what is retrying so it does not wait for its next try.
        </p>
      </div>
    </section>

    <section v-else-if="destination.status === 'disabled'" class="flex items-start gap-3 rounded-(--ps-radius-x-large) bg-ink/4 p-4 ring-1 ring-ink/8 ring-inset">
      <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/50" name="material-symbols:pause-circle-outline" />
      <p class="m-0 text-xs leading-5 text-ink/70">
        Disabled {{ destination.disabled_reason === 'failing' ? 'by PlayerSense after three days of nothing but failures' : 'by hand' }}. Nothing is sent to it, and events that happen meanwhile are not kept for it: enabling it sends what happens from then on.
      </p>
    </section>

    <FactGrid :columns="3" :facts="facts" label="About this destination" />

    <!-- Nothing sent yet: the next step, rather than figures of nothing. -->
    <LayerCard v-if="!deliveries.length">
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
        description="The last 24 hours, against the 24 before."
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
        description="Every event sent here in the last 7 days, newest first. Open one for its tries, the request and the answer; tick several to resend them together."
        title="Deliveries"
      >
        <DeliveryTable
          :key="destination.id + initialStatus"
          :deliveries="deliveries"
          :destinations="[destination]"
          :initial-status="initialStatus"
          :loading="pending"
        />
      </SectionCard>

      <SectionCard
        v-else-if="tab === 'events'"
        :padded="false"
        :description="destination.events.includes(EVERY_EVENT) ? 'Every event, and new ones as they are added.' : 'The events sent here, in their groups, with how many went out this week.'"
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
            <Icon :name="group.icon" aria-hidden="true" class="text-base text-ink/55" />
            <h3 class="m-0 text-xs font-medium text-ink">
              {{ group.label }}
            </h3>
          </header>
          <ul class="m-0 list-none p-0">
            <li v-for="entry in events" :key="entry.type" class="grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto_auto] items-center gap-4 border-t border-line/60 px-5 py-2.5">
              <span class="truncate font-mono text-xs text-ink">{{ entry.type }}</span>
              <span class="text-xs text-ink/65">{{ entry.summary }}</span>
              <span class="text-right font-mono text-[11px] whitespace-nowrap text-ink/50">{{ writeFigure(sentCount.get(entry.type) ?? 0, { compact: true }) }} this week</span>
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
        <WebhookSigning :destination="destination" />
      </SectionCard>

      <SectionCard
        v-else
        :padded="false"
        description="Every change to this destination, newest first."
        title="History"
      >
        <ol class="m-0 flex list-none flex-col p-0">
          <li v-for="(entry, index) in [...destination.history].reverse()" :key="index" class="flex items-start gap-3 border-t border-line/70 px-5 py-3 first:border-t-0">
            <span aria-hidden="true" class="mt-1.5 size-1.5 flex-none rounded-full bg-ink/30" />
            <span class="flex min-w-0 flex-1 flex-col gap-0.5">
              <span class="text-xs text-ink">{{ entry.text }}</span>
              <span class="text-[11px] text-ink/45">{{ entry.actor }} · {{ stamp(entry.at) }}</span>
            </span>
          </li>
        </ol>
      </SectionCard>
    </div>

    <TestEventDialog v-if="testing" v-model="testing" :destination="destination" />

    <WebhookEventDialog
      v-if="previewing"
      v-model="previewOpen"
      :event-type="previewing"
      :version="destination.version"
    />

    <ConfirmDialog
      v-if="disabling"
      v-model="disabling"
      :description="`Nothing is sent to ${destination.name} while it is disabled, and events that happen meanwhile are not kept for it. Deliveries retrying now stop.`"
      action="Disable"
      title="Disable this destination?"
      @confirm="disable(destination.id)"
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
