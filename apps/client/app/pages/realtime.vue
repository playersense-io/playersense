<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'
import type { LiveMinute } from '~/composables/realtime'

// What is happening right now in the selected project, minute by minute: the last 30 minutes
// of the chosen environment's traffic, against the same 30 minutes yesterday
// (GET …/analytics/{env}/realtime). It keeps itself current and can be paused to read a moment
// without it moving. Minutes and event names only: individual events are on an event's page.
const workspace = useWorkspaceStore()
const { window: live, loading, error, updatedAt, live: running, pause, resume, reload } = useRealtime()

// "Updated 2s ago", ticking on its own so it keeps counting while paused.
const clock = useNow({ interval: 1_000 })
const since = computed(() => Math.max(0, Math.round((clock.value.getTime() - updatedAt.value) / 1_000)))

// --- The window --------------------------------------------------------------------------

const minutes = computed<LiveMinute[]>(() => [...(live.value?.minutes ?? [])])
const yesterday = computed<LiveMinute[]>(() => [...(live.value?.yesterday ?? [])])
const threshold = computed(() => live.value?.suppression_threshold ?? 5)

const sumOf = (list: readonly LiveMinute[], pick: (minute: LiveMinute) => number) => list.reduce((sum, minute) => sum + pick(minute), 0)

/** How much of the current minute has passed, from 0 to 1. */
const share = computed(() => Math.min(1, Math.max(0.05, live.value?.elapsed ?? 1)))

/**
 * Yesterday's window cut to the same moment: its last minute counted only as far as this
 * one has run, so a half-filled minute is never set against a whole one.
 */
function yesterdaySum(pick: (minute: LiveMinute) => number): number {
  const list = yesterday.value
  if (!list.length) return 0
  return sumOf(list.slice(0, -1), pick) + pick(list[list.length - 1]!) * share.value
}

// The last whole minute, which is the one "per minute" figures read.
const lastWhole = computed(() => minutes.value[minutes.value.length - 2] ?? null)
const lastWholeYesterday = computed(() => yesterday.value[yesterday.value.length - 2] ?? null)

// --- Right now ---------------------------------------------------------------------------

const compact = (value: number) => writeFigure(value, { compact: true })
const money = (micros: number) => `$${writeFigure(micros / 1_000_000, { format: 'money' })}`
const playersOf = (minute: LiveMinute | null) => minute?.players ?? null

const board = computed<Kpi[]>(() => {
  const players = playersOf(lastWhole.value)
  const events = sumOf(minutes.value, minute => minute.events)
  const revenue = sumOf(minutes.value, minute => minute.revenue_micros)
  return [
    {
      key: 'players',
      label: 'Players now',
      value: players === null ? `Under ${threshold.value}` : compact(players),
      trend: players === null ? undefined : trendOf(players, playersOf(lastWholeYesterday.value) ?? 0),
      info: `Players seen in the last whole minute. Approximate, and not shown when fewer than ${threshold.value} played.`
    },
    {
      key: 'rate',
      label: 'Events per minute',
      value: compact(lastWhole.value?.events ?? 0),
      trend: trendOf(lastWhole.value?.events ?? 0, lastWholeYesterday.value?.events ?? 0),
      info: 'Events accepted in the last whole minute, every event name.'
    },
    {
      key: 'events',
      label: 'Events, 30 minutes',
      value: compact(events),
      trend: trendOf(events, yesterdaySum(minute => minute.events)),
      info: 'Events accepted in the last 30 minutes.'
    },
    {
      key: 'revenue',
      label: 'Reported revenue',
      value: money(revenue),
      trend: trendOf(revenue, yesterdaySum(minute => minute.revenue_micros)),
      info: 'Purchases in the last 30 minutes as the game reported them, before verification and in the currencies reported. Revenue in USD is on the Revenue page.'
    }
  ]
})

// --- Minute by minute -------------------------------------------------------------------

const METRICS: MetricOption[] = [
  { value: 'events', label: 'Events', description: 'Events accepted per minute.' },
  { value: 'players', label: 'Players', description: 'Players seen per minute. Approximate; a minute with too few players is drawn as none.' },
  { value: 'revenue', label: 'Reported revenue', description: 'Purchases per minute as the game reported them.' }
]

const metric = ref('events')

function valueOf(minute: LiveMinute): number {
  if (metric.value === 'players') return minute.players ?? 0
  if (metric.value === 'revenue') return Math.round(minute.revenue_micros / 1_000_000)
  return minute.events
}

const clockTick = new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit', hour12: false })

const categories = computed(() => minutes.value.map(minute => clockTick.format(Date.parse(minute.at))))
const titles = computed(() => minutes.value.map((minute, index) =>
  index === minutes.value.length - 1 ? `${clockTick.format(Date.parse(minute.at))} · this minute, at its current pace` : clockTick.format(Date.parse(minute.at))))

// The minute still filling is drawn at its pace so far (what it has, over how much of it has
// passed) so the line runs up to now instead of dropping at the end.
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

const eventRows = computed<EventRow[]>(() => (live.value?.names ?? []).map(entry => ({
  name: entry.name,
  total: entry.events,
  previous: yesterdaySum(minute => minute.by_event[entry.name] ?? 0),
  perMinute: lastWhole.value?.by_event[entry.name] ?? 0
})))
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Realtime">
      <template #badge>
        <span
          :class="running ? 'text-success' : 'text-ink/55'"
          class="inline-flex items-center gap-1.5 rounded-full bg-ink/5 px-2 py-0.5 text-xs"
        >
          <span class="relative flex size-2">
            <span v-if="running" class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" />
            <span :class="running ? 'bg-success' : 'bg-ink/40'" class="relative inline-flex size-2 rounded-full" />
          </span>
          {{ running ? 'Live' : 'Paused' }}
        </span>
      </template>
      <template #description>
        <span class="text-ink">{{ workspace.project?.name }}</span>, {{ workspace.environment }}, minute by minute. An event
        shows up here within about ten seconds of being sent.
      </template>
      <template #actions>
        <span class="text-xs text-ink/55 tabular-nums" aria-live="off">
          Updated {{ since === 0 ? 'just now' : `${since}s ago` }}
        </span>
        <v-btn
          :prepend-icon="running ? 'material-symbols:pause' : 'material-symbols:play-arrow'"
          class="text-xs"
          color="neutral"
          size="small"
          variant="text"
          @click="running ? pause() : resume()"
        >
          {{ running ? 'Pause' : 'Resume' }}
        </v-btn>
      </template>
    </PageHeader>

    <LoadState
      :error="error"
      :loading="loading"
      what="realtime"
      @retry="reload"
    >
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

      <SectionCard :padded="false" description="The busiest event names, busiest first. Quieter ones are counted together as other." title="Top events">
        <DataTable
          v-model:column-state="eventColumns"
          v-model:sort-by="eventSort"
          :columns="EVENT_COLUMNS"
          :items="eventRows"
          :paged="false"
          item-value="name"
          empty-description="Nothing was sent in the last 30 minutes. Events show up here within seconds of the SDK sending them."
          empty-title="No events yet."
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
    </LoadState>
  </div>
</template>
