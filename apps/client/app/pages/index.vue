<script setup lang="ts">
import type { OverviewDay } from '~/types'

// The organization at a glance: how its games are playing and earning over a period, how
// each project contributes, where the players are, and what the month has used against
// the plan. Production only — development and staging traffic is not the business.
//
// Sample data until the API client is wired in (app/utils/overview-sample.ts names the
// endpoints each part comes from).

// Placeholder until control-api's /organizations list is wired in (§55).
const organization = 'Acme Games'

const PERIODS: SegmentedOption[] = [
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' }
]

const period = ref('30')
const data = computed(() => sampleOverview(Number(period.value) as 7 | 30 | 90))

const compare = computed(() => `vs previous ${period.value} days`)

// --- Headline figures -------------------------------------------------------------------

type DayMetric = keyof Omit<OverviewDay, 'day'>

const total = (days: OverviewDay[], metric: DayMetric) => days.reduce((sum, day) => sum + day[metric], 0)
const average = (days: OverviewDay[], metric: DayMetric) => (days.length ? total(days, metric) / days.length : 0)

const now = computed(() => data.value.current)
const before = computed(() => data.value.previous)

// Micros to dollars, for every money figure on the page.
const dollars = (micros: number) => micros / 1_000_000

// --- Charts ------------------------------------------------------------------------------

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const dayTitle = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })

const categories = computed(() => now.value.map(day => dayTick.format(new Date(day.day))))
const titles = computed(() => now.value.map(day => dayTitle.format(new Date(day.day))))

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
  { name: 'This period', data: now.value.map(day => playerValue(day, playerMetric.value)), type: 'area', tone: 1 },
  { name: 'Previous period', data: before.value.map(day => playerValue(day, playerMetric.value)), dashed: true, tone: 'muted' }
])

const playerHeadline = computed(() => {
  const series = now.value.map(day => playerValue(day, playerMetric.value))
  const previous = before.value.map(day => playerValue(day, playerMetric.value))
  // Players are read as a daily average; sessions and playtime as the period's total.
  const daily = ['active_players', 'returning_players'].includes(playerMetric.value)
  const reduce = (values: number[]) => values.reduce((a, b) => a + b, 0) / (daily ? values.length || 1 : 1)
  return {
    value: Math.round(reduce(series)),
    previous: Math.round(reduce(previous)),
    unit: playerMetric.value === 'playtime' ? 'h' : ''
  }
})

const revenueSeries = computed<MetricSeries[]>(() => [
  { name: 'Revenue', data: now.value.map(day => dollars(day.revenue_micros)), type: 'bar', tone: 3 },
  { name: 'Previous period', data: before.value.map(day => dollars(day.revenue_micros)), type: 'line', dashed: true, tone: 'muted' }
])

// --- Engagement and monetization -------------------------------------------------------

const money = (value: number) => `$${writeFigure(value, { format: 'money' })}`
const count = (value: number, decimals = 1) => writeFigure(value, { decimals })

const facts = computed<Fact[]>(() => {
  const days = now.value
  const activeDays = total(days, 'active_players')
  const revenue = dollars(total(days, 'revenue_micros'))
  const payerDays = total(days, 'payers')
  return [
    {
      key: 'sessions-per-player',
      label: 'Sessions per player',
      value: count(total(days, 'sessions') / activeDays),
      mono: true,
      info: 'Sessions divided by active players, per day.'
    },
    {
      key: 'playtime-per-player',
      label: 'Playtime per player',
      value: `${count(total(days, 'playtime_ms') / activeDays / 60_000, 0)} min`,
      mono: true,
      info: 'Minutes played per active player, per day.'
    },
    {
      key: 'session-length',
      label: 'Average session',
      value: `${count(total(days, 'playtime_ms') / total(days, 'sessions') / 60_000)} min`,
      mono: true
    },
    {
      key: 'arpdau',
      label: 'ARPDAU',
      value: money(revenue / activeDays),
      mono: true,
      info: 'Average revenue per daily active player: revenue divided by active players, summed by day.'
    },
    {
      key: 'arppu',
      label: 'ARPPU',
      value: money(revenue / payerDays),
      mono: true,
      info: 'Average revenue per paying player, per day they paid.'
    },
    {
      key: 'conversion',
      label: 'Payer conversion',
      value: `${count(payerDays / activeDays * 100, 2)}%`,
      mono: true,
      info: 'The share of each day\'s active players who made a verified purchase.'
    }
  ]
})

// --- Where players are -------------------------------------------------------------------

const BREAKDOWNS: SegmentedOption[] = [
  { value: 'platform', label: 'Platform' },
  { value: 'country', label: 'Country' }
]

const breakdown = ref('platform')

const breakdownRows = computed(() =>
  (breakdown.value === 'platform' ? data.value.by_platform : data.value.by_country)
    .map(row => ({ value: row.value, figure: row.active_players })))

// --- Projects ----------------------------------------------------------------------------

type ProjectRow = typeof data.value.projects[number]

const PROJECT_COLUMNS: DataTableColumn<ProjectRow>[] = [
  { key: 'name', title: 'Project', fixed: true },
  { key: 'active_players', title: 'Active players', align: 'end', mono: true },
  { key: 'new_players', title: 'New players', align: 'end', mono: true },
  { key: 'revenue_micros', title: 'Revenue', align: 'end', mono: true },
  { key: 'change', title: 'Change', align: 'end', mono: true, value: row => relativeChange(row.revenue_micros, row.previous_revenue_micros) ?? 0 },
  { key: 'events', title: 'Events', align: 'end', mono: true }
]

const projectColumns = ref(defaultColumnState(PROJECT_COLUMNS))

// --- Usage ------------------------------------------------------------------------------

const usage = computed(() => data.value.usage)

const month = computed(() =>
  new Intl.DateTimeFormat('en', { month: 'long', timeZone: 'UTC' }).format(new Date(usage.value.period_start)))

const wallet = computed<Fact[]>(() => [
  {
    key: 'charged',
    label: `Charged in ${month.value}`,
    value: money(dollars(usage.value.charged_micros)),
    mono: true,
    info: 'Usage beyond the plan\'s allowance, taken from the wallet so far this month.'
  },
  {
    key: 'pending',
    label: 'Pending',
    value: money(dollars(usage.value.pending_micros)),
    mono: true,
    info: 'Usage counted and not yet taken from the wallet — minutes old at most.'
  },
  {
    key: 'available',
    label: 'Available credit',
    value: money(dollars(usage.value.available_micros)),
    mono: true,
    to: '/settings/billing'
  }
])
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Overview">
      <template #description>
        How <span class="text-ink">{{ organization }}</span>'s games are playing and earning —
        production, every project.
      </template>
      <template #actions>
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </PageHeader>

    <!-- Headline -->
    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <Statistic
        :compare="compare"
        :previous="Math.round(average(before, 'active_players'))"
        :value="Math.round(average(now, 'active_players'))"
        compact
        description="Daily average"
        info="Players with at least one session, averaged over the days in the period."
        label="Active players"
      />
      <Statistic
        :compare="compare"
        :previous="total(before, 'new_players')"
        :value="total(now, 'new_players')"
        compact
        description="First seen in the period"
        label="New players"
      />
      <Statistic
        :compare="compare"
        :previous="dollars(total(before, 'revenue_micros'))"
        :value="dollars(total(now, 'revenue_micros'))"
        description="Verified purchases, gross"
        format="money"
        info="Store receipts verified server-side, before platform fees and refunds."
        label="Revenue"
        prefix="$"
      />
      <Statistic
        :compare="compare"
        :previous="Math.round(average(before, 'payers'))"
        :value="Math.round(average(now, 'payers'))"
        compact
        description="Daily average"
        label="Paying players"
      />
    </div>

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
        :previous="dollars(total(before, 'revenue_micros'))"
        :series="revenueSeries"
        :titles="titles"
        :value="dollars(total(now, 'revenue_micros'))"
        category-label="Day"
        compact
        description="Verified purchases per day, against the same days last period."
        prefix="$"
        title="Revenue"
        type="bar"
      />
    </div>

    <!-- Engagement and monetization -->
    <PageSection
      :divided="false"
      description="Per player and per day, over the period."
      title="Engagement and monetization"
    >
      <FactGrid :columns="6" :facts="facts" label="Engagement and monetization" />
    </PageSection>

    <!-- Projects, and where the players are -->
    <div class="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
      <LayerCard :padded="false" description="Each game's share of the period." title="Projects">
        <DataTable
          v-model:column-state="projectColumns"
          :columns="PROJECT_COLUMNS"
          :items="data.projects"
          :paged="false"
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
            {{ writeFigure(item.active_players, { compact: true }) }}
          </template>
          <template #cell.new_players="{ item }">
            {{ writeFigure(item.new_players, { compact: true }) }}
          </template>
          <template #cell.revenue_micros="{ item }">
            {{ money(dollars(item.revenue_micros)) }}
          </template>
          <template #cell.change="{ item }">
            <span
              :class="item.revenue_micros >= item.previous_revenue_micros ? 'text-success' : 'text-error'"
            >
              {{ formatChange(relativeChange(item.revenue_micros, item.previous_revenue_micros) ?? 0) }}
            </span>
          </template>
          <template #cell.events="{ item }">
            {{ writeFigure(item.events, { compact: true }) }}
          </template>
        </DataTable>
      </LayerCard>

      <LayerCard description="Active players, daily average." title="Where players are">
        <template #actions>
          <SegmentedControl v-model="breakdown" :options="BREAKDOWNS" label="Break down by" />
        </template>
        <ShareBars
          :label="`Active players by ${breakdown}`"
          :rows="breakdownRows"
        />
      </LayerCard>
    </div>

    <!-- Usage -->
    <LayerCard
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
            info="Raw events kept for replay and export, averaged over the month."
            label="Retained storage"
          />
        </div>

        <FactGrid :columns="3" :facts="wallet" label="Wallet this month" />
      </div>
    </LayerCard>
  </div>
</template>
