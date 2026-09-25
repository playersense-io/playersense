<script setup lang="ts">
/**
 * DateRangePicker to the minute: the same button, the same rail of presets, the same two months
 * and footer, with DateTimeField's columns of hours and minutes beside them for the start and the
 * end. For pages that read time rather than days, where the last fifteen minutes is a question
 * and a calendar day is too coarse an answer: Cloud Logging's explorer and issues.
 *
 * It behaves as the day picker does. **Nothing is applied until Apply**: a preset, a span on the
 * calendar and a time all stage, and Cancel puts every one back. A preset is relative (it follows
 * the present) and a span is fixed, so choosing one drops the other, and the button says which is
 * on screen.
 *
 * Times are the reader's own clock, as the calendar's days are: a person reading logs thinks in
 * the hour on their wall, and the API is sent the instant it names.
 */
export interface TimePreset {
  value: string
  title: string
  hint: string
  /** How far back from now it starts. */
  span: number
}

const props = withDefaults(defineProps<{
  from: number
  to: number
  /** The preset on screen, or null for a fixed span. */
  preset: string | null
  presets: readonly TimePreset[]
  /** The oldest moment there is anything to read. */
  earliest?: number
  /** The longest span the page reads, in milliseconds. */
  maxSpan?: number
  loading?: boolean
}>(), { earliest: 0, maxSpan: 90 * 86_400_000, loading: false })

const emit = defineEmits<{
  preset: [value: string]
  apply: [from: number, to: number]
}>()

const open = ref(false)
const pad = (value: number) => String(value).padStart(2, '0')

// --- Words --------------------------------------------------------------------------------------

const dayWords = (at: number, withYear: boolean) => new Date(at).toLocaleDateString('en', { month: 'short', day: 'numeric', ...(withYear ? { year: 'numeric' } : {}) })
const clockWords = (at: number) => `${pad(new Date(at).getHours())}:${pad(new Date(at).getMinutes())}`

/** A span in words: "Sep 24, 14:05 to 16:30", the second day only when it differs. */
function spanWords(from: number, to: number): string {
  const thisYear = new Date().getFullYear()
  const withYear = new Date(from).getFullYear() !== thisYear || new Date(to).getFullYear() !== thisYear
  const sameDay = new Date(from).toDateString() === new Date(to).toDateString()
  return sameDay
    ? `${dayWords(from, withYear)}, ${clockWords(from)} to ${clockWords(to)}`
    : `${dayWords(from, withYear)}, ${clockWords(from)} to ${dayWords(to, withYear)}, ${clockWords(to)}`
}

function durationWords(ms: number): string {
  const minutes = Math.round(ms / 60_000)
  if (minutes < 120) return `${minutes} ${minutes === 1 ? 'minute' : 'minutes'}`
  const hours = Math.round(minutes / 60)
  if (hours < 72) return `${hours} hours`
  return `${Math.round(hours / 24)} days`
}

const label = computed(() => props.presets.find(entry => entry.value === props.preset)?.title ?? spanWords(props.from, props.to))

// --- The draft ----------------------------------------------------------------------------------

/** What Apply would write: a preset, or a start and an end. */
const pendingPreset = ref<string | null>(null)
const start = ref(0)
const end = ref(0)
/** Which end the time columns set. */
const editing = ref<'start' | 'end'>('start')

/*
 * The calendar's own selection, which it owns while a span is drawn: one day after the first click,
 * two after the second. Bound back as it came rather than rebuilt from the start and the end,
 * because a rebuilt value always has both ends, and the calendar reads a value with both ends as a
 * finished span, so the second click would start a new one instead of ending this one.
 */
const draft = ref<Date[]>([])

function seed() {
  pendingPreset.value = props.preset
  start.value = props.from
  end.value = props.to
  draft.value = [midnight(props.from), midnight(props.to)]
  editing.value = 'start'
}
watch(open, (value) => {
  if (value) seed()
})

function choosePreset(value: string) {
  const preset = props.presets.find(entry => entry.value === value)
  if (!preset) return
  pendingPreset.value = value
  end.value = Date.now()
  start.value = end.value - preset.span
  draft.value = [midnight(start.value), midnight(end.value)]
}

const midnight = (at: number) => {
  const d = new Date(at)
  return new Date(d.getFullYear(), d.getMonth(), d.getDate())
}

function onCalendar(value: unknown) {
  const days = (Array.isArray(value) ? value : []).filter((entry): entry is Date => entry instanceof Date)
  const first = days[0]
  const last = days.at(-1)
  draft.value = days
  if (!first || !last) return
  pendingPreset.value = null
  // Days picked are whole days, the first from midnight and the last to its final minute (or to
  // now, when it is today); the time columns narrow either end afterwards. After the first click
  // the span is that one day, until the second click ends it.
  const endOfDay = (day: Date) => Math.min(new Date(day.getFullYear(), day.getMonth(), day.getDate(), 23, 59).getTime(), Date.now())
  start.value = midnight(first.getTime()).getTime()
  end.value = endOfDay(last)
}

const HOURS = Array.from({ length: 24 }, (_, hour) => hour)
const editedAt = computed(() => (editing.value === 'start' ? start.value : end.value))

/* Every five minutes, and the end's own minute when it falls between them. */
const minutes = computed(() => {
  const list = Array.from({ length: 12 }, (_, index) => index * 5)
  const own = new Date(editedAt.value).getMinutes()
  return list.includes(own) ? list : [...list, own].sort((a, b) => a - b)
})

function setTime(hour: number | null, minute: number | null) {
  const d = new Date(editedAt.value)
  const next = new Date(d.getFullYear(), d.getMonth(), d.getDate(), hour ?? d.getHours(), minute ?? d.getMinutes()).getTime()
  pendingPreset.value = null
  if (editing.value === 'start') start.value = next
  else end.value = next
}

const hoursList = useTemplateRef<HTMLElement>('hours')
const minutesList = useTemplateRef<HTMLElement>('minutes')

/* The chosen hour and minute are brought into the middle of their columns, on opening and when
   the other end is taken up. */
watch([open, editing], async ([value]) => {
  if (!value) return
  await nextTick()
  for (const list of [hoursList.value, minutesList.value]) {
    const chosen = list?.querySelector<HTMLElement>('[aria-pressed="true"]')
    if (list && chosen) list.scrollTop = chosen.offsetTop - list.clientHeight / 2 + chosen.clientHeight / 2
  }
})

// --- Apply --------------------------------------------------------------------------------------

const min = computed(() => (props.earliest ? midnight(props.earliest) : undefined))
const max = computed(() => midnight(Date.now()))

const problem = computed(() => {
  if (pendingPreset.value) return ''
  if (start.value >= end.value) return 'The start is after the end.'
  if (end.value - start.value > props.maxSpan) return `A range covers at most ${durationWords(props.maxSpan)}.`
  if (start.value > Date.now()) return 'The start is in the future.'
  return ''
})

const note = computed(() => {
  const preset = props.presets.find(entry => entry.value === pendingPreset.value)
  if (preset) return preset.hint
  if (draft.value.length === 1) return `From ${spanWords(start.value, end.value)}. Pick the last day, or Apply for this one.`
  return problem.value || `${durationWords(end.value - start.value)}, ${spanWords(start.value, end.value)}.`
})

const changed = computed(() => (pendingPreset.value
  ? pendingPreset.value !== props.preset
  : props.preset !== null || start.value !== props.from || end.value !== props.to))

function apply() {
  if (problem.value || !changed.value) return
  if (pendingPreset.value) emit('preset', pendingPreset.value)
  else emit('apply', start.value, Math.min(end.value, Date.now()))
  open.value = false
}

function cancel() {
  seed()
  open.value = false
}
</script>

<template>
  <v-btn
    :aria-label="`Time shown: ${label}`"
    :disabled="loading"
    append-icon="material-symbols:unfold-more"
    class="ps-toolbar-control text-xs font-medium"
    color="neutral"
    prepend-icon="material-symbols:date-range-outline"
    size="small"
    variant="text"
  >
    {{ label }}

    <v-menu
      v-model="open"
      :close-on-content-click="false"
      :offset="4"
      activator="parent"
      location="bottom end"
    >
      <v-card
        border
        class="ps-range__card flex"
        color="surface"
        flat
      >
        <div class="flex w-44 flex-none flex-col gap-0.5 p-1.5">
          <v-btn
            v-for="entry in presets"
            :key="entry.value"
            :aria-label="`${entry.title}. ${entry.hint}`"
            :aria-pressed="pendingPreset === entry.value"
            :class="pendingPreset === entry.value ? 'bg-ink/8 text-ink' : 'text-ink/70 hover:text-ink'"
            class="justify-start text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="choosePreset(entry.value)"
          >
            {{ entry.title }}
          </v-btn>
        </div>

        <v-divider class="self-stretch" vertical />

        <v-date-range-picker
          :first-day-of-week="1"
          :max="max"
          :min="min"
          :model-value="draft"
          :weeks-in-month="'dynamic'"
          class="ps-range__picker min-w-0"
          color="primary"
          hide-header
          independent-months
          @update:model-value="onCalendar"
        >
          <template #footer>
            <p :class="problem ? 'text-warning' : 'text-ink/50'" class="m-0 text-[11px] leading-4">
              {{ note }}
            </p>
            <v-spacer />
            <v-btn
              class="text-xs font-medium text-ink/70 hover:text-ink"
              size="small"
              variant="text"
              @click="cancel"
            >
              Cancel
            </v-btn>
            <v-btn
              :disabled="!!problem || !changed"
              class="text-xs font-medium"
              color="primary"
              size="small"
              variant="text"
              @click="apply"
            >
              Apply
            </v-btn>
          </template>
        </v-date-range-picker>

        <v-divider class="self-stretch" vertical />

        <!-- The times: which end, then its hour and minute, each a column to pick from. -->
        <div class="flex w-40 flex-none flex-col">
          <div aria-label="Which end the time sets" class="grid grid-cols-2 border-b border-line" role="group">
            <button
              v-for="end_ in (['start', 'end'] as const)"
              :key="end_"
              :aria-pressed="editing === end_"
              :class="editing === end_ ? 'bg-primary/8 text-ink' : 'text-ink/55 hover:bg-ink/5'"
              class="flex cursor-pointer flex-col items-center gap-0.5 py-2 transition-colors duration-100"
              type="button"
              @click="editing = end_"
            >
              <span class="text-[10px] font-medium tracking-wide uppercase">{{ end_ === 'start' ? 'From' : 'To' }}</span>
              <span class="font-mono text-base">{{ clockWords(end_ === 'start' ? start : end) }}</span>
            </button>
          </div>
          <div class="flex min-h-0 flex-1">
            <ul ref="hours" aria-label="Hour" class="m-0 h-64 flex-1 list-none overflow-y-auto border-r border-line/60 p-1">
              <li v-for="hour in HOURS" :key="hour">
                <button
                  :aria-pressed="new Date(editedAt).getHours() === hour"
                  :class="new Date(editedAt).getHours() === hour ? 'bg-primary text-on-primary' : 'text-ink/75 hover:bg-ink/5'"
                  class="w-full cursor-pointer rounded-(--ps-radius-small) py-1.5 font-mono text-xs transition-colors duration-100 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary"
                  type="button"
                  @click="setTime(hour, null)"
                >
                  {{ pad(hour) }}
                </button>
              </li>
            </ul>
            <ul ref="minutes" aria-label="Minute" class="m-0 h-64 flex-1 list-none overflow-y-auto p-1">
              <li v-for="minute in minutes" :key="minute">
                <button
                  :aria-pressed="new Date(editedAt).getMinutes() === minute"
                  :class="new Date(editedAt).getMinutes() === minute ? 'bg-primary text-on-primary' : 'text-ink/75 hover:bg-ink/5'"
                  class="w-full cursor-pointer rounded-(--ps-radius-small) py-1.5 font-mono text-xs transition-colors duration-100 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary"
                  type="button"
                  @click="setTime(null, minute)"
                >
                  {{ pad(minute) }}
                </button>
              </li>
            </ul>
          </div>
        </div>
      </v-card>
    </v-menu>
  </v-btn>
</template>
