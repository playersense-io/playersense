<script setup lang="ts">
// Every event on a calendar of days, the way a live game is planned and reviewed: a column a
// day with the weekend shaded, the whole game's players, new players and revenue along the top
// so a busy weekend can be read against what the game did, and the events below in a band per
// kind (seasonal events, offers, tournaments, boosts, and whatever kinds the studio has), with
// the A/B tests running at the time in a band of their own, since a test and an event on the
// same days blur each other's result.
//
// A bar is placed to the hour, so a weekend that opens Friday at 18:00 starts three quarters
// into Friday. A repeating event is a bar per window; one that opens every day or every few
// hours is one bar across the days with a tick per window, because a two-hour happy hour drawn
// thirty times is noise. Past windows are solid, the one on now is ringed, what is scheduled is
// a tint, a draft is an outline and a paused event is hatched. Bars share a row where they do
// not overlap, so a band is only as tall as what ran at once; a band folds to a strip.
//
// Full screen is the same calendar in a dialog the size of the window: a month in view at
// once, or two compact, with the bands given the height they need.
const props = withDefaults(defineProps<{
  events: readonly LiveOpsEventDef[]
  experiments: readonly ExperimentDef[]
  /** Drawn in a full-screen dialog: more days, and the window's height. */
  fullscreen?: boolean
}>(), {
  fullscreen: false
})

const emit = defineEmits<{ expand: [], close: [] }>()

const now = useNow({ interval: 60_000 })

const compact = ref(false)
const span = computed(() => (props.fullscreen ? 35 : 21) * (compact.value ? 2 : 1))

/* Days shown before today: a third of the window, so what ran lately and what is due are both in view. */
const lead = computed(() => Math.round(span.value / 3))

const from = ref(startOfDay(Date.now()) - lead.value * DAY)
watch(compact, () => {
  from.value = startOfDay(Date.now()) - lead.value * DAY
})

/* Whole days, whatever the clocks did: a step across a change of hour lands on midnight still. */
function move(days: number) {
  from.value = startOfDay(from.value + days * DAY + 12 * 3_600_000)
}

function today() {
  from.value = startOfDay(Date.now()) - lead.value * DAY
}

const windowEnd = computed(() => from.value + span.value * DAY)

const days = computed(() => Array.from({ length: span.value }, (_, index) => {
  const at = startOfDay(from.value + index * DAY + 12 * 3_600_000)
  const date = new Date(at)
  const weekday = date.getDay()
  return {
    at,
    day: date.getDate(),
    month: date.getDate() === 1 || index === 0 ? date.toLocaleDateString('en', { month: 'short' }) : '',
    weekday: date.toLocaleDateString('en', { weekday: 'short' }),
    weekend: weekday === 0 || weekday === 6,
    today: at === startOfDay(now.value.getTime()),
    figures: sampleGameDay(at, props.events.filter(entry => entry.status !== 'draft'))
  }
}))

const rangeLabel = computed(() => {
  const first = new Date(from.value)
  const last = new Date(windowEnd.value - 1)
  const month = (date: Date) => date.toLocaleDateString('en', { month: 'short' })
  return `${month(first)} ${first.getDate()} to ${month(last)} ${last.getDate()}, ${last.getFullYear()}`
})

const columns = computed(() => ({ gridTemplateColumns: `repeat(${span.value}, minmax(0, 1fr))` }))

const at = (time: number) => (time - from.value) / (span.value * DAY)
const nowAt = computed(() => at(now.value.getTime()))

// --- The strip ----------------------------------------------------------------------------

const compactFigure = (value: number) => writeFigure(value, { compact: true })

/* A day well above or below the window's usual is coloured: the eye finds the weekend that paid. */
const typical = computed(() => {
  const known = days.value.map(day => day.figures).filter(Boolean) as DayFigures[]
  const mean = (key: LiveOpsMetricKey) => known.reduce((sum, figures) => sum + figures[key], 0) / Math.max(known.length, 1)
  return { players: mean('players'), new_players: mean('new_players'), revenue: mean('revenue') }
})

const STRIP: { key: 'players' | 'new_players' | 'revenue', icon: string, label: string, write: (value: number) => string }[] = [
  { key: 'players', icon: 'material-symbols:group-outline', label: 'Daily players', write: compactFigure },
  { key: 'new_players', icon: 'material-symbols:person-add-outline', label: 'New players', write: compactFigure },
  { key: 'revenue', icon: 'material-symbols:payments-outline', label: 'Revenue', write: value => `$${compactFigure(value)}` }
]

function stripTone(key: 'players' | 'new_players' | 'revenue', figures: DayFigures, today: boolean) {
  if (today) return 'text-ink/45'
  const ratio = figures[key] / (typical.value[key] || 1)
  if (ratio >= 1.08) return 'text-success'
  if (ratio <= 0.92) return 'text-error'
  return 'text-ink/80'
}

// --- The bands ----------------------------------------------------------------------------

type Look = 'ended' | 'live' | 'scheduled' | 'draft' | 'paused'

interface Bar {
  id: string
  to: string
  name: string
  figure: string
  tone: number
  start: number
  end: number
  look: Look
  /** Windows inside a rhythm bar, as shares of it. */
  ticks: { left: number, width: number }[] | null
  tip: { title: string, lines: string[], kind: string }
}

interface Band {
  id: string
  label: string
  icon: string
  tone: number
  lanes: Bar[][]
  count: number
}

const folded = ref<string[]>([])

function toggle(id: string) {
  folded.value = folded.value.includes(id) ? folded.value.filter(entry => entry !== id) : [...folded.value, id]
}

function lookOf(event: LiveOpsEventDef, window: Occurrence): Look {
  if (event.status === 'draft') return 'draft'
  const time = now.value.getTime()
  if (window.start > servedUntil(event) || (event.status === 'paused' && window.end > time)) return 'paused'
  if (window.end <= time) return 'ended'
  if (window.start <= time) return 'live'
  return 'scheduled'
}

/* Written out, so a class list is never assembled at run time. */
const LOOK_CLASS: Record<Look, string> = {
  ended: 'ps-liveops-bar--ended',
  live: 'ps-liveops-bar--live',
  scheduled: 'ps-liveops-bar--scheduled',
  draft: 'ps-liveops-bar--draft',
  paused: 'ps-liveops-bar--paused'
}

const LOOK_WORDS: Record<Look, string> = { ended: 'Ran', live: 'On now', scheduled: 'Scheduled', draft: 'Draft, not published', paused: 'Paused, not served' }

function figureOf(event: LiveOpsEventDef, window: Occurrence, look: Look): string {
  if (look === 'scheduled') return relativeWords(window.start, now.value.getTime())
  if (look !== 'ended' && look !== 'live') return ''
  const impact = sampleEventImpact(event, window, now.value.getTime())
  if (!impact) return ''
  return event.goal === 'revenue' || event.goal === 'arpdau' || event.kind === 'offer'
    ? `$${compactFigure(impact.revenue)}`
    : `${compactFigure(impact.players)} players`
}

function tipOf(event: LiveOpsEventDef, window: Occurrence, look: Look): Bar['tip'] {
  const lines = [windowWords(window), describeRule(event.audience) || 'Every player']
  if (event.rollout < FULL_ROLLOUT) lines[1] += `, ${formatRollout(event.rollout)} of them`
  if ((look === 'ended' || look === 'live') && event.goal) {
    const impact = sampleEventImpact(event, window, now.value.getTime())
    if (impact) {
      const metric = impact.metrics[event.goal]
      lines.push(`${LIVEOPS_METRICS[event.goal].label} ${writeOpsLift(event.goal, metric.comparison)} against expected${look === 'live' ? ' so far' : ''}`)
    }
  }
  return { title: event.name, kind: `${kindLook(event.kind).label} · ${LOOK_WORDS[look]}`, lines }
}

function pack(bars: Bar[]): Bar[][] {
  const lanes: Bar[][] = []
  const ends: number[] = []
  for (const bar of [...bars].sort((a, b) => a.start - b.start)) {
    const lane = ends.findIndex(end => end <= bar.start)
    if (lane === -1) {
      lanes.push([bar])
      ends.push(bar.end)
    } else {
      lanes[lane]!.push(bar)
      ends[lane] = bar.end
    }
  }
  return lanes
}

const bands = computed<Band[]>(() => {
  const time = now.value.getTime()
  const byKind = new Map<string, Bar[]>()
  const visible = props.events.filter(event => event.status !== 'archived' || servedWindows(event, from.value, windowEnd.value).length)

  for (const event of visible) {
    const look = kindLook(event.kind)
    const windows = occurrencesOf(event.schedule, from.value, windowEnd.value)
    if (!windows.length) continue
    const list = byKind.get(event.kind) ?? []
    const base = { tone: look.tone, to: `/growth/liveops/${event.key}` }

    if (isRhythm(event.schedule)) {
      const start = windows[0]!.start
      const end = windows.at(-1)!.end
      const length = end - start
      const state = event.status === 'draft' ? 'draft' : event.status === 'paused' ? 'paused' : end <= time ? 'ended' : start <= time ? 'live' : 'scheduled'
      list.push({
        ...base,
        id: `${event.key}:rhythm`,
        name: event.name,
        figure: scheduleWords(event.schedule).split(' for ')[0]!.replace('Every day at', 'daily'),
        start,
        end,
        look: state,
        ticks: windows.map(window => ({ left: (window.start - start) / length, width: (window.end - window.start) / length })),
        tip: { title: event.name, kind: `${look.label} · ${LOOK_WORDS[state]}`, lines: [scheduleWords(event.schedule), describeRule(event.audience) || 'Every player'] }
      })
    } else {
      for (const window of windows) {
        const state = lookOf(event, window)
        list.push({
          ...base,
          id: `${event.key}:${window.start}`,
          to: `/growth/liveops/${event.key}?at=${window.start}`,
          name: event.name,
          figure: figureOf(event, window, state),
          start: window.start,
          end: window.end,
          look: state,
          ticks: null,
          tip: tipOf(event, window, state)
        })
      }
    }
    byKind.set(event.kind, list)
  }

  const order = (kind: string) => {
    const index = KIND_ORDER.indexOf(kind)
    return index === -1 ? KIND_ORDER.length : index
  }
  const out: Band[] = [...byKind.entries()]
    .sort(([a], [b]) => order(a) - order(b) || a.localeCompare(b))
    .map(([kind, bars]) => {
      const look = kindLook(kind)
      return { id: kind, label: look.lane, icon: look.icon, tone: look.tone, lanes: pack(bars), count: new Set(bars.map(bar => bar.id.split(':')[0])).size }
    })

  // The tests running on the same days, drawn plainer: they are here to be seen, not read.
  const tests: Bar[] = props.experiments
    .filter(test => test.started_at)
    .map((test) => {
      const start = Date.parse(test.started_at!)
      const end = test.ended_at ? Date.parse(test.ended_at) : Math.max(start + test.plannedDays * DAY, time)
      const running = test.status === 'running'
      return {
        id: test.id,
        to: `/growth/experiments/${test.id}`,
        name: test.name,
        figure: running ? 'running' : EXPERIMENT_STATUS[test.status].label.toLowerCase(),
        tone: 0,
        start,
        end,
        look: (running ? 'live' : 'ended') as Look,
        ticks: null,
        tip: { title: test.name, kind: `A/B test · ${EXPERIMENT_STATUS[test.status].label}`, lines: [whenWords(test), test.audience] }
      }
    })
    .filter(bar => bar.end > from.value && bar.start < windowEnd.value)
  if (tests.length) out.push({ id: 'ab-tests', label: 'A/B tests', icon: 'material-symbols:science-outline', tone: 0, lanes: pack(tests), count: tests.length })
  return out
})

function barStyle(bar: Bar) {
  const left = Math.max(0, at(bar.start))
  const right = Math.min(1, at(bar.end))
  const colour = bar.tone ? toneColour(bar.tone) : 'color-mix(in srgb, rgb(var(--v-theme-on-surface)) 50%, rgb(var(--v-theme-surface)))'
  const style: Record<string, string> = { 'left': `${left * 100}%`, 'width': `max(${(right - left) * 100}%, 6px)`, '--bar': colour }
  return style
}

const clipped = (bar: Bar) => ({ start: bar.start < from.value, end: bar.end > windowEnd.value })

/* A rhythm bar's ticks, placed against the part of it in view. */
function tickStyle(bar: Bar, tick: { left: number, width: number }) {
  const length = bar.end - bar.start
  const shownStart = Math.max(bar.start, from.value)
  const shownEnd = Math.min(bar.end, windowEnd.value)
  const shown = shownEnd - shownStart
  const start = bar.start + tick.left * length
  return { left: `${((start - shownStart) / shown) * 100}%`, width: `max(${(tick.width * length / shown) * 100}%, 2px)` }
}

const hasTicksInView = (bar: Bar) => bar.ticks?.some((tick) => {
  const start = bar.start + tick.left * (bar.end - bar.start)
  return start < windowEnd.value && start >= from.value - DAY
})
</script>

<template>
  <div :class="fullscreen ? 'h-full' : 'rounded-(--ps-radius-large) border border-line'" class="flex flex-col overflow-hidden">
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
      <div class="flex items-center gap-3">
        <div class="w-32">
          <SwitchRow v-model="compact" label="Compact" />
        </div>
        <v-btn
          v-if="fullscreen"
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:close-fullscreen"
          size="small"
          variant="outlined"
          @click="emit('close')"
        >
          Exit full screen
        </v-btn>
        <v-btn
          v-else
          aria-label="Full screen"
          color="neutral"
          icon="material-symbols:open-in-full"
          size="small"
          variant="text"
          @click="emit('expand')"
        />
      </div>
    </div>

    <div :class="fullscreen ? 'min-h-0 flex-1 overflow-auto' : 'overflow-x-auto'">
      <div :class="[compact ? 'min-w-[1100px]' : 'min-w-[900px]', fullscreen ? 'flex min-h-full flex-col' : '']" class="relative">
        <!-- The days. -->
        <div class="flex border-b border-line">
          <div class="w-11 flex-none" />
          <div :style="columns" class="grid flex-1">
            <div
              v-for="day in days"
              :key="day.at"
              :class="[day.weekend ? 'bg-warning/6' : '', day.today ? 'text-primary' : 'text-ink']"
              class="flex items-baseline justify-center gap-1 border-l border-line/60 py-1.5"
            >
              <span v-if="day.month" class="text-[10px] font-semibold tracking-wide text-ink/50 uppercase">{{ day.month }}</span>
              <span :class="[compact ? 'text-xs' : 'text-sm', day.today ? 'rounded-full bg-primary px-1.5 text-on-primary' : '']" class="font-medium">{{ day.day }}</span>
              <span v-if="!compact && !day.month" class="text-[10px] tracking-wide text-ink/45 uppercase">{{ day.weekday }}</span>
            </div>
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
              <v-tooltip :text="`${row.label}, the whole game. Coloured where a day is well above or below the others in view.`" location="right" max-width="260">
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
                :class="[day.weekend ? 'bg-warning/6' : '', day.figures ? stripTone(row.key, day.figures, day.today) : 'text-ink/25']"
                class="border-l border-line/60 py-2 text-center font-mono text-xs"
              >{{ day.figures ? row.write(day.figures[row.key]) : '–' }}</span>
            </div>
          </div>
        </template>

        <!-- The bands. Full screen, they reach the foot of the window, the days running on below them. -->
        <div :class="fullscreen ? 'flex flex-1 flex-col' : ''" class="relative">
          <span
            v-if="nowAt > 0 && nowAt < 1"
            :style="{ left: `calc(2.75rem + (100% - 2.75rem) * ${nowAt})` }"
            aria-hidden="true"
            class="pointer-events-none absolute inset-y-0 z-20 w-px bg-ink"
          >
            <span class="absolute -top-px left-1/2 -translate-x-1/2 rounded-b-(--ps-radius-x-small) bg-ink px-1 font-mono text-[9px] leading-3.5 text-surface">{{ now.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }) }}</span>
          </span>

          <section
            v-for="band in bands"
            :key="band.id"
            :aria-label="band.label"
            class="flex flex-col border-b border-line last:border-b-0"
          >
            <!-- The band's name across its top, which is also the fold. -->
            <button
              :aria-expanded="!folded.includes(band.id)"
              class="sticky left-0 flex h-6 w-full cursor-pointer items-center gap-1.5 border-b border-line/60 bg-ink/2 px-3 text-left transition-colors duration-150 hover:bg-ink/4 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary"
              type="button"
              @click="toggle(band.id)"
            >
              <Icon
                :name="folded.includes(band.id) ? 'material-symbols:chevron-right' : 'material-symbols:expand-more'"
                aria-hidden="true"
                class="flex-none text-sm text-ink/45"
              />
              <Icon
                :name="band.icon"
                :style="{ color: band.tone ? toneColour(band.tone) : undefined }"
                aria-hidden="true"
                class="flex-none text-sm"
              />
              <span class="text-[10px] font-semibold tracking-widest text-ink/60 uppercase">{{ band.label }}</span>
              <span class="font-mono text-[10px] text-ink/40">{{ band.count }}</span>
            </button>

            <div class="flex">
              <div class="w-11 flex-none border-r border-line/60" />

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

                <!-- Folded: every bar as a sliver in one row, so where things ran still shows. -->
                <div v-if="folded.includes(band.id)" class="relative flex h-full min-h-7 items-center">
                  <div class="relative h-2 w-full">
                    <span
                      v-for="bar in band.lanes.flat()"
                      :key="bar.id"
                      :style="barStyle(bar)"
                      class="ps-liveops-bar absolute inset-y-0 rounded-full"
                      :class="LOOK_CLASS[bar.look]"
                    />
                  </div>
                </div>

                <div v-else :class="compact ? 'gap-0.5 py-1.5' : 'gap-1 py-2'" class="relative flex flex-col">
                  <div
                    v-for="(lane, index) in band.lanes"
                    :key="index"
                    :class="compact ? 'h-[18px]' : 'h-6'"
                    class="relative"
                  >
                    <v-tooltip
                      v-for="bar in lane"
                      :key="bar.id"
                      location="top"
                      max-width="320"
                      open-delay="150"
                    >
                      <template #activator="{ props: tip }">
                        <NuxtLink
                          v-bind="tip"
                          :class="[
                            LOOK_CLASS[bar.look],
                            compact ? 'text-[10.5px]' : 'text-xs',
                            clipped(bar).start ? '' : 'rounded-l-(--ps-radius-small)',
                            clipped(bar).end ? '' : 'rounded-r-(--ps-radius-small)'
                          ]"
                          :style="barStyle(bar)"
                          :to="bar.to"
                          class="ps-liveops-bar absolute inset-y-0 flex min-w-0 items-center gap-1.5 overflow-hidden px-2 leading-none font-medium no-underline transition-[filter] hover:brightness-110"
                        >
                          <template v-if="bar.ticks && hasTicksInView(bar)">
                            <span
                              v-for="(tick, tickIndex) in bar.ticks"
                              :key="tickIndex"
                              :style="tickStyle(bar, tick)"
                              aria-hidden="true"
                              class="ps-liveops-bar__tick absolute inset-y-[3px] rounded-[2px]"
                            />
                          </template>
                          <Icon
                            v-if="clipped(bar).start"
                            aria-hidden="true"
                            class="relative flex-none text-sm opacity-80"
                            name="material-symbols:chevron-left"
                          />
                          <span v-if="bar.look === 'live'" aria-hidden="true" class="ps-liveops-bar__pulse relative size-1.5 flex-none rounded-full" />
                          <Icon
                            v-else-if="bar.look === 'scheduled' && !compact"
                            aria-hidden="true"
                            class="relative flex-none text-[13px]"
                            name="material-symbols:schedule-outline"
                          />
                          <span class="relative min-w-6 truncate">{{ bar.name }}</span>
                          <span v-if="bar.figure && !compact" class="relative min-w-0 shrink-[100] truncate font-mono text-[10.5px] font-normal opacity-80">{{ bar.figure }}</span>
                        </NuxtLink>
                      </template>
                      <div class="flex flex-col gap-1 py-0.5">
                        <span class="text-[10px] tracking-wide uppercase opacity-70">{{ bar.tip.kind }}</span>
                        <span class="text-xs font-medium">{{ bar.tip.title }}</span>
                        <span v-for="(line, lineIndex) in bar.tip.lines" :key="lineIndex" class="text-[11px] opacity-85">{{ line }}</span>
                      </div>
                    </v-tooltip>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <p v-if="!bands.length" class="m-0 py-12 text-center text-xs text-ink/50">
            Nothing is scheduled in these days.
          </p>

          <div v-if="fullscreen" aria-hidden="true" class="flex flex-1">
            <div class="w-11 flex-none border-r border-line/60" />
            <div :style="columns" class="grid flex-1">
              <span
                v-for="day in days"
                :key="day.at"
                :class="day.weekend ? 'bg-warning/6' : ''"
                class="border-l border-line/60"
              />
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-3 py-2 text-[11px] text-ink/55">
      <span class="flex items-center gap-1.5"><span class="ps-liveops-bar ps-liveops-bar--ended h-2.5 w-5 rounded-sm" style="--bar: rgb(var(--v-theme-chart-2))" />Ran</span>
      <span class="flex items-center gap-1.5"><span class="ps-liveops-bar ps-liveops-bar--live h-2.5 w-5 rounded-sm" style="--bar: rgb(var(--v-theme-chart-2))" />On now</span>
      <span class="flex items-center gap-1.5"><span class="ps-liveops-bar ps-liveops-bar--scheduled h-2.5 w-5 rounded-sm" style="--bar: rgb(var(--v-theme-chart-2))" />Scheduled</span>
      <span class="flex items-center gap-1.5"><span class="ps-liveops-bar ps-liveops-bar--draft h-2.5 w-5 rounded-sm" style="--bar: rgb(var(--v-theme-chart-2))" />Draft</span>
      <span class="flex items-center gap-1.5"><span class="ps-liveops-bar ps-liveops-bar--paused h-2.5 w-5 rounded-sm" style="--bar: rgb(var(--v-theme-chart-2))" />Paused</span>
      <span class="flex items-center gap-1.5"><span class="h-2.5 w-px bg-ink" />Now</span>
      <span class="ml-auto">Colour is the event's kind. Figures are players reached, or revenue for an offer.</span>
    </div>
  </div>
</template>

<style scoped>
/* A bar's look says where it is in time; its colour, `--bar`, says what kind it is. */
.ps-liveops-bar--ended {
  background: var(--bar);
  color: white;
}

.ps-liveops-bar--live {
  background: var(--bar);
  box-shadow: 0 0 0 1.5px rgb(var(--v-theme-surface)), 0 0 0 3px var(--bar);
  color: white;
}

/* Still to come: the kind's colour, lightly, with a hairline of it all round and a clock, so it
   reads as the same event not yet here, and never as a draft's empty outline. */
.ps-liveops-bar--scheduled {
  background: color-mix(in srgb, var(--bar) 12%, rgb(var(--v-theme-surface)));
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--bar) 45%, transparent);
  color: color-mix(in srgb, var(--bar) 75%, rgb(var(--v-theme-on-surface)));
}

.ps-liveops-bar--draft {
  background: transparent;
  border: 1.5px dashed color-mix(in srgb, var(--bar) 70%, transparent);
  color: color-mix(in srgb, var(--bar) 70%, rgb(var(--v-theme-on-surface)));
}

.ps-liveops-bar--paused {
  background: repeating-linear-gradient(135deg, color-mix(in srgb, var(--bar) 22%, rgb(var(--v-theme-surface))) 0 6px, rgb(var(--v-theme-surface)) 6px 11px);
  border: 1px solid color-mix(in srgb, var(--bar) 35%, transparent);
  color: rgba(var(--v-theme-on-surface), 0.6);
}

/* A rhythm bar is a tint with its windows solid inside it. */
.ps-liveops-bar:has(.ps-liveops-bar__tick).ps-liveops-bar--ended,
.ps-liveops-bar:has(.ps-liveops-bar__tick).ps-liveops-bar--live {
  background: color-mix(in srgb, var(--bar) 22%, rgb(var(--v-theme-surface)));
  color: color-mix(in srgb, var(--bar) 60%, rgb(var(--v-theme-on-surface)));
}

.ps-liveops-bar__tick {
  background: var(--bar);
  opacity: 0.85;
}

.ps-liveops-bar__pulse {
  background: white;
  animation: ps-liveops-pulse 1.6s ease-in-out infinite;
}

@keyframes ps-liveops-pulse {
  50% {
    opacity: 0.35;
  }
}

@media (prefers-reduced-motion: reduce) {
  .ps-liveops-bar__pulse {
    animation: none;
  }
}
</style>
