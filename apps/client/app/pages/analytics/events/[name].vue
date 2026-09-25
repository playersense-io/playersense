<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'

// One event in full: how often it arrived over the period against the one before, who sends
// it and from where, what it carries, and its latest occurrences (GET …/events/{name} and
// …/occurrences): what somebody opens to check an event is instrumented the way they meant.
// Its category and description are the studio's own words (…/event-definitions), edited here.
const route = useRoute()
const router = useRouter()
const workspace = useWorkspaceStore()

const name = computed(() => String(route.params.name))
usePageCrumb(name)

type EventPeriod = '1d' | '7d' | '30d'

const PERIODS: SegmentedOption[] = [
  { value: '1d', label: 'Today' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' }
]

const period = computed<EventPeriod>({
  get: () => (['1d', '7d', '30d'].includes(String(route.query.period)) ? route.query.period as EventPeriod : '7d'),
  set: value => void router.replace({ query: { ...route.query, period: value } })
})

const periodWords = computed(() => (period.value === '1d' ? 'day' : (PERIODS.find(option => option.value === period.value)?.label ?? '').toLowerCase()))

/* Up to and including today; a day or a week by the hour, a month by the day. */
const query = computed(() => {
  const days = Number.parseInt(period.value, 10)
  return {
    from: utcDay(Date.now() - (days - 1) * 86_400_000),
    to: utcDay(Date.now()),
    interval: days <= 7 ? 'hour' : 'day'
  }
})

const eventPath = computed(() => workspace.analyticsPath && `${workspace.analyticsPath}/events/${encodeURIComponent(name.value)}`)
const read = useApiGet<Schema<'EventDetailOut'>>(() => eventPath.value && withQuery(eventPath.value, query.value))
const recentRead = useApiGet<Schema<'EventOccurrencesOut'>>(() => eventPath.value && withQuery(`${eventPath.value}/occurrences`, { limit: 50 }))
const definitionsRead = useApiGet<Schema<'EventDefinitionListOut'>>(() => workspace.projectPath && `${workspace.projectPath}/event-definitions`)

const detail = computed(() => read.data.value)
const missing = computed(() => (read.error.value as ApiError | null)?.status === 404)
const error = computed(() => (missing.value ? null : read.error.value as ApiError | null))
const loading = computed(() => read.isFetching.value && !read.data.value)
const threshold = computed(() => detail.value?.suppression_threshold ?? 5)

const definition = computed(() => definitionsRead.data.value?.definitions.find(entry => entry.name === name.value) ?? null)
const category = computed(() => (definition.value?.category ?? (name.value.startsWith('session_') ? 'session' : 'custom')) as EventCategory)

const compact = (value: number) => writeFigure(value, { compact: true })

/** "4s ago", "3m ago", "2h ago", "3d ago". */
function ago(iso: string): string {
  const seconds = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 1_000))
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3_600) return `${Math.round(seconds / 60)}m ago`
  if (seconds < 172_800) return `${Math.round(seconds / 3_600)}h ago`
  return `${Math.round(seconds / 86_400)}d ago`
}

// --- Figures ------------------------------------------------------------------------------

/*
 * The API draws a quiet hour as a zero inside a period with occurrences. A period with none at
 * all has nothing to show: its figures are dashes and its chart the no-data mark, rather than a
 * flat line at zero. A change needs the period before to have had occurrences too.
 */
const any = computed(() => (detail.value?.totals.events ?? 0) > 0)
const anyBefore = computed(() => (detail.value?.totals.previous_events ?? 0) > 0)

const board = computed<Kpi[]>(() => {
  if (!detail.value) return []
  const { totals } = detail.value
  const players = totals.players ?? null
  const seen = any.value
  return [
    {
      key: 'count',
      label: 'Occurrences',
      value: seen ? compact(totals.events) : null,
      trend: seen && anyBefore.value ? trendOf(totals.events, totals.previous_events) : undefined,
      info: `Times the event arrived from ${workspace.environment} in the period.`
    },
    {
      key: 'players',
      label: 'Players',
      value: !seen ? null : players === null ? `Under ${threshold.value}` : compact(players),
      trend: !seen || players === null || totals.previous_players == null ? undefined : trendOf(players, totals.previous_players),
      info: 'Distinct players who sent it at least once. Approximate above a few thousand.'
    },
    {
      key: 'per-player',
      label: 'Per player',
      value: seen && players ? writeFigure(totals.events / players) : null,
      info: 'Occurrences divided by the players who sent it.'
    },
    {
      key: 'properties',
      label: 'Properties',
      value: seen ? String(detail.value.properties.length) : null,
      info: 'Distinct property keys seen on the event in the period.'
    },
    {
      key: 'last',
      label: 'Last seen',
      value: detail.value.last_seen ? ago(detail.value.last_seen) : null,
      info: 'When it last arrived. Events take up to about ten seconds to show.'
    }
  ]
})

// --- Over time ----------------------------------------------------------------------------

const hourTick = new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit', hour12: false })
const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

const categories = computed(() => (detail.value?.series ?? []).map(bucket =>
  (detail.value!.interval === 'hour' ? hourTick : dayTick).format(new Date(bucket.at))))

const series = computed<MetricSeries[]>(() => [
  { name: 'This period', data: (detail.value?.series ?? []).map(bucket => (any.value ? bucket.events : null)), type: 'area', tone: 1 },
  { name: 'Previous period', data: (detail.value?.series ?? []).map(bucket => (anyBefore.value ? bucket.previous_events : null)), dashed: true, tone: 'muted' }
])

// --- Where it comes from ------------------------------------------------------------------

const breakdown = (key: string) => (detail.value?.breakdowns[key] ?? []).map(slice => ({ value: slice.value || 'Unknown', count: slice.events }))

// --- Properties ---------------------------------------------------------------------------

type PropertyRow = Schema<'EventPropertyOut'>

const properties = computed<PropertyRow[]>(() => [...(detail.value?.properties ?? [])])

const PROPERTY_COLUMNS: DataTableColumn<PropertyRow>[] = [
  { key: 'key', title: 'Property', fixed: true, mono: true },
  { key: 'type', title: 'Type' },
  { key: 'fill', title: 'Present on', align: 'end', mono: true },
  { key: 'distinct', title: 'Distinct values', align: 'end', mono: true },
  { key: 'top', title: 'Top values', sortable: false }
]

const propertyColumns = ref(defaultColumnState(PROPERTY_COLUMNS))

// --- Recent occurrences -------------------------------------------------------------------

type Occurrence = Schema<'EventOccurrenceOut'>

const recent = computed<Occurrence[]>(() => [...(recentRead.data.value?.occurrences ?? [])])

const OCCURRENCE_COLUMNS: DataTableColumn<Occurrence>[] = [
  { key: 'at', title: 'Time', fixed: true, mono: true, value: occurrence => Date.parse(occurrence.at) },
  { key: 'player', title: 'Player', mono: true, value: occurrence => occurrence.player_id || occurrence.install_id },
  { key: 'platform', title: 'Platform' },
  { key: 'app_version', title: 'App version', mono: true },
  { key: 'country', title: 'Country' },
  { key: 'properties', title: 'Properties', mono: true, sortable: false }
]

const occurrenceColumns = ref(defaultColumnState(OCCURRENCE_COLUMNS))

const clock = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const shortId = (id: string) => (id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-5)}` : id)

// --- Describing it ------------------------------------------------------------------------

const describing = ref(false)
const draftCategory = ref<string>('custom')
const draftDescription = ref('')
const saving = ref(false)
const problem = ref('')

const CATEGORY_ITEMS = (Object.keys(EVENT_CATEGORY_LABELS) as EventCategory[]).map(value => ({ value, title: EVENT_CATEGORY_LABELS[value] }))

function describe() {
  draftCategory.value = category.value
  draftDescription.value = definition.value?.description ?? ''
  problem.value = ''
  describing.value = true
}

async function saveDefinition() {
  if (!workspace.projectPath) return
  saving.value = true
  problem.value = ''
  try {
    await apiRequest('PUT', `${workspace.projectPath}/event-definitions/${encodeURIComponent(name.value)}`, {
      category: draftCategory.value,
      description: draftDescription.value.trim()
    })
    await definitionsRead.execute()
    describing.value = false
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <LoadState
      v-if="!missing"
      :error="error"
      :loading="loading"
      what="the event"
      @retry="read.execute()"
    >
      <template v-if="detail">
        <PageHeader :description="definition?.description || 'Not described yet. Say what it means, so everyone reads it the same way.'" :title="name">
          <template #badge>
            <Tag :label="EVENT_CATEGORY_LABELS[category] ?? category" :tone="EVENT_CATEGORY_TONES[category] ?? 'neutral'" />
          </template>
          <template #actions>
            <v-btn
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:edit-outline"
              size="small"
              variant="text"
              @click="describe"
            >
              Describe
            </v-btn>
            <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
          </template>
        </PageHeader>

        <KpiBoard
          :description="period === '1d' ? 'Today so far, against yesterday.' : `The last ${periodWords}, against the ${periodWords} before.`"
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
            <BreakdownList :rows="breakdown('platform')" label="Occurrences by platform" />
          </SectionCard>
          <SectionCard description="Occurrences by the player's country." title="Country">
            <BreakdownList :rows="breakdown('country')" label="Occurrences by country" />
          </SectionCard>
          <SectionCard description="Occurrences by the build that sent them." title="App version">
            <BreakdownList :rows="breakdown('app_version')" label="Occurrences by app version" />
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
            :items="properties"
            :paged="false"
            empty-description="The event carried no properties in the period."
            empty-title="No properties."
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
                  v-for="value in item.top.slice(0, 4)"
                  :key="value.value"
                  class="inline-flex items-center gap-1 rounded-full bg-ink/5 px-2 py-0.5 font-mono text-[11px] text-ink/80"
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
            :items="recent"
            :paged="false"
            empty-description="None in the last few days."
            empty-title="Nothing recent."
            item-value="event_id"
            label="Recent occurrences"
          >
            <template #cell.at="{ item }">
              {{ clock.format(new Date(item.at)) }}
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
            <template #cell.country="{ item }">
              {{ item.country || '–' }}
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

    <SectionCard v-else :padded="false" title="Events">
      <EmptyState
        :description="`Nothing from this project's ${workspace.environment} environment has been called “${name}”.`"
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

    <v-dialog v-model="describing" max-width="480">
      <v-card border flat>
        <form class="flex flex-col gap-5 p-5" @submit.prevent="saveDefinition">
          <header class="flex flex-col gap-1">
            <h2 class="m-0 font-mono text-sm font-medium text-ink">
              {{ name }}
            </h2>
            <p class="m-0 text-xs leading-5 text-ink/60">
              What the event means, in the studio's words. Shown wherever it is listed.
            </p>
          </header>
          <div class="flex flex-col gap-1">
            <label class="text-xs font-medium text-ink" for="event-category">Category</label>
            <v-select
              id="event-category"
              v-model="draftCategory"
              :items="CATEGORY_ITEMS"
              hide-details
              variant="filled"
            />
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-xs font-medium text-ink" for="event-description">Description</label>
            <v-textarea
              id="event-description"
              v-model="draftDescription"
              auto-grow
              hide-details
              maxlength="500"
              rows="3"
              variant="filled"
            />
          </div>
          <p v-if="problem" class="m-0 text-xs text-error">
            {{ problem }}
          </p>
          <footer class="flex justify-end gap-2">
            <v-btn
              class="text-xs font-medium"
              color="neutral"
              size="small"
              variant="text"
              @click="describing = false"
            >
              Cancel
            </v-btn>
            <v-btn
              :loading="saving"
              class="text-xs font-medium"
              color="primary"
              size="small"
              type="submit"
              variant="flat"
            >
              Save
            </v-btn>
          </footer>
        </form>
      </v-card>
    </v-dialog>
  </div>
</template>
