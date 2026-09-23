<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Every event the selected project's game sends: how often, from how many players, how
// recently, and how that moved. The catalogue is where somebody checks that an event is
// arriving at all, and each name opens the event in detail.
//
// Sample data until the API client is wired in (app/utils/events-sample.ts says what each
// part will come from).
const { project } = useWorkspace()

const route = useRoute()
const router = useRouter()

const PERIODS: SegmentedOption[] = [
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' }
]

// In the URL, so a link to an event and the way back keep the period.
const period = computed<EventPeriod>({
  get: () => (['24h', '7d', '30d'].includes(String(route.query.period)) ? route.query.period as EventPeriod : '7d'),
  set: value => void router.replace({ query: { ...route.query, period: value } })
})

const rows = computed(() => sampleEventSummaries(period.value))

const periodWords = computed(() => (PERIODS.find(option => option.value === period.value)?.label ?? '').toLowerCase())

// --- Summary ------------------------------------------------------------------------------

const compact = (value: number) => writeFigure(value, { compact: true })

/** "4s ago", "3m ago", "2h ago". */
function ago(seconds: number): string {
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3_600) return `${Math.round(seconds / 60)}m ago`
  return `${Math.round(seconds / 3_600)}h ago`
}

const board = computed<Kpi[]>(() => {
  const total = rows.value.reduce((sum, row) => sum + row.count, 0)
  const previous = rows.value.reduce((sum, row) => sum + row.previous, 0)
  const custom = rows.value.filter(row => row.category === 'custom')
  const players = Math.max(...rows.value.map(row => row.players))
  return [
    {
      key: 'events',
      label: 'Events',
      value: compact(total),
      trend: trendOf(total, previous),
      info: 'Every event accepted from this project\'s production environment.'
    },
    {
      key: 'names',
      label: 'Event names',
      value: String(rows.value.length),
      info: 'Distinct event names seen in the period.'
    },
    {
      key: 'per-player',
      label: 'Events per player',
      value: compact(total / players),
      info: 'Events divided by the players who sent any.'
    },
    {
      key: 'custom',
      label: 'Custom events',
      value: `${writeFigure(custom.reduce((sum, row) => sum + row.count, 0) / total * 100, { decimals: 1 })}%`,
      info: 'The share of events with a name of the game\'s own rather than a standard one.'
    },
    {
      key: 'last',
      label: 'Last event',
      value: ago(Math.min(...rows.value.map(row => row.lastSeen))),
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

const statusOf = (row: EventSummary) => (row.lastSeen < QUIET_AFTER ? 'arriving' : 'quiet')

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const { category = [], status = [] } = filters.value
  return rows.value.filter(row =>
    (!category.length || category.includes(row.category))
    && (!status.length || status.includes(statusOf(row)))
    && (!needle || row.name.includes(needle) || row.description.toLowerCase().includes(needle)))
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

const COLUMNS: DataTableColumn<EventSummary>[] = [
  { key: 'name', title: 'Event', fixed: true, minWidth: 260 },
  { key: 'category', title: 'Category', value: row => EVENT_CATEGORY_LABELS[row.category] },
  { key: 'count', title: 'Occurrences', align: 'end', mono: true },
  { key: 'players', title: 'Players', align: 'end', mono: true },
  { key: 'perPlayer', title: 'Per player', align: 'end', mono: true, value: row => row.count / row.players },
  { key: 'properties', title: 'Properties', align: 'end', mono: true },
  { key: 'lastSeen', title: 'Last seen', align: 'end', mono: true }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'count', order: 'desc' }])
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Events">
      <template #description>
        Every event <span class="text-ink">{{ project.name }}</span> sends from production: how
        often, from how many players, and whether it is still arriving.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <KpiBoard
      :description="`The last ${periodWords}, against the ${periodWords} before.`"
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
        empty-description="No event matches these filters. Remove one, or clear them all."
        empty-title="No events match."
        item-value="name"
        label="Events"
      >
        <template #cell.name="{ item }">
          <NuxtLink
            :to="{ path: `/analytics/events/${item.name}`, query: { period } }"
            class="
              group flex min-w-0 flex-col no-underline
            "
          >
            <span
              class="
                truncate font-mono text-[13px] text-ink transition-colors
                group-hover:text-primary
              "
            >{{ item.name }}</span>
            <span class="truncate text-xs text-ink/55">{{ item.description }}</span>
          </NuxtLink>
        </template>
        <template #cell.category="{ item }">
          <Tag :label="EVENT_CATEGORY_LABELS[item.category]" :tone="EVENT_CATEGORY_TONES[item.category]" />
        </template>
        <template #cell.count="{ item }">
          <ProjectFigure :previous="item.previous" :value="item.count" :written="compact(item.count)" />
        </template>
        <template #cell.players="{ item }">
          {{ compact(item.players) }}
        </template>
        <template #cell.perPlayer="{ item }">
          {{ writeFigure(item.count / item.players) }}
        </template>
        <template #cell.lastSeen="{ item }">
          <span class="inline-flex items-center gap-1.5">
            <span
              :class="statusOf(item) === 'arriving' ? 'bg-success' : 'bg-warning'"
              aria-hidden="true"
              class="size-1.5 rounded-full"
            />
            {{ ago(item.lastSeen) }}
          </span>
        </template>
      </DataTable>
    </SectionCard>
  </div>
</template>
