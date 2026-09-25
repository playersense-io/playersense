<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'

// Every event the selected project's game sends: how often, from how many players, how
// recently, and how that moved (GET …/analytics/{env}/events). The catalogue is where somebody
// checks that an event is arriving at all, and each name opens the event in detail.
const workspace = useWorkspaceStore()

const route = useRoute()
const router = useRouter()

type EventPeriod = '1d' | '7d' | '30d'

const PERIODS: SegmentedOption[] = [
  { value: '1d', label: 'Today' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' }
]

// In the URL, so a link to an event and the way back keep the period.
const period = computed<EventPeriod>({
  get: () => (['1d', '7d', '30d'].includes(String(route.query.period)) ? route.query.period as EventPeriod : '7d'),
  set: value => void router.replace({ query: { ...route.query, period: value } })
})

/* Up to and including today: the catalogue is about what is arriving now. */
const range = computed(() => {
  const days = Number.parseInt(period.value, 10)
  const to = utcDay(Date.now())
  return { from: utcDay(Date.now() - (days - 1) * 86_400_000), to }
})

const read = useApiGet<Schema<'EventCatalogueOut'>>(() => workspace.analyticsPath
  && withQuery(`${workspace.analyticsPath}/events`, range.value))

type EventRow = Schema<'EventSummaryOut'>

const rows = computed<EventRow[]>(() => [...(read.data.value?.events ?? [])])
const loading = computed(() => read.isFetching.value && !read.data.value)
const error = computed(() => read.error.value as ApiError | null)
const threshold = computed(() => read.data.value?.suppression_threshold ?? 5)

const periodWords = computed(() => (period.value === '1d' ? 'day' : (PERIODS.find(option => option.value === period.value)?.label ?? '').toLowerCase()))

// --- Summary ------------------------------------------------------------------------------

const compact = (value: number) => writeFigure(value, { compact: true })

/** Seconds since an ISO time. */
const secondsSince = (iso: string) => Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 1_000))

/** "4s ago", "3m ago", "2h ago", "3d ago". */
function ago(seconds: number): string {
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3_600) return `${Math.round(seconds / 60)}m ago`
  if (seconds < 172_800) return `${Math.round(seconds / 3_600)}h ago`
  return `${Math.round(seconds / 86_400)}d ago`
}

const board = computed<Kpi[]>(() => {
  const total = rows.value.reduce((sum, row) => sum + row.events, 0)
  const previous = rows.value.reduce((sum, row) => sum + row.previous_events, 0)
  const custom = rows.value.filter(row => row.type === 'custom').reduce((sum, row) => sum + row.events, 0)
  const last = rows.value.length ? Math.min(...rows.value.map(row => secondsSince(row.last_seen))) : null
  return [
    {
      key: 'events',
      label: 'Events',
      value: compact(total),
      trend: trendOf(total, previous),
      info: `Every event accepted from this project's ${workspace.environment} environment.`
    },
    {
      key: 'names',
      label: 'Event names',
      value: String(rows.value.length),
      info: 'Distinct event names seen in the period.'
    },
    {
      key: 'custom',
      label: 'Custom events',
      value: `${writeFigure(total ? custom / total * 100 : 0, { decimals: 1 })}%`,
      info: 'The share of events the game named itself, rather than the SDK\'s session, purchase and exposure events.'
    },
    {
      key: 'last',
      label: 'Last event',
      value: last === null ? 'Never' : ago(last),
      info: 'When the most recent event arrived. Events take up to about ten seconds to show.'
    }
  ]
})

// --- Catalogue ----------------------------------------------------------------------------

// An event that has not arrived for five minutes has probably stopped being sent.
const QUIET_AFTER = 300

const FILTER_GROUPS: FilterGroup[] = [
  {
    key: 'category',
    label: 'Category',
    options: (Object.keys(EVENT_CATEGORY_LABELS) as EventCategory[]).map(value => ({ value, label: EVENT_CATEGORY_LABELS[value] }))
  },
  {
    key: 'status',
    label: 'Status',
    options: [
      { value: 'arriving', label: 'Arriving' },
      { value: 'quiet', label: 'Quiet for 5 minutes or more' }
    ]
  }
]

const filters = ref<Record<string, string[]>>({ category: [], status: [] })
const query = ref('')

const statusOf = (row: EventRow) => (secondsSince(row.last_seen) < QUIET_AFTER ? 'arriving' : 'quiet')

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const { category = [], status = [] } = filters.value
  return rows.value.filter(row =>
    (!category.length || category.includes(row.category))
    && (!status.length || status.includes(statusOf(row)))
    && (!needle || row.name.toLowerCase().includes(needle) || (row.description ?? '').toLowerCase().includes(needle)))
})

// What the toolbar shows on its left: one chip per ticked value, and the search.
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
  const [group = '', value] = key.split(':')
  filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(item => item !== value) }
}

function clearFilters() {
  query.value = ''
  filters.value = { category: [], status: [] }
}

const categoryLabel = (category: string) => EVENT_CATEGORY_LABELS[category as EventCategory] ?? category
const categoryTone = (category: string) => EVENT_CATEGORY_TONES[category as EventCategory] ?? 'neutral'

const COLUMNS: DataTableColumn<EventRow>[] = [
  { key: 'name', title: 'Event', fixed: true, minWidth: 260 },
  { key: 'category', title: 'Category', value: row => categoryLabel(row.category) },
  { key: 'events', title: 'Occurrences', align: 'end', mono: true },
  { key: 'players', title: 'Players', align: 'end', mono: true, value: row => row.players ?? -1 },
  { key: 'perPlayer', title: 'Per player', align: 'end', mono: true, value: row => (row.players ? row.events / row.players : -1) },
  { key: 'properties', title: 'Properties', align: 'end', mono: true },
  { key: 'lastSeen', title: 'Last seen', align: 'end', mono: true, value: row => -Date.parse(row.last_seen) }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'events', order: 'desc' }])
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Events">
      <template #description>
        Every event <span class="text-ink">{{ workspace.project?.name }}</span> sends from {{ workspace.environment }}: how
        often, from how many players, and whether it is still arriving.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <LoadState
      :error="error"
      :loading="loading"
      what="the events"
      @retry="read.execute()"
    >
      <KpiBoard
        :description="period === '1d' ? 'Today so far, against yesterday.' : `The last ${periodWords}, against the ${periodWords} before.`"
        :items="board"
        title="Summary"
      />

      <SectionCard
        :padded="false"
        description="Each event name, busiest first. Open one to see it over time, where it comes from and what it carries."
        title="Event catalogue"
      >
        <TableToolbar :filters="active" @clear="clearFilters" @remove="removeFilter">
          <SearchField
            v-model="query"
            class="w-56"
            label="Search events"
            placeholder="Search events"
          />
          <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
          <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
        </TableToolbar>

        <DataTable
          v-model:column-state="columns"
          v-model:sort-by="sortBy"
          :columns="COLUMNS"
          :items="matched"
          :paged="false"
          :empty-description="rows.length ? 'No event matches these filters. Remove one, or clear them all.' : 'Nothing arrived in the period. Send an event with the SDK and it shows here within seconds.'"
          :empty-title="rows.length ? 'No events match.' : 'No events yet.'"
          item-value="name"
          label="Events"
        >
          <template #cell.name="{ item }">
            <NuxtLink
              :to="{ path: `/analytics/events/${encodeURIComponent(item.name)}`, query: { period } }"
              class="group flex min-w-0 flex-col no-underline"
            >
              <span class="truncate font-mono text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.name }}</span>
              <span class="truncate text-xs text-ink/55">{{ item.description }}</span>
            </NuxtLink>
          </template>
          <template #cell.category="{ item }">
            <Tag :label="categoryLabel(item.category)" :tone="categoryTone(item.category)" />
          </template>
          <template #cell.events="{ item }">
            <ProjectFigure :previous="item.previous_events" :value="item.events" :written="compact(item.events)" />
          </template>
          <template #cell.players="{ item }">
            <span v-if="item.players != null">{{ compact(item.players) }}</span>
            <span v-else :title="`Fewer than ${threshold} players`" class="text-ink/40">–</span>
          </template>
          <template #cell.perPlayer="{ item }">
            {{ item.players ? writeFigure(item.events / item.players) : '–' }}
          </template>
          <template #cell.lastSeen="{ item }">
            <span class="inline-flex items-center gap-1.5">
              <span
                :class="statusOf(item) === 'arriving' ? 'bg-success' : 'bg-warning'"
                aria-hidden="true"
                class="size-1.5 rounded-full"
              />
              {{ ago(secondsSince(item.last_seen)) }}
            </span>
          </template>
        </DataTable>
      </SectionCard>
    </LoadState>
  </div>
</template>
