<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Webhooks: what the project sends to the studio's own servers, and whether it gets there.
// Laid out for the questions people come with: is anything broken (the warning at the top,
// before any figure), is delivery healthy (the figures and the chart), where do events go
// (the destinations), what was sent and what came back (the log), and what could be sent
// (the catalog, every event with its payload).
//
// The figures are each destination's last 24 hours (…/webhooks/{id}/health), added up.
const { destinations, loading, error, reload } = useWebhooks()
const limit = useWebhookLimit()

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const ids = computed(() => destinations.value.map(destination => destination.id))
const { byId: healthById, loading: pending } = useWebhooksHealth(ids)

// --- Health ------------------------------------------------------------------------------

/* Every destination's day, added up: counts summed, shares weighted by what each sent. */
function total(days: readonly WebhookDay[]): WebhookDay {
  const deliveries = days.reduce((sum, day) => sum + day.deliveries, 0)
  const weigh = (pick: (day: WebhookDay) => number) => (deliveries ? days.reduce((sum, day) => sum + pick(day) * day.deliveries, 0) / deliveries : 0)
  return {
    deliveries,
    firstTry: weigh(day => day.firstTry),
    trySuccess: weigh(day => day.trySuccess),
    succeeded: days.reduce((sum, day) => sum + day.succeeded, 0),
    failed: days.reduce((sum, day) => sum + day.failed, 0),
    p50: Math.max(0, ...days.filter(day => day.deliveries).map(day => day.p50)),
    p95: Math.max(0, ...days.filter(day => day.deliveries).map(day => day.p95))
  }
}

const figures = computed(() => {
  const read = Object.values(healthById.value)
  return { current: total(read.map(entry => entry.current)), previous: total(read.map(entry => entry.previous)) }
})

const retrying = computed(() => destinations.value.reduce((sum, destination) => sum + destination.retrying, 0))

/*
 * With no destination there is nothing to measure, so every figure is no data (a dash). With
 * destinations, a zero is what they did: nothing sent is a real answer. A figure that needs a
 * delivery to exist (the first-try share, the answer time) is a dash when there was none.
 */
const board = computed<Kpi[]>(() => {
  const { current, previous } = figures.value
  const known = destinations.value.length > 0 && Object.keys(healthById.value).length > 0
  const sent = known && current.deliveries > 0
  const move = (value: number, before: number, positive: 'up' | 'down' = 'up') => (before > 0 ? trendOf(value, before, positive) : undefined)
  return [
    { key: 'deliveries', label: 'Events sent', value: known ? writeFigure(current.deliveries, { compact: true }) : null, exact: known ? writeFigure(current.deliveries) : undefined, trend: known ? move(current.deliveries, previous.deliveries) : undefined, info: 'Events sent to every destination, one each. A delivery tried three times counts once.' },
    { key: 'first', label: 'Delivered first try', value: sent ? `${(current.firstTry * 100).toFixed(current.firstTry > 0.995 ? 2 : 1)}%` : null, trend: sent && previous.deliveries ? move(current.firstTry, previous.firstTry) : undefined, info: 'Of the events sent, how many your servers took on the first try, with no retry needed.' },
    { key: 'retrying', label: 'Retrying now', value: destinations.value.length ? writeFigure(retrying.value) : null, info: 'Not delivered yet, and due another try. Each is tried for up to three days.' },
    { key: 'failed', label: 'Failed', value: known ? writeFigure(current.failed) : null, trend: known ? move(current.failed, previous.failed, 'down') : undefined, info: 'Gave up after three days of tries. Resend them from the destination once the server is fixed.' },
    { key: 'p50', label: 'Slowest median answer', value: sent ? msWords(current.p50) : null, trend: sent && previous.deliveries ? move(current.p50, previous.p50, 'down') : undefined, info: `Half of the tries to the slowest destination were answered faster than this. Its slowest one in twenty took ${msWords(current.p95)} or more; at 10 seconds a try times out.` }
  ]
})

/* Tries an hour, every destination together. */
const hourStarts = computed(() => {
  const first = Object.values(healthById.value)[0]
  return first ? first.hours.map(hour => hour.at) : []
})

const categories = computed(() => hourStarts.value.map(at => new Date(at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })))

const trySeries = computed<MetricSeries[]>(() => {
  const read = Object.values(healthById.value)
  const sum = (pick: (hour: { ok: number, failed: number }) => number) => hourStarts.value.map((_, index) => read.reduce((total, entry) => total + (entry.hours[index] ? pick(entry.hours[index]!) : 0), 0))
  return [
    { name: 'Delivered', data: sum(hour => hour.ok), stack: 'tries', tone: 3 },
    { name: 'Failed', data: sum(hour => hour.failed), stack: 'tries', tone: 2 }
  ]
})

// --- Destinations -------------------------------------------------------------------------

interface Row extends WebhookDestination {
  spark: number[]
}

const rows = computed<Row[]>(() => destinations.value.map(destination => ({
  ...destination,
  spark: (healthById.value[destination.id]?.hours ?? []).map(hour => hour.ok + hour.failed)
})))

const failing = computed(() => rows.value.filter(row => row.health === 'failing'))

const COLUMNS: DataTableColumn<Row>[] = [
  { key: 'name', title: 'Destination', fixed: true, minWidth: 280, skeleton: 'identity' },
  { key: 'health', title: 'Status', value: row => ['failing', 'healthy', 'disabled'].indexOf(row.health), skeleton: 'pill' },
  { key: 'events', title: 'Listening to', minWidth: 180, value: row => eventsOf(row.events).length },
  { key: 'environments', title: 'From', sortable: false },
  { key: 'delivered', title: 'Last 24 hours', minWidth: 220, value: row => row.lastDay.deliveries },
  { key: 'answer', title: 'Median answer', align: 'end', value: row => row.lastDay.p50, hidden: true },
  { key: 'retrying', title: 'Retrying', align: 'end', value: row => row.retrying },
  { key: 'version', title: 'Version', mono: true, hidden: true },
  { key: 'id', title: 'Id', mono: true, hidden: true }
]

const columns = useLocalStorage('playersense.webhooks.columns.v3', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'health', order: 'asc' }])

const atLimit = computed(() => limit.value !== null && destinations.value.length >= limit.value)

// --- Tabs ---------------------------------------------------------------------------------

const TABS = computed<PageTab[]>(() => [
  { value: 'destinations', label: 'Destinations', count: destinations.value.length },
  { value: 'deliveries', label: 'Deliveries' },
  { value: 'events', label: 'Event types', count: WEBHOOK_EVENTS.length }
])
const tab = useQueryChoice('tab', ['destinations', 'deliveries', 'events'], 'destinations')

/* The log, every destination's newest pages merged: read only while its tab is open. */
const status = ref<'all' | DeliveryStatus>('all')
const log = useWebhookDeliveries(() => (tab.value === 'deliveries' ? ids.value : []), () => ({ status: status.value }))

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
        What this project sends to your own servers the moment it happens, and whether it got there.
      </template>
      <template #actions>
        <span v-if="limit !== null" class="text-[11px] text-ink/50"><span class="font-mono text-ink/70">{{ destinations.length }}</span> of {{ limit }} destinations</span>
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

    <LoadState
      :error="error"
      :loading="loading"
      what="the webhooks"
      @retry="reload"
    >
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
            <template v-if="row.failing_since">
              Every try since {{ new Date(row.failing_since).toLocaleString('en', { weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false }) }} has failed.
            </template>
            {{ row.retrying }} {{ row.retrying === 1 ? 'event is' : 'events are' }} waiting to be retried.
            <template v-if="row.auto_disable_at">
              Unless it answers, it is turned off {{ relativeWords(Date.parse(row.auto_disable_at), time) }}.
            </template>
          </p>
        </div>
        <v-btn
          :to="`/webhooks/${row.id}?status=failed`"
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
          description="Every destination, the last 24 hours to the hour, against the 24 before."
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
          description="Every try made in the last 24 hours, hour by hour. A retry is a try of its own, so an outage shows as a band of failures, then a burst of deliveries as it recovers."
          title="Tries"
          type="bar"
        />
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
                <span v-if="item.health === 'failing' && item.auto_disable_at" class="text-[11px] text-error">off {{ relativeWords(Date.parse(item.auto_disable_at), time) }}</span>
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
              <span v-if="item.lastDay.deliveries" class="flex items-center gap-3">
                <span class="flex w-24 flex-none flex-col">
                  <span class="font-mono text-xs text-ink">{{ writeFigure(item.lastDay.deliveries, { compact: true }) }}</span>
                  <span :class="item.lastDay.firstTry < 0.9 ? 'text-error' : item.lastDay.firstTry < 0.99 ? 'text-warning' : 'text-ink/45'" class="font-mono text-[11px]">{{ (item.lastDay.firstTry * 100).toFixed(1) }}% first try</span>
                </span>
                <Sparkline :height="24" :values="item.spark" class="h-6 w-20 flex-none" />
              </span>
              <span v-else class="text-[11px] text-ink/40">{{ item.health === 'disabled' ? 'Off' : 'Nothing sent' }}</span>
            </template>
            <template #cell.answer="{ item }">
              <span class="font-mono text-xs text-ink/65">{{ item.lastDay.deliveries ? msWords(item.lastDay.p50) : '–' }}</span>
            </template>
            <template #cell.retrying="{ item }">
              <span :class="item.retrying ? 'text-warning' : 'text-ink/40'" class="font-mono text-xs">{{ writeFigure(item.retrying) }}</span>
            </template>
          </DataTable>
        </SectionCard>

        <!-- What was sent, everywhere. -->
        <SectionCard
          v-else-if="tab === 'deliveries'"
          :padded="false"
          description="Every event sent in the last 30 days, to every destination, newest first. Open one for its tries, the request and the answer."
          title="Deliveries"
        >
          <p v-if="log.error.value" class="m-0 border-b border-line px-5 py-3 text-xs text-error">
            {{ log.error.value.message }}
          </p>
          <DeliveryTable
            v-model:status="status"
            :deliveries="log.deliveries.value"
            :destinations="destinations"
            :has-more="log.hasMore.value"
            :loading="log.loading.value"
            :loading-more="log.loadingMore.value"
            @changed="log.reload"
            @more="log.loadMore"
          />
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
                    <span class="text-xs text-ink/75">{{ entry.summary }}<span v-if="!entry.emitted" class="ml-1.5 rounded-full bg-ink/6 px-1.5 text-[10px] leading-4 text-ink/55">Not sent yet</span></span>
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
    </LoadState>

    <WebhookEventDialog
      v-if="previewing"
      v-model="previewOpen"
      :event-type="previewing"
    />
  </div>
</template>
