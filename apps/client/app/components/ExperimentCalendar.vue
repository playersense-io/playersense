<script setup lang="ts">
// Tests on a calendar of days, the way a live game is planned: a column a day with the weekend
// shaded, the whole game's players, new players and revenue along the top so a test can be read
// against the week it ran in, and every test as a bar across the days it ran, with how many
// players it holds. Bars share a row wherever they do not overlap, so the calendar stays as
// short as what ran at once. Now is a line down every row.
//
// A running test's bar carries on, fainter, to its planned end. The window moves a week at a
// time (‹ ›) or a whole window (« »), and Compact fits twice the days and drops the strip.
const props = defineProps<{
  experiments: readonly ExperimentDef[]
}>()

const DAY = 86_400_000

const now = useNow({ interval: 60_000 })

const compact = ref(false)
const span = computed(() => (compact.value ? 42 : 21))

const startOfDay = (time: number) => {
  const at = new Date(time)
  at.setHours(0, 0, 0, 0)
  return at.getTime()
}

/* The first day shown. Opens with today a third of the way in: what ran lately, and what is due. */
const from = ref(startOfDay(Date.now()) - 7 * DAY)
watch(compact, (value) => {
  from.value = startOfDay(Date.now()) - (value ? 14 : 7) * DAY
})

function move(days: number) {
  from.value += days * DAY
}

function today() {
  from.value = startOfDay(Date.now()) - (compact.value ? 14 : 7) * DAY
}

const days = computed(() => Array.from({ length: span.value }, (_, index) => {
  const at = from.value + index * DAY
  const date = new Date(at)
  const weekday = date.getDay()
  return {
    at,
    day: date.getDate(),
    weekday: date.toLocaleDateString('en', { weekday: 'short' }),
    weekend: weekday === 0 || weekday === 6,
    today: at === startOfDay(now.value.getTime()),
    totals: sampleDayTotals(at)
  }
}))

const rangeLabel = computed(() => {
  const first = new Date(from.value)
  const last = new Date(from.value + (span.value - 1) * DAY)
  const month = (date: Date) => date.toLocaleDateString('en', { month: 'short' })
  return `${month(first)} ${first.getDate()} to ${month(last)} ${last.getDate()}, ${last.getFullYear()}`
})

const columns = computed(() => ({ gridTemplateColumns: `repeat(${span.value}, minmax(0, 1fr))` }))

/* Where now falls across the window, as a share of its width. */
const nowAt = computed(() => (now.value.getTime() - from.value) / (span.value * DAY))

// --- The bars -----------------------------------------------------------------------------

interface Bar {
  experiment: ExperimentDef
  start: number
  end: number
  /** Where the days it has run end and its plan carries on, as a share of the bar. */
  ranUntil: number
  clippedStart: boolean
  clippedEnd: boolean
  label: string
}

const bars = computed(() => {
  const windowEnd = from.value + span.value * DAY
  const list = props.experiments
    .filter(entry => entry.started_at)
    .map((entry) => {
      const started = Date.parse(entry.started_at!)
      const ended = entry.ended_at ? Date.parse(entry.ended_at) : null
      const last = ended ?? Math.max(started + entry.plannedDays * DAY, now.value.getTime())
      return { entry, started, last, ranTo: ended ?? now.value.getTime() }
    })
    .filter(item => item.last > from.value && item.started < windowEnd)
    .sort((a, b) => a.started - b.started)

  /* Where the days run so far end, as a share of the part of the bar in view. */
  const ranShare = (ranTo: number, end: number, start: number) => {
    const visibleStart = from.value + start * DAY
    const visibleEnd = from.value + end * DAY
    return Math.min(1, Math.max(0, (ranTo - visibleStart) / Math.max(visibleEnd - visibleStart, 1)))
  }

  const lanes: Bar[][] = []
  const laneEnds: number[] = []
  for (const item of list) {
    const start = Math.max(0, Math.floor((startOfDay(item.started) - from.value) / DAY))
    const end = Math.min(span.value, Math.ceil((item.last - from.value) / DAY))
    const results = sampleExperimentResults(item.entry)
    const players = results ? results.variants.reduce((sum, variant) => sum + variant.players, 0) : 0
    const bar: Bar = {
      experiment: item.entry,
      start,
      end: Math.max(end, start + 1),
      ranUntil: ranShare(item.ranTo, Math.max(end, start + 1), start),
      clippedStart: item.started < from.value,
      clippedEnd: item.last > windowEnd,
      label: `${item.entry.name} · ${writeFigure(players, { compact: true })} players`
    }
    const lane = laneEnds.findIndex(laneEnd => laneEnd <= start)
    if (lane === -1) {
      lanes.push([bar])
      laneEnds.push(bar.end)
    } else {
      lanes[lane]!.push(bar)
      laneEnds[lane] = bar.end
    }
  }
  return lanes
})

/* A bar's colour says how the test ended; a running one fades past today into its plan. */
function barStyle(bar: Bar) {
  const colour = {
    running: 'rgb(var(--v-theme-primary))',
    concluded: bar.experiment.winner ? 'color-mix(in srgb, rgb(var(--v-theme-success)) 78%, black)' : 'color-mix(in srgb, rgb(var(--v-theme-on-surface)) 45%, rgb(var(--v-theme-surface)))',
    stopped: 'color-mix(in srgb, rgb(var(--v-theme-warning)) 78%, black)',
    draft: 'transparent'
  }[bar.experiment.status]
  const faded = `color-mix(in srgb, ${colour} 40%, transparent)`
  const ran = bar.experiment.status === 'running' ? bar.ranUntil * 100 : 100
  return {
    gridColumn: `${bar.start + 1} / ${bar.end + 1}`,
    background: `linear-gradient(to right, ${colour} 0 ${ran}%, ${faded} ${ran}% 100%)`
  }
}

function outcome(experiment: ExperimentDef): string {
  if (experiment.status === 'running') return whenWords(experiment)
  if (experiment.status === 'stopped') return `Stopped early. ${whenWords(experiment)}`
  const winner = experiment.variants.find(entry => entry.id === experiment.winner)
  return `${winner ? `${variantTitle(experiment, winner)} won` : 'No clear winner'}. ${whenWords(experiment)}`
}

const compactFigure = (value: number) => writeFigure(value, { compact: true })

/* The strip over the calendar: the whole game, a figure a day. */
const STRIP: { key: string, icon: string, label: string, tone: string, value: (totals: DayTotals) => string }[] = [
  { key: 'players', icon: 'material-symbols:group-outline', label: 'Players', tone: '', value: totals => compactFigure(totals.players) },
  { key: 'new', icon: 'material-symbols:person-add-outline', label: 'New players', tone: 'text-primary', value: totals => compactFigure(totals.newPlayers) },
  { key: 'revenue', icon: 'material-symbols:payments-outline', label: 'Revenue', tone: 'text-success', value: totals => `$${compactFigure(totals.revenue)}` }
]
</script>

<template>
  <div class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border border-line">
    <!-- Where the window is, and how to move it. -->
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-ink/2 px-3 py-2">
      <v-btn
        class="text-xs font-medium"
        color="neutral"
        size="small"
        variant="outlined"
        @click="today"
      >
        Today
      </v-btn>
      <div class="flex items-center gap-0.5">
        <v-btn
          aria-label="Back a window"
          color="neutral"
          icon="material-symbols:keyboard-double-arrow-left"
          size="small"
          variant="text"
          @click="move(-span)"
        />
        <v-btn
          aria-label="Back a week"
          color="neutral"
          icon="material-symbols:chevron-left"
          size="small"
          variant="text"
          @click="move(-7)"
        />
        <span class="min-w-48 text-center text-xs font-medium text-ink">{{ rangeLabel }}</span>
        <v-btn
          aria-label="On a week"
          color="neutral"
          icon="material-symbols:chevron-right"
          size="small"
          variant="text"
          @click="move(7)"
        />
        <v-btn
          aria-label="On a window"
          color="neutral"
          icon="material-symbols:keyboard-double-arrow-right"
          size="small"
          variant="text"
          @click="move(span)"
        />
      </div>
      <div class="w-32">
        <SwitchRow v-model="compact" label="Compact" />
      </div>
    </div>

    <div class="overflow-x-auto">
      <div :class="compact ? 'min-w-[1100px]' : 'min-w-[860px]'" class="relative">
        <!-- The days. -->
        <div class="flex border-b border-line">
          <div class="w-11 flex-none" />
          <div :style="columns" class="relative grid flex-1">
            <div
              v-for="day in days"
              :key="day.at"
              :class="[day.weekend ? 'bg-warning/6' : '', day.today ? 'text-primary' : 'text-ink']"
              class="flex flex-col items-center justify-center gap-0.5 border-l border-line/60 py-3"
            >
              <span :class="compact ? 'text-xs' : 'text-base'" class="leading-5 font-medium">{{ day.day }}</span>
              <span v-if="!compact" class="text-[10px] tracking-wide text-ink/45 uppercase">{{ day.weekday }}</span>
            </div>
            <span
              v-if="nowAt > 0 && nowAt < 1"
              :style="{ left: `${nowAt * 100}%` }"
              class="absolute -bottom-2 z-10 -translate-x-1/2 rounded-(--ps-radius-x-small) bg-ink px-1.5 font-mono text-[10px] leading-4 text-surface"
            >{{ now.toLocaleTimeString('en', { hour: '2-digit', minute: '2-digit' }) }}</span>
          </div>
        </div>

        <!-- The whole game, day by day. -->
        <template v-if="!compact">
          <div
            v-for="row in STRIP"
            :key="row.key"
            class="flex border-b border-line/60"
          >
            <div class="flex w-11 flex-none items-center justify-center text-ink/45">
              <v-tooltip :text="row.label" location="right">
                <template #activator="{ props: tip }">
                  <Icon
                    v-bind="tip"
                    :aria-label="row.label"
                    :name="row.icon"
                    class="text-base"
                  />
                </template>
              </v-tooltip>
            </div>
            <div :style="columns" class="grid flex-1">
              <span
                v-for="day in days"
                :key="day.at"
                :class="[day.weekend ? 'bg-warning/6' : '', day.totals ? row.tone || 'text-ink/80' : 'text-ink/25']"
                class="border-l border-line/60 py-2.5 text-center font-mono text-xs"
              >{{ day.totals ? row.value(day.totals) : '–' }}</span>
            </div>
          </div>
        </template>

        <!-- The tests. -->
        <div class="flex">
          <div class="flex w-11 flex-none items-end justify-center border-r border-line/60 pb-3">
            <span class="flex rotate-180 items-center gap-1.5 text-[10px] font-semibold tracking-widest text-ink/45 uppercase [writing-mode:vertical-rl]">
              A/B tests
              <Icon aria-hidden="true" class="rotate-90 text-sm" name="material-symbols:science-outline" />
            </span>
          </div>
          <div class="relative flex-1">
            <!-- The day columns behind the bars. -->
            <div :style="columns" aria-hidden="true" class="absolute inset-0 grid">
              <span
                v-for="day in days"
                :key="day.at"
                :class="day.weekend ? 'bg-warning/6' : ''"
                class="border-l border-line/60"
              />
            </div>
            <span
              v-if="nowAt > 0 && nowAt < 1"
              :style="{ left: `${nowAt * 100}%` }"
              aria-hidden="true"
              class="absolute inset-y-0 z-10 w-px bg-ink"
            />

            <div :class="compact ? 'min-h-48 gap-1.5 py-3' : 'min-h-72 gap-2.5 py-4'" class="relative flex flex-col">
              <div
                v-for="(lane, index) in bars"
                :key="index"
                :style="columns"
                class="grid"
              >
                <v-tooltip
                  v-for="bar in lane"
                  :key="bar.experiment.id"
                  :text="outcome(bar.experiment)"
                  location="top"
                >
                  <template #activator="{ props: tip }">
                    <NuxtLink
                      v-bind="tip"
                      :class="[
                        compact ? 'h-7 text-[11px]' : 'h-10 text-[13px]',
                        bar.clippedStart ? '' : 'ml-0.5 rounded-l-(--ps-radius-small)',
                        bar.clippedEnd ? '' : 'mr-0.5 rounded-r-(--ps-radius-small)'
                      ]"
                      :style="barStyle(bar)"
                      :to="`/growth/experiments/${bar.experiment.id}`"
                      class="flex min-w-0 items-center gap-1.5 px-2.5 font-medium text-white no-underline shadow-sm transition-[filter] hover:brightness-110"
                    >
                      <Icon
                        v-if="bar.clippedStart"
                        aria-hidden="true"
                        class="flex-none text-sm opacity-80"
                        name="material-symbols:chevron-left"
                      />
                      <span class="truncate">{{ bar.label }}</span>
                    </NuxtLink>
                  </template>
                </v-tooltip>
              </div>
              <p v-if="!bars.length" class="m-0 py-10 text-center text-xs text-ink/50">
                No test ran in these days.
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-3 py-2 text-[11px] text-ink/55">
      <span class="flex items-center gap-1.5"><span class="h-2.5 w-5 rounded-sm bg-primary" />Running</span>
      <span class="flex items-center gap-1.5"><span class="h-2.5 w-5 rounded-sm bg-primary/40" />Still to run</span>
      <span class="flex items-center gap-1.5"><span class="h-2.5 w-5 rounded-sm bg-success" />A variant won</span>
      <span class="flex items-center gap-1.5"><span class="h-2.5 w-5 rounded-sm bg-ink/40" />No clear winner</span>
      <span class="flex items-center gap-1.5"><span class="h-2.5 w-5 rounded-sm bg-warning" />Stopped early</span>
    </div>
  </div>
</template>
