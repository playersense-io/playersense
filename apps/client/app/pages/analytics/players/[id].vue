<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// One player: who they are as far as the game has said, what their whole life adds up to,
// how the last 30 days went day by day, and what they did last. What somebody opens to
// answer a support ticket or to see what a real player's week looks like.
//
// Sample data until a player endpoint exists (app/utils/players-sample.ts says what each
// part will come from; the latest events need a raw-event query path that does not exist
// yet).
const route = useRoute()

const id = computed(() => String(route.params.id))
const detail = computed(() => samplePlayerDetail(id.value))

// The breadcrumb names the player; an install ID is cut to its first block.
usePageCrumb(() => (id.value.length > 12 ? `${id.value.slice(0, 8)}…` : id.value))

const compact = (value: number) => writeFigure(value, { compact: true })
const money = (value: number) => `$${writeFigure(value, { format: 'money' })}`
const hours = (ms: number) => `${writeFigure(ms / 3_600_000, { decimals: 1 })} h`

const date = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' })
const dateTime = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

/** "12m ago", "5h ago", "3d ago". */
function ago(at: number): string {
  const minutes = Math.max(1, Math.round((Date.now() - at) / 60_000))
  if (minutes < 60) return `${minutes}m ago`
  if (minutes < 1_440) return `${Math.round(minutes / 60)}h ago`
  return `${Math.round(minutes / 1_440)}d ago`
}

const STATUS_TONES: Record<PlayerStatus, BadgeTone> = { active: 'success', lapsed: 'warning', churned: 'neutral' }

// --- Who ----------------------------------------------------------------------------------

const facts = computed<Fact[]>(() => {
  if (!detail.value) return []
  const { player } = detail.value
  return [
    { key: 'id', label: player.identifiedBy === 'actor_key' ? 'Player ID' : 'Install ID', value: player.id, mono: true, copy: player.id },
    { key: 'first', label: 'First seen', value: date.format(player.firstSeen), note: ago(player.firstSeen) },
    { key: 'last', label: 'Last seen', value: dateTime.format(player.lastSeen), note: ago(player.lastSeen) },
    { key: 'platform', label: 'Platform', value: player.platform, note: `App ${player.version}` },
    { key: 'country', label: 'Country', value: player.country },
    { key: 'source', label: 'Acquisition', value: player.source, info: 'Where the player came from, as reported with their first attributed event.' }
  ]
})

// --- Lifetime -----------------------------------------------------------------------------

const lifetime = computed<Kpi[]>(() => {
  if (!detail.value) return []
  const { player } = detail.value
  return [
    { key: 'days', label: 'Active days', value: String(player.activeDays), info: 'Days with at least one session, since first seen.' },
    { key: 'sessions', label: 'Sessions', value: compact(player.sessions) },
    { key: 'playtime', label: 'Playtime', value: hours(player.playtimeMs) },
    { key: 'revenue', label: 'Revenue', value: money(player.revenueMicros / 1_000_000), info: 'Verified purchases over the player\'s life, gross.' },
    { key: 'purchases', label: 'Purchases', value: String(player.purchases) }
  ]
})

// --- Last 30 days -------------------------------------------------------------------------

const METRICS: MetricOption[] = [
  { value: 'playtime', label: 'Playtime', description: 'Minutes played each day.' },
  { value: 'sessions', label: 'Sessions', description: 'Sessions started each day.' },
  { value: 'events', label: 'Events', description: 'Events sent each day.' },
  { value: 'revenue', label: 'Revenue', description: 'Verified purchases each day, gross.' }
]

const metric = ref('playtime')

type Day = NonNullable<typeof detail.value>['days'][number]

function valueOf(day: Day): number {
  if (metric.value === 'sessions') return day.sessions
  if (metric.value === 'events') return day.events
  if (metric.value === 'revenue') return day.revenueMicros / 1_000_000
  return Math.round(day.playtimeMs / 60_000)
}

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })

const categories = computed(() => (detail.value?.days ?? []).map(day => dayTick.format(day.at)))
const series = computed<MetricSeries[]>(() => [{
  name: METRICS.find(option => option.value === metric.value)?.label ?? '',
  data: (detail.value?.days ?? []).map(valueOf),
  type: 'area',
  tone: metric.value === 'revenue' ? 3 : 1
}])

const playedDays = computed(() => (detail.value?.days ?? []).filter(day => day.sessions > 0).reverse())

const DAY_COLUMNS: DataTableColumn<Day>[] = [
  { key: 'at', title: 'Day', fixed: true, mono: true },
  { key: 'sessions', title: 'Sessions', align: 'end', mono: true },
  { key: 'playtimeMs', title: 'Playtime', align: 'end', mono: true },
  { key: 'events', title: 'Events', align: 'end', mono: true },
  { key: 'purchases', title: 'Purchases', align: 'end', mono: true },
  { key: 'revenueMicros', title: 'Revenue', align: 'end', mono: true }
]

const dayColumns = ref(defaultColumnState(DAY_COLUMNS))
const daysPerPage = ref(10)

// --- Latest events ------------------------------------------------------------------------

type Occurrence = NonNullable<typeof detail.value>['recent'][number]

const EVENT_COLUMNS: DataTableColumn<Occurrence>[] = [
  { key: 'at', title: 'Time', fixed: true, mono: true },
  { key: 'name', title: 'Event', mono: true },
  { key: 'payload', title: 'Properties', mono: true, sortable: false }
]

const eventColumns = ref(defaultColumnState(EVENT_COLUMNS))
</script>

<template>
  <div class="flex flex-col gap-8">
    <template v-if="detail">
      <PageHeader :title="detail.player.id">
        <template #badge>
          <StatusBadge
            :detail="detail.player.status === 'active' ? '' : `since ${ago(detail.player.lastSeen).replace(' ago', '')}`"
            :label="PLAYER_STATUS_LABELS[detail.player.status]"
            :tone="STATUS_TONES[detail.player.status]"
          />
        </template>
        <template #description>
          {{ detail.player.platform }} player from {{ detail.player.country }}, first seen
          {{ date.format(detail.player.firstSeen) }} via {{ detail.player.source }}.
          <template v-if="detail.player.identifiedBy === 'install_id'">
            Known by install ID only — a reinstall would start a new history.
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

      <div class="grid gap-4 xl:grid-cols-2">
        <SectionCard :padded="false" description="The days they played in the last 30, latest first." title="Days played">
          <DataTable
            v-model:column-state="dayColumns"
            v-model:items-per-page="daysPerPage"
            :columns="DAY_COLUMNS"
            :items="playedDays"
            empty-description="No sessions in the last 30 days."
            empty-title="Nothing recent."
            item-value="at"
            label="Days played"
          >
            <template #cell.at="{ item }">
              {{ dayTick.format(item.at) }}
            </template>
            <template #cell.playtimeMs="{ item }">
              {{ writeFigure(item.playtimeMs / 60_000, { decimals: 0 }) }} min
            </template>
            <template #cell.revenueMicros="{ item }">
              <span :class="item.revenueMicros ? 'text-ink' : 'text-ink/40'">
                {{ money(item.revenueMicros / 1_000_000) }}
              </span>
            </template>
          </DataTable>
        </SectionCard>

        <SectionCard :padded="false" description="What the player did last, with what each event carried." title="Latest events">
          <DataTable
            v-model:column-state="eventColumns"
            :columns="EVENT_COLUMNS"
            :items="detail.recent"
            :paged="false"
            item-value="at"
            label="Latest events"
          >
            <template #cell.at="{ item }">
              {{ dateTime.format(item.at) }}
            </template>
            <template #cell.name="{ item }">
              <NuxtLink
                :to="`/analytics/events/${item.name}`"
                class="
                  text-ink no-underline
                  hover:text-primary
                "
              >
                {{ item.name }}
              </NuxtLink>
            </template>
            <template #cell.payload="{ item }">
              <span :title="JSON.stringify(item.payload, null, 2)" class="text-ink/70">
                {{ JSON.stringify(item.payload) }}
              </span>
            </template>
          </DataTable>
        </SectionCard>
      </div>
    </template>

    <SectionCard v-else :padded="false" title="Players">
      <EmptyState
        :description="`No player in this project has been seen as “${id}”. An install ID changes when the game is reinstalled.`"
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
