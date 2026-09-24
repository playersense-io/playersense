<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Webhooks: what the project sends to the studio's own servers, and whether it gets there.
// Laid out for the questions people come with: is anything broken (the warning at the top,
// before any figure), is delivery healthy (the figures and the chart), where do events go
// (the destinations), what was sent and what came back (the log), and what could be sent
// (the catalog, every event with its payload).
//
// Sample data (app/utils/webhooks-sample.ts). Kept in this browser until the dashboard is
// wired to /webhook-endpoints and the delivery log.
const { project } = useWorkspace()
const { destinations, healthOfDestination, deliveriesOf } = useWebhooks()

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const HOUR = 3_600_000

// --- Health ------------------------------------------------------------------------------

const PERIODS: SegmentedOption[] = [
  { value: '24', label: '24 hours' },
  { value: '168', label: '7 days' }
]
const period = ref('24')
const hours = computed(() => Number(period.value))

const all = computed(() => destinations.value.flatMap(destination => deliveriesOf(destination, time.value)).sort((a, b) => b.created - a.created))

const { data: figures, pending } = useSampleQuery(
  () => ({ hours: hours.value, at: Math.floor(time.value / HOUR), count: all.value.length }),
  question => ({
    current: healthOf(all.value, time.value - question.hours * HOUR, time.value),
    previous: healthOf(all.value, time.value - 2 * question.hours * HOUR, time.value - question.hours * HOUR)
  })
)

const periodWords = computed(() => (hours.value === 24 ? 'the last 24 hours' : 'the last 7 days'))

const board = computed<Kpi[]>(() => {
  const { current, previous } = figures.value
  const move = (value: number, before: number, positive: 'up' | 'down' = 'up') => (before > 0 ? trendOf(value, before, positive) : undefined)
  return [
    { key: 'deliveries', label: 'Events sent', value: writeFigure(current.deliveries, { compact: true }), exact: writeFigure(current.deliveries), trend: move(current.deliveries, previous.deliveries), info: 'Events sent to every destination, one each. A delivery tried three times counts once.' },
    { key: 'first', label: 'Delivered first try', value: `${(current.firstTry * 100).toFixed(current.firstTry > 0.995 ? 2 : 1)}%`, trend: move(current.firstTry, previous.firstTry), info: 'Of the events sent, how many your servers took on the first try, with no retry needed.' },
    { key: 'retrying', label: 'Retrying now', value: writeFigure(all.value.filter(entry => entry.status === 'retrying').length), info: 'Not delivered yet, and due another try. Each is tried for up to three days.' },
    { key: 'failed', label: 'Failed', value: writeFigure(current.failed), trend: move(current.failed, previous.failed, 'down'), info: 'Gave up after three days of tries. Resend them from the log once the server is fixed.' },
    { key: 'p50', label: 'Median answer', value: msWords(current.p50), trend: move(current.p50, previous.p50, 'down'), info: `How long your servers took to answer, half of the tries faster. One in twenty took ${msWords(current.p95)} or more; at 10 seconds a try times out.` }
  ]
})

/* Tries an hour, or a day, as delivered and failed. */
const buckets = computed(() => {
  const size = hours.value === 24 ? HOUR : 24 * HOUR
  const count = hours.value === 24 ? 24 : 7
  const end = Math.floor(time.value / size) * size
  return { size, starts: Array.from({ length: count }, (_, index) => end - (count - 1 - index) * size) }
})

const categories = computed(() => buckets.value.starts.map(at => (hours.value === 24
  ? new Date(at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
  : new Date(at).toLocaleDateString('en', { weekday: 'short', day: 'numeric' }))))

const trySeries = computed<MetricSeries[]>(() => {
  const { ok, bad } = attemptSeries(all.value, buckets.value.starts, buckets.value.size)
  return [
    { name: 'Delivered', data: ok, stack: 'tries', tone: 3 },
    { name: 'Failed', data: bad, stack: 'tries', tone: 2 }
  ]
})

// --- Destinations -------------------------------------------------------------------------

interface Row extends WebhookDestination {
  health: WebhookHealth
  figures: WebhookHealthFigures
  last: WebhookDelivery | null
  spark: number[]
}

const rows = computed<Row[]>(() => destinations.value.map((destination) => {
  const deliveries = deliveriesOf(destination, time.value)
  const from = time.value - 24 * HOUR
  const spark = Array.from({ length: 24 }, (_, index) => {
    const start = from + index * HOUR
    return deliveries.filter(entry => entry.created >= start && entry.created < start + HOUR).length
  })
  return { ...destination, health: healthOfDestination(destination, time.value), figures: healthOf(deliveries, from, time.value), last: deliveries[0] ?? null, spark }
}))

const failing = computed(() => rows.value.filter(row => row.health === 'failing'))

const COLUMNS: DataTableColumn<Row>[] = [
  { key: 'name', title: 'Destination', fixed: true, minWidth: 280, skeleton: 'identity' },
  { key: 'health', title: 'Status', value: row => ['failing', 'healthy', 'disabled'].indexOf(row.health), skeleton: 'pill' },
  { key: 'events', title: 'Listening to', minWidth: 180, value: row => eventsOf(row.events).length },
  { key: 'environments', title: 'From', sortable: false },
  { key: 'delivered', title: 'Last 24 hours', minWidth: 220, value: row => row.figures.deliveries },
  { key: 'answer', title: 'Median answer', align: 'end', value: row => row.figures.p50, hidden: true },
  { key: 'last', title: 'Last event', align: 'end', value: row => row.last?.created ?? 0 },
  { key: 'version', title: 'Version', mono: true, hidden: true },
  { key: 'id', title: 'Id', mono: true, hidden: true }
]

const columns = useLocalStorage('playersense.webhooks.columns.v2', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'health', order: 'asc' }])

const atLimit = computed(() => destinations.value.length >= WEBHOOK_LIMIT)

// --- Tabs ---------------------------------------------------------------------------------

const TABS = computed<PageTab[]>(() => [
  { value: 'destinations', label: 'Destinations', count: destinations.value.length },
  { value: 'deliveries', label: 'Deliveries', count: writeFigure(all.value.length, { compact: true }) },
  { value: 'events', label: 'Event types', count: WEBHOOK_EVENTS.length }
])
const tab = useQueryChoice('tab', ['destinations', 'deliveries', 'events'], 'destinations')

/* The catalog: which destinations hear each event. */
const listeners = (type: string) => destinations.value.filter(destination => destination.status === 'enabled' && eventsOf(destination.events).includes(type))

const catalogQuery = ref('')
const catalog = computed(() => {
  const needle = catalogQuery.value.trim().toLowerCase()
  return WEBHOOK_GROUPS.map(group => ({
    group,
    events: WEBHOOK_EVENTS.filter(entry => entry.group === group.key && (!needle || entry.type.includes(needle) || entry.summary.toLowerCase().includes(needle)))
  })).filter(entry => entry.events.length)
})
const previewing = ref<WebhookEventType | null>(null)
const previewOpen = computed({
  get: () => previewing.value !== null,
  set: (value) => {
    if (!value) previewing.value = null
  }
})

const HOW_IT_WORKS = [
  { icon: 'material-symbols:checklist', title: 'Choose events', text: 'A destination is a URL on your server and the events it wants: a verified purchase, a published config, a finished export.' },
  { icon: 'material-symbols:send-outline', title: 'We POST them, signed', text: 'Each one as JSON the moment it happens, signed with the destination\'s secret so your server knows it is ours.' },
  { icon: 'material-symbols:replay', title: 'Answer 2xx, or we retry', text: 'Anything else, or no answer in 10 seconds, is tried again for three days. Every try is in the log.' }
]
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Webhooks">
      <template #description>
        What <span class="text-ink">{{ project.name }}</span> sends to your own servers the moment it happens, and whether it got there.
      </template>
      <template #actions>
        <span class="text-[11px] text-ink/50"><span class="font-mono text-ink/70">{{ destinations.length }}</span> of {{ WEBHOOK_LIMIT }} destinations</span>
        <v-btn
          :disabled="atLimit"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          to="/webhooks/new"
          variant="flat"
        >
          Add destination
        </v-btn>
      </template>
    </PageHeader>

    <InfoNote text="Destinations and deliveries are sample data until the webhook worker is built (project.md §49-51). Adding, editing and test events work in this browser." />

    <!-- Anything broken, before any figure. -->
    <section v-for="row in failing" :key="row.id" class="flex flex-wrap items-start gap-4 rounded-(--ps-radius-x-large) bg-error/6 p-4 ring-1 ring-error/20 ring-inset">
      <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-error/12 text-lg text-error">
        <Icon name="material-symbols:error-outline" />
      </span>
      <div class="flex min-w-0 flex-1 basis-80 flex-col gap-0.5">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ row.name }} is failing
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/70">
          Every try since {{ new Date(failingSince(row, time)!).toLocaleString('en', { weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false }) }} has failed, most recently with
          <span class="font-mono">{{ row.last?.attempts.at(-1) ? answerWords(row.last.attempts.at(-1)!) : 'no answer' }}</span>.
          {{ deliveriesOf(row, time).filter(entry => entry.status === 'retrying').length }} events are waiting to be retried. Unless it answers, it is turned off {{ relativeWords(autoDisableAt(row, time)!, time) }} and its owners are emailed.
        </p>
      </div>
      <v-btn
        :to="`/webhooks/${row.id}?status=retrying`"
        class="flex-none self-center text-xs font-medium"
        color="error"
        size="small"
        variant="outlined"
      >
        See what failed
      </v-btn>
    </section>

    <template v-if="destinations.length">
      <KpiBoard
        :description="`Every destination, ${periodWords}, against the ${hours === 24 ? '24 hours' : '7 days'} before.`"
        :items="board"
        :loading="pending"
        title="Delivery"
      />

      <MetricCard
        :categories="categories"
        :decimals="0"
        :height="220"
        :loading="pending"
        :series="trySeries"
        category-label="When"
        :description="`Every try made, ${hours === 24 ? 'hour' : 'day'} by ${hours === 24 ? 'hour' : 'day'}. A retry is a try of its own, so an outage shows as a band of failures, then a burst of deliveries as it recovers.`"
        title="Tries"
        type="bar"
      >
        <template #actions>
          <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
        </template>
      </MetricCard>
    </template>

    <!-- No destination yet: how it works, and the first step. -->
    <LayerCard v-else>
      <div class="flex flex-col items-center gap-6 px-4 py-10 text-center">
        <div class="flex flex-col gap-1">
          <h2 class="m-0 text-base font-medium text-ink">
            Send events to your own servers
          </h2>
          <p class="m-0 max-w-xl text-sm text-ink/60">
            Grant a purchase the moment the store confirms it, post a publish to your team, record a deletion in your compliance system.
          </p>
        </div>
        <ol class="m-0 grid max-w-3xl list-none gap-3 p-0 text-left sm:grid-cols-3">
          <li v-for="(step, index) in HOW_IT_WORKS" :key="step.title" class="flex flex-col gap-1.5 rounded-(--ps-radius-large) border border-line p-4">
            <span class="flex items-center gap-2 text-xs font-medium text-ink">
              <span class="flex size-5 items-center justify-center rounded-full bg-ink/6 font-mono text-[10px] text-ink/70">{{ index + 1 }}</span>
              {{ step.title }}
            </span>
            <span class="text-[11px] leading-4 text-ink/60">{{ step.text }}</span>
          </li>
        </ol>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          to="/webhooks/new"
          variant="flat"
        >
          Add your first destination
        </v-btn>
      </div>
    </LayerCard>

    <div v-if="destinations.length" class="flex flex-col gap-6">
      <PageTabs v-model="tab" :tabs="TABS" label="Webhook views" />

      <!-- Where events go. -->
      <SectionCard
        v-if="tab === 'destinations'"
        :padded="false"
        description="Every URL events are sent to, with what it hears and how it has answered today."
        title="Destinations"
      >
        <DataTable
          v-model:column-state="columns"
          v-model:sort-by="sortBy"
          :columns="COLUMNS"
          :items="rows"
          :paged="false"
          item-value="id"
          label="Webhook destinations"
        >
          <template #cell.name="{ item }">
            <NuxtLink :to="`/webhooks/${item.id}`" class="group flex min-w-0 items-center gap-3 py-1.5 no-underline">
              <span :class="item.health === 'disabled' ? 'bg-ink/5 text-ink/40' : 'bg-primary/10 text-primary'" aria-hidden="true" class="flex size-8 flex-none items-center justify-center rounded-(--ps-radius) text-base">
                <Icon name="material-symbols:webhook" />
              </span>
              <span class="flex min-w-0 flex-col">
                <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.name }}</span>
                <span class="truncate font-mono text-[11px] text-ink/45">{{ item.url }}</span>
              </span>
            </NuxtLink>
          </template>
          <template #cell.health="{ item }">
            <span class="flex flex-col items-start gap-0.5">
              <StatusBadge :label="WEBHOOK_HEALTH[item.health].label" :tone="WEBHOOK_HEALTH[item.health].tone" />
              <span v-if="item.health === 'failing'" class="text-[11px] text-error">off {{ relativeWords(autoDisableAt(item, time)!, time) }}</span>
              <span v-else-if="item.health === 'disabled'" class="text-[11px] text-ink/45">{{ item.disabled_reason === 'failing' ? 'after 3 days failing' : 'by hand' }}</span>
            </span>
          </template>
          <template #cell.events="{ item }">
            <span class="flex min-w-0 flex-col gap-0.5">
              <span class="truncate text-xs text-ink/75">{{ listeningWords(item.events) }}</span>
              <span class="text-[11px] text-ink/45">{{ item.events.includes(EVERY_EVENT) ? 'All' : eventsOf(item.events).length }} {{ eventsOf(item.events).length === 1 ? 'event' : 'events' }}</span>
            </span>
          </template>
          <template #cell.environments="{ item }">
            <span class="flex flex-wrap gap-1">
              <span v-for="environment in item.environments" :key="environment" class="rounded-full bg-ink/5 px-1.5 text-[10px] leading-4 text-ink/65">{{ ENVIRONMENT_SHORT[environment] }}</span>
            </span>
          </template>
          <template #cell.delivered="{ item }">
            <span v-if="item.figures.deliveries" class="flex items-center gap-3">
              <span class="flex w-24 flex-none flex-col">
                <span class="font-mono text-xs text-ink">{{ writeFigure(item.figures.deliveries, { compact: true }) }}</span>
                <span :class="item.figures.firstTry < 0.9 ? 'text-error' : item.figures.firstTry < 0.99 ? 'text-warning' : 'text-ink/45'" class="font-mono text-[11px]">{{ (item.figures.firstTry * 100).toFixed(1) }}% first try</span>
              </span>
              <Sparkline :height="24" :values="item.spark" class="h-6 w-20 flex-none" />
            </span>
            <span v-else class="text-[11px] text-ink/40">{{ item.health === 'disabled' ? 'Off' : 'Nothing sent' }}</span>
          </template>
          <template #cell.answer="{ item }">
            <span class="font-mono text-xs text-ink/65">{{ item.figures.deliveries ? msWords(item.figures.p50) : '–' }}</span>
          </template>
          <template #cell.last="{ item }">
            <span v-if="item.last" class="flex items-center justify-end gap-1.5 text-xs whitespace-nowrap text-ink/60">
              <span :class="item.last.status === 'succeeded' ? 'bg-success' : item.last.status === 'retrying' ? 'bg-warning' : 'bg-error'" aria-hidden="true" class="size-1.5 rounded-full" />
              {{ relativeWords(item.last.created, time) }}
            </span>
            <span v-else class="text-[11px] text-ink/40">Never</span>
          </template>
        </DataTable>
      </SectionCard>

      <!-- What was sent, everywhere. -->
      <SectionCard
        v-else-if="tab === 'deliveries'"
        :padded="false"
        description="Every event sent in the last 7 days, to every destination, newest first. Open one for its tries, the request and the answer."
        title="Deliveries"
      >
        <DeliveryTable :deliveries="all" :destinations="destinations" :loading="pending" />
      </SectionCard>

      <!-- What could be sent. -->
      <SectionCard
        v-else
        :padded="false"
        description="Every event a destination can listen to, with what it means and what it carries. Raw analytics events are not among them: Report Builder hands those over in bulk."
        title="Event types"
      >
        <template #actions>
          <SearchField
            v-model="catalogQuery"
            class="w-56"
            label="Search event types"
            placeholder="purchase, export…"
          />
        </template>
        <div class="flex flex-col">
          <section v-for="{ group, events } in catalog" :key="group.key" class="border-t border-line first:border-t-0">
            <header class="flex items-center gap-2.5 bg-ink/2 px-5 py-2.5">
              <Icon :name="group.icon" aria-hidden="true" class="text-base text-ink/55" />
              <h3 class="m-0 text-xs font-medium text-ink">
                {{ group.label }}
              </h3>
              <span class="truncate text-[11px] text-ink/50">{{ group.note }}</span>
            </header>
            <ul class="m-0 list-none p-0">
              <li v-for="entry in events" :key="entry.type" class="grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto_auto] items-center gap-4 border-t border-line/60 px-5 py-2.5">
                <span class="truncate font-mono text-xs text-ink">{{ entry.type }}</span>
                <span class="flex min-w-0 flex-col">
                  <span class="text-xs text-ink/75">{{ entry.summary }}</span>
                  <span class="text-[11px] text-ink/45">{{ entry.holds }}</span>
                </span>
                <span class="text-right text-[11px] whitespace-nowrap text-ink/50">
                  <template v-if="listeners(entry.type).length">{{ listeners(entry.type).length }} listening</template>
                  <template v-else>Nobody listening</template>
                </span>
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
          <p v-if="!catalog.length" class="m-0 px-5 py-8 text-center text-xs text-ink/50">
            No event type matches “{{ catalogQuery.trim() }}”.
          </p>
        </div>
      </SectionCard>
    </div>

    <WebhookEventDialog
      v-if="previewing"
      v-model="previewOpen"
      :event-type="previewing"
      :version="LATEST_VERSION"
    />
  </div>
</template>
