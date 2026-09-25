<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'
import type { OverviewDay } from '~/types'

// The organization at a glance: how its games are playing and earning over a period, how
// each project contributes, and what the month has used against the plan. One environment,
// the one chosen in the sidebar: production is the business, staging and development are not.
//
// The days are each project's metrics/query added together; the projects table is the
// organization's analytics/{env}/projects; usage is …/usage and the wallet …/billing.
//
// A day no project answered for (nothing sent, or too few players to show) is null, not zero:
// the charts leave it out, the figures are taken over the days that have rows, and a period
// with no rows at all shows no figure and no chart rather than a row of zeros.

const workspace = useWorkspaceStore()

const PERIODS: SegmentedOption[] = [
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' }
]

const period = ref('30')
const range = computed(() => periodOf(Number(period.value)))

const compare = computed(() => `vs previous ${period.value} days`)

// --- The days, every project together -----------------------------------------------------

const days = ref<{ current: (OverviewDay | null)[], previous: (OverviewDay | null)[] } | null>(null)
const daysLoading = ref(false)
const daysError = ref<ApiError | null>(null)
/** Days some project had too few players to show: left out of the sums. */
const withheld = ref(0)

async function loadDays() {
  const org = workspace.orgPath
  if (!org) return
  const { previousFrom, to, days: current, previousDays } = range.value
  daysLoading.value = true
  daysError.value = null
  const answers = await Promise.allSettled(workspace.projects.map(project =>
    dailyMetrics(`${org}/projects/${project.slug}/analytics/${workspace.environment}`, previousFrom, to)))
  const read = answers.flatMap(answer => (answer.status === 'fulfilled' ? [answer.value] : []))
  const failed = answers.find(answer => answer.status === 'rejected') as PromiseRejectedResult | undefined
  if (!read.length && failed) {
    daysError.value = failed.reason instanceof ApiError ? failed.reason : new ApiError(String(failed.reason), 0)
  }
  withheld.value = read.reduce((sum, answer) => sum + answer.suppressed_cells, 0)
  const merged = byDay(read.flatMap(answer => answer.rows))
  const dayOf = (day: string): OverviewDay | null => {
    const row = merged.get(day)
    if (!row) return null
    return {
      day,
      active_players: row.active_players ?? 0,
      new_players: row.new_players ?? 0,
      returning_players: row.returning_players ?? 0,
      sessions: row.sessions ?? 0,
      playtime_ms: row.playtime_ms ?? 0,
      revenue_micros: row.revenue_micros ?? 0,
      purchases: row.purchases ?? 0,
      payers: row.payers ?? 0
    }
  }
  days.value = { current: current.map(dayOf), previous: previousDays.map(dayOf) }
  daysLoading.value = false
}

watch([range, () => workspace.environment, () => workspace.projects.map(project => project.slug).join(','), () => workspace.orgPath], loadDays, { immediate: true })

// --- The projects table, and usage -----------------------------------------------------------

const overview = useApiGet<Schema<'ProjectsOverviewOut'>>(() => workspace.orgPath
  && withQuery(`${workspace.orgPath}/analytics/${workspace.environment}/projects`, { from: range.value.from, to: range.value.to }))
const usageRead = useApiGet<Schema<'UsageOut'>>(() => workspace.orgPath && `${workspace.orgPath}/usage`)
const billingRead = useApiGet<Schema<'BillingOut'>>(() => workspace.orgPath && `${workspace.orgPath}/billing`)

// --- Headline figures -------------------------------------------------------------------

type DayMetric = keyof Omit<OverviewDay, 'day'>

const total = (days: OverviewDay[], metric: DayMetric) => days.reduce((sum, day) => sum + day[metric], 0)
const average = (days: OverviewDay[], metric: DayMetric) => (days.length ? total(days, metric) / days.length : 0)

/** Each day of the period, null where no row came back. */
const now = computed(() => days.value?.current ?? range.value.days.map(() => null))
const before = computed(() => days.value?.previous ?? range.value.previousDays.map(() => null))
/** The days that have rows: what the figures are taken over. */
const present = (list: readonly (OverviewDay | null)[]) => list.filter((day): day is OverviewDay => day !== null)
const nowDays = computed(() => present(now.value))
const beforeDays = computed(() => present(before.value))

// Micros to dollars, for every money figure on the page.
const dollars = (micros: number) => micros / 1_000_000

// --- Charts ------------------------------------------------------------------------------

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const dayTitle = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })

const categories = computed(() => range.value.days.map(day => dayTick.format(new Date(day))))
const titles = computed(() => range.value.days.map(day => dayTitle.format(new Date(day))))

const PLAYER_METRICS: MetricOption[] = [
  { value: 'active_players', label: 'Active players', description: 'Players with at least one session that day.' },
  { value: 'new_players', label: 'New players', description: 'Players seen for the first time that day.' },
  { value: 'returning_players', label: 'Returning players', description: 'Active players who had played before.' },
  { value: 'sessions', label: 'Sessions', description: 'Sessions started, every platform together.' },
  { value: 'playtime', label: 'Playtime', description: 'Hours played across every player, per day.' }
]

const playerMetric = ref('active_players')

// Playtime is stored in milliseconds and read in hours.
function playerValue(day: OverviewDay, metric: string): number {
  if (metric === 'playtime') return Math.round(day.playtime_ms / 3_600_000)
  return day[metric as DayMetric]
}

const playerSeries = computed<MetricSeries[]>(() => [
  { name: 'This period', data: now.value.map(day => (day ? playerValue(day, playerMetric.value) : null)), type: 'area', tone: 1 },
  { name: 'Previous period', data: before.value.map(day => (day ? playerValue(day, playerMetric.value) : null)), dashed: true, tone: 'muted' }
])

const playerHeadline = computed(() => {
  const series = nowDays.value.map(day => playerValue(day, playerMetric.value))
  const previous = beforeDays.value.map(day => playerValue(day, playerMetric.value))
  // Players are read as a daily average; sessions and playtime as the period's total.
  const daily = ['active_players', 'returning_players'].includes(playerMetric.value)
  const reduce = (values: number[]) => (values.length
    ? Math.round(values.reduce((a, b) => a + b, 0) / (daily ? values.length : 1))
    : null)
  return {
    value: reduce(series),
    previous: reduce(previous),
    unit: playerMetric.value === 'playtime' ? 'h' : ''
  }
})

const revenueSeries = computed<MetricSeries[]>(() => [
  { name: 'Revenue', data: now.value.map(day => (day ? dollars(day.revenue_micros) : null)), type: 'area', tone: 3 },
  { name: 'Previous period', data: before.value.map(day => (day ? dollars(day.revenue_micros) : null)), dashed: true, tone: 'muted' }
])

/** The period's revenue, or null when no day has a row. */
const revenueOf = (list: OverviewDay[]) => (list.length ? dollars(total(list, 'revenue_micros')) : null)

// --- Engagement and monetization -------------------------------------------------------

const money = (value: number) => `$${writeFigure(value, { format: 'money' })}`

type Figures = Record<'active' | 'fresh' | 'sessions' | 'playtime' | 'session' | 'revenue' | 'payers' | 'conversion' | 'arpdau' | 'arppu', number | null>

// Each headline figure over the days that have rows. Null when none has, and a ratio is null
// when what it divides by is nothing (no players, no sessions, no payers).
function figuresOf(days: OverviewDay[]): Figures {
  const active = total(days, 'active_players')
  const sessions = total(days, 'sessions')
  const playtime = total(days, 'playtime_ms')
  const revenue = dollars(total(days, 'revenue_micros'))
  const payers = total(days, 'payers')
  const per = (value: number, over: number) => (over ? value / over : null)
  if (!days.length) {
    return { active: null, fresh: null, sessions: null, playtime: null, session: null, revenue: null, payers: null, conversion: null, arpdau: null, arppu: null }
  }
  const minutes = (value: number | null) => (value === null ? null : value / 60_000)
  const percent = (value: number | null) => (value === null ? null : value * 100)
  return {
    active: average(days, 'active_players'),
    fresh: total(days, 'new_players'),
    sessions: per(sessions, active),
    playtime: minutes(per(playtime, active)),
    session: minutes(per(playtime, sessions)),
    revenue,
    payers: average(days, 'payers'),
    conversion: percent(per(payers, active)),
    arpdau: per(revenue, active),
    arppu: per(revenue, payers)
  }
}

const figures = computed(() => ({ now: figuresOf(nowDays.value), before: figuresOf(beforeDays.value) }))

/* A figure is written only when there is one; a change only when both periods have one. */
function kpi(key: keyof Figures, fields: Omit<Kpi, 'key' | 'trend' | 'value' | 'exact'> & { write: (value: number) => string, exact?: (value: number) => string }): Kpi {
  const value = figures.value.now[key]
  const previous = figures.value.before[key]
  const { write, exact, ...rest } = fields
  return {
    key,
    value: value === null ? null : write(value),
    exact: value === null || !exact ? undefined : exact(value),
    trend: value === null || previous === null ? undefined : trendOf(value, previous),
    ...rest
  }
}

const compact = (value: number) => writeFigure(value, { compact: true })

const audience = computed<Kpi[]>(() => [
  kpi('active', {
    label: 'Active players',
    write: compact,
    info: 'Players with at least one session, as a daily average over the period.'
  }),
  kpi('fresh', {
    label: 'New players',
    write: compact,
    info: 'Players seen for the first time during the period.'
  }),
  kpi('sessions', {
    label: 'Sessions per player',
    write: value => writeFigure(value),
    info: 'Sessions divided by active players, per day.'
  }),
  kpi('playtime', {
    label: 'Playtime per player',
    write: value => writeFigure(value, { decimals: 0 }),
    unit: 'min',
    info: 'Minutes played per active player, per day.'
  }),
  kpi('session', {
    label: 'Average session',
    write: value => writeFigure(value),
    unit: 'min',
    info: 'Minutes played per session.'
  })
])

const monetization = computed<Kpi[]>(() => [
  kpi('revenue', {
    label: 'Revenue',
    write: value => `$${compact(value)}`,
    exact: money,
    info: 'Purchases per day as counted for analytics, gross, before store fees and refunds. The Revenue page has them verified and in USD.'
  }),
  kpi('payers', {
    label: 'Paying players',
    write: compact,
    info: 'Players with a verified purchase, as a daily average over the period.'
  }),
  kpi('conversion', {
    label: 'Payer conversion',
    write: value => writeFigure(value, { decimals: 2, unit: '%' }),
    info: 'The share of each day\'s active players who made a verified purchase.'
  }),
  kpi('arpdau', {
    label: 'ARPDAU',
    write: money,
    info: 'Average revenue per daily active player: revenue divided by active players, summed by day.'
  }),
  kpi('arppu', {
    label: 'ARPPU',
    write: money,
    info: 'Average revenue per paying player, per day they paid.'
  })
])

// --- Projects ----------------------------------------------------------------------------

interface ProjectRow {
  slug: string
  name: string
  /** Daily averages over the period; null when too few players to show. */
  active_players: number | null
  previous_active_players: number | null
  new_players: number
  sessions_per_player: number | null
  playtime_per_player_ms: number | null
  conversion: number | null
  arpdau: number | null
  revenue_micros: number
  previous_revenue_micros: number
  events: number
}

const projects = computed<ProjectRow[]>(() => (overview.data.value?.projects ?? []).map((row) => {
  const count = Number(period.value)
  const playerDays = row.player_days ?? null
  const per = (value: number) => (playerDays ? value / playerDays : null)
  return {
    slug: row.slug,
    name: row.name,
    active_players: playerDays === null ? null : playerDays / count,
    previous_active_players: row.previous_player_days == null ? null : row.previous_player_days / count,
    new_players: row.new_players,
    sessions_per_player: per(row.sessions),
    playtime_per_player_ms: per(row.playtime_ms),
    conversion: per(row.payer_days),
    arpdau: playerDays ? dollars(row.revenue_micros) / playerDays : null,
    revenue_micros: row.revenue_micros,
    previous_revenue_micros: row.previous_revenue_micros,
    events: row.events
  }
}))

// Each game in full: who plays it, how, and what it earns. Players and revenue carry their
// change against the previous period beside them; the ratios are per player, per day.
const PROJECT_COLUMNS: DataTableColumn<ProjectRow>[] = [
  { key: 'name', title: 'Project', fixed: true },
  { key: 'active_players', title: 'Active players', align: 'end', mono: true, value: row => row.active_players ?? -1 },
  { key: 'new_players', title: 'New players', align: 'end', mono: true },
  { key: 'sessions_per_player', title: 'Sessions / player', align: 'end', mono: true, value: row => row.sessions_per_player ?? -1 },
  { key: 'playtime_per_player_ms', title: 'Playtime / player', align: 'end', mono: true, value: row => row.playtime_per_player_ms ?? -1 },
  { key: 'conversion', title: 'Payer conversion', align: 'end', mono: true, value: row => row.conversion ?? -1 },
  { key: 'arpdau', title: 'ARPDAU', align: 'end', mono: true, value: row => row.arpdau ?? -1 },
  { key: 'revenue_micros', title: 'Revenue', align: 'end', mono: true },
  { key: 'events', title: 'Events', align: 'end', mono: true }
]

const projectColumns = ref(defaultColumnState(PROJECT_COLUMNS))
const threshold = computed(() => overview.data.value?.suppression_threshold ?? 5)

// --- Usage ------------------------------------------------------------------------------

/* Binary, as writeFigure writes bytes. */
const GB = 1024 ** 3

const month = computed(() => {
  const start = (billingRead.data.value?.month as { period_start?: string } | undefined)?.period_start ?? usageRead.data.value?.from
  return start ? new Intl.DateTimeFormat('en', { month: 'long', timeZone: 'UTC' }).format(new Date(start)) : 'This month'
})

const usage = computed(() => {
  const read = usageRead.data.value
  const billing = billingRead.data.value
  const monthly = billing?.month as { tracked_players: number, included_players: number, charged_micros: number, pending_micros: number } | undefined
  const included = read?.included ?? {}
  return {
    tracked_players: monthly?.tracked_players ?? 0,
    events: read?.totals.events ?? 0,
    retained_bytes: read?.totals.retained_bytes ?? 0,
    included: {
      tracked_players: monthly?.included_players ?? included.included_mau ?? 0,
      events: included.included_events ?? 0,
      retained_bytes: (included.included_storage_gb_month ?? 0) * GB
    },
    charged_micros: monthly?.charged_micros ?? 0,
    pending_micros: monthly?.pending_micros ?? billing?.live.pending_micros ?? 0,
    available_micros: billing?.live.available_micros ?? 0
  }
})

/* Money the API has not answered for is a dash, not $0.00. */
const walletMoney = (micros: number) => (billingRead.data.value ? money(dollars(micros)) : '–')

const wallet = computed<Fact[]>(() => [
  {
    key: 'charged',
    label: `Charged in ${month.value}`,
    value: walletMoney(usage.value.charged_micros),
    mono: true,
    info: 'Usage beyond the plan\'s allowance, taken from the wallet so far this month.'
  },
  {
    key: 'pending',
    label: 'Pending',
    value: walletMoney(usage.value.pending_micros),
    mono: true,
    info: 'Usage counted and not yet taken from the wallet: minutes old at most.'
  },
  {
    key: 'available',
    label: 'Available credit',
    value: walletMoney(usage.value.available_micros),
    mono: true,
    to: '/settings/billing'
  }
])
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Overview">
      <template #description>
        How <span class="text-ink">{{ workspace.organization?.name }}</span>'s games are playing and earning:
        {{ workspace.environment }}, every project.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <LoadState
      :error="daysError"
      :loading="daysLoading && !days"
      what="the overview"
      @retry="loadDays"
    >
      <p v-if="!workspace.projects.length" class="m-0 rounded-(--ps-radius-large) bg-ink/4 px-4 py-3 text-xs leading-5 text-ink/70">
        No projects yet. Make one from the project menu, send events with its SDK key, and they show here.
      </p>
      <p v-else-if="withheld" class="m-0 flex items-start gap-2 rounded-(--ps-radius-large) bg-ink/4 px-4 py-3 text-xs leading-5 text-ink/70">
        <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-ink/45" name="material-symbols:visibility-off-outline" />
        Days with fewer than {{ threshold }} players in a project are left out, so no one player can be picked out: {{ withheld }} {{ withheld === 1 ? 'day was' : 'days were' }} held back.
      </p>

      <!-- Headline: who played and how, then what it earned. -->
      <KpiBoard :description="`Over the last ${period} days, against the ${period} before.`" :items="audience" title="Audience" />
      <KpiBoard :description="`Over the last ${period} days, against the ${period} before.`" :items="monetization" title="Monetization" />

      <!-- Trends, one full-width row each: a period of days reads better wide than halved. -->
      <div class="flex flex-col gap-4">
        <MetricCard
          v-model="playerMetric"
          :categories="categories"
          :compare="compare"
          :options="PLAYER_METRICS"
          :previous="playerHeadline.previous"
          :series="playerSeries"
          :titles="titles"
          :unit="playerHeadline.unit"
          :value="playerHeadline.value"
          category-label="Day"
          compact
        />
        <MetricCard
          :categories="categories"
          :compare="compare"
          :previous="revenueOf(beforeDays)"
          :series="revenueSeries"
          :titles="titles"
          :value="revenueOf(nowDays)"
          category-label="Day"
          compact
          description="Purchases per day, gross, against the same days last period."
          prefix="$"
          title="Revenue"
        />
      </div>

      <!-- Projects -->
      <SectionCard :padded="false" description="Each game over the period: who plays it, how, and what it earns." title="Projects">
        <DataTable
          v-model:column-state="projectColumns"
          :columns="PROJECT_COLUMNS"
          :items="projects"
          :loading="overview.isFetching.value && !overview.data.value"
          :paged="false"
          empty-description="Nothing was sent to any project in this environment over the period."
          empty-title="No activity yet."
          item-value="slug"
          label="Projects"
        >
          <template #cell.name="{ item }">
            <span class="flex min-w-0 items-center gap-2.5">
              <span
                aria-hidden="true"
                class="
                flex size-6 flex-none items-center justify-center rounded-md bg-ink/6
                text-[10px] text-ink/70
              "
              >
                {{ item.name.charAt(0) }}
              </span>
              <span class="truncate text-ink">{{ item.name }}</span>
            </span>
          </template>
          <template #cell.active_players="{ item }">
            <ProjectFigure
              v-if="item.active_players !== null"
              :previous="item.previous_active_players"
              :value="item.active_players"
              :written="writeFigure(item.active_players, { compact: true })"
            />
            <span v-else :title="`Fewer than ${threshold} players`" class="text-ink/40">–</span>
          </template>
          <template #cell.new_players="{ item }">
            {{ writeFigure(item.new_players, { compact: true }) }}
          </template>
          <template #cell.sessions_per_player="{ item }">
            {{ item.sessions_per_player === null ? '–' : writeFigure(item.sessions_per_player) }}
          </template>
          <template #cell.playtime_per_player_ms="{ item }">
            {{ item.playtime_per_player_ms === null ? '–' : `${writeFigure(item.playtime_per_player_ms / 60_000, { decimals: 0 })} min` }}
          </template>
          <template #cell.conversion="{ item }">
            {{ item.conversion === null ? '–' : writeFigure(item.conversion * 100, { decimals: 2, unit: '%' }) }}
          </template>
          <template #cell.arpdau="{ item }">
            {{ item.arpdau === null ? '–' : money(item.arpdau) }}
          </template>
          <template #cell.revenue_micros="{ item }">
            <ProjectFigure
              :previous="item.previous_revenue_micros"
              :value="item.revenue_micros"
              :written="money(dollars(item.revenue_micros))"
            />
          </template>
          <template #cell.events="{ item }">
            {{ writeFigure(item.events, { compact: true }) }}
          </template>
        </DataTable>
      </SectionCard>

      <!-- Usage -->
      <SectionCard
        :description="`${month} so far, across every project. Usage past the allowance is taken from the wallet.`"
        title="Usage this month"
      >
        <template #actions>
          <NuxtLink
            class="
            inline-flex items-center gap-0.5 text-xs text-ink/55 no-underline transition-colors
            hover:text-ink
          "
            to="/settings/billing"
          >
            Billing
            <Icon aria-hidden="true" class="text-sm" name="material-symbols:chevron-right" />
          </NuxtLink>
        </template>

        <div class="flex flex-col gap-6">
          <div class="grid gap-6 md:grid-cols-3">
            <UsageMeter
              :included="usage.included.tracked_players"
              :used="usage.tracked_players"
              info="Players with qualifying activity this month, counted once however many days they played."
              label="Tracked players"
            />
            <UsageMeter
              :included="usage.included.events"
              :used="usage.events"
              info="Events accepted by ingestion this month, every project and environment."
              label="Events"
            />
            <UsageMeter
              :included="usage.included.retained_bytes"
              :used="usage.retained_bytes"
              format="bytes"
              info="Raw events kept for replay and export, inside the retention window today."
              label="Retained storage"
            />
          </div>

          <FactGrid :columns="3" :facts="wallet" label="Wallet this month" />
        </div>
      </SectionCard>
    </LoadState>
  </div>
</template>
