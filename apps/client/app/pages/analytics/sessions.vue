<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// How the selected project is played, session by session: how many and how long, the
// hours of the week players open the game, how long sessions run and on what, how they
// end, and the latest ones — each linking to its player.
//
// Sample data until the API client is wired in (app/utils/sessions-sample.ts says what
// each part will come from; lengths, endings and the session list need the session
// finalizer, which is not built yet).
const { project } = useWorkspace()

const route = useRoute()
const router = useRouter()

const PERIODS: SegmentedOption[] = [
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' }
]

const period = computed<SessionPeriod>({
  get: () => (['7d', '30d', '90d'].includes(String(route.query.period)) ? route.query.period as SessionPeriod : '30d'),
  set: value => void router.replace({ query: { ...route.query, period: value } })
})

const periodWords = computed(() => (PERIODS.find(option => option.value === period.value)?.label ?? '').toLowerCase())

const overview = computed(() => sampleSessionOverview(period.value))

const compact = (value: number) => writeFigure(value, { compact: true })
const minutes = (ms: number) => writeFigure(ms / 60_000, { decimals: 1 })

// --- Figures ------------------------------------------------------------------------------

const board = computed<Kpi[]>(() => {
  const days = overview.value.days
  const sessions = days.reduce((sum, day) => sum + day.sessions, 0)
  const previous = days.reduce((sum, day) => sum + day.previous, 0)
  const playtime = days.reduce((sum, day) => sum + day.playtimeMs, 0)
  const previousPlaytime = days.reduce((sum, day) => sum + day.previousPlaytimeMs, 0)
  const playerDays = days.reduce((sum, day) => sum + day.players, 0)
  return [
    {
      key: 'sessions',
      label: 'Sessions',
      value: compact(sessions),
      trend: trendOf(sessions, previous),
      info: 'Sessions started: session_start events from production.'
    },
    {
      key: 'per-player',
      label: 'Sessions per player',
      value: writeFigure(sessions / playerDays),
      info: 'Sessions divided by active players, per day.'
    },
    {
      key: 'playtime',
      label: 'Playtime',
      value: compact(playtime / 3_600_000),
      unit: 'h',
      trend: trendOf(playtime, previousPlaytime),
      info: 'Hours played across every session.'
    },
    {
      key: 'length',
      label: 'Average session',
      value: minutes(playtime / sessions),
      unit: 'min',
      trend: trendOf(playtime / sessions, previousPlaytime / previous),
      info: 'Playtime divided by sessions.'
    },
    {
      key: 'player-playtime',
      label: 'Playtime per player',
      value: writeFigure(playtime / playerDays / 60_000, { decimals: 0 }),
      unit: 'min',
      info: 'Minutes played per active player, per day.'
    }
  ]
})

// --- Over time ----------------------------------------------------------------------------

const METRICS: MetricOption[] = [
  { value: 'sessions', label: 'Sessions', description: 'Sessions started each day.' },
  { value: 'playtime', label: 'Playtime', description: 'Hours played each day.' },
  { value: 'length', label: 'Average session', description: 'Minutes per session, each day.' }
]

const metric = ref('sessions')

type Day = typeof overview.value.days[number]

function valueOf(day: Day, previous = false): number {
  const sessions = previous ? day.previous : day.sessions
  const playtime = previous ? day.previousPlaytimeMs : day.playtimeMs
  if (metric.value === 'playtime') return Math.round(playtime / 3_600_000)
  if (metric.value === 'length') return Math.round(playtime / sessions / 6_000) / 10
  return sessions
}

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })

const categories = computed(() => overview.value.days.map(day => dayTick.format(day.at)))
const series = computed<MetricSeries[]>(() => [
  { name: 'This period', data: overview.value.days.map(day => valueOf(day)), type: 'area', tone: 1 },
  { name: 'Previous period', data: overview.value.days.map(day => valueOf(day, true)), dashed: true, tone: 'muted' }
])

// --- How sessions end ---------------------------------------------------------------------

const ENDING_TONES: Record<SessionEnd, BadgeTone> = { session_end: 'success', timeout: 'neutral', crash: 'error' }

const endings = computed(() => {
  const total = overview.value.ends.reduce((sum, row) => sum + row.count, 0)
  return overview.value.ends.map(row => ({ ...row, share: row.count / total }))
})

// --- By platform --------------------------------------------------------------------------

type PlatformRow = typeof overview.value.platforms[number]

const PLATFORM_COLUMNS: DataTableColumn<PlatformRow>[] = [
  { key: 'platform', title: 'Platform', fixed: true },
  { key: 'sessions', title: 'Sessions', mono: true },
  { key: 'share', title: 'Share', mono: true, value: row => row.sessions },
  { key: 'length', title: 'Average session', mono: true, value: row => row.playtimeMs / row.sessions },
  { key: 'perPlayer', title: 'Per player', mono: true, value: row => row.sessions / row.players }
]

const platformColumns = ref(defaultColumnState(PLATFORM_COLUMNS))

const platformTotal = computed(() => overview.value.platforms.reduce((sum, row) => sum + row.sessions, 0))

// --- Recent sessions ----------------------------------------------------------------------

const recent = sampleRecentSessions()

const SESSION_COLUMNS: DataTableColumn<SessionRow>[] = [
  { key: 'id', title: 'Session ID', fixed: true, mono: true },
  { key: 'player', title: 'Player', mono: true },
  { key: 'platform', title: 'Platform' },
  { key: 'version', title: 'App version', mono: true, hidden: true },
  { key: 'durationMs', title: 'Length', mono: true },
  { key: 'events', title: 'Events', mono: true },
  { key: 'end', title: 'Ended', value: row => SESSION_END_LABELS[row.end] },
  { key: 'startedAt', title: 'Started', align: 'end', mono: true }
]

const sessionColumns = ref(defaultColumnState(SESSION_COLUMNS))
const sessionSort = ref<DataTableSort[]>([{ key: 'startedAt', order: 'desc' }])
const sessionsPerPage = ref(10)

/** "12m 04s", "58s". */
function length(ms: number): string {
  const seconds = Math.round(ms / 1_000)
  const m = Math.floor(seconds / 60)
  return m ? `${m}m ${String(seconds % 60).padStart(2, '0')}s` : `${seconds}s`
}

const shortId = (id: string) => (id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-5)}` : id)
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Sessions">
      <template #description>
        How <span class="text-ink">{{ project.name }}</span> is played: how often, for how
        long, at what hours, and how sessions end.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <KpiBoard
      :description="`The last ${periodWords}, against the ${periodWords} before.`"
      :items="board"
      title="Sessions"
    />

    <MetricCard
      v-model="metric"
      :categories="categories"
      :compact="metric === 'sessions'"
      :options="METRICS"
      :series="series"
      :unit="metric === 'playtime' ? 'h' : metric === 'length' ? 'min' : ''"
      category-label="Day"
    />

    <SectionCard
      description="Session starts by weekday and hour, in your local time, over the period."
      title="When players play"
    >
      <WeekHeatmap :values="overview.heatmap" unit="session starts" />
    </SectionCard>

    <div class="grid gap-4 xl:grid-cols-2">
      <SectionCard description="How long sessions ran, from the first event to the last." title="Session length">
        <BreakdownList :rows="overview.lengths" label="Sessions by length" />
      </SectionCard>

      <SectionCard description="How each session was closed." title="How sessions end">
        <div class="flex flex-col gap-5">
          <!-- One bar cut by share, then the legend under it with the figures. -->
          <div class="flex h-2.5 overflow-hidden rounded-full bg-ink/6">
            <span
              v-for="row in endings"
              :key="row.end"
              :class="{ 'bg-success': row.end === 'session_end', 'bg-ink/30': row.end === 'timeout', 'bg-error': row.end === 'crash' }"
              :style="{ width: `${row.share * 100}%` }"
            />
          </div>
          <ul class="m-0 flex list-none flex-col gap-3 p-0">
            <li v-for="row in endings" :key="row.end" class="flex items-center justify-between gap-3 text-[13px]">
              <span class="flex min-w-0 flex-col">
                <StatusBadge :label="SESSION_END_LABELS[row.end]" :tone="ENDING_TONES[row.end]" class="w-fit" />
                <span class="mt-1 text-xs text-ink/55">
                  <template v-if="row.end === 'session_end'">The game sent session_end.</template>
                  <template v-else-if="row.end === 'timeout'">No event before the session timeout; closed by the finalizer.</template>
                  <template v-else>The game stopped without closing, after an error.</template>
                </span>
              </span>
              <span class="flex flex-none items-baseline gap-2 font-mono tabular-nums">
                <span class="text-ink">{{ writeFigure(row.count) }}</span>
                <span class="w-10 text-right text-xs text-ink/50">{{ writeFigure(row.share * 100, { decimals: 1, unit: '%' }) }}</span>
              </span>
            </li>
          </ul>
        </div>
      </SectionCard>
    </div>

    <SectionCard :padded="false" description="Sessions and their length on each platform." title="By platform">
      <DataTable
        v-model:column-state="platformColumns"
        :columns="PLATFORM_COLUMNS"
        :items="overview.platforms"
        :paged="false"
        item-value="platform"
        label="Sessions by platform"
      >
        <template #cell.sessions="{ item }">
          {{ compact(item.sessions) }}
        </template>
        <template #cell.share="{ item }">
          {{ writeFigure(item.sessions / platformTotal * 100, { decimals: 1, unit: '%' }) }}
        </template>
        <template #cell.length="{ item }">
          {{ minutes(item.playtimeMs / item.sessions) }} min
        </template>
        <template #cell.perPlayer="{ item }">
          {{ writeFigure(item.sessions / item.players) }}
        </template>
      </DataTable>
    </SectionCard>

    <SectionCard :padded="false" description="The latest sessions to close, with the player behind each." title="Recent sessions">
      <DataTable
        v-model:column-state="sessionColumns"
        v-model:items-per-page="sessionsPerPage"
        v-model:sort-by="sessionSort"
        :columns="SESSION_COLUMNS"
        :items="recent"
        item-value="id"
        label="Recent sessions"
      >
        <template #cell.id="{ item }">
          <span :title="item.id" class="font-mono text-ink/70">{{ shortId(item.id) }}</span>
        </template>
        <template #cell.player="{ item }">
          <NuxtLink
            :title="item.player"
            :to="`/analytics/players/${item.player}`"
            class="
              font-mono text-ink no-underline
              hover:text-primary
            "
          >
            {{ shortId(item.player) }}
          </NuxtLink>
        </template>
        <template #cell.durationMs="{ item }">
          {{ length(item.durationMs) }}
        </template>
        <template #cell.end="{ item }">
          <StatusBadge :label="SESSION_END_LABELS[item.end]" :tone="ENDING_TONES[item.end]" />
        </template>
        <template #cell.startedAt="{ item }">
          <span class="font-mono">{{ formatTimestamp(item.startedAt) }}</span>
        </template>
      </DataTable>
    </SectionCard>
  </div>
</template>
