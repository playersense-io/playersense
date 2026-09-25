<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'

// How the selected project is played, session by session: how many and how long, the hours of
// the week players open the game, how long sessions run and on what, how they end, and the
// latest ones, each linking to its player (GET …/analytics/{env}/sessions and sessions/recent).
//
// A session is counted once its day is over, so the period ends with yesterday.
const workspace = useWorkspaceStore()

const route = useRoute()
const router = useRouter()

type SessionPeriod = '7d' | '30d' | '90d'

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
const range = computed(() => periodOf(Number.parseInt(period.value, 10)))
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone

const read = useApiGet<Schema<'SessionsOut'>>(() => workspace.analyticsPath
  && withQuery(`${workspace.analyticsPath}/sessions`, { from: range.value.from, to: range.value.to, tz: timezone }))
const recentRead = useApiGet<Schema<'RecentSessionsOut'>>(() => workspace.analyticsPath
  && withQuery(`${workspace.analyticsPath}/sessions/recent`, { limit: 100 }))

const overview = computed(() => read.data.value)
const loading = computed(() => read.isFetching.value && !read.data.value)
const error = computed(() => read.error.value as ApiError | null)

const compact = (value: number) => writeFigure(value, { compact: true })
const minutes = (ms: number) => writeFigure(ms / 60_000, { decimals: 1 })
const per = (value: number, over: number) => (over ? value / over : 0)

// --- Figures ------------------------------------------------------------------------------

const board = computed<Kpi[]>(() => {
  const totals = overview.value?.totals
  if (!totals) return []
  const { sessions, previous_sessions: previous, playtime_ms: playtime, duration_ms: duration, previous_duration_ms: previousDuration, player_days: playerDays } = totals
  return [
    {
      key: 'sessions',
      label: 'Sessions',
      value: compact(sessions),
      trend: trendOf(sessions, previous),
      info: `Sessions started in ${workspace.environment}.`
    },
    {
      key: 'per-player',
      label: 'Sessions per player',
      value: writeFigure(per(sessions, playerDays)),
      info: 'Sessions divided by active players, per day.'
    },
    {
      key: 'playtime',
      label: 'Playtime',
      value: compact(playtime / 3_600_000),
      unit: 'h',
      info: 'Hours played across every session, time in the foreground only.'
    },
    {
      key: 'length',
      label: 'Average session',
      value: minutes(per(duration, sessions)),
      unit: 'min',
      trend: trendOf(per(duration, sessions), per(previousDuration, previous)),
      info: `From the first event to the last. Half of all sessions ran under ${minutes(totals.median_ms)} min, nine in ten under ${minutes(totals.p90_ms)} min.`
    },
    {
      key: 'player-playtime',
      label: 'Playtime per player',
      value: writeFigure(per(playtime, playerDays) / 60_000, { decimals: 0 }),
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

type Day = Schema<'SessionDayOut'>

function valueOf(day: Day | undefined, previous = false): number {
  if (!day) return 0
  const sessions = previous ? day.previous_sessions : day.sessions
  if (metric.value === 'playtime') return previous ? 0 : Math.round(day.playtime_ms / 3_600_000)
  if (metric.value === 'length') return Math.round(per(previous ? day.previous_duration_ms : day.duration_ms, sessions) / 6_000) / 10
  return sessions
}

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

/* Every day of the period, a day with no row drawn as nothing played. */
const dayRows = computed(() => {
  const held = new Map((overview.value?.days ?? []).map(day => [day.day.slice(0, 10), day]))
  return range.value.days.map(day => ({ day, row: held.get(day) }))
})

const categories = computed(() => dayRows.value.map(entry => dayTick.format(new Date(entry.day))))
const series = computed<MetricSeries[]>(() => [
  { name: 'This period', data: dayRows.value.map(entry => valueOf(entry.row)), type: 'area', tone: 1 },
  ...(metric.value === 'playtime' ? [] : [{ name: 'Previous period', data: dayRows.value.map(entry => valueOf(entry.row, true)), dashed: true, tone: 'muted' as const }])
])

// --- Lengths ------------------------------------------------------------------------------

/** "30s", "5m", "1h", "4h". */
function span(ms: number): string {
  if (ms < 60_000) return `${Math.round(ms / 1_000)}s`
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)}m`
  return `${writeFigure(ms / 3_600_000, { decimals: ms % 3_600_000 ? 1 : 0 })}h`
}

const lengths = computed(() => (overview.value?.lengths ?? []).map(rung => ({
  value: !rung.from_ms ? `Under ${span(rung.to_ms ?? 0)}` : rung.to_ms ? `${span(rung.from_ms)} to ${span(rung.to_ms)}` : `Over ${span(rung.from_ms)}`,
  count: rung.sessions
})))

// --- How sessions end ---------------------------------------------------------------------

const ENDINGS: Record<string, { label: string, tone: BadgeTone, note: string, bar: string }> = {
  ended: { label: 'Ended', tone: 'success', note: 'The game sent session_end.', bar: 'bg-success' },
  timed_out: { label: 'Timed out', tone: 'neutral', note: 'No session_end arrived; the SDK closed the session after the timeout.', bar: 'bg-ink/30' },
  open: { label: 'Open', tone: 'warning', note: 'An event in the last thirty minutes: still playing.', bar: 'bg-warning' }
}

const endingOf = (outcome: string) => ENDINGS[outcome] ?? { label: outcome, tone: 'neutral' as BadgeTone, note: '', bar: 'bg-ink/20' }

const endings = computed(() => {
  const rows = overview.value?.outcomes ?? []
  const total = rows.reduce((sum, row) => sum + row.sessions, 0)
  return rows.map(row => ({ ...row, share: per(row.sessions, total) }))
})

// --- By platform --------------------------------------------------------------------------

type PlatformRow = Schema<'SessionPlatformOut'>

const PLATFORM_COLUMNS: DataTableColumn<PlatformRow>[] = [
  { key: 'platform', title: 'Platform', fixed: true },
  { key: 'sessions', title: 'Sessions', mono: true },
  { key: 'share', title: 'Share', mono: true, value: row => row.sessions },
  { key: 'length', title: 'Average session', mono: true, value: row => per(row.duration_ms, row.sessions) },
  { key: 'perPlayer', title: 'Per player', mono: true, value: row => per(row.sessions, row.player_days) }
]

const platformColumns = ref(defaultColumnState(PLATFORM_COLUMNS))
const platforms = computed(() => [...(overview.value?.platforms ?? [])])
const platformTotal = computed(() => platforms.value.reduce((sum, row) => sum + row.sessions, 0))

// --- Recent sessions ----------------------------------------------------------------------

type SessionRow = Schema<'SessionOut'>

const recent = computed<SessionRow[]>(() => [...(recentRead.data.value?.sessions ?? [])])

const SESSION_COLUMNS: DataTableColumn<SessionRow>[] = [
  { key: 'session_id', title: 'Session ID', fixed: true, mono: true },
  { key: 'player', title: 'Player', mono: true, value: row => row.player_id || row.install_id },
  { key: 'platform', title: 'Platform' },
  { key: 'app_version', title: 'App version', mono: true, hidden: true },
  { key: 'duration_ms', title: 'Length', mono: true },
  { key: 'events', title: 'Events', mono: true },
  { key: 'outcome', title: 'Ended', value: row => endingOf(row.outcome).label },
  { key: 'started_at', title: 'Started', align: 'end', mono: true, value: row => Date.parse(row.started_at) }
]

const sessionColumns = ref(defaultColumnState(SESSION_COLUMNS))
const sessionSort = ref<DataTableSort[]>([{ key: 'started_at', order: 'desc' }])
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
        How <span class="text-ink">{{ workspace.project?.name }}</span> is played: how often, for how
        long, at what hours, and how sessions end.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <LoadState
      :error="error"
      :loading="loading"
      what="sessions"
      @retry="read.execute()"
    >
      <p v-if="overview?.complete_through" class="m-0 text-xs text-ink/55">
        Sessions are counted once their day is over: complete through {{ formatDate(overview.complete_through) }}.
      </p>
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
        :description="`Session starts by weekday and hour, in your time zone (${overview?.timezone ?? timezone}), over the period.`"
        title="When players play"
      >
        <WeekHeatmap :values="overview?.heatmap ?? []" unit="session starts" />
      </SectionCard>

      <div class="grid gap-4 xl:grid-cols-2">
        <SectionCard description="How long sessions ran, from the first event to the last." title="Session length">
          <BreakdownList :rows="lengths" label="Sessions by length" />
        </SectionCard>

        <SectionCard description="How each session was closed." title="How sessions end">
          <div class="flex flex-col gap-5">
            <!-- One bar cut by share, then the legend under it with the figures. -->
            <div class="flex h-2.5 overflow-hidden rounded-full bg-ink/6">
              <span
                v-for="row in endings"
                :key="row.outcome"
                :class="endingOf(row.outcome).bar"
                :style="{ width: `${row.share * 100}%` }"
              />
            </div>
            <ul class="m-0 flex list-none flex-col gap-3 p-0">
              <li v-for="row in endings" :key="row.outcome" class="flex items-center justify-between gap-3 text-[13px]">
                <span class="flex min-w-0 flex-col">
                  <StatusBadge :label="endingOf(row.outcome).label" :tone="endingOf(row.outcome).tone" class="w-fit" />
                  <span class="mt-1 text-xs text-ink/55">{{ endingOf(row.outcome).note }}</span>
                </span>
                <span class="flex flex-none items-baseline gap-2 font-mono tabular-nums">
                  <span class="text-ink">{{ writeFigure(row.sessions) }}</span>
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
          :items="platforms"
          :paged="false"
          item-value="platform"
          label="Sessions by platform"
        >
          <template #cell.sessions="{ item }">
            {{ compact(item.sessions) }}
          </template>
          <template #cell.share="{ item }">
            {{ writeFigure(per(item.sessions, platformTotal) * 100, { decimals: 1, unit: '%' }) }}
          </template>
          <template #cell.length="{ item }">
            {{ minutes(per(item.duration_ms, item.sessions)) }} min
          </template>
          <template #cell.perPlayer="{ item }">
            {{ writeFigure(per(item.sessions, item.player_days)) }}
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
          :loading="recentRead.isFetching.value && !recentRead.data.value"
          empty-description="A session shows here once the SDK has sent its first events."
          empty-title="No sessions yet."
          item-value="session_id"
          label="Recent sessions"
        >
          <template #cell.session_id="{ item }">
            <span :title="item.session_id" class="font-mono text-ink/70">{{ shortId(item.session_id) }}</span>
          </template>
          <template #cell.player="{ item }">
            <NuxtLink
              :title="item.player_id || item.install_id"
              :to="`/analytics/players/${encodeURIComponent(item.player_id || item.install_id)}`"
              class="font-mono text-ink no-underline hover:text-primary"
            >
              {{ shortId(item.player_id || item.install_id) }}
            </NuxtLink>
          </template>
          <template #cell.duration_ms="{ item }">
            {{ length(item.duration_ms) }}
          </template>
          <template #cell.outcome="{ item }">
            <StatusBadge :label="endingOf(item.outcome).label" :tone="endingOf(item.outcome).tone" />
          </template>
          <template #cell.started_at="{ item }">
            <span class="font-mono">{{ formatTimestamp(item.started_at) }}</span>
          </template>
        </DataTable>
      </SectionCard>
    </LoadState>
  </div>
</template>
