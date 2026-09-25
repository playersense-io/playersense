<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'

// One player (GET …/analytics/{env}/players/{id}): who they are as far as the game has said,
// what their whole life adds up to, how the last 90 days went day by day, and what they did in
// the last week. What somebody opens to answer a support ticket or to see what a real player's
// week looks like. The ID is the game's own player ID or the SDK's install ID.
const route = useRoute()
const workspace = useWorkspaceStore()

const id = computed(() => String(route.params.id))
const read = useApiGet<Schema<'PlayerProfileOut'>>(() => workspace.analyticsPath && `${workspace.analyticsPath}/players/${encodeURIComponent(id.value)}`)

const detail = computed(() => read.data.value)
const missing = computed(() => (read.error.value as ApiError | null)?.status === 404)
const error = computed(() => (missing.value ? null : read.error.value as ApiError | null))
const loading = computed(() => read.isFetching.value && !read.data.value)

// The breadcrumb names the player; an install ID is cut to its first block.
usePageCrumb(() => (id.value.length > 12 ? `${id.value.slice(0, 8)}…` : id.value))

const compact = (value: number) => writeFigure(value, { compact: true })
const money = (value: number) => `$${writeFigure(value, { format: 'money' })}`
const hours = (ms: number) => `${writeFigure(ms / 3_600_000, { decimals: 1 })} h`

const date = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' })

/** "12m ago", "5h ago", "3d ago". */
function ago(iso: string): string {
  const minutes = Math.max(1, Math.round((Date.now() - Date.parse(iso)) / 60_000))
  if (minutes < 60) return `${minutes}m ago`
  if (minutes < 1_440) return `${Math.round(minutes / 60)}h ago`
  return `${Math.round(minutes / 1_440)}d ago`
}

const STATUS: Record<string, { label: string, tone: BadgeTone }> = {
  active: { label: 'Active', tone: 'success' },
  lapsed: { label: 'Lapsed', tone: 'warning' },
  churned: { label: 'Churned', tone: 'neutral' }
}
const status = computed(() => STATUS[detail.value?.summary.status ?? ''] ?? { label: detail.value?.summary.status ?? '', tone: 'neutral' as BadgeTone })

// --- Who ----------------------------------------------------------------------------------

const facts = computed<Fact[]>(() => {
  if (!detail.value) return []
  const { summary } = detail.value
  return [
    { key: 'id', label: detail.value.kind === 'account' ? 'Player ID' : 'Install ID', value: detail.value.id, mono: true, copy: detail.value.id },
    { key: 'first', label: 'First seen', value: date.format(new Date(summary.first_seen)), note: ago(summary.first_seen) },
    { key: 'last', label: 'Last seen', value: formatTimestamp(summary.last_seen), note: ago(summary.last_seen) },
    { key: 'platform', label: 'Platform', value: summary.platform, note: `App ${summary.app_version}` },
    { key: 'country', label: 'Country', value: summary.country || 'Unknown' },
    {
      key: 'installs',
      label: 'Installs',
      value: String(detail.value.installs.length),
      info: 'The installs this history covers. A player ID joins every install the game sent it from; without one, a reinstall starts a new history.'
    }
  ]
})

// --- Lifetime -----------------------------------------------------------------------------

const lifetime = computed<Kpi[]>(() => {
  if (!detail.value) return []
  const { summary } = detail.value
  return [
    { key: 'days', label: 'Active days', value: String(summary.active_days), info: 'Days with at least one session, since first seen.' },
    { key: 'sessions', label: 'Sessions', value: compact(summary.sessions) },
    { key: 'playtime', label: 'Playtime', value: hours(summary.playtime_ms) },
    { key: 'revenue', label: 'Revenue', value: money(summary.revenue_micros / 1_000_000), info: 'Purchases over the player\'s life, gross, as reported.' },
    { key: 'purchases', label: 'Purchases', value: String(summary.purchases) }
  ]
})

// --- Last 90 days -------------------------------------------------------------------------

const METRICS: MetricOption[] = [
  { value: 'playtime', label: 'Playtime', description: 'Minutes played each day.' },
  { value: 'sessions', label: 'Sessions', description: 'Sessions started each day.' },
  { value: 'events', label: 'Events', description: 'Events sent each day.' },
  { value: 'revenue', label: 'Revenue', description: 'Purchases each day, gross.' }
]

const metric = ref('playtime')

type Day = Schema<'PlayerDayOut'>

function valueOf(day: Day | undefined): number {
  if (!day) return 0
  if (metric.value === 'sessions') return day.sessions
  if (metric.value === 'events') return day.events
  if (metric.value === 'revenue') return day.revenue_micros / 1_000_000
  return Math.round(day.playtime_ms / 60_000)
}

const lastNinety = computed(() => periodOf(91).days.slice(1).concat(utcDay(Date.now())))
const byDate = computed(() => new Map((detail.value?.days ?? []).map(day => [day.day.slice(0, 10), day])))

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

const categories = computed(() => lastNinety.value.map(day => dayTick.format(new Date(day))))
const series = computed<MetricSeries[]>(() => [{
  name: METRICS.find(option => option.value === metric.value)?.label ?? '',
  data: lastNinety.value.map(day => valueOf(byDate.value.get(day))),
  type: 'area',
  tone: metric.value === 'revenue' ? 3 : 1
}])

const playedDays = computed(() => [...(detail.value?.days ?? [])].filter(day => day.sessions > 0 || day.events > 0).reverse())

const DAY_COLUMNS: DataTableColumn<Day>[] = [
  { key: 'day', title: 'Day', fixed: true, mono: true, value: day => Date.parse(day.day) },
  { key: 'sessions', title: 'Sessions', mono: true },
  { key: 'playtime_ms', title: 'Playtime', mono: true },
  { key: 'events', title: 'Events', mono: true },
  { key: 'purchases', title: 'Purchases', mono: true },
  { key: 'revenue_micros', title: 'Revenue', mono: true }
]

const dayColumns = ref(defaultColumnState(DAY_COLUMNS))
const daysPerPage = ref(10)

// --- Latest events ------------------------------------------------------------------------

type Occurrence = Schema<'EventOccurrenceOut'>

const events = computed<Occurrence[]>(() => [...(detail.value?.events ?? [])])

const EVENT_COLUMNS: DataTableColumn<Occurrence>[] = [
  { key: 'event_id', title: 'Event ID', fixed: true, mono: true },
  { key: 'name', title: 'Event', mono: true },
  { key: 'properties', title: 'Properties', mono: true, sortable: false },
  { key: 'at', title: 'Time', align: 'end', mono: true, value: event => Date.parse(event.at) }
]

const eventColumns = ref(defaultColumnState(EVENT_COLUMNS))
const eventsPerPage = ref(10)
</script>

<template>
  <div class="flex flex-col gap-8">
    <LoadState
      v-if="!missing"
      :error="error"
      :loading="loading"
      what="the player"
      @retry="read.execute()"
    >
      <template v-if="detail">
        <PageHeader :title="detail.id">
          <template #badge>
            <StatusBadge
              :detail="detail.summary.status === 'active' ? '' : `since ${ago(detail.summary.last_seen).replace(' ago', '')}`"
              :label="status.label"
              :tone="status.tone"
            />
          </template>
          <template #description>
            {{ detail.summary.platform }} player<template v-if="detail.summary.country">
              from {{ detail.summary.country }}
            </template>, first seen
            {{ date.format(new Date(detail.summary.first_seen)) }}.
            <template v-if="detail.kind === 'install'">
              Known by install ID only: a reinstall would start a new history.
            </template>
          </template>
        </PageHeader>

        <FactGrid :columns="6" :facts="facts" label="Player facts" />

        <KpiBoard :items="lifetime" description="Everything since the player was first seen." title="Lifetime" />

        <MetricCard
          v-model="metric"
          :categories="categories"
          :compact="metric !== 'revenue'"
          :options="METRICS"
          :prefix="metric === 'revenue' ? '$' : ''"
          :series="series"
          category-label="Day"
        />

        <SectionCard :padded="false" description="The days they played in the last 90, latest first." title="Days played">
          <DataTable
            v-model:column-state="dayColumns"
            v-model:items-per-page="daysPerPage"
            :columns="DAY_COLUMNS"
            :items="playedDays"
            empty-description="No sessions in the last 90 days."
            empty-title="Nothing recent."
            item-value="day"
            label="Days played"
          >
            <template #cell.day="{ item }">
              <span class="inline-flex items-baseline gap-1.5">
                <span class="font-mono">{{ formatDay(item.day) }}</span>
                <span class="text-xs text-ink/50">({{ relativeDay(Date.parse(item.day)) }})</span>
              </span>
            </template>
            <template #cell.playtime_ms="{ item }">
              {{ writeFigure(item.playtime_ms / 60_000, { decimals: 0 }) }} min
            </template>
            <template #cell.revenue_micros="{ item }">
              <span :class="item.revenue_micros ? 'text-ink' : 'text-ink/40'">
                {{ money(item.revenue_micros / 1_000_000) }}
              </span>
            </template>
          </DataTable>
        </SectionCard>

        <SectionCard :padded="false" description="What the player did in the last seven days, newest first, with what each event carried." title="Latest events">
          <DataTable
            v-model:column-state="eventColumns"
            v-model:items-per-page="eventsPerPage"
            :columns="EVENT_COLUMNS"
            :items="events"
            empty-description="No events in the last seven days."
            empty-title="Nothing this week."
            item-value="event_id"
            label="Latest events"
          >
            <template #cell.at="{ item }">
              <span class="font-mono">{{ formatTimestamp(item.at) }}</span>
            </template>
            <template #cell.event_id="{ item }">
              <span class="inline-flex items-center gap-1 font-mono text-ink/70">
                {{ item.event_id.length > 16 ? `${item.event_id.slice(0, 8)}…` : item.event_id }}
                <CopyButton :value="item.event_id" label="Copy the event ID" />
              </span>
            </template>
            <template #cell.name="{ item }">
              <NuxtLink
                :to="`/analytics/events/${encodeURIComponent(item.name)}`"
                class="text-ink no-underline hover:text-primary"
              >
                {{ item.name }}
              </NuxtLink>
            </template>
            <template #cell.properties="{ item }">
              <span :title="JSON.stringify(item.properties ?? {}, null, 2)" class="text-ink/70">
                {{ JSON.stringify(item.properties ?? {}) }}
              </span>
            </template>
          </DataTable>
        </SectionCard>
      </template>
    </LoadState>

    <SectionCard v-else :padded="false" title="Players">
      <EmptyState
        :description="`No player in this project and environment has been seen as “${id}”. An install ID changes when the game is reinstalled.`"
        icon="material-symbols:person-search-outline"
        title="No such player"
      >
        <template #actions>
          <v-btn color="neutral" to="/analytics/players" variant="text">
            All players
          </v-btn>
        </template>
      </EmptyState>
    </SectionCard>
  </div>
</template>
