<script setup lang="ts">
// One event, read top to bottom as the questions come: did it work (the answer, in words, with
// anything it made worse said out loud), did it work every time (each window side by side, for
// an event that repeats), what did its players do day by day against what they would have done
// without it (the chart, with the window drawn on it), what else did it move, what else was on
// at the time, and then how it is set up, what the game receives, and who changed what.
//
// "What they would have done without it" is the whole question. The API answers it against the
// game's four weeks before, weekday for weekday, which is sound for a weekend and weaker for a
// six-week season, and the page says so. How sure a lift is comes from how far the days before
// the window strayed from their own expectation (stores/liveops.ts).
const route = useRoute()
const router = useRouter()

const { events, eventByKey, activate, pause, resume, archive, restore, duplicate, keyProblem, impactFor, gameDay, loading, error, reload } = useLiveOps()
const { experiments } = useExperiments()
const { segmentName } = useLiveConfig()

const key = computed(() => String(route.params.key ?? ''))
const event = computed(() => eventByKey(key.value))

usePageCrumb(() => event.value?.name ?? 'Event')

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const status = computed(() => (event.value ? stateOf(event.value, time.value) : null))
const look = computed(() => kindLook(event.value?.kind ?? ''))

// --- Its runs -----------------------------------------------------------------------------

/* Every window that has begun, oldest first, with what it did: the strip and the picker. */
const runs = computed(() => {
  const current = event.value
  if (!current || current.status === 'draft') return []
  return runsOf(current, time.value, 10).reverse().map(run => ({ run, impact: impactFor(current, run)! })).filter(entry => entry.impact)
})

const selectedAt = computed({
  get: () => {
    const asked = Number(route.query.at)
    const found = runs.value.find(entry => entry.run.start === asked)
    return found ? found.run.start : runs.value.at(-1)?.run.start ?? null
  },
  set: (value: number | null) => {
    router.replace({ query: { ...route.query, at: value === null ? undefined : String(value) } })
  }
})

const selected = computed(() => runs.value.find(entry => entry.run.start === selectedAt.value) ?? null)
const impact = computed(() => selected.value?.impact ?? null)

const goal = computed(() => event.value?.goal ?? null)

const isRecurring = computed(() => (event.value ? event.value.schedule.mode !== 'once' : false))

// --- The answer ---------------------------------------------------------------------------

type VerdictTone = 'good' | 'bad' | 'neutral' | 'waiting'

const verdict = computed<{ tone: VerdictTone, icon: string, title: string, detail: string }>(() => {
  const current = event.value
  const state = status.value
  if (!current || !state) return { tone: 'neutral', icon: '', title: '', detail: '' }
  const methodWords = (entry: EventImpact) => (entry.method === 'holdout'
    ? `against the ${writeFigure(entry.heldBack, { compact: true })} players a day held back from it`
    : 'against the same players\' four weeks before, weekday for weekday')

  if (!impact.value) {
    if (state.state === 'draft') return { tone: 'neutral', icon: 'material-symbols:edit-note-outline', title: 'A draft', detail: `It reaches nobody until it is activated and published. ${state.next ? `As set, its first window would be ${windowWords(state.next)}.` : ''}` }
    if (state.next) return { tone: 'waiting', icon: 'material-symbols:hourglass-top-outline', title: `Starts ${relativeWords(state.next.start, time.value)}`, detail: `${windowWords(state.next)}. Its results appear here from its first hour: ${goal.value ? LIVEOPS_METRICS[goal.value].label.toLowerCase() : 'its players and revenue'} for the players it reaches, ${current.rollout < FULL_ROLLOUT ? 'against the players held back from it' : 'against their own weeks before'}.` }
    return { tone: 'neutral', icon: 'material-symbols:event-busy-outline', title: 'Never ran', detail: 'Its schedule has no window that has begun.' }
  }

  const entry = impact.value
  const when = entry.live ? 'so far' : 'while it ran'
  if (!goal.value) {
    return { tone: 'neutral', icon: 'material-symbols:campaign-outline', title: 'Not measured', detail: `It is not meant to move a metric. It reached ${writeFigure(entry.players, { compact: true })} players a day ${when}; every figure is below all the same.` }
  }
  const metric = entry.metrics[goal.value]
  const label = LIVEOPS_METRICS[goal.value].label
  if (!metric.measured || !metric.baseline) {
    return { tone: 'neutral', icon: 'material-symbols:hourglass-empty', title: 'No data yet', detail: `${label} for its players has not come back ${when}: none of them has sent anything yet, or too few to show.` }
  }
  const outcome = outcomeOf(metric.comparison)
  const figures = `${writeOpsMetric(goal.value, metric.during)} a day against ${writeOpsMetric(goal.value, metric.expected)} expected, measured ${methodWords(entry)}. A ${writeChance(metric.comparison.chance)} chance it really rose.`
  if (outcome === 'better') return { tone: 'good', icon: 'material-symbols:trending-up', title: `${label} ${writeOpsLift(goal.value, metric.comparison)} ${when}`, detail: figures }
  if (outcome === 'worse') return { tone: 'bad', icon: 'material-symbols:trending-down', title: `${label} fell ${writeOpsLift(goal.value, metric.comparison).replace('-', '')} ${when}`, detail: figures }
  return { tone: 'neutral', icon: 'material-symbols:balance', title: entry.live ? `No clear effect on ${label.toLowerCase()} yet` : `No clear effect on ${label.toLowerCase()}`, detail: `${figures} ${entry.live ? 'A day or two more may settle it.' : 'Within its noise: it did not move it by more than an ordinary week does.'}` }
})

const VERDICT_TONE: Record<VerdictTone, string> = {
  good: 'bg-success/12 text-success',
  bad: 'bg-error/12 text-error',
  neutral: 'bg-ink/6 text-ink/60',
  waiting: 'bg-primary/10 text-primary'
}

/* What it made worse, and the dip after it: said whatever the headline is. */
const warnings = computed(() => {
  const current = event.value
  const entry = impact.value
  if (!current || !entry) return []
  const out: { icon: string, text: string }[] = []
  for (const metric of current.watch) {
    if (!entry.metrics[metric].measured || !entry.metrics[metric].baseline) continue
    const comparison = entry.metrics[metric].comparison
    if (outcomeOf(comparison) === 'worse') out.push({ icon: 'material-symbols:warning-outline', text: `Must not get worse: ${LIVEOPS_METRICS[metric].label.toLowerCase()} fell ${writeOpsLift(metric, comparison).replace('-', '')} while it was on, and it is ${writeChance(1 - comparison.chance)} sure that is real.` })
  }
  if (goal.value) {
    const after = entry.metrics[goal.value].measured ? entry.metrics[goal.value].after : null
    if (after && outcomeOf(after) === 'worse') out.push({ icon: 'material-symbols:south-east', text: `Pulled forward: in the three days after it ended, ${LIVEOPS_METRICS[goal.value].label.toLowerCase()} ran ${writeOpsLift(goal.value, after).replace('-', '')} under expected. Part of the rise was spending that would have come anyway.` })
  }
  return out
})

const overlaps = computed(() => (event.value && selected.value ? overlapsOf(event.value, selected.value.run, events.value, experiments.value) : []))

const progress = computed(() => {
  const current = status.value?.current
  return current ? Math.min(1, (time.value - current.start) / (current.end - current.start)) : 0
})

// --- Day by day ---------------------------------------------------------------------------

const metric = ref<LiveOpsMetricKey>('revenue')
watch(event, (value) => {
  if (value) metric.value = value.goal ?? 'players'
}, { immediate: true })

const METRIC_OPTIONS = LIVEOPS_METRIC_KEYS.map(entry => ({ value: entry, label: LIVEOPS_METRICS[entry].label, description: LIVEOPS_METRICS[entry].note }))
const metricModel = computed({
  get: () => metric.value as string,
  set: (value: string) => {
    metric.value = value as LiveOpsMetricKey
  }
})

const SCOPES = [
  { value: 'players', label: 'Its players' },
  { value: 'game', label: 'Whole game' }
]
const scope = ref('players')

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })
const titleFormat = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric' })

const days = computed(() => impact.value?.days ?? [])
const categories = computed(() => days.value.map(at => dayFormat.format(at)))

const scaleOf = (entry: LiveOpsMetricKey) => (LIVEOPS_METRICS[entry].kind === 'rate' ? 100 : 1)
/* A day with no figure (NaN from the store) is no data: a gap in the line, not a zero. */
const scaled = (values: number[]) => values.map(value => (Number.isFinite(value) ? Math.round(value * scaleOf(metric.value) * 1000) / 1000 : null))

const dayIndex = (at: number) => days.value.findIndex(day => day === startOfDay(at))

const titles = computed(() => days.value.map((at) => {
  const run = selected.value?.run
  const on = run && at + DAY > run.start && at < run.end
  return `${titleFormat.format(at)}${on ? ` · ${event.value?.name} on` : ''}`
}))

const series = computed<MetricSeries[]>(() => {
  const entry = impact.value
  if (!entry) return []
  if (scope.value === 'game') {
    return [{
      name: 'Whole game',
      data: days.value.map((at) => {
        const figures = gameDay(at)
        return figures && at !== startOfDay(time.value) && Number.isFinite(figures[metric.value]) ? Math.round(figures[metric.value] * scaleOf(metric.value) * 1000) / 1000 : null
      }),
      type: 'area',
      tone: 1
    }]
  }
  return [
    { name: 'Its players', data: scaled(entry.daily[metric.value].actual), type: 'line', tone: look.value.tone },
    { name: entry.method === 'holdout' ? 'Players held back' : 'Expected without it', data: scaled(entry.daily[metric.value].expected), type: 'line', tone: 'muted', dashed: true }
  ]
})

/*
 * The window read, shaded, with a line where it began and one where it ended, and the event's
 * other windows in view washed more lightly, so a repeating event's rhythm shows against the
 * line. Other events on at the time are named above the chart rather than drawn on it: their
 * labels on top of this one's were the chart's noise.
 */
const marks = computed<MetricMark[]>(() => {
  const current = event.value
  const run = selected.value?.run
  if (!run || !current || !days.value.length) return []
  const first = days.value[0]!
  const end = days.value.at(-1)! + DAY
  const lastIndex = (window: Occurrence) => {
    const index = dayIndex(lastMinute(window))
    return index === -1 ? days.value.length - 1 : index
  }
  const own: MetricMark[] = [{ from: Math.max(0, dayIndex(run.start)), to: lastIndex(run), label: `${current.name} starts`, tone: look.value.tone }]
  if (run.end <= time.value && dayIndex(lastMinute(run)) !== -1) own.push({ from: lastIndex(run), label: 'Ends', tone: look.value.tone })
  const others = isRhythm(current.schedule)
    ? []
    : servedWindows(current, first, end)
        .filter(window => window.start !== run.start)
        .map(window => ({ from: Math.max(0, dayIndex(window.start)), to: lastIndex(window), tone: look.value.tone, quiet: true }))
  return [...others, ...own]
})

const metricPrefix = computed(() => (LIVEOPS_METRICS[metric.value].kind === 'money' ? '$' : ''))
const metricUnit = computed(() => ({ rate: '%', money: '', count: '', minutes: 'min', ratio: '' })[LIVEOPS_METRICS[metric.value].kind])

// --- Every metric -------------------------------------------------------------------------

const metricRows = computed(() => {
  const current = event.value
  const entry = impact.value
  if (!current || !entry) return []
  const order = [...(current.goal ? [current.goal] : []), ...current.watch, ...LIVEOPS_METRIC_KEYS.filter(item => item !== current.goal && !current.watch.includes(item))]
  return order.map(item => ({
    key: item,
    role: item === current.goal ? 'Meant to move' : current.watch.includes(item) ? 'Must not worsen' : '',
    ...entry.metrics[item]
  }))
})

/* A row is compared only when both its figures came back; the rest are no data. */
const comparedRow = (row: { measured: boolean, baseline: boolean }) => row.measured && row.baseline

const metricDomain = computed<[number, number]>(() => [
  Math.min(0, ...metricRows.value.filter(comparedRow).map(row => row.comparison.low)),
  Math.max(0.05, ...metricRows.value.filter(comparedRow).map(row => row.comparison.high))
])

const OUTCOME_TEXT = { better: 'text-success', worse: 'text-error', unclear: 'text-ink/60' } as const

// --- Tiles --------------------------------------------------------------------------------

const tiles = computed(() => {
  const current = event.value
  const entry = impact.value
  if (!current || !entry) return []
  const keys = [...new Set([...(current.goal ? [current.goal] : []), ...current.watch, 'players' as const, 'revenue' as const])].slice(0, 4)
  return keys.map(item => ({
    key: item,
    label: `${LIVEOPS_METRICS[item].label}${item === current.goal ? ' · goal' : ''}`,
    // No figure behind it is a dash, and the comparison needs both sides.
    value: !entry.metrics[item].measured ? null : LIVEOPS_METRICS[item].kind === 'rate' ? entry.metrics[item].during * 100 : entry.metrics[item].during,
    previous: !comparedRow(entry.metrics[item]) ? null : LIVEOPS_METRICS[item].kind === 'rate' ? entry.metrics[item].expected * 100 : entry.metrics[item].expected,
    prefix: LIVEOPS_METRICS[item].kind === 'money' ? '$' : '',
    unit: ({ rate: '%', money: '', count: '', minutes: 'min', ratio: '' })[LIVEOPS_METRICS[item].kind],
    decimals: LIVEOPS_METRICS[item].kind === 'money' && entry.metrics[item].during < 10 ? 3 : 1,
    info: `${LIVEOPS_METRICS[item].note} A day, for its players, while it was on; against ${entry.method === 'holdout' ? 'the players held back' : 'what they usually do'}.`
  }))
})

// --- Setup --------------------------------------------------------------------------------

const TABS = computed<PageTab[]>(() => [
  { value: 'results', label: 'Results' },
  { value: 'setup', label: 'Schedule & audience' },
  { value: 'payload', label: 'Payload & code' },
  { value: 'history', label: 'History', count: event.value?.history.length }
])
const tab = ref(runs.value.length ? 'results' : 'setup')

const facts = computed<Fact[]>(() => {
  const current = event.value
  if (!current) return []
  return [
    { key: 'key', label: 'Key', value: current.key, mono: true, copy: current.key, info: 'What the game reads it by. Fixed when it was made.' },
    { key: 'kind', label: 'Kind', value: `${kindLook(current.kind).label} (${current.kind})`, info: 'The studio\'s own word for it. The game reads it too.' },
    { key: 'priority', label: 'Priority', value: String(current.priority), mono: true, info: 'Higher wins when two events of the same kind are on for one player at once.' },
    { key: 'rollout', label: 'Rollout', value: current.rollout < FULL_ROLLOUT ? `${formatRollout(current.rollout)} of the audience, ${formatRollout(FULL_ROLLOUT - current.rollout)} held back` : 'Everybody in the audience', info: 'The same players each time: who gets it is a hash of the install, so a player is in or out for good.' },
    { key: 'goal', label: 'Meant to move', value: current.goal ? LIVEOPS_METRICS[current.goal].label : 'Not measured' },
    { key: 'watch', label: 'Must not get worse', value: current.watch.map(item => LIVEOPS_METRICS[item].label).join(', ') || 'Nothing' },
    { key: 'created', label: 'Created', value: `${shortDate(current.created_at)} by ${current.history[0]?.actor ?? current.updated_by}` },
    { key: 'updated', label: 'Last changed', value: `${sinceWords(current.updated_at, now.value)} by ${current.updated_by}` }
  ]
})

/* The windows ahead, and the last few behind, for reading a schedule back as dates. */
const upcomingWindows = computed(() => {
  const current = event.value
  if (!current) return []
  return occurrencesOf(current.schedule, time.value, time.value + 3 * 366 * DAY).slice(0, 8)
})

const pastCount = computed(() => (event.value ? occurrencesOf(event.value.schedule, Date.parse(event.value.schedule.anchor), Math.min(time.value, servedUntil(event.value))).filter(window => window.start <= time.value).length : 0))

const audienceWords = computed(() => (event.value ? describeRule(event.value.audience, segmentName) || 'Every player' : ''))
const payloadCode = computed(() => JSON.stringify(event.value?.payload ?? null, null, 2))

// --- Actions ------------------------------------------------------------------------------

const pausing = ref(false)
const activating = ref(false)
const archiving = ref(false)
const copying = ref(false)
const copyName = ref('')
const copyKey = ref('')

function openCopy() {
  if (!event.value) return
  const year = new Date().getFullYear()
  copyName.value = `${event.value.name} (copy)`
  copyKey.value = event.value.key.includes(String(year)) ? event.value.key.replace(String(year), String(year + 1)) : `${event.value.key}_copy`
  copying.value = true
}

const copyProblem = computed(() => keyProblem(copyKey.value))

const refusal = ref('')

async function act(write: Promise<string | null>) {
  refusal.value = (await write) ?? ''
  return !refusal.value
}

async function confirmCopy() {
  if (!event.value || copyProblem.value) return
  const done = await act(duplicate(event.value.key, copyKey.value, copyName.value.trim() || event.value.name))
  copying.value = false
  if (done) void router.push(`/growth/liveops/${copyKey.value}`)
}

async function confirmPause() {
  if (!event.value) return
  pausing.value = false
  await act(pause(event.value.key))
}

const activateWords = computed(() => (event.value
  ? `${scheduleWords(event.value.schedule)}, for ${audienceWords.value.toLowerCase()}${event.value.rollout < FULL_ROLLOUT ? ` (${formatRollout(event.value.rollout)} of them)` : ''}. It is saved to the draft; publish it for players to get it.`
  : ''))
</script>

<template>
  <div v-if="(loading || error) && !event" class="flex flex-col gap-8">
    <PageHeader title="Event" />
    <LoadState
      :error="error"
      :loading="loading"
      what="the event"
      @retry="reload"
    />
  </div>

  <div v-else-if="!event || !status" class="flex flex-col gap-8">
    <PageHeader title="Event not found" />
    <LayerCard>
      <EmptyState description="There is no Live Ops event with this key in the project." icon="material-symbols:event-busy-outline" title="No such event">
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            to="/growth/liveops"
            variant="flat"
          >
            All events
          </v-btn>
        </template>
      </EmptyState>
    </LayerCard>
  </div>

  <div v-else class="flex flex-col gap-8">
    <PageHeader :description="event.description" :title="event.name">
      <template #badge>
        <StatusBadge :detail="stateWords(event, time)" :label="LIVEOPS_STATE[status.state].label" :tone="LIVEOPS_STATE[status.state].tone" />
      </template>
      <template #actions>
        <v-btn
          v-if="event.status !== 'archived'"
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:edit-outline"
          size="small"
          variant="outlined"
          :to="`/growth/liveops/${event.key}/edit`"
        >
          Edit
        </v-btn>
        <v-btn
          v-if="event.status === 'draft'"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:play-arrow-outline"
          size="small"
          variant="flat"
          @click="activating = true"
        >
          Activate
        </v-btn>
        <v-btn
          v-else-if="event.status === 'active'"
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:pause-outline"
          size="small"
          variant="outlined"
          @click="pausing = true"
        >
          Pause
        </v-btn>
        <v-btn
          v-else-if="event.status === 'paused'"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:play-arrow-outline"
          size="small"
          variant="flat"
          @click="act(resume(event.key))"
        >
          Resume
        </v-btn>
        <v-btn
          v-else
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:unarchive-outline"
          size="small"
          variant="outlined"
          @click="act(restore(event.key))"
        >
          Restore to draft
        </v-btn>
        <v-menu location="bottom end">
          <template #activator="{ props: menu }">
            <v-btn
              v-bind="menu"
              aria-label="More"
              color="neutral"
              icon="material-symbols:more-vert"
              size="small"
              variant="text"
            />
          </template>
          <v-list density="compact" nav>
            <v-list-item prepend-icon="material-symbols:content-copy-outline" title="Duplicate" @click="openCopy" />
            <v-list-item
              v-if="event.status !== 'archived'"
              base-color="error"
              prepend-icon="material-symbols:archive-outline"
              title="Archive"
              @click="archiving = true"
            />
          </v-list>
        </v-menu>
      </template>
    </PageHeader>

    <p v-if="refusal" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ refusal }}
    </p>

    <!-- The answer, before any chart. -->
    <section class="flex flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="flex min-w-0 flex-1 basis-96 items-start gap-4">
          <span :class="VERDICT_TONE[verdict.tone]" aria-hidden="true" class="flex size-11 flex-none items-center justify-center rounded-(--ps-radius-large)">
            <Icon :name="verdict.icon" class="text-2xl" />
          </span>
          <div class="flex min-w-0 flex-col gap-1">
            <span v-if="impact" class="text-[11px] text-ink/50">{{ impact.live ? 'The window on now' : isRecurring ? `The window of ${windowShort(impact.occurrence)}` : windowShort(impact.occurrence) }}</span>
            <h2 class="m-0 text-lg leading-tight font-semibold tracking-tight text-ink">
              {{ verdict.title }}
            </h2>
            <p class="m-0 text-sm leading-6 text-ink/65">
              {{ verdict.detail }}
            </p>
          </div>
        </div>
        <div class="flex flex-none flex-col items-end gap-1 text-right">
          <LiveOpsKindMark :kind="event.kind" />
          <span class="max-w-64 text-[11px] text-ink/50">{{ audienceWords }}<template v-if="event.rollout < FULL_ROLLOUT">, {{ formatRollout(event.rollout) }}</template></span>
        </div>
      </div>

      <div v-if="status.state === 'live' && status.current" class="flex flex-col gap-1.5">
        <div class="flex items-center justify-between text-[11px] text-ink/55">
          <span>{{ windowWords(status.current) }}</span>
          <span>{{ stateWords(event, time) }}</span>
        </div>
        <span class="relative h-1.5 overflow-hidden rounded-full bg-ink/8">
          <span :style="{ width: `${progress * 100}%`, background: toneColour(look.tone) }" class="absolute inset-y-0 left-0 rounded-full" />
        </span>
      </div>

      <ul v-if="warnings.length || overlaps.length" class="m-0 flex list-none flex-col gap-2 p-0">
        <li
          v-for="(warning, index) in warnings"
          :key="index"
          class="flex items-start gap-2.5 rounded-(--ps-radius) bg-warning/8 px-3 py-2.5 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset"
        >
          <Icon :name="warning.icon" :aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" />
          <span>{{ warning.text }}</span>
        </li>
        <li v-if="overlaps.length" class="flex items-start gap-2.5 rounded-(--ps-radius) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/75 ring-1 ring-ink/6 ring-inset">
          <Icon :aria-hidden="true" class="mt-0.5 flex-none text-sm text-ink/50" name="material-symbols:layers-outline" />
          <span>
            Also on at the time:
            <template v-for="(entry, index) in overlaps.slice(0, 4)" :key="entry.id">
              <NuxtLink :to="entry.to" class="text-ink underline decoration-ink/25 underline-offset-2 hover:decoration-ink">{{ entry.name }}</NuxtLink><span class="text-ink/50"> ({{ entry.share >= 0.99 ? 'all of it' : `${Math.round(entry.share * 100)}% of it` }})</span>{{ index < Math.min(overlaps.length, 4) - 1 ? ', ' : '' }}
            </template>{{ overlaps.length > 4 ? ` and ${overlaps.length - 4} more.` : '.' }}
            {{ impact?.method === 'holdout' ? 'The players held back had them too, so this result is its own.' : 'Part of what moved may be theirs: hold some players back next time for a result that is only this event\'s.' }}
          </span>
        </li>
      </ul>
    </section>

    <!-- Each window it ran, as a chart: did it work every time, and is it wearing off. -->
    <SectionCard
      v-if="runs.length > 1 && goal"
      :description="`${LIVEOPS_METRICS[goal].label} against expected, ${isRhythm(event.schedule) ? 'a week of windows at a time' : 'window by window'}. Choose a column to read that window below.`"
      title="Each time it ran"
    >
      <LiveOpsRuns
        v-model="selectedAt"
        :goal="goal"
        :next="status.next"
        :rhythm="isRhythm(event.schedule)"
        :runs="runs"
      />
    </SectionCard>

    <div class="flex flex-col gap-6">
      <PageTabs v-model="tab" :tabs="TABS" label="Event views" />

      <!-- Results. -->
      <template v-if="tab === 'results'">
        <LayerCard v-if="!impact">
          <EmptyState :description="status.next ? `Its first window is ${windowWords(status.next)}. Results appear here from its first hour.` : 'It has not run.'" icon="material-symbols:monitoring" title="No results yet" />
        </LayerCard>

        <template v-else>
          <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <Statistic
              v-for="tile in tiles"
              :key="tile.key"
              :compare="impact.method === 'holdout' ? 'vs held back' : 'vs expected'"
              :decimals="tile.decimals"
              :info="tile.info"
              :label="tile.label"
              :positive="'up'"
              :prefix="tile.prefix"
              :previous="tile.previous"
              :unit="tile.unit"
              :value="tile.value"
              compact
            />
          </div>

          <MetricCard
            v-model="metricModel"
            :categories="categories"
            :decimals="LIVEOPS_METRICS[metric].kind === 'money' && metric === 'arpdau' ? 3 : 1"
            :height="300"
            :marks="marks"
            :options="METRIC_OPTIONS"
            :prefix="metricPrefix"
            :series="series"
            :titles="titles"
            :unit="metricUnit"
            category-label="Day"
            type="line"
          >
            <template #description>
              <template v-if="scope === 'players'">
                Its players each day, two weeks either side of the window (its other windows in view are washed lighter), against
                {{ impact.method === 'holdout' ? 'the players held back from it: the same audience, without the event.' : 'what they usually do on that weekday, from the four weeks before.' }}
                The gap between the lines inside the shading is the event.
              </template>
              <template v-else>
                The whole game each day. An event that reached a small audience barely shows here, which is why it is read on its own players.
              </template>
            </template>
            <template #actions>
              <SegmentedControl v-model="scope" :options="SCOPES" label="Whose figures" />
            </template>
          </MetricCard>

          <SectionCard :padded="false" :description="`Everything measured for its players while it was on, against ${impact.method === 'holdout' ? 'the players held back' : 'what they usually do'}, and in the three days after it ended.`" title="Every metric">
            <div class="overflow-x-auto">
              <table class="w-full min-w-[820px] border-collapse text-left">
                <thead>
                  <tr class="border-b border-line text-[11px] text-ink/45">
                    <th class="px-5 py-2.5 font-normal">
                      Metric
                    </th>
                    <th class="px-3 py-2.5 text-right font-normal">
                      While on
                    </th>
                    <th class="px-3 py-2.5 text-right font-normal">
                      {{ impact.method === 'holdout' ? 'Held back' : 'Expected' }}
                    </th>
                    <th class="px-3 py-2.5 text-right font-normal">
                      Effect
                    </th>
                    <th class="w-56 px-5 py-2.5 font-normal">
                      <span class="sr-only">Effect, drawn</span>
                    </th>
                    <th class="px-5 py-2.5 text-right font-normal">
                      After it
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="row in metricRows" :key="row.key" class="border-b border-line/70 last:border-b-0">
                    <td class="px-5 py-3">
                      <span class="flex items-center gap-2">
                        <span class="text-xs text-ink">{{ LIVEOPS_METRICS[row.key].label }}</span>
                        <Tag v-if="row.role" :label="row.role" :tone="row.role === 'Meant to move' ? `chart-${look.tone}` : 'neutral'" />
                      </span>
                    </td>
                    <td class="px-3 py-3 text-right font-mono text-xs text-ink">
                      <template v-if="row.measured">
                        {{ writeOpsMetric(row.key, row.during) }}
                      </template>
                      <span v-else class="text-ink/30">&ndash;</span>
                    </td>
                    <td class="px-3 py-3 text-right font-mono text-xs text-ink/60">
                      <template v-if="row.baseline">
                        {{ writeOpsMetric(row.key, row.expected) }}
                      </template>
                      <span v-else class="text-ink/30">&ndash;</span>
                    </td>
                    <td class="px-3 py-3 text-right">
                      <span v-if="!comparedRow(row)" class="font-mono text-xs text-ink/30">&ndash;</span>
                      <span v-else class="flex flex-col items-end">
                        <span :class="OUTCOME_TEXT[outcomeOf(row.comparison)]" class="font-mono text-xs">{{ writeOpsLift(row.key, row.comparison) }}</span>
                        <span class="font-mono text-[10px] text-ink/45">{{ writeChance(row.comparison.chance) }} sure</span>
                      </span>
                    </td>
                    <td class="px-5 py-3">
                      <EffectBar v-if="comparedRow(row)" :comparison="row.comparison" :domain="metricDomain" />
                      <NoDataPlaceholder
                        v-else
                        :height="28"
                        :seed="row.key.length"
                        label="No data yet"
                      />
                    </td>
                    <td class="px-5 py-3 text-right font-mono text-[11px]">
                      <span v-if="row.after && comparedRow(row)" :class="OUTCOME_TEXT[outcomeOf(row.after)]">{{ writeOpsLift(row.key, row.after) }}</span>
                      <span v-else class="text-ink/35">{{ impact.live ? 'on now' : '–' }}</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </SectionCard>

          <SectionCard description="What this result is measured against, and how far to trust it." title="How this is measured">
            <div class="grid gap-5 md:grid-cols-3">
              <div class="flex flex-col gap-1">
                <span class="text-[11px] text-ink/50">Players it reached</span>
                <span v-if="impact.metrics.players.measured" class="font-mono text-lg text-ink">{{ writeFigure(impact.players, { compact: true }) }} <span class="text-xs text-ink/45">a day</span></span>
                <span v-else class="font-mono text-lg text-ink/30">&ndash;</span>
                <span class="text-[11px] leading-4 text-ink/55">{{ audienceWords }}{{ event.rollout < FULL_ROLLOUT ? `, ${formatRollout(event.rollout)} of them` : '' }}.</span>
              </div>
              <div class="flex flex-col gap-1">
                <span class="text-[11px] text-ink/50">Measured against</span>
                <span class="text-sm text-ink">{{ impact.method === 'holdout' ? 'Players held back' : 'Their own weeks before' }}</span>
                <span class="text-[11px] leading-4 text-ink/55">
                  <template v-if="impact.method === 'holdout'">{{ writeFigure(impact.heldBack, { compact: true }) }} players a day matched the audience and did not get it. The cleanest answer there is: the same days, the same other events, only this one differs.</template>
                  <template v-else>Everybody in the audience got it, so its effect is read against what the same players did on the same weekdays in the four weeks before. Sound for a short event; a long one also catches whatever else changed.</template>
                </span>
              </div>
              <div class="flex flex-col gap-1">
                <span class="text-[11px] text-ink/50">Revenue from its players {{ impact.live ? 'so far' : 'while it was on' }}</span>
                <span v-if="impact.metrics.revenue.measured" class="font-mono text-lg text-ink">${{ writeFigure(impact.revenue, { compact: true }) }}</span>
                <span v-else class="font-mono text-lg text-ink/30">&ndash;</span>
                <span class="text-[11px] leading-4 text-ink/55">All of it, not only what the event added: the table above says how much it added.</span>
              </div>
            </div>
          </SectionCard>
        </template>
      </template>

      <!-- Schedule and audience. -->
      <template v-else-if="tab === 'setup'">
        <FactGrid :columns="4" :facts="facts" label="About this event" />

        <div class="grid gap-6 lg:grid-cols-2">
          <SectionCard :description="`${isRecurring ? `It has opened ${pastCount} ${pastCount === 1 ? 'time' : 'times'} so far.` : ''} The server works out each window; the game is only told the one it is in and the next.`" title="When it is on">
            <div class="flex flex-col gap-4">
              <div class="flex items-start gap-2.5 rounded-(--ps-radius) bg-ink/4 px-3.5 py-3 ring-1 ring-ink/6 ring-inset">
                <Icon :name="isRecurring ? 'material-symbols:event-repeat-outline' : 'material-symbols:event-outline'" :aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/55" />
                <span class="flex flex-col gap-0.5">
                  <span class="text-[13px] font-medium text-ink">{{ scheduleWords(event.schedule) }}</span>
                  <span class="text-[11px] text-ink/50">{{ event.schedule.timezone ?? 'UTC' }} clock{{ event.status === 'paused' ? '. Paused: none of these are served until it is resumed.' : '' }}</span>
                </span>
              </div>
              <ol v-if="upcomingWindows.length" class="m-0 flex list-none flex-col p-0">
                <li v-for="(window, index) in upcomingWindows" :key="window.start" class="flex items-center justify-between gap-3 border-b border-line/70 py-2 last:border-b-0">
                  <span class="flex min-w-0 items-center gap-2.5">
                    <span :style="{ background: window.start <= time ? toneColour(look.tone) : `color-mix(in srgb, ${toneColour(look.tone)} 30%, transparent)` }" aria-hidden="true" class="size-2 flex-none rounded-full" />
                    <span class="truncate text-xs text-ink/80">{{ windowWords(window) }}</span>
                  </span>
                  <span class="flex-none text-[11px] text-ink/45">{{ window.start <= time ? 'on now' : index === 0 || index === upcomingWindows.length - 1 ? relativeWords(window.start, time) : '' }}</span>
                </li>
              </ol>
              <p v-else class="m-0 text-xs text-ink/55">
                No window is left in its schedule.
              </p>
            </div>
          </SectionCard>

          <SectionCard description="Who it is on for. Checked on the server, per request, against what the game and PlayerSense know about the player." title="Who gets it">
            <div class="flex flex-col gap-4">
              <p class="m-0 rounded-(--ps-radius) bg-ink/4 px-3.5 py-3 text-[13px] leading-6 text-ink ring-1 ring-ink/6 ring-inset">
                {{ audienceWords }}
              </p>
              <div class="flex flex-col gap-1.5">
                <div class="flex items-center justify-between text-[11px] text-ink/55">
                  <span>Get it</span>
                  <span>Held back</span>
                </div>
                <span class="flex h-2 overflow-hidden rounded-full bg-ink/8">
                  <span :style="{ width: `${event.rollout / 100}%`, background: toneColour(look.tone) }" class="h-full" />
                </span>
                <div class="flex items-center justify-between font-mono text-xs">
                  <span class="text-ink">{{ formatRollout(event.rollout) }}</span>
                  <span class="text-ink/55">{{ formatRollout(FULL_ROLLOUT - event.rollout) }}</span>
                </div>
              </div>
              <p class="m-0 text-[11px] leading-4 text-ink/55">
                <template v-if="event.rollout < FULL_ROLLOUT">
                  The held-back players are what its results are measured against. They are the same players every window, so a recurring event is compared like with like.
                </template>
                <template v-else>
                  Nobody is held back, so its results are read against the audience's own weeks before. Hold back 5 to 10% for a result that is only this event's.
                </template>
              </p>
            </div>
          </SectionCard>
        </div>
      </template>

      <!-- Payload and code. -->
      <template v-else-if="tab === 'payload'">
        <div class="grid gap-6 xl:grid-cols-2">
          <SectionCard description="What the game receives while the event is on for a player, exactly as it was saved. PlayerSense never reads it." title="Payload">
            <CodeBlock
              :code="payloadCode"
              :title="`${event.key}.payload`"
              lang="json"
              line-numbers
            />
          </SectionCard>
          <SectionCard description="How the game reads this event." title="In the game">
            <LiveOpsCode :event="event" />
          </SectionCard>
        </div>
      </template>

      <!-- History. -->
      <SectionCard v-else description="What was set and changed, and when, as it happened." title="History">
        <ol class="m-0 flex list-none flex-col p-0">
          <li v-for="(item, index) in [...event.history].reverse()" :key="index" class="relative flex gap-3 pb-4 last:pb-0">
            <span aria-hidden="true" class="absolute top-4 bottom-0 left-[5px] w-px bg-line [li:last-child>&]:hidden" />
            <span :style="{ borderColor: toneColour(look.tone) }" aria-hidden="true" class="relative mt-1.5 size-[11px] flex-none rounded-full border-2 bg-surface" />
            <div class="flex min-w-0 flex-1 flex-wrap items-baseline justify-between gap-x-3">
              <span class="text-xs text-ink">{{ item.text }}</span>
              <span class="text-[11px] text-ink/45">{{ shortDate(item.at) }}, {{ sinceWords(item.at, now) }} · {{ item.actor }}</span>
            </div>
          </li>
        </ol>
      </SectionCard>
    </div>

    <ConfirmDialog
      v-if="activating"
      v-model="activating"
      :description="activateWords"
      action="Activate"
      title="Activate this event?"
      tone="primary"
      @confirm="act(activate(event.key))"
    />

    <ConfirmDialog
      v-if="archiving"
      v-model="archiving"
      description="It stops being served at the next publish and leaves the calendar. Its results and history are kept, and it can be restored to a draft."
      action="Archive event"
      title="Archive this event?"
      @confirm="act(archive(event.key))"
    />

    <v-dialog v-if="pausing" v-model="pausing" max-width="480">
      <v-card border flat>
        <div class="flex flex-col gap-4 p-5">
          <div class="flex flex-col gap-1">
            <h2 class="m-0 text-sm font-medium text-ink">
              Pause {{ event.name }}?
            </h2>
            <p class="m-0 text-xs leading-5 text-ink/60">
              It is off for everybody from the next publish, a window on now included, whatever its schedule says. Resuming it picks the schedule up where it is by then.
            </p>
          </div>
        </div>
        <footer class="flex justify-end gap-2 border-t border-line px-5 py-3">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="pausing = false"
          >
            Cancel
          </v-btn>
          <v-btn
            class="text-xs font-medium"
            color="warning"
            size="small"
            variant="flat"
            @click="confirmPause"
          >
            Pause event
          </v-btn>
        </footer>
      </v-card>
    </v-dialog>

    <v-dialog v-if="copying" v-model="copying" max-width="480">
      <v-card border flat>
        <div class="flex flex-col gap-4 p-5">
          <div class="flex flex-col gap-1">
            <h2 class="m-0 text-sm font-medium text-ink">
              Duplicate {{ event.name }}
            </h2>
            <p class="m-0 text-xs leading-5 text-ink/60">
              A draft copy with the same schedule, audience and payload, under a key of its own: the same offer for other countries, or a variation to run beside it.
            </p>
          </div>
          <v-text-field
            v-model="copyName"
            aria-label="Name"
            hide-details
            maxlength="120"
            variant="outlined"
          />
          <div class="flex flex-col gap-1">
            <v-text-field
              v-model="copyKey"
              :error="Boolean(copyProblem)"
              aria-label="Key"
              class="font-mono"
              hide-details
              maxlength="64"
              spellcheck="false"
              variant="outlined"
            />
            <span v-if="copyProblem" class="text-[11px] text-error">{{ copyProblem }}</span>
          </div>
        </div>
        <footer class="flex justify-end gap-2 border-t border-line px-5 py-3">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="copying = false"
          >
            Cancel
          </v-btn>
          <v-btn
            :disabled="Boolean(copyProblem)"
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="confirmCopy"
          >
            Duplicate
          </v-btn>
        </footer>
      </v-card>
    </v-dialog>
  </div>
</template>
