<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'

// The selected project's players: how the base is doing as a whole (metrics/query), how each
// day's players split between regulars, newcomers and the ones who came back (the lifecycle
// split), and every player on file (…/players), each opening their own page.
const workspace = useWorkspaceStore()

const route = useRoute()
const router = useRouter()

type PlayerPeriod = '7d' | '30d' | '90d'

const PERIODS: SegmentedOption[] = [
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' }
]

const period = computed<PlayerPeriod>({
  get: () => (['7d', '30d', '90d'].includes(String(route.query.period)) ? route.query.period as PlayerPeriod : '30d'),
  set: value => void router.replace({ query: { ...route.query, period: value } })
})

const periodWords = computed(() => (PERIODS.find(option => option.value === period.value)?.label ?? '').toLowerCase())
const range = computed(() => periodOf(Number.parseInt(period.value, 10)))

const compact = (value: number) => writeFigure(value, { compact: true })
const money = (value: number) => `$${writeFigure(value, { format: 'money' })}`
/* A ratio over nothing (no players, no rows) is no figure, not zero. */
const per = (value: number, over: number) => (over ? value / over : null)

// --- The player base ----------------------------------------------------------------------

const metrics = ref<Schema<'MetricResult'> | null>(null)
const lifecycle = ref<Schema<'MetricResult'> | null>(null)
const baseError = ref<ApiError | null>(null)
const baseLoading = ref(false)

async function loadBase() {
  const path = workspace.analyticsPath
  if (!path) {
    metrics.value = null
    lifecycle.value = null
    return
  }
  baseError.value = null
  baseLoading.value = !metrics.value
  const { previousFrom, from, to } = range.value
  try {
    const [daily, split] = await Promise.all([
      dailyMetrics(path, previousFrom, to),
      apiRequest<Schema<'MetricResult'>>('POST', `${path}/metrics/query`, { metric: 'continuing_players', interval: 'day', from, to })
    ])
    metrics.value = daily
    lifecycle.value = split
  } catch (caught) {
    baseError.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
  } finally {
    baseLoading.value = false
  }
}

watch([range, () => workspace.analyticsPath], loadBase, { immediate: true })

/* A period's figures, or null when no day of it came back (nothing sent, or every day held back). */
function sums(from: string, to: string) {
  const rows = (metrics.value?.rows ?? []).filter(row => row.bucket.slice(0, 10) >= from && row.bucket.slice(0, 10) <= to)
  if (!rows.length) return null
  const add = (pick: (row: typeof rows[number]) => number | undefined) => rows.reduce((sum, row) => sum + (pick(row) ?? 0), 0)
  return {
    playerDays: add(row => row.active_players),
    fresh: add(row => row.new_players),
    payerDays: add(row => row.payers),
    playtime: add(row => row.playtime_ms),
    sessions: add(row => row.sessions)
  }
}

const board = computed<Kpi[]>(() => {
  const count = range.value.days.length
  const now = sums(range.value.from, range.value.to)
  const before = sums(range.value.previousFrom, range.value.previousTo)
  type Figures = NonNullable<typeof now>
  const scaled = (value: number | null, by: number) => (value === null ? null : value * by)
  const conversion = (figures: Figures | null) => (figures ? scaled(per(figures.payerDays, figures.playerDays), 100) : null)
  const playtime = (figures: Figures | null) => (figures ? scaled(per(figures.playtime, figures.playerDays), 1 / 60_000) : null)
  const sessions = (figures: Figures | null) => (figures ? per(figures.sessions, figures.playerDays) : null)
  /* A figure only when there is one, and a change only when both periods have one. */
  const moved = (value: number | null, previous: number | null) => (value === null || previous === null ? undefined : trendOf(value, previous))
  const written = (value: number | null, write: (value: number) => string) => (value === null ? null : write(value))
  return [
    {
      key: 'active',
      label: 'Active players',
      value: written(now ? now.playerDays / count : null, compact),
      trend: moved(now?.playerDays ?? null, before?.playerDays ?? null),
      info: 'Players with at least one session, as a daily average over the period.'
    },
    {
      key: 'new',
      label: 'New players',
      value: written(now?.fresh ?? null, compact),
      trend: moved(now?.fresh ?? null, before?.fresh ?? null),
      info: 'Players seen for the first time in the period.'
    },
    {
      key: 'payers',
      label: 'Payer conversion',
      value: written(conversion(now), value => writeFigure(value, { decimals: 2, unit: '%' })),
      trend: moved(conversion(now), conversion(before)),
      info: 'The share of each day\'s active players who made a purchase that day.'
    },
    {
      key: 'playtime',
      label: 'Playtime per player',
      value: written(playtime(now), value => writeFigure(value, { decimals: 0 })),
      unit: 'min',
      trend: moved(playtime(now), playtime(before)),
      info: 'Minutes played per active player, per day.'
    },
    {
      key: 'sessions',
      label: 'Sessions per player',
      value: written(sessions(now), value => writeFigure(value, { decimals: 1 })),
      trend: moved(sessions(now), sessions(before)),
      info: 'Sessions per active player, per day.'
    }
  ]
})

// --- Lifecycle ----------------------------------------------------------------------------

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

const lifecycleDays = computed(() => {
  const held = new Map((lifecycle.value?.rows ?? []).map(row => [row.bucket.slice(0, 10), row]))
  return range.value.days.map(day => ({ day, row: held.get(day) }))
})

const lifecycleCategories = computed(() => lifecycleDays.value.map(entry => dayTick.format(new Date(entry.day))))

const lifecycleSeries = computed<MetricSeries[]>(() => [
  /* A day with no row is no bar, not a bar of zero. */
  { name: 'Continuing', data: lifecycleDays.value.map(entry => (entry.row ? entry.row.continuing_players ?? 0 : null)), type: 'bar', stack: 'players', tone: 1 },
  { name: 'New', data: lifecycleDays.value.map(entry => (entry.row ? entry.row.new_players ?? 0 : null)), type: 'bar', stack: 'players', tone: 3 },
  { name: 'Reactivated', data: lifecycleDays.value.map(entry => (entry.row ? entry.row.reactivated_players ?? 0 : null)), type: 'bar', stack: 'players', tone: 4 }
])

// --- Directory ----------------------------------------------------------------------------

type PlayerRow = Schema<'PlayerSummaryOut'>
type PlayerStatus = 'active' | 'lapsed' | 'churned'

const STATUS: Record<PlayerStatus, { label: string, tone: BadgeTone }> = {
  active: { label: 'Active', tone: 'success' },
  lapsed: { label: 'Lapsed', tone: 'warning' },
  churned: { label: 'Churned', tone: 'neutral' }
}
const statusOf = (value: string) => STATUS[value as PlayerStatus] ?? { label: value, tone: 'neutral' as BadgeTone }

/* One of each at a time: the API filters on a single value, so each group is a choice. */
const FILTER_GROUPS: FilterGroup[] = [
  {
    key: 'status',
    label: 'Status',
    options: [
      { value: 'active', label: 'Active, seen within 7 days' },
      { value: 'lapsed', label: 'Lapsed, 7 to 30 days away' },
      { value: 'churned', label: 'Churned, over 30 days away' }
    ]
  },
  { key: 'spend', label: 'Spend', options: [{ value: 'paying', label: 'Paying' }, { value: 'never', label: 'Never paid' }] }
]

const EMPTY_FILTERS = () => Object.fromEntries(FILTER_GROUPS.map(group => [group.key, [] as string[]]))

const filters = ref<Record<string, string[]>>(EMPTY_FILTERS())
const query = ref('')

/* A second choice in a group replaces the first. */
watch(filters, (value) => {
  const trimmed = Object.fromEntries(Object.entries(value).map(([key, list]) => [key, list.slice(-1)]))
  if (Object.entries(trimmed).some(([key, list]) => list.length !== (value[key] ?? []).length)) filters.value = trimmed
}, { deep: true })

const SORTS: Record<string, string> = {
  lastSeen: 'last_seen',
  firstSeen: 'first_seen',
  revenue: 'revenue',
  playtime: 'playtime',
  sessions: 'sessions',
  activeDays: 'active_days'
}

const sortBy = ref<DataTableSort[]>([{ key: 'lastSeen', order: 'desc' }])

const directoryUrl = computed(() => workspace.analyticsPath && withQuery(`${workspace.analyticsPath}/players`, {
  status: filters.value.status?.[0],
  spend: filters.value.spend?.[0],
  sort: SORTS[sortBy.value[0]?.key ?? 'lastSeen'] ?? 'last_seen',
  limit: 200
}))

const directory = useApiGet<Schema<'PlayerListOut'>>(directoryUrl)
const more = ref<PlayerRow[]>([])
const nextOffset = ref<number | undefined>()
const loadingMore = ref(false)

watch(directory.data, (out) => {
  more.value = []
  nextOffset.value = out?.next_offset || undefined
})

async function loadMore() {
  if (!directoryUrl.value || !nextOffset.value) return
  loadingMore.value = true
  try {
    const out = await apiRequest<Schema<'PlayerListOut'>>('GET', `${directoryUrl.value}&offset=${nextOffset.value}`)
    more.value = [...more.value, ...(out.players ?? [])]
    nextOffset.value = out.next_offset || undefined
  } finally {
    loadingMore.value = false
  }
}

const players = computed<PlayerRow[]>(() => [...(directory.data.value?.players ?? []), ...more.value])

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return needle ? players.value.filter(player => player.install_id.toLowerCase().includes(needle)) : players.value
})

/* A search that finds nobody loaded may still be somebody: offer their page by ID. */
const lookup = computed(() => {
  const text = query.value.trim()
  return text.length >= 4 && !matched.value.length ? text : ''
})

const active = computed<ActiveFilter[]>(() => [
  ...FILTER_GROUPS.flatMap(group => (filters.value[group.key] ?? []).map(value => ({
    key: `${group.key}:${value}`,
    label: `${group.label}: ${group.options.find(option => option.value === value)?.label ?? value}`
  }))),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') {
    query.value = ''
    return
  }
  const [group = '', ...value] = key.split(':')
  filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(item => item !== value.join(':')) }
}

function clearFilters() {
  query.value = ''
  filters.value = EMPTY_FILTERS()
}

const COLUMNS: DataTableColumn<PlayerRow>[] = [
  { key: 'id', title: 'Player', fixed: true, minWidth: 220, sortable: false },
  { key: 'status', title: 'Status', sortable: false },
  { key: 'platform', title: 'Platform', hidden: true, sortable: false },
  { key: 'country', title: 'Country', sortable: false },
  { key: 'app_version', title: 'App version', mono: true, hidden: true, sortable: false },
  { key: 'firstSeen', title: 'First seen', mono: true, value: player => Date.parse(player.first_seen) },
  { key: 'lastSeen', title: 'Last seen', align: 'end', mono: true, value: player => Date.parse(player.last_seen) },
  { key: 'activeDays', title: 'Active days', align: 'end', mono: true, value: player => player.active_days },
  { key: 'sessions', title: 'Sessions', align: 'end', mono: true, hidden: true, value: player => player.sessions },
  { key: 'playtime', title: 'Playtime', align: 'end', mono: true, value: player => player.playtime_ms },
  { key: 'revenue', title: 'Revenue', align: 'end', mono: true, value: player => player.revenue_micros }
]

const columns = ref(defaultColumnState(COLUMNS))
const perPage = ref(25)

const date = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' })

/** "12m ago", "5h ago", "3d ago". */
function ago(iso: string): string {
  const minutes = Math.max(1, Math.round((Date.now() - Date.parse(iso)) / 60_000))
  if (minutes < 60) return `${minutes}m ago`
  if (minutes < 1_440) return `${Math.round(minutes / 60)}h ago`
  return `${Math.round(minutes / 1_440)}d ago`
}

const hours = (ms: number) => `${writeFigure(ms / 3_600_000, { decimals: 1 })} h`

/** An install UUID shortened in the middle, where it is least read: "1a55f872…27ce7". */
const shortId = (id: string) => (id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-5)}` : id)
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Players">
      <template #description>
        Who plays <span class="text-ink">{{ workspace.project?.name }}</span>: the player base as a whole,
        and every player on file, one click from their own history.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <p v-if="baseError" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ baseError.message }}
    </p>
    <KpiBoard
      :description="`The last ${periodWords}, against the ${periodWords} before.`"
      :items="board"
      :loading="baseLoading"
      title="Player base"
    />

    <MetricCard
      :categories="lifecycleCategories"
      :loading="baseLoading"
      :series="lifecycleSeries"
      category-label="Day"
      compact
      description="Each day's active players: regulars who played within the last week, first-timers, and players back after 7 days or more away."
      title="Active players by lifecycle"
      type="bar"
    />

    <SectionCard
      :padded="false"
      description="Everyone who has played, most recently seen first. Open a player to see their history. Sorting asks the API; search looks through the players loaded."
      title="Player directory"
    >
      <TableToolbar :filters="active" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search players"
          placeholder="Install ID, or a player ID to open"
        />
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </TableToolbar>

      <div class="border-b border-line p-3">
        <InfoNote>
          A player is known by your own player ID when the game sends one, and otherwise by the
          SDK's install ID. An install ID is new after a reinstall, so a player who reinstalled
          without your ID starts a second history that cannot be joined to the first.
        </InfoNote>
      </div>

      <DataTable
        v-model:column-state="columns"
        v-model:items-per-page="perPage"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="matched"
        :loading="directory.isFetching.value && !directory.data.value"
        :empty-description="active.length ? 'No player matches these filters. Remove one, or clear them all.' : 'Nobody has played yet. Players show here once the SDK sends their first session.'"
        :empty-title="active.length ? 'No players match.' : 'No players yet.'"
        item-value="install_id"
        label="Players"
      >
        <template #cell.id="{ item }">
          <NuxtLink
            :title="item.install_id"
            :to="`/analytics/players/${encodeURIComponent(item.install_id)}`"
            class="group flex min-w-0 items-center gap-3 no-underline"
          >
            <span
              aria-hidden="true"
              class="
                flex size-8 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/5
                text-ink/60 transition-colors
                group-hover:bg-primary/10 group-hover:text-primary
              "
            >
              <PlatformIcon :platform="item.platform" :size="16" />
            </span>
            <span class="flex min-w-0 flex-col">
              <span
                class="
                  truncate font-mono text-[13px] leading-5 text-ink transition-colors
                  group-hover:text-primary
                "
              >{{ shortId(item.install_id) }}</span>
              <span class="truncate text-[11px] leading-4 text-ink/50">
                Install ID · {{ item.platform }}
              </span>
            </span>
          </NuxtLink>
        </template>
        <template #cell.status="{ item }">
          <StatusBadge :label="statusOf(item.status).label" :tone="statusOf(item.status).tone" />
        </template>
        <template #cell.country="{ item }">
          {{ item.country || '–' }}
        </template>
        <template #cell.firstSeen="{ item }">
          {{ date.format(new Date(item.first_seen)) }}
        </template>
        <template #cell.lastSeen="{ item }">
          {{ ago(item.last_seen) }}
        </template>
        <template #cell.activeDays="{ item }">
          {{ writeFigure(item.active_days) }}
        </template>
        <template #cell.sessions="{ item }">
          {{ writeFigure(item.sessions) }}
        </template>
        <template #cell.playtime="{ item }">
          {{ hours(item.playtime_ms) }}
        </template>
        <template #cell.revenue="{ item }">
          <span :class="item.revenue_micros ? 'text-ink' : 'text-ink/40'">
            {{ money(item.revenue_micros / 1_000_000) }}
          </span>
        </template>
      </DataTable>

      <div v-if="lookup" class="flex items-center justify-center gap-2 border-t border-line px-5 py-3 text-xs text-ink/60">
        Not among the players loaded.
        <NuxtLink :to="`/analytics/players/${encodeURIComponent(lookup)}`" class="text-primary no-underline hover:underline">
          Open {{ lookup }}
        </NuxtLink>
      </div>
      <div v-else-if="nextOffset" class="flex justify-center border-t border-line px-5 py-3">
        <v-btn
          :loading="loadingMore"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="loadMore"
        >
          Load more players
        </v-btn>
      </div>
    </SectionCard>
  </div>
</template>
