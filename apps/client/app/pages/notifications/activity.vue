<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// What was sent to the project's channels, laid out for the questions people come with: is
// anything stuck or failing (the warning, before any figure), is delivery healthy (the figures
// against the period before, and the chart), where it goes (each channel, with its own trend),
// who sends it (each rule and report), and what exactly went out (the log).
//
// The figures and charts are read from an hourly rollup of the delivery log, so the page costs
// the same at ten notifications a day and at ten thousand. Tests are counted apart: they are real
// sends, but "how many did we send" does not mean the ones somebody sent by hand.
const range = useQueryChoice<ActivityRange>('range', ['24h', '7d', '30d'], '7d')
const RANGES: SegmentedOption[] = [
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' }
]
const rangeWords = computed(() => ({ '24h': 'the last 24 hours', '7d': 'the last 7 days', '30d': 'the last 30 days' })[range.value])

const { data, loading: figuresLoading, error: figuresError, reload: reloadFigures } = useChannelActivity(range)
const { deliveries, loading, error, reload: reloadLog, channelById } = useChannels()

async function reload() {
  await Promise.all([reloadFigures(), reloadLog()])
}

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

// --- Figures ------------------------------------------------------------------------------

const share = (part: number, whole: number) => (whole ? part / whole : 0)
const percent = (value: number) => `${(value * 100).toFixed(value > 0.995 || value < 0.005 ? 2 : 1)}%`

/*
 * A dash is a figure with nothing behind it; a zero is what happened. Nothing sent is a real
 * answer, but a delivered share or an answer time of nothing sent is not a figure at all.
 */
const board = computed<Kpi[]>(() => {
  const d = data.value
  if (!d) return []
  const { current, previous } = d
  const sent = current.sent > 0
  const answered = current.sent + current.tests > 0
  const move = (value: number, before: number, positive: 'up' | 'down' = 'up') => (before > 0 ? trendOf(value, before, positive) : undefined)
  return [
    { key: 'sent', label: 'Sent', value: writeFigure(current.sent, { compact: true }), exact: writeFigure(current.sent), trend: move(current.sent, previous.sent), info: `Notifications that rules and reports sent in ${rangeWords.value}, one a channel. ${current.tests ? `${writeFigure(current.tests)} tests besides, not counted here.` : 'Tests are not counted here.'}` },
    { key: 'delivered', label: 'Delivered', value: sent ? percent(share(current.delivered, current.sent)) : null, trend: sent && previous.sent ? move(share(current.delivered, current.sent), share(previous.delivered, previous.sent)) : undefined, info: 'Of what was sent, how much the destination accepted: Slack, the mail server, PagerDuty or your webhook answered with success.' },
    { key: 'failed', label: 'Failed', value: writeFigure(current.failed), trend: move(current.failed, previous.failed, 'down'), info: 'Sends a destination refused or never answered. A rule\'s send is tried again for about four hours before it counts here.' },
    { key: 'p50', label: 'Median answer', value: answered ? msWords(current.p50) : null, trend: answered && previous.p50 ? move(current.p50, previous.p50, 'down') : undefined, info: `Half of the sends were answered faster than this. One in twenty took ${msWords(current.p95)} or more. Tests included: their answer times are real.` },
    { key: 'waiting', label: 'Waiting now', value: writeFigure(d.waiting + d.retrying), info: `Queued and not sent yet: ${writeFigure(d.waiting)} on their first try, ${writeFigure(d.retrying)} failed once and due again.` }
  ]
})

// --- Chart --------------------------------------------------------------------------------

const categories = computed(() => (data.value?.buckets ?? []).map(b => data.value!.bucket === 'hour'
  ? new Date(b.at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
  : new Date(b.at).toLocaleDateString('en', { month: 'short', day: 'numeric' })))
const titles = computed(() => (data.value?.buckets ?? []).map(b => data.value!.bucket === 'hour'
  ? new Date(b.at).toLocaleString('en', { weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false })
  : new Date(b.at).toLocaleDateString('en', { weekday: 'short', month: 'short', day: 'numeric' })))

const series = computed<MetricSeries[]>(() => {
  const buckets = data.value?.buckets ?? []
  return [
    { name: 'Delivered', data: buckets.map(b => b.delivered), stack: 'sends', tone: 3 },
    { name: 'Failed', data: buckets.map(b => b.failed), stack: 'sends', tone: 2 },
    { name: 'Tests', data: buckets.map(b => b.tests), stack: 'sends', tone: 'muted' }
  ]
})

// --- Where and who ------------------------------------------------------------------------

const channels = computed(() => data.value?.channels ?? [])
const sources = computed(() => (data.value?.sources ?? []).map(s => ({ value: s.source, count: s.sent, failed: s.failed })))
const failingSources = computed(() => sources.value.filter(s => s.failed > 0))

const failureShare = (row: { sent: number, failed: number }) => share(row.failed, row.sent)
const failureTone = (row: { sent: number, failed: number }) => {
  const rate = failureShare(row)
  // A stray failure is not news; one in fifty is worth a look, one in ten is broken.
  return rate >= 0.1 ? 'text-error' : rate >= 0.02 ? 'text-warning' : 'text-ink/45'
}

/* Something to look at first: sends stuck in the queue, or a channel whose latest send failed. */
const attention = computed(() => {
  const d = data.value
  if (!d) return null
  const broken = d.channels.filter(c => c.lastAt && !c.lastOk && c.sent)
  if (d.retrying) return `${writeFigure(d.retrying)} ${d.retrying === 1 ? 'notification is' : 'notifications are'} being retried after a failed send${broken.length ? `, and ${broken.map(c => c.name).join(', ')} failed ${broken.length === 1 ? 'its' : 'their'} latest send` : ''}.`
  if (broken.length) return `${broken.map(c => c.name).join(', ')} failed ${broken.length === 1 ? 'its' : 'their'} latest send. Check the channel and send it a test.`
  return null
})

// --- The log ------------------------------------------------------------------------------

const view = useQueryChoice<'all' | 'failed' | 'tests'>('show', ['all', 'failed', 'tests'], 'all')
const VIEWS: SegmentedOption[] = [
  { value: 'all', label: 'All' },
  { value: 'failed', label: 'Failed' },
  { value: 'tests', label: 'Tests' }
]

const shown = computed(() => deliveries.value.filter(entry => view.value === 'all'
  || (view.value === 'failed' ? !entry.ok : entry.test)))

const stamp = (at: number) => new Date(at).toLocaleString('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Activity">
      <template #description>
        What was sent to this project's channels: how much, how much arrived, how fast, and who sent it.
      </template>
      <template #actions>
        <SegmentedControl v-model="range" :options="RANGES" label="Period" />
        <v-btn
          class="text-xs"
          color="neutral"
          prepend-icon="material-symbols:refresh"
          size="small"
          variant="outlined"
          @click="reload"
        >
          Refresh
        </v-btn>
      </template>
    </PageHeader>

    <p
      v-if="attention"
      class="m-0 flex items-start gap-2.5 rounded-(--ps-radius-large) bg-error/6 px-4 py-3 text-xs leading-5 text-ink/80 ring-1 ring-error/20 ring-inset"
      role="status"
    >
      <Icon :aria-hidden="true" class="mt-0.5 flex-none text-base text-error" name="material-symbols:error-outline" />
      <span>{{ attention }}</span>
    </p>

    <LoadState
      :error="figuresError"
      :loading="false"
      what="the figures"
      @retry="reloadFigures"
    >
      <div class="flex flex-col gap-6">
        <KpiBoard
          :description="`${rangeWords[0]!.toUpperCase()}${rangeWords.slice(1)}, against the ${range === '24h' ? '24 hours' : range === '7d' ? '7 days' : '30 days'} before.`"
          :items="board.length ? board : [{ key: 'sent', label: 'Sent', value: null }, { key: 'delivered', label: 'Delivered', value: null }, { key: 'failed', label: 'Failed', value: null }, { key: 'p50', label: 'Median answer', value: null }, { key: 'waiting', label: 'Waiting now', value: null }]"
          :loading="figuresLoading"
          title="Delivery"
        />

        <MetricCard
          :categories="categories"
          :decimals="0"
          :height="220"
          :loading="figuresLoading"
          :series="series"
          :titles="titles"
          :category-label="range === '24h' ? 'Hour' : 'Day'"
          :description="`Every send ${range === '24h' ? 'hour by hour' : 'day by day'}, tests in grey. A failure that is retried and then lands shows as one of each.`"
          title="Sends"
          type="bar"
        />

        <div class="grid gap-6 lg:grid-cols-2">
          <SectionCard :padded="false" :description="`Where notifications went in ${rangeWords}, busiest first.`" title="By channel">
            <p v-if="!channels.length" class="m-0 px-5 py-4 text-xs text-ink/50">
              Nothing sent to a channel in {{ rangeWords }}.
            </p>
            <ul v-else class="m-0 flex list-none flex-col p-0">
              <li v-for="row in channels" :key="row.channelId ?? row.name" class="flex items-center gap-3 border-t border-line/70 px-5 py-3 first:border-t-0">
                <ChannelMark v-if="row.type" :type="row.type" size="small" />
                <span v-else aria-hidden="true" class="flex size-6 flex-none items-center justify-center rounded-full bg-ink/5 text-ink/40">
                  <Icon class="text-sm" name="material-symbols:delete-outline" />
                </span>
                <span class="flex min-w-0 flex-1 flex-col">
                  <span class="truncate text-xs text-ink">{{ row.name }}<template v-if="!row.channelId"> (deleted)</template></span>
                  <span class="truncate text-[11px] text-ink/50">
                    <span :class="failureTone(row)">{{ row.sent ? `${percent(1 - failureShare(row))} delivered` : 'Tests only' }}</span>
                    · median {{ msWords(row.p50) }}
                    <template v-if="row.lastAt">
                      · last {{ relativeWords(row.lastAt, time) }}<span v-if="!row.lastOk" class="text-error">, failed</span>
                    </template>
                  </span>
                </span>
                <Sparkline :height="24" :values="row.spark" class="h-6 w-20 flex-none" />
                <span class="w-12 flex-none text-right font-mono text-xs text-ink tabular-nums">{{ writeFigure(row.sent, { compact: true }) }}</span>
              </li>
            </ul>
          </SectionCard>

          <SectionCard :description="`What each rule and report sent in ${rangeWords}.`" title="By rule or report">
            <BreakdownList :rows="sources" label="Notifications by rule or report" />
            <p v-if="failingSources.length" class="m-0 mt-4 text-[11px] leading-4 text-ink/55">
              Failed:
              <template v-for="(row, i) in failingSources" :key="row.value">
                <span class="text-error">{{ row.value }} {{ writeFigure(row.failed) }}</span><template v-if="i < failingSources.length - 1">
                  ·
                </template>
              </template>
            </p>
          </SectionCard>
        </div>
      </div>
    </LoadState>

    <LoadState
      :error="error"
      :loading="loading"
      what="the log"
      @retry="reloadLog"
    >
      <SectionCard :padded="false" description="The latest fifty sends, newest first, with what each destination answered. Kept ninety days." title="Log">
        <template #actions>
          <SegmentedControl v-model="view" :options="VIEWS" label="Which notifications" />
        </template>

        <p v-if="!shown.length" class="m-0 px-5 py-4 text-xs text-ink/50">
          <template v-if="view === 'failed'">
            Nothing failed lately.
          </template>
          <template v-else-if="view === 'tests'">
            No tests lately.
          </template>
          <template v-else>
            Nothing sent yet. A rule's test, or a channel's, is the first.
          </template>
        </p>
        <ul v-else class="m-0 flex list-none flex-col p-0">
          <li v-for="entry in shown" :key="entry.id" class="flex items-center gap-3 border-t border-line/70 px-5 py-2.5 first:border-t-0">
            <Icon
              :class="entry.ok ? 'text-success' : 'text-error'"
              :name="entry.ok ? 'material-symbols:check-circle-outline' : 'material-symbols:cancel-outline'"
              :aria-hidden="true"
              class="flex-none text-base"
            />
            <ChannelMark v-if="entry.channelId && channelById(entry.channelId)" :type="channelById(entry.channelId)!.type" size="small" />
            <span class="flex min-w-0 flex-1 flex-col">
              <span class="truncate text-xs text-ink">{{ entry.title }}</span>
              <span class="truncate text-[11px] text-ink/50">
                {{ entry.channelName }}<template v-if="!entry.channelId"> (deleted)</template> · {{ entry.test ? entry.source : `from ${entry.source}` }} · <span :class="entry.ok ? '' : 'text-error'">{{ entry.detail }}</span>
              </span>
            </span>
            <Tag v-if="entry.test" label="Test" />
            <span class="flex-none font-mono text-[11px] whitespace-nowrap text-ink/45">{{ entry.ms }} ms</span>
            <span class="flex-none text-[11px] whitespace-nowrap text-ink/45">{{ stamp(entry.at) }}</span>
          </li>
        </ul>
      </SectionCard>
    </LoadState>
  </div>
</template>
