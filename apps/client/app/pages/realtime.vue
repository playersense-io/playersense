<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// What is happening right now in the selected project, minute by minute: the last 30
// minutes of production traffic, against the same 30 minutes yesterday. It keeps itself
// current and can be paused to read a moment without it moving.
//
// Sample data until the API client is wired in (app/utils/realtime-sample.ts says what it
// will poll). Minutes and event names only — there is no raw-event query path yet.
const { project } = useWorkspace()
const { minutes, yesterday, updatedAt, live, pause, resume, restart } = useRealtime()

// Another project is another stream: start its window afresh rather than carry this one's.
watch(() => project.value.id, restart)

// "Updated 2s ago", ticking on its own so it keeps counting while paused.
const clock = useNow({ interval: 1_000 })
const since = computed(() => Math.max(0, Math.round((clock.value.getTime() - updatedAt.value) / 1_000)))

// --- The window --------------------------------------------------------------------------

type Minute = typeof minutes.value[number]

const eventsIn = (minute: Minute) => Object.values(minute.events).reduce((sum, count) => sum + count, 0)
const sumOf = (list: readonly Minute[], pick: (minute: Minute) => number) => list.reduce((sum, minute) => sum + pick(minute), 0)

/** How much of the current minute has passed, from 0 to 1. */
const share = computed(() => {
  const current = minutes.value[minutes.value.length - 1]!
  return Math.min(1, Math.max(0.05, (updatedAt.value - current.at) / 60_000))
})

/**
 * Yesterday's window cut to the same moment: its last minute counted only as far as this
 * one has run, so a half-filled minute is never set against a whole one.
 */
function yesterdaySum(pick: (minute: Minute) => number): number {
  const list = yesterday.value
  const whole = sumOf(list.slice(0, -1), pick)
  return whole + pick(list[list.length - 1]!) * share.value
}

// The last whole minute, which is the one "per minute" figures read.
const lastWhole = computed(() => minutes.value[minutes.value.length - 2]!)
const lastWholeYesterday = computed(() => yesterday.value[yesterday.value.length - 2]!)

// --- Right now ---------------------------------------------------------------------------

const compact = (value: number) => writeFigure(value, { compact: true })
const money = (value: number) => `$${writeFigure(value, { format: 'money' })}`

const board = computed<Kpi[]>(() => {
  const sessions = sumOf(minutes.value, minute => minute.events.session_start)
  const purchases = sumOf(minutes.value, minute => minute.events.purchase)
  const revenue = sumOf(minutes.value, minute => minute.revenue)
  return [
    {
      key: 'players',
      label: 'Players now',
      value: compact(lastWhole.value.players),
      trend: trendOf(lastWhole.value.players, lastWholeYesterday.value.players),
      info: 'Players seen in the last whole minute. Approximate: unique counts per minute are estimated.'
    },
    {
      key: 'events',
      label: 'Events per minute',
      value: compact(eventsIn(lastWhole.value)),
      trend: trendOf(eventsIn(lastWhole.value), eventsIn(lastWholeYesterday.value)),
      info: 'Events accepted in the last whole minute, every event name.'
    },
    {
      key: 'sessions',
      label: 'Sessions started',
      value: compact(sessions),
      trend: trendOf(sessions, yesterdaySum(minute => minute.events.session_start)),
      info: 'session_start events in the last 30 minutes.'
    },
    {
      key: 'purchases',
      label: 'Purchases',
      value: compact(purchases),
      trend: trendOf(purchases, yesterdaySum(minute => minute.events.purchase)),
      info: 'Verified purchases in the last 30 minutes.'
    },
    {
      key: 'revenue',
      label: 'Revenue',
      value: money(revenue),
      trend: trendOf(revenue, yesterdaySum(minute => minute.revenue)),
      info: 'Verified purchases in the last 30 minutes, gross.'
    }
  ]
})

// --- Minute by minute -------------------------------------------------------------------

const METRICS: MetricOption[] = [
  { value: 'events', label: 'Events', description: 'Events accepted per minute.' },
  { value: 'players', label: 'Players', description: 'Players seen per minute. Approximate.' },
  { value: 'revenue', label: 'Revenue', description: 'Verified purchases per minute, gross.' }
]

const metric = ref('events')

function valueOf(minute: Minute): number {
  if (metric.value === 'players') return minute.players
  if (metric.value === 'revenue') return Math.round(minute.revenue)
  return eventsIn(minute)
}

const clockTick = new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit', hour12: false })

const categories = computed(() => minutes.value.map(minute => clockTick.format(minute.at)))
const titles = computed(() => minutes.value.map((minute, index) =>
  index === minutes.value.length - 1 ? `${clockTick.format(minute.at)} · this minute, at its current pace` : clockTick.format(minute.at)))

// The minute still filling is drawn at its pace so far — what it has, over how much of it
// has passed — so the line runs up to now instead of dropping at the end.
const series = computed<MetricSeries[]>(() => [
  {
    name: 'Last 30 minutes',
    data: minutes.value.map((minute, index) =>
      index === minutes.value.length - 1 ? Math.round(valueOf(minute) / share.value) : valueOf(minute)),
    type: 'area',
    tone: metric.value === 'revenue' ? 3 : 1
  },
  { name: 'Yesterday', data: yesterday.value.map(valueOf), dashed: true, tone: 'muted' }
])

// --- Top events -------------------------------------------------------------------------

interface EventRow {
  name: string
  total: number
  previous: number
  perMinute: number
}

const EVENT_COLUMNS: DataTableColumn<EventRow>[] = [
  { key: 'name', title: 'Event', fixed: true, mono: true },
  { key: 'perMinute', title: 'Per minute', align: 'end', mono: true },
  { key: 'total', title: 'Last 30 minutes', align: 'end', mono: true }
]

const eventColumns = ref(defaultColumnState(EVENT_COLUMNS))
const eventSort = ref<DataTableSort[]>([{ key: 'total', order: 'desc' }])

const eventRows = computed<EventRow[]>(() => REALTIME_EVENTS.map(event => ({
  name: event.name,
  total: sumOf(minutes.value, minute => minute.events[event.name]),
  previous: yesterdaySum(minute => minute.events[event.name]),
  perMinute: lastWhole.value.events[event.name]
})))
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Realtime">
      <template #badge>
        <span
          :class="live ? 'text-success' : 'text-ink/55'"
          class="inline-flex items-center gap-1.5 rounded-full bg-ink/5 px-2 py-0.5 text-xs"
        >
          <span class="relative flex size-2">
            <span v-if="live" class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" />
            <span :class="live ? 'bg-success' : 'bg-ink/40'" class="relative inline-flex size-2 rounded-full" />
          </span>
          {{ live ? 'Live' : 'Paused' }}
        </span>
      </template>
      <template #description>
        <span class="text-ink">{{ project.name }}</span>, production, minute by minute. An event
        shows up here within about ten seconds of being sent.
      </template>
      <template #actions>
        <span class="text-xs text-ink/55 tabular-nums" aria-live="off">
          Updated {{ since === 0 ? 'just now' : `${since}s ago` }}
        </span>
        <v-btn
          :prepend-icon="live ? 'material-symbols:pause' : 'material-symbols:play-arrow'"
          class="text-xs"
          color="neutral"
          size="small"
          variant="text"
          @click="live ? pause() : resume()"
        >
          {{ live ? 'Pause' : 'Resume' }}
        </v-btn>
      </template>
    </PageHeader>

    <KpiBoard
      :items="board"
      description="The last 30 minutes, against the same 30 minutes yesterday."
      title="Right now"
    />

    <MetricCard
      v-model="metric"
      :categories="categories"
      :compact="metric !== 'revenue'"
      :options="METRICS"
      :prefix="metric === 'revenue' ? '$' : ''"
      :series="series"
      :titles="titles"
      category-label="Minute"
    />

    <SectionCard :padded="false" description="Every event name, busiest first." title="Top events">
      <DataTable
        v-model:column-state="eventColumns"
        v-model:sort-by="eventSort"
        :columns="EVENT_COLUMNS"
        :items="eventRows"
        :paged="false"
        item-value="name"
        label="Top events"
      >
        <template #cell.perMinute="{ item }">
          {{ compact(item.perMinute) }}
        </template>
        <template #cell.total="{ item }">
          <ProjectFigure :previous="item.previous" :value="item.total" :written="compact(item.total)" />
        </template>
      </DataTable>
    </SectionCard>
  </div>
</template>
