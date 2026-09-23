<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// The selected project's players: how the base is doing as a whole, how each day's players
// split between newcomers, regulars and the ones who came back, and every player on file,
// searchable, each opening their own page.
//
// Sample data until a player endpoint exists (app/utils/players-sample.ts says what each
// part will come from).
const { project } = useWorkspace()

const route = useRoute()
const router = useRouter()

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

const base = computed(() => samplePlayerBase(period.value))

const compact = (value: number) => writeFigure(value, { compact: true })
const money = (value: number) => `$${writeFigure(value, { format: 'money' })}`

// --- The player base ----------------------------------------------------------------------

const board = computed<Kpi[]>(() => [
  {
    key: 'active',
    label: 'Active players',
    value: compact(base.value.active),
    trend: trendOf(base.value.active, base.value.previousActive),
    info: 'Players with at least one session in the period, each counted once.'
  },
  {
    key: 'new',
    label: 'New players',
    value: compact(base.value.fresh),
    trend: trendOf(base.value.fresh, base.value.previousFresh),
    info: 'Players seen for the first time in the period.'
  },
  {
    key: 'payers',
    label: 'Paying players',
    value: writeFigure(base.value.payersShare, { decimals: 1, unit: '%' }),
    trend: trendOf(base.value.payersShare, base.value.previousPayersShare),
    info: 'The share of active players who have ever made a verified purchase.'
  },
  {
    key: 'ltv',
    label: 'Lifetime value',
    value: money(base.value.lifetimeValue),
    trend: trendOf(base.value.lifetimeValue, base.value.previousLifetimeValue),
    info: 'Average revenue per player over their whole life so far, payers and non-payers together.'
  },
  {
    key: 'playtime',
    label: 'Playtime per player',
    value: writeFigure(base.value.playtimePerPlayer, { decimals: 0 }),
    unit: 'min',
    trend: trendOf(base.value.playtimePerPlayer, base.value.previousPlaytimePerPlayer),
    info: 'Minutes played per active player, per day.'
  }
])

// --- Lifecycle ----------------------------------------------------------------------------

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })

const lifecycleCategories = computed(() => base.value.lifecycle.map(day => dayTick.format(day.at)))

const lifecycleSeries = computed<MetricSeries[]>(() => [
  { name: 'Returning', data: base.value.lifecycle.map(day => day.returning), type: 'bar', stack: 'players', tone: 1 },
  { name: 'New', data: base.value.lifecycle.map(day => day.fresh), type: 'bar', stack: 'players', tone: 3 },
  { name: 'Reactivated', data: base.value.lifecycle.map(day => day.reactivated), type: 'bar', stack: 'players', tone: 4 }
])

// --- Directory ----------------------------------------------------------------------------

const players = samplePlayers()

const FILTER_GROUPS: FilterGroup[] = [
  {
    key: 'status',
    label: 'Status',
    options: (Object.keys(PLAYER_STATUS_LABELS) as PlayerStatus[]).map(value => ({ value, label: PLAYER_STATUS_LABELS[value] }))
  },
  { key: 'spend', label: 'Spend', options: [{ value: 'payer', label: 'Paying' }, { value: 'free', label: 'Never paid' }] },
  { key: 'platform', label: 'Platform', options: PLAYER_PLATFORMS.map(value => ({ value, label: value })) },
  { key: 'source', label: 'Acquisition', options: PLAYER_SOURCES.map(value => ({ value, label: value })) }
]

const EMPTY_FILTERS = () => Object.fromEntries(FILTER_GROUPS.map(group => [group.key, [] as string[]]))

const filters = ref<Record<string, string[]>>(EMPTY_FILTERS())
const query = ref('')

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const { status = [], spend = [], platform = [], source = [] } = filters.value
  return players.filter(player =>
    (!status.length || status.includes(player.status))
    && (!spend.length || spend.includes(player.purchases > 0 ? 'payer' : 'free'))
    && (!platform.length || platform.includes(player.platform))
    && (!source.length || source.includes(player.source))
    && (!needle || player.id.toLowerCase().includes(needle)))
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

const COLUMNS: DataTableColumn<PlayerSummary>[] = [
  { key: 'id', title: 'Player', fixed: true, minWidth: 220 },
  { key: 'status', title: 'Status', value: player => PLAYER_STATUS_LABELS[player.status] },
  { key: 'platform', title: 'Platform', hidden: true },
  { key: 'country', title: 'Country' },
  { key: 'source', title: 'Acquisition', hidden: true },
  { key: 'firstSeen', title: 'First seen', mono: true },
  { key: 'lastSeen', title: 'Last seen', align: 'end', mono: true },
  { key: 'activeDays', title: 'Active days', align: 'end', mono: true },
  { key: 'sessions', title: 'Sessions', align: 'end', mono: true, hidden: true },
  { key: 'playtimeMs', title: 'Playtime', align: 'end', mono: true },
  { key: 'revenueMicros', title: 'Revenue', align: 'end', mono: true }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'lastSeen', order: 'desc' }])
const perPage = ref(10)

const STATUS_TONES: Record<PlayerStatus, BadgeTone> = { active: 'success', lapsed: 'warning', churned: 'neutral' }

const date = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' })

/** "12m ago", "5h ago", "3d ago". */
function ago(at: number): string {
  const minutes = Math.max(1, Math.round((Date.now() - at) / 60_000))
  if (minutes < 60) return `${minutes}m ago`
  if (minutes < 1_440) return `${Math.round(minutes / 60)}h ago`
  return `${Math.round(minutes / 1_440)}d ago`
}

const hours = (ms: number) => `${writeFigure(ms / 3_600_000, { decimals: 1 })} h`

// Written out so @nuxt/icon bundles them.
const PLATFORM_ICONS: Record<string, string> = {
  iOS: 'material-symbols:phone-iphone',
  Android: 'material-symbols:android',
  Steam: 'material-symbols:desktop-windows-outline'
}

/** An install UUID shortened in the middle, where it is least read: "1a55f872…27ce7". */
const shortId = (id: string) => (id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-5)}` : id)
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Players">
      <template #description>
        Who plays <span class="text-ink">{{ project.name }}</span>: the player base as a whole,
        and every player on file, one click from their own history.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <KpiBoard
      :description="`The last ${periodWords}, against the ${periodWords} before.`"
      :items="board"
      title="Player base"
    />

    <MetricCard
      :categories="lifecycleCategories"
      :series="lifecycleSeries"
      category-label="Day"
      compact
      description="Each day's active players: returning regulars, first-timers, and players back after 14 days or more away."
      title="Active players by lifecycle"
      type="bar"
    />

    <SectionCard
      :padded="false"
      description="Everyone who has played, most recently seen first. Open a player to see their history."
      title="Player directory"
    >
      <TableToolbar :filters="active" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search players"
          placeholder="Player or install ID"
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
        empty-description="No player matches these filters. Remove one, or clear them all."
        empty-title="No players match."
        item-value="id"
        label="Players"
      >
        <template #cell.id="{ item }">
          <NuxtLink
            :title="item.id"
            :to="`/analytics/players/${item.id}`"
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
              <Icon :name="PLATFORM_ICONS[item.platform] ?? 'material-symbols:sports-esports-outline'" class="text-base" />
            </span>
            <span class="flex min-w-0 flex-col">
              <span
                class="
                  truncate font-mono text-[13px] leading-5 text-ink transition-colors
                  group-hover:text-primary
                "
              >{{ shortId(item.id) }}</span>
              <span class="truncate text-[11px] leading-4 text-ink/50">
                {{ item.identifiedBy === 'actor_key' ? 'Player ID' : 'Install ID' }} · {{ item.platform }}
              </span>
            </span>
          </NuxtLink>
        </template>
        <template #cell.status="{ item }">
          <StatusBadge :label="PLAYER_STATUS_LABELS[item.status]" :tone="STATUS_TONES[item.status]" />
        </template>
        <template #cell.firstSeen="{ item }">
          {{ date.format(item.firstSeen) }}
        </template>
        <template #cell.lastSeen="{ item }">
          {{ ago(item.lastSeen) }}
        </template>
        <template #cell.playtimeMs="{ item }">
          {{ hours(item.playtimeMs) }}
        </template>
        <template #cell.revenueMicros="{ item }">
          <span :class="item.revenueMicros ? 'text-ink' : 'text-ink/40'">
            {{ money(item.revenueMicros / 1_000_000) }}
          </span>
        </template>
      </DataTable>
    </SectionCard>
  </div>
</template>
