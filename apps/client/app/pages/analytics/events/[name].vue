<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// One event in full: how often it arrived over the period against the one before, who
// sends it and from where, what it carries, and its latest occurrences — what somebody
// opens to check an event is instrumented the way they meant.
//
// Sample data until the API client is wired in (app/utils/events-sample.ts says what each
// part will come from; properties and occurrences need a raw-event query path that does
// not exist yet).
const route = useRoute()
const router = useRouter()

const name = computed(() => String(route.params.name))
usePageCrumb(name)

const PERIODS: SegmentedOption[] = [
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' }
]

const period = computed<EventPeriod>({
  get: () => (['24h', '7d', '30d'].includes(String(route.query.period)) ? route.query.period as EventPeriod : '7d'),
  set: value => void router.replace({ query: { ...route.query, period: value } })
})

const periodWords = computed(() => (PERIODS.find(option => option.value === period.value)?.label ?? '').toLowerCase())

const detail = computed(() => sampleEventDetail(name.value, period.value))

const compact = (value: number) => writeFigure(value, { compact: true })

/** "4s ago", "3m ago", "2h ago". */
function ago(seconds: number): string {
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3_600) return `${Math.round(seconds / 60)}m ago`
  return `${Math.round(seconds / 3_600)}h ago`
}

// --- Figures ------------------------------------------------------------------------------

const board = computed<Kpi[]>(() => {
  if (!detail.value) return []
  const { row } = detail.value
  return [
    {
      key: 'count',
      label: 'Occurrences',
      value: compact(row.count),
      trend: trendOf(row.count, row.previous),
      info: 'Times the event arrived from production in the period.'
    },
    {
      key: 'players',
      label: 'Players',
      value: compact(row.players),
      info: 'Distinct players who sent it at least once. Approximate.'
    },
    {
      key: 'per-player',
      label: 'Per player',
      value: writeFigure(row.count / row.players),
      info: 'Occurrences divided by the players who sent it.'
    },
    {
      key: 'properties',
      label: 'Properties',
      value: String(row.properties),
      info: 'Distinct property keys seen on the event.'
    },
    {
      key: 'last',
      label: 'Last seen',
      value: ago(row.lastSeen),
      info: 'When it last arrived. Events take up to about ten seconds to show.'
    }
  ]
})

// --- Over time ----------------------------------------------------------------------------

const hourTick = new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit', hour12: false })
const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })

const categories = computed(() => (detail.value?.buckets ?? []).map(bucket =>
  (detail.value!.interval === 'hour' ? hourTick : dayTick).format(bucket.at)))

const series = computed<MetricSeries[]>(() => [
  { name: 'This period', data: (detail.value?.buckets ?? []).map(bucket => bucket.count), type: 'area', tone: 1 },
  { name: 'Previous period', data: (detail.value?.buckets ?? []).map(bucket => bucket.previous), dashed: true, tone: 'muted' }
])

// --- Properties ---------------------------------------------------------------------------

type PropertyRow = NonNullable<typeof detail.value>['properties'][number]

const PROPERTY_COLUMNS: DataTableColumn<PropertyRow>[] = [
  { key: 'key', title: 'Property', fixed: true, mono: true },
  { key: 'type', title: 'Type' },
  { key: 'fill', title: 'Present on', align: 'end', mono: true },
  { key: 'distinct', title: 'Distinct values', align: 'end', mono: true },
  { key: 'top', title: 'Top values', sortable: false }
]

const propertyColumns = ref(defaultColumnState(PROPERTY_COLUMNS))

// --- Recent occurrences -------------------------------------------------------------------

type Occurrence = NonNullable<typeof detail.value>['recent'][number]

const OCCURRENCE_COLUMNS: DataTableColumn<Occurrence>[] = [
  { key: 'at', title: 'Time', fixed: true, mono: true },
  { key: 'player', title: 'Player', mono: true },
  { key: 'platform', title: 'Platform' },
  { key: 'version', title: 'App version', mono: true },
  { key: 'country', title: 'Country' },
  { key: 'payload', title: 'Properties', mono: true, sortable: false }
]

const occurrenceColumns = ref(defaultColumnState(OCCURRENCE_COLUMNS))

const clock = new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
</script>

<template>
  <div class="flex flex-col gap-8">
    <template v-if="detail">
      <PageHeader :description="detail.definition.description" :title="detail.definition.name">
        <template #badge>
          <Tag
            :label="EVENT_CATEGORY_LABELS[detail.definition.category]"
            :tone="EVENT_CATEGORY_TONES[detail.definition.category]"
          />
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

      <MetricCard
        :categories="categories"
        :category-label="detail.interval === 'hour' ? 'Hour' : 'Day'"
        :series="series"
        compact
        :description="`Occurrences per ${detail.interval}, against the same ${detail.interval}s of the period before.`"
        title="Over time"
      />

      <div class="grid gap-4 lg:grid-cols-3">
        <SectionCard description="Occurrences by the platform they came from." title="Platform">
          <BreakdownList :rows="detail.platforms" label="Occurrences by platform" />
        </SectionCard>
        <SectionCard description="Occurrences by the player's country." title="Country">
          <BreakdownList :rows="detail.countries" label="Occurrences by country" />
        </SectionCard>
        <SectionCard description="Occurrences by the build that sent them." title="App version">
          <BreakdownList :rows="detail.versions" label="Occurrences by app version" />
        </SectionCard>
      </div>

      <SectionCard
        :padded="false"
        description="Every key the event has carried, how often it is present, and its most common values."
        title="Properties"
      >
        <DataTable
          v-model:column-state="propertyColumns"
          :columns="PROPERTY_COLUMNS"
          :items="detail.properties"
          :paged="false"
          item-value="key"
          label="Properties"
        >
          <template #cell.type="{ item }">
            <Tag :label="item.type" />
          </template>
          <template #cell.fill="{ item }">
            <span :class="item.fill < 0.9 ? 'text-warning' : ''">
              {{ writeFigure(item.fill * 100, { decimals: 0, unit: '%' }) }}
            </span>
          </template>
          <template #cell.top="{ item }">
            <span class="flex items-center gap-1.5">
              <span
                v-for="value in item.top"
                :key="value.value"
                class="
                  inline-flex items-center gap-1 rounded-full bg-ink/5 px-2 py-0.5 font-mono
                  text-[11px] text-ink/80
                "
              >
                {{ value.value }}
                <span class="text-ink/45">{{ Math.round(value.share * 100) }}%</span>
              </span>
            </span>
          </template>
        </DataTable>
      </SectionCard>

      <SectionCard
        :padded="false"
        description="The latest times it arrived, with what it carried."
        title="Recent occurrences"
      >
        <DataTable
          v-model:column-state="occurrenceColumns"
          :columns="OCCURRENCE_COLUMNS"
          :items="detail.recent"
          :paged="false"
          item-value="at"
          label="Recent occurrences"
        >
          <template #cell.at="{ item }">
            {{ clock.format(item.at) }}
          </template>
          <template #cell.payload="{ item }">
            <span :title="JSON.stringify(item.payload, null, 2)" class="text-ink/70">
              {{ JSON.stringify(item.payload) }}
            </span>
          </template>
        </DataTable>
      </SectionCard>
    </template>

    <SectionCard v-else :padded="false" title="Events">
      <EmptyState
        :description="`Nothing from this project's production environment has been called “${name}”.`"
        icon="material-symbols:search-off"
        title="No such event"
      >
        <template #actions>
          <v-btn color="neutral" to="/analytics/events" variant="text">
            All events
          </v-btn>
        </template>
      </EmptyState>
    </SectionCard>
  </div>
</template>
