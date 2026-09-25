<script setup lang="ts">
// A new event, or an edit to one, as a page: the form down the left in the order somebody thinks
// it through (what it is, when it is on, who gets it, what the game receives, what it should
// move), and down the right, held in view as the form scrolls, the event as it stands, what is
// still missing before it can be saved, and the buttons that save, reset and leave.
//
// The schedule is the part people get wrong, so it is set the way it is said ("on for 3 days,
// every 7 days", "every Friday and Saturday", "every year, ending after 5 times") and read back
// as a sentence and as the actual dates of the next windows, which is the only check anybody
// trusts. One event with a repeat stands for every New Year there will ever be: nobody should
// have to make twelve events a year by hand, and a studio that has to will eventually miss one.
const props = defineProps<{
  /** The event being edited. Absent is a new one. */
  event?: LiveOpsEventDef | null
  /** For a new event: the day it starts on, as a day on the calendar was chosen. */
  startDay?: number | null
}>()

/** `reset` asks the page to draw the form again from what was saved; `cancel` to leave it. */
const emit = defineEmits<{ saved: [key: string], reset: [], cancel: [] }>()

const { create, update, keyProblem } = useLiveOps()
const { segments } = useLiveConfig()

const editing = computed(() => Boolean(props.event))

// --- What it is ---------------------------------------------------------------------------

const name = ref(props.event?.name ?? '')
const key = ref(props.event?.key ?? '')
const keyTouched = ref(editing.value)
const description = ref(props.event?.description ?? '')
const kind = ref(props.event?.kind ?? 'seasonal_event')
const customKind = ref(props.event && !KIND_PRESETS[props.event.kind] ? props.event.kind : '')

const slug = (text: string) => text.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '').slice(0, 64)

watch(name, (value) => {
  if (!keyTouched.value) key.value = slug(value)
})

const keyError = computed(() => (editing.value || !key.value ? null : keyProblem(key.value)))

const KIND_CHOICES = computed(() => [
  ...KIND_ORDER.map(value => ({ value, look: KIND_PRESETS[value]! })),
  { value: 'other', look: { ...kindLook(customKind.value || 'other'), label: 'Other', note: 'A kind of your own: its own word, colour and calendar band.' } }
])

const chosenKind = computed(() => (KIND_PRESETS[kind.value] ? kind.value : 'other'))
const kindKey = computed(() => (chosenKind.value === 'other' ? slug(customKind.value) : kind.value))

function chooseKind(value: string) {
  const before = kindKey.value
  kind.value = value === 'other' ? (customKind.value || 'other') : value
  // A new event follows its kind's template until somebody has changed the payload or the goal.
  if (!editing.value && value !== 'other') {
    if (sameJson(payload.value, kindLook(before).payload)) payload.value = JSON.parse(JSON.stringify(KIND_PRESETS[value]!.payload)) as Json
    if (goal.value === kindLook(before).goal) goal.value = KIND_PRESETS[value]!.goal
  }
}

watch(customKind, (value) => {
  if (chosenKind.value === 'other') kind.value = value || 'other'
})

// --- When ---------------------------------------------------------------------------------

/* The day before the first one offered: today, or the eve of the day chosen on the calendar, so
   the start and every preset (the next Friday, the next 1st) count from that day. */
const now = props.startDay ? props.startDay - DAY : Date.now()

/* The first day, from midnight: an event starts at the top of its day unless it is set otherwise. */
const nextHour = () => {
  const at = new Date(now + 24 * 3_600_000)
  at.setHours(0, 0, 0, 0)
  return at.getTime()
}

const repeat = ref<RepeatDraft>(props.event
  ? draftFromSchedule(props.event.schedule)
  : { repeats: false, every: 1, unit: 'weeks', countFrom: 'start', weekdays: [], start: localInput(nextHour()), length: 3, lengthUnit: 'days', ends: 'never', until: '', times: 10, timezone: 'UTC' })

const patchRepeat = (next: Partial<RepeatDraft>) => {
  repeat.value = { ...repeat.value, ...next }
}

/** The next occurrence of a weekday at a clock time, from tomorrow. */
const nextWeekday = (weekday: number, hour: number) => {
  const at = new Date(now)
  at.setDate(at.getDate() + 1 + ((weekday - at.getDay() - 1 + 7) % 7))
  at.setHours(hour, 0, 0, 0)
  return localInput(at.getTime())
}

const nextMonthStart = () => {
  const at = new Date(now)
  at.setMonth(at.getMonth() + 1, 1)
  at.setHours(0, 0, 0, 0)
  return localInput(at.getTime())
}

const nextNewYear = () => localInput(new Date(new Date(now).getFullYear(), 11, 30, 0, 0).getTime())

/* The shapes most events take, a click each. Every field stays editable afterwards. */
const PRESETS: { id: string, label: string, icon: string, draft: () => Partial<RepeatDraft> }[] = [
  { id: 'once', label: 'One time', icon: 'material-symbols:event-outline', draft: () => ({ repeats: false }) },
  { id: 'weekend', label: 'Every weekend', icon: 'material-symbols:weekend-outline', draft: () => ({ repeats: true, unit: 'weeks', every: 1, countFrom: 'start', weekdays: [5], start: nextWeekday(5, 18), length: 54, lengthUnit: 'hours' }) },
  { id: 'daily', label: 'Every day', icon: 'material-symbols:today-outline', draft: () => ({ repeats: true, unit: 'days', every: 1, countFrom: 'start', length: 2, lengthUnit: 'hours', start: `${repeat.value.start.slice(0, 10)}T20:00` }) },
  { id: 'monthly', label: 'Start of every month', icon: 'material-symbols:calendar-month-outline', draft: () => ({ repeats: true, unit: 'months', every: 1, countFrom: 'start', start: nextMonthStart(), length: 5, lengthUnit: 'days' }) },
  { id: 'yearly', label: 'Every year', icon: 'material-symbols:celebration-outline', draft: () => ({ repeats: true, unit: 'years', every: 1, countFrom: 'start', start: nextNewYear(), length: 5, lengthUnit: 'days' }) },
  { id: 'cycle', label: 'On, then off', icon: 'material-symbols:event-repeat-outline', draft: () => ({ repeats: true, unit: 'days', every: 7, countFrom: 'end', length: 3, lengthUnit: 'days' }) }
]

const schedule = computed(() => scheduleFromDraft(repeat.value))
const problem = computed(() => (Number.isFinite(Date.parse(repeat.value.start)) ? scheduleProblem(schedule.value) : 'A schedule needs a start.'))

const preview = computed(() => {
  if (problem.value) return []
  const anchor = Date.parse(schedule.value.anchor)
  return occurrencesOf(schedule.value, anchor, anchor + 12 * 366 * DAY).slice(0, 6)
})

/* How many windows, in words: "One window", "12 windows in all", "Repeats until stopped". */
const windowCount = computed(() => {
  if (problem.value) return ''
  if (schedule.value.mode === 'once') return 'One window'
  if (!schedule.value.until) return 'Repeats until stopped'
  const anchor = Date.parse(schedule.value.anchor)
  const count = occurrencesOf(schedule.value, anchor, Date.parse(schedule.value.until) + 400 * DAY).length
  return `${count} ${count === 1 ? 'window' : 'windows'} in all`
})

/* A window's two ends for a row: "Fri 00:00" and "Sun, Sep 27, 23:59", the date only where it moves. */
function windowEdges(window: Occurrence) {
  const start = new Date(window.start)
  const last = new Date(lastMinute(window))
  const time = (date: Date) => date.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
  const sameDay = start.toDateString() === last.toDateString()
  return {
    start: `${start.toLocaleDateString('en', { weekday: 'short' })} ${time(start)}`,
    end: sameDay ? time(last) : `${last.toLocaleDateString('en', { weekday: 'short', month: 'short', day: 'numeric' })}, ${time(last)}`
  }
}

/* Which preset the draft is, when it is one, so the chip stays lit while it is edited within it. */
const activePreset = computed(() => {
  const draft = repeat.value
  if (!draft.repeats) return 'once'
  if (draft.countFrom === 'end') return 'cycle'
  if (draft.unit === 'years') return 'yearly'
  if (draft.unit === 'months') return 'monthly'
  if (draft.unit === 'days') return 'daily'
  if (draft.unit === 'weeks' && draft.weekdays.length === 1 && draft.weekdays[0] === 5) return 'weekend'
  return ''
})

const UNITS: { value: RepeatUnit, one: string, many: string }[] = [
  { value: 'hours', one: 'hour', many: 'hours' },
  { value: 'days', one: 'day', many: 'days' },
  { value: 'weeks', one: 'week', many: 'weeks' },
  { value: 'months', one: 'month', many: 'months' },
  { value: 'years', one: 'year', many: 'years' }
]
const UNIT_ITEMS = computed(() => UNITS.map(unit => ({ value: unit.value, title: repeat.value.every === 1 ? unit.one : unit.many })))
/* A unit in a dropdown is a label, so it is capitalised: "Hours", or "Hour" for one. */
const LENGTH_ITEMS = computed(() => (['minutes', 'hours', 'days'] as const).map((value) => {
  const word = repeat.value.length === 1 ? value.slice(0, -1) : value
  return { value, title: word.charAt(0).toUpperCase() + word.slice(1) }
}))

const countable = computed(() => ['hours', 'days', 'weeks'].includes(repeat.value.unit))

/* The two readings of "every 7 days", each with its own first dates, so the choice is made on dates. */
const COUNT_FROM = computed<ChoiceOption[]>(() => {
  const example = (countFrom: 'start' | 'end') => {
    const compiled = scheduleFromDraft({ ...repeat.value, countFrom, ends: 'never' })
    if (scheduleProblem(compiled)) return 'The event would overlap itself.'
    const anchor = Date.parse(compiled.anchor)
    const [first, second] = occurrencesOf(compiled, anchor, anchor + 800 * DAY)
    return first && second ? `${windowShort(first)}, then ${windowShort(second)}` : ''
  }
  const unit = UNITS.find(entry => entry.value === repeat.value.unit)!
  const every = `${repeat.value.every} ${repeat.value.every === 1 ? unit.one : unit.many}`
  return [
    { value: 'start', label: `Every ${every}, counted from each start`, note: `Keeps the same weekday and hour. ${example('start')}.` },
    { value: 'end', label: `${every} off after each one ends`, note: `Keeps the break the same length. ${example('end')}.` }
  ]
})

const countFromModel = computed({
  get: () => repeat.value.countFrom as string,
  set: (value: string | null) => patchRepeat({ countFrom: value === 'end' ? 'end' : 'start' })
})

const ENDS = [
  { value: 'never', label: 'Never' },
  { value: 'on', label: 'On a date' },
  { value: 'after', label: 'After a number of times' }
]
const endsModel = computed({
  get: () => repeat.value.ends as string,
  set: (value: string) => patchRepeat({ ends: value as RepeatDraft['ends'] })
})

function toggleWeekday(day: number) {
  const days = repeat.value.weekdays.includes(day) ? repeat.value.weekdays.filter(entry => entry !== day) : [...repeat.value.weekdays, day]
  patchRepeat({ weekdays: days.length ? days : [new Date(repeat.value.start).getDay()] })
}

/* A weekly event opens on its start's weekday unless others are chosen; the start moving moves it. */
watch(() => repeat.value.start, (value, before) => {
  const was = new Date(before).getDay()
  if (repeat.value.weekdays.length <= 1 && (repeat.value.weekdays[0] === undefined || repeat.value.weekdays[0] === was)) patchRepeat({ weekdays: [new Date(value).getDay()] })
})

const ORDERED_WEEKDAYS = [1, 2, 3, 4, 5, 6, 0]

// --- Who ----------------------------------------------------------------------------------

const audience = ref<TargetRule>(props.event ? JSON.parse(JSON.stringify(props.event.audience)) as TargetRule : {})
const rollout = ref(props.event?.rollout ?? FULL_ROLLOUT)
const audienceProblem = computed(() => ruleProblem(audience.value))

// --- What the game gets -------------------------------------------------------------------

/* Typed through the cast: unwrapping `Json` through a ref recurses without end. */
const payload = shallowRef<Json>(JSON.parse(JSON.stringify(props.event ? props.event.payload : KIND_PRESETS['seasonal_event']!.payload)))
const payloadInvalid = ref(false)
const priority = ref(props.event?.priority ?? 0)

function useTemplate() {
  payload.value = JSON.parse(JSON.stringify(kindLook(kindKey.value).payload)) as Json
}

// --- Measured by --------------------------------------------------------------------------

const goal = ref<LiveOpsMetricKey | null>(props.event ? props.event.goal : KIND_PRESETS['seasonal_event']!.goal)
const watchList = ref<string[]>(props.event ? [...props.event.watch] : ['revenue'])

const GOAL_ITEMS = [
  { value: 'none', title: 'Not measured', subtitle: 'For an event not meant to move a metric: a notice, a fix.' },
  ...LIVEOPS_METRIC_KEYS.map(key => ({ value: key, title: LIVEOPS_METRICS[key].label, subtitle: LIVEOPS_METRICS[key].note }))
]

const goalModel = computed({
  get: () => goal.value ?? 'none',
  set: (value: string | null) => {
    goal.value = !value || value === 'none' ? null : value as LiveOpsMetricKey
  }
})

const WATCH_OPTIONS = computed(() => LIVEOPS_METRIC_KEYS.map(key => ({
  value: key,
  label: LIVEOPS_METRICS[key].label,
  note: LIVEOPS_METRICS[key].note,
  ...(key === goal.value ? { disabledReason: 'It is meant to move this one already.' } : {})
})))

watch(goal, (value) => {
  watchList.value = watchList.value.filter(entry => entry !== value)
})

// --- Saving -------------------------------------------------------------------------------

const ready = computed(() => name.value.trim().length >= 2 && key.value.length >= 2 && !keyError.value && kindKey.value.length >= 2
  && !problem.value && !audienceProblem.value && !payloadInvalid.value)

function draft(status: LiveOpsStatus): LiveOpsDraft {
  return {
    key: key.value,
    kind: kindKey.value,
    name: name.value.trim(),
    description: description.value.trim(),
    status,
    schedule: schedule.value,
    priority: priority.value || 0,
    audience: audience.value,
    payload: payload.value,
    rollout: rollout.value,
    goal: goal.value,
    watch: watchList.value as LiveOpsMetricKey[]
  }
}

const saving = ref(false)
const refusal = ref('')

async function save(status: LiveOpsStatus) {
  if (!ready.value || saving.value) return
  saving.value = true
  refusal.value = ''
  const problem = props.event ? await update(props.event.key, draft(props.event.status)) : await create(draft(status))
  saving.value = false
  if (problem) {
    refusal.value = problem
    return
  }
  saved.value = true
  emit('saved', key.value)
}

// --- Changes, and leaving with them -------------------------------------------------------

/* Everything the form edits, as one string, so "has anything changed" is one comparison. */
const snapshot = () => JSON.stringify([name.value, key.value, description.value, kindKey.value, repeat.value, audience.value, rollout.value, payload.value, priority.value, goal.value, watchList.value])
const initial = snapshot()
const dirty = computed(() => snapshot() !== initial)
const saved = ref(false)

/*
 * Leaving with changes asks first; the route asked for waits until the answer is yes. The yes
 * is a flag of its own, not a value of `leaving`: the dialog closes itself as it confirms,
 * which clears `leaving`, and a yes kept there was gone before the route guard read it, so
 * the question came back every time.
 */
const leaving = ref<string | null>(null)
let leaveConfirmed = false

const leaveOpen = computed({
  get: () => leaving.value !== null,
  set: (value) => {
    if (!value) leaving.value = null
  }
})

onBeforeRouteLeave((to) => {
  if (saved.value || !dirty.value || leaveConfirmed) return true
  leaving.value = to.fullPath
  return false
})

const router = useRouter()

function leave() {
  const to = leaving.value
  leaveConfirmed = true
  leaving.value = null
  if (to) router.push(to)
}

beforeUnload()

/* A reload or a closed tab with changes gets the browser's own question. */
function beforeUnload() {
  const guard = (event: BeforeUnloadEvent) => {
    if (dirty.value && !saved.value) event.preventDefault()
  }
  onMounted(() => window.addEventListener('beforeunload', guard))
  onBeforeUnmount(() => window.removeEventListener('beforeunload', guard))
}

// --- The summary --------------------------------------------------------------------------

/* What stands between the form and a save, each with the section it is fixed in. */
const missing = computed(() => {
  const out: { section: string, text: string }[] = []
  if (name.value.trim().length < 2) out.push({ section: 'what', text: 'A name of two characters or more' })
  if (key.value.length < 2) out.push({ section: 'what', text: 'A key the game reads it by' })
  else if (keyError.value) out.push({ section: 'what', text: keyError.value })
  if (kindKey.value.length < 2) out.push({ section: 'what', text: 'A kind of your own, two characters or more' })
  if (problem.value) out.push({ section: 'when', text: problem.value })
  if (audienceProblem.value) out.push({ section: 'who', text: audienceProblem.value })
  if (payloadInvalid.value) out.push({ section: 'payload', text: 'The payload is not valid JSON yet' })
  return out
})

const nextWindow = computed(() => preview.value.find(window => window.end > Date.now()) ?? null)

const payloadWords = computed(() => {
  const value = payload.value
  if (value === null) return 'Nothing: null'
  if (Array.isArray(value)) return `A list of ${value.length}`
  if (typeof value === 'object') {
    const count = Object.keys(value).length
    return `${count} ${count === 1 ? 'field' : 'fields'}: ${Object.keys(value).slice(0, 3).join(', ')}${count > 3 ? ', …' : ''}`
  }
  return `A ${typeof value}: ${JSON.stringify(value)}`
})

const { segmentName } = useLiveConfig()

function jump(section: string) {
  document.getElementById(`section-${section}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const ids = { name: useId(), key: useId(), description: useId(), custom: useId(), start: useId(), length: useId(), every: useId(), timezone: useId(), until: useId(), times: useId(), priority: useId(), goal: useId() }

const SECTIONS = [
  { id: 'what', number: 1, title: 'What it is' },
  { id: 'when', number: 2, title: 'When it is on' },
  { id: 'who', number: 3, title: 'Who gets it' },
  { id: 'payload', number: 4, title: 'What the game gets' },
  { id: 'measure', number: 5, title: 'What it should move' }
]
</script>

<template>
  <form class="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]" @submit.prevent="save(editing ? event!.status : 'draft')">
    <div class="flex min-w-0 flex-col gap-6">
      <!-- 1. What it is. -->
      <section :id="`section-${SECTIONS[0]!.id}`" :aria-labelledby="`heading-${SECTIONS[0]!.id}`" class="flex scroll-mt-20 flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <h2 :id="`heading-${SECTIONS[0]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
          <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">1</span>
          What it is
        </h2>

        <div class="grid gap-4 sm:grid-cols-2">
          <div class="flex flex-col gap-1.5">
            <label :for="ids.name" class="text-xs font-medium text-ink">Name</label>
            <v-text-field
              :id="ids.name"
              v-model="name"
              autocomplete="off"
              autofocus
              hide-details
              maxlength="120"
              placeholder="Halloween: Night of the Pumpkin King"
              variant="outlined"
            />
          </div>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.key" class="text-xs font-medium text-ink">Key <span class="font-normal text-ink/45">{{ editing ? 'fixed: the game reads it by this' : 'what the game reads it by' }}</span></label>
            <v-text-field
              :id="ids.key"
              v-model="key"
              :disabled="editing"
              :error="Boolean(keyError)"
              autocomplete="off"
              class="font-mono"
              hide-details
              maxlength="64"
              placeholder="halloween"
              spellcheck="false"
              variant="outlined"
              @update:model-value="keyTouched = true"
            />
            <span v-if="keyError" class="text-[11px] text-error">{{ keyError }}</span>
          </div>
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Kind</span>
          <span class="text-[11px] leading-4 text-ink/55">Your own word for what sort of event it is. It sets its colour and its band on the calendar, and the game can read it too.</span>
          <div class="mt-1 grid grid-cols-2 gap-1.5 sm:grid-cols-3" role="radiogroup" aria-label="Kind">
            <button
              v-for="choice in KIND_CHOICES"
              :key="choice.value"
              :aria-checked="chosenKind === choice.value"
              :class="chosenKind === choice.value ? 'border-primary bg-primary/6' : 'border-line bg-surface hover:bg-ink/3'"
              class="flex cursor-pointer items-center gap-2.5 rounded-(--ps-radius-small) border px-2.5 py-2 text-left transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              role="radio"
              type="button"
              @click="chooseKind(choice.value)"
            >
              <span
                :style="{ background: `color-mix(in srgb, ${toneColour(choice.look.tone)} 14%, transparent)`, color: toneColour(choice.look.tone) }"
                aria-hidden="true"
                class="flex size-7 flex-none items-center justify-center rounded-(--ps-radius-small) text-base"
              >
                <Icon :name="choice.look.icon" />
              </span>
              <span class="flex min-w-0 flex-col">
                <span class="truncate text-xs font-medium text-ink">{{ choice.look.label }}</span>
                <span class="truncate font-mono text-[10px] text-ink/45">{{ choice.value === 'other' ? 'your_own' : choice.value }}</span>
              </span>
            </button>
          </div>
          <div v-if="chosenKind === 'other'" class="mt-1 flex flex-col gap-1.5">
            <label :for="ids.custom" class="text-[11px] text-ink/60">The kind, as the game will read it</label>
            <v-text-field
              :id="ids.custom"
              v-model="customKind"
              autocomplete="off"
              class="font-mono"
              density="comfortable"
              hide-details
              maxlength="64"
              placeholder="raid_boss"
              spellcheck="false"
              variant="outlined"
            />
          </div>
          <span v-else class="text-[11px] text-ink/50">{{ kindLook(kindKey).note }}</span>
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.description" class="text-xs font-medium text-ink">Description <span class="font-normal text-ink/45">for the team, not the players</span></label>
          <v-textarea
            :id="ids.description"
            v-model="description"
            auto-grow
            hide-details
            maxlength="500"
            placeholder="A haunted map, candy as a currency and a shop of Halloween skins."
            rows="2"
            variant="outlined"
          />
        </div>
      </section>

      <!-- 2. When it is on. -->
      <section :id="`section-${SECTIONS[1]!.id}`" :aria-labelledby="`heading-${SECTIONS[1]!.id}`" class="flex scroll-mt-20 flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <h2 :id="`heading-${SECTIONS[1]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
          <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">2</span>
          When it is on
        </h2>

        <div class="flex flex-wrap gap-1.5" role="group" aria-label="Common schedules">
          <button
            v-for="preset in PRESETS"
            :key="preset.id"
            :aria-pressed="activePreset === preset.id"
            :class="activePreset === preset.id ? 'border-primary bg-primary/6 text-ink' : 'border-line bg-surface text-ink/75 hover:bg-ink/3'"
            class="inline-flex cursor-pointer items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
            type="button"
            @click="patchRepeat(preset.draft())"
          >
            <Icon :name="preset.icon" :aria-hidden="true" class="text-sm" />
            {{ preset.label }}
          </button>
        </div>

        <div class="grid gap-4 sm:grid-cols-[1.3fr_1fr_1fr]">
          <div class="flex flex-col gap-1.5">
            <label :for="ids.start" class="text-xs font-medium text-ink">{{ repeat.repeats ? 'First starts' : 'Starts' }}</label>
            <DateTimeField
              :id="ids.start"
              :model-value="repeat.start"
              label="Starts"
              @update:model-value="patchRepeat({ start: $event })"
            />
          </div>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.length" class="text-xs font-medium text-ink">{{ repeat.repeats ? 'Each time lasts' : 'Lasts' }}</label>
            <div class="flex gap-1.5">
              <v-text-field
                :id="ids.length"
                :model-value="repeat.length"
                class="ps-liveops-number w-20 flex-none"
                hide-details
                inputmode="numeric"
                min="1"
                type="number"
                variant="outlined"
                @update:model-value="patchRepeat({ length: Number($event) || 0 })"
              />
              <v-select
                v-bind="FIELD_MENU"
                :items="LENGTH_ITEMS"
                :model-value="repeat.lengthUnit"
                aria-label="Unit"
                class="min-w-0 flex-1"
                hide-details
                variant="outlined"
                @update:model-value="patchRepeat({ lengthUnit: $event })"
              />
            </div>
          </div>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.timezone" class="text-xs font-medium text-ink">Clock</label>
            <v-autocomplete
              :id="ids.timezone"
              v-bind="FIELD_MENU"
              :items="TIMEZONES"
              :model-value="repeat.timezone"
              hide-details
              variant="outlined"
              @update:model-value="patchRepeat({ timezone: $event || 'UTC' })"
            />
          </div>
        </div>

        <SwitchRow
          :model-value="repeat.repeats"
          label="Repeats"
          note="One event for every window: a weekly tournament, a monthly calendar, a New Year that comes back every year."
          @update:model-value="patchRepeat({ repeats: $event })"
        />

        <div v-if="repeat.repeats" class="flex flex-col gap-4 rounded-(--ps-radius-large) border border-line bg-ink/2 p-4">
          <div class="flex flex-wrap items-center gap-2">
            <label :for="ids.every" class="text-xs text-ink/70">{{ repeat.countFrom === 'end' && countable ? 'Then off for' : 'Every' }}</label>
            <v-text-field
              :id="ids.every"
              :model-value="repeat.every"
              class="ps-liveops-number w-20 flex-none"
              density="comfortable"
              hide-details
              inputmode="numeric"
              min="1"
              type="number"
              variant="outlined"
              @update:model-value="patchRepeat({ every: Math.max(1, Math.round(Number($event)) || 1) })"
            />
            <v-select
              v-bind="FIELD_MENU"
              :items="UNIT_ITEMS"
              :model-value="repeat.unit"
              aria-label="Repeat unit"
              class="w-36 flex-none"
              density="comfortable"
              hide-details
              variant="outlined"
              @update:model-value="patchRepeat({ unit: $event, ...($event === 'months' || $event === 'years' ? { countFrom: 'start' } : {}) })"
            />
          </div>

          <div v-if="countable" class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-ink">Counted</span>
            <ChoiceList v-model="countFromModel" :options="COUNT_FROM" label="How the repeat is counted" />
          </div>

          <div v-if="repeat.unit === 'weeks' && repeat.countFrom === 'start'" class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-ink">Starts on</span>
            <div class="flex flex-wrap gap-1" role="group" aria-label="Weekdays">
              <button
                v-for="day in ORDERED_WEEKDAYS"
                :key="day"
                :aria-pressed="repeat.weekdays.includes(day)"
                :class="repeat.weekdays.includes(day) ? 'border-primary bg-primary text-on-primary' : 'border-line bg-surface text-ink/70 hover:bg-ink/3'"
                class="h-8 w-11 cursor-pointer rounded-(--ps-radius-small) border text-xs font-medium transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
                type="button"
                @click="toggleWeekday(day)"
              >
                {{ WEEKDAY_SHORT[day] }}
              </button>
            </div>
          </div>

          <div class="flex flex-col gap-2">
            <span class="text-xs font-medium text-ink">Stops repeating</span>
            <div class="flex flex-wrap items-center gap-3">
              <SegmentedControl v-model="endsModel" :options="ENDS" label="Stops repeating" />
              <DateTimeField
                v-if="repeat.ends === 'on'"
                :id="ids.until"
                :min="repeat.start.slice(0, 10)"
                :model-value="repeat.until"
                class="w-60 flex-none"
                date-only
                density="comfortable"
                label="Last day a window may start"
                @update:model-value="patchRepeat({ until: $event })"
              />
              <span v-if="repeat.ends === 'after'" class="flex items-center gap-2">
                <v-text-field
                  :id="ids.times"
                  :model-value="repeat.times"
                  aria-label="How many times in all"
                  class="ps-liveops-number w-20 flex-none"
                  density="comfortable"
                  hide-details
                  inputmode="numeric"
                  min="1"
                  type="number"
                  variant="outlined"
                  @update:model-value="patchRepeat({ times: Math.max(1, Math.round(Number($event)) || 1) })"
                />
                <span class="text-xs text-ink/60">times in all</span>
              </span>
            </div>
          </div>
        </div>

        <!-- Read back: the schedule in a sentence, and the dates it means, a window a row. -->
        <div :class="problem ? 'border-error/40' : 'border-line'" class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border">
          <header :class="problem ? 'bg-error/6' : 'bg-ink/2'" class="flex items-start justify-between gap-3 px-4 py-3">
            <div class="flex min-w-0 items-start gap-2.5">
              <Icon
                v-if="problem"
                :aria-hidden="true"
                class="mt-0.5 flex-none text-base text-error"
                name="material-symbols:error-outline"
              />
              <div class="flex min-w-0 flex-col gap-0.5">
                <span class="text-[13px] font-medium text-ink">{{ problem ?? scheduleWords(schedule) }}</span>
                <span v-if="!problem" class="text-[11px] text-ink/50">{{ repeat.timezone }} clock · {{ windowCount }}</span>
              </div>
            </div>
            <span v-if="!problem" class="flex flex-none cursor-help text-ink/40 transition-colors hover:text-ink/80">
              <Icon aria-label="How a window ends" class="text-base" name="material-symbols:info-outline" />
              <v-tooltip activator="parent" location="top" max-width="280">
                A window is on until its length runs out: three days from Jan 1, 00:00 are on until Jan 3, 23:59, and a break after it is counted from Jan 4, 00:00.
              </v-tooltip>
            </span>
          </header>

          <ol v-if="preview.length" class="m-0 flex list-none flex-col border-t border-line p-0">
            <li v-for="window in preview" :key="window.start" class="flex items-center gap-3 border-b border-line/60 px-4 py-2 last:border-b-0">
              <!-- The day it opens, as a leaf of a calendar. -->
              <span class="flex w-10 flex-none flex-col items-center rounded-(--ps-radius-small) bg-ink/4 py-1 leading-none ring-1 ring-line ring-inset">
                <span class="text-[9px] font-semibold tracking-wide text-ink/50 uppercase">{{ new Date(window.start).toLocaleDateString('en', { month: 'short' }) }}</span>
                <span class="mt-0.5 font-mono text-sm text-ink">{{ new Date(window.start).getDate() }}</span>
              </span>
              <span class="flex min-w-0 flex-1 items-center gap-1.5 text-xs text-ink/80">
                <span class="truncate">{{ windowEdges(window).start }}</span>
                <Icon :aria-hidden="true" class="flex-none text-sm text-ink/35" name="material-symbols:arrow-forward" />
                <span class="truncate">{{ windowEdges(window).end }}</span>
              </span>
              <span class="flex-none font-mono text-[11px] text-ink/45">{{ relativeWords(window.start, Date.now()) }}</span>
            </li>
          </ol>
          <footer v-if="preview.length === 6 && schedule.mode !== 'once'" class="border-t border-line/60 px-4 py-2 text-[11px] text-ink/50">
            {{ schedule.until ? `The last window starts by ${whenAt(Date.parse(schedule.until)).split(',')[0]}.` : 'And on like this, until somebody stops it.' }}
          </footer>
        </div>
      </section>

      <!-- 3. Who gets it. -->
      <section :id="`section-${SECTIONS[2]!.id}`" :aria-labelledby="`heading-${SECTIONS[2]!.id}`" class="flex scroll-mt-20 flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <h2 :id="`heading-${SECTIONS[2]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
          <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">3</span>
          Who gets it
        </h2>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Audience</span>
          <span class="text-[11px] leading-4 text-ink/55">Countries, segments, platforms, versions, levels, anything the game sends. No conditions is every player.</span>
          <AudienceEditor
            v-model="audience"
            :problem="audienceProblem ?? ''"
            :segments="segments"
            class="mt-1"
            grants-value
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Rollout</span>
          <span class="text-[11px] leading-4 text-ink/55">
            The share of that audience who get it. The rest are held back, always the same players, and they are what its effect is measured against:
            holding back 5 to 10% gives the cleanest answer an event can have.
          </span>
          <RolloutField v-model="rollout" class="mt-1" label="Event rollout" />
          <!-- What the share means, and the one-click way to the share that measures best. -->
          <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
            <span class="text-[11px] text-ink/55">
              <template v-if="rollout === FULL_ROLLOUT">Everybody in the audience gets it; its effect is read against their own weeks before.</template>
              <template v-else>{{ formatRollout(rollout) }} get it and {{ formatRollout(FULL_ROLLOUT - rollout) }} are held back to measure it against.</template>
            </span>
            <v-btn
              v-if="rollout === FULL_ROLLOUT"
              class="-my-1 text-xs"
              color="primary"
              density="comfortable"
              size="small"
              variant="text"
              @click="rollout = 9000"
            >
              Hold back 10%
            </v-btn>
          </div>
        </div>
      </section>

      <!-- 4. What the game gets. -->
      <section :id="`section-${SECTIONS[3]!.id}`" :aria-labelledby="`heading-${SECTIONS[3]!.id}`" class="flex scroll-mt-20 flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <h2 :id="`heading-${SECTIONS[3]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
          <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">4</span>
          What the game gets
        </h2>

        <div class="flex flex-col gap-1.5">
          <div class="flex items-end justify-between gap-2">
            <span class="flex flex-col gap-0.5">
              <span class="text-xs font-medium text-ink">Payload</span>
              <span class="text-[11px] leading-4 text-ink/55">Any JSON, a number included. PlayerSense never reads it: the SDK hands it to the game while the event is on for that player.</span>
            </span>
            <v-btn
              class="flex-none text-xs"
              color="neutral"
              prepend-icon="material-symbols:data-object-outline"
              size="small"
              variant="text"
              @click="useTemplate"
            >
              {{ kindLook(kindKey).label }} template
            </v-btn>
          </div>
          <JsonEditor
            v-model="payload"
            v-model:invalid="payloadInvalid"
            :title="`${kindKey || 'event'}.payload`"
            label="Payload"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.priority" class="text-xs font-medium text-ink">Priority</label>
          <span class="text-[11px] leading-4 text-ink/55">When two events of the same kind are on for one player, the higher one is served: a limited offer in front of an evergreen one, without ending it.</span>
          <v-number-input
            :id="ids.priority"
            v-model="priority"
            :max="1000"
            :min="-1000"
            class="w-44"
            control-variant="split"
            hide-details
            inset
            variant="outlined"
          />
        </div>
      </section>

      <!-- 5. What it should move. -->
      <section :id="`section-${SECTIONS[4]!.id}`" :aria-labelledby="`heading-${SECTIONS[4]!.id}`" class="flex scroll-mt-20 flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <h2 :id="`heading-${SECTIONS[4]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
          <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">5</span>
          What it should move
        </h2>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.goal" class="text-xs font-medium text-ink">Meant to move</label>
          <span class="text-[11px] leading-4 text-ink/55">The metric its results page leads with. Choose it before it runs, not after.</span>
          <v-autocomplete
            :id="ids.goal"
            v-model="goalModel"
            v-bind="FIELD_MENU"
            :items="GOAL_ITEMS"
            auto-select-first
            hide-details
            item-props
            placeholder="Find a metric"
            prepend-inner-icon="material-symbols:search"
            variant="outlined"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Must not get worse</span>
          <span class="text-[11px] leading-4 text-ink/55">The results page warns if the event makes one of these worse, whatever it does for the metric above.</span>
          <CheckList
            v-model="watchList"
            :columns="2"
            :options="WATCH_OPTIONS"
            class="mt-1"
            label="Must not get worse"
          />
        </div>
      </section>
    </div>

    <!-- The event as it stands, what is missing, and the buttons: in view however far the form scrolls. -->
    <aside class="flex flex-col gap-3 lg:sticky lg:top-20">
      <section aria-label="Summary" class="flex flex-col overflow-hidden rounded-(--ps-radius-x-large) border border-line bg-surface">
        <header class="flex items-center gap-3 border-b border-line p-4">
          <LiveOpsKindMark :kind="kindKey || 'event'" bare size="large" />
          <div class="flex min-w-0 flex-col">
            <span :class="name.trim() ? 'text-ink' : 'text-ink/40'" class="truncate text-sm font-medium">{{ name.trim() || 'Untitled event' }}</span>
            <span class="truncate font-mono text-[11px] text-ink/45">{{ key || 'no_key_yet' }} · {{ kindLook(kindKey).label }}</span>
          </div>
        </header>

        <dl class="m-0 flex flex-col divide-y divide-line/70">
          <button class="flex cursor-pointer flex-col gap-0.5 px-4 py-3 text-left hover:bg-ink/2" type="button" @click="jump('when')">
            <dt class="text-[11px] text-ink/45">
              When
            </dt>
            <dd :class="problem ? 'text-error' : 'text-ink'" class="m-0 text-xs leading-5">
              {{ problem ?? scheduleWords(schedule) }}
            </dd>
            <dd v-if="nextWindow" class="m-0 text-[11px] text-ink/50">
              Next: {{ windowWords(nextWindow) }}
            </dd>
          </button>
          <button class="flex cursor-pointer flex-col gap-0.5 px-4 py-3 text-left hover:bg-ink/2" type="button" @click="jump('who')">
            <dt class="text-[11px] text-ink/45">
              Who
            </dt>
            <dd class="m-0 text-xs leading-5 text-ink">
              {{ describeRule(audience, segmentName) || 'Every player' }}
            </dd>
            <dd class="m-0 text-[11px] text-ink/50">
              {{ rollout === FULL_ROLLOUT ? 'All of them' : `${formatRollout(rollout)} of them, ${formatRollout(FULL_ROLLOUT - rollout)} held back` }}
            </dd>
          </button>
          <button class="flex cursor-pointer flex-col gap-0.5 px-4 py-3 text-left hover:bg-ink/2" type="button" @click="jump('payload')">
            <dt class="text-[11px] text-ink/45">
              The game gets
            </dt>
            <dd :class="payloadInvalid ? 'text-error' : 'text-ink'" class="m-0 truncate text-xs leading-5">
              {{ payloadInvalid ? 'Not valid JSON yet' : payloadWords }}
            </dd>
            <dd class="m-0 text-[11px] text-ink/50">
              Priority {{ priority || 0 }}
            </dd>
          </button>
          <button class="flex cursor-pointer flex-col gap-0.5 px-4 py-3 text-left hover:bg-ink/2" type="button" @click="jump('measure')">
            <dt class="text-[11px] text-ink/45">
              Meant to move
            </dt>
            <dd class="m-0 text-xs leading-5 text-ink">
              {{ goal ? LIVEOPS_METRICS[goal].label : 'Nothing: not measured' }}
            </dd>
            <dd v-if="watchList.length" class="m-0 text-[11px] text-ink/50">
              {{ watchList.length }} must not get worse
            </dd>
          </button>
        </dl>

        <!-- What is left to do, each a link to the section it is done in. -->
        <div class="border-t border-line p-4">
          <ul v-if="missing.length" class="m-0 flex list-none flex-col gap-1.5 p-0">
            <li v-for="item in missing" :key="item.text">
              <button class="flex cursor-pointer items-start gap-2 text-left text-xs leading-4 text-ink/75 hover:text-ink" type="button" @click="jump(item.section)">
                <Icon :aria-hidden="true" class="mt-px flex-none text-sm text-warning" name="material-symbols:error-outline" />
                {{ item.text }}
              </button>
            </li>
          </ul>
          <p v-else class="m-0 flex items-center gap-2 text-xs text-success">
            <Icon :aria-hidden="true" class="flex-none text-sm" name="material-symbols:check-circle-outline" />
            Ready to save
          </p>
        </div>

        <footer class="flex flex-col gap-2 border-t border-line p-4">
          <p v-if="refusal" class="m-0 text-xs leading-5 text-error">
            {{ refusal }}
          </p>
          <template v-if="editing">
            <v-btn
              :disabled="!ready || !dirty"
              :loading="saving"
              block
              class="text-xs font-medium"
              color="primary"
              variant="flat"
              @click="save(event!.status)"
            >
              Save changes
            </v-btn>
          </template>
          <template v-else>
            <v-btn
              :disabled="!ready"
              :loading="saving"
              block
              class="text-xs font-medium"
              color="primary"
              variant="flat"
              @click="save('active')"
            >
              Save and activate
            </v-btn>
            <v-btn
              :disabled="!ready"
              block
              class="text-xs font-medium"
              color="neutral"
              variant="outlined"
              @click="save('draft')"
            >
              Save as draft
            </v-btn>
          </template>
          <div class="flex items-center justify-between gap-2">
            <v-btn
              :disabled="!dirty"
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:restart-alt"
              size="small"
              variant="text"
              @click="emit('reset')"
            >
              Reset
            </v-btn>
            <v-btn
              class="text-xs"
              color="neutral"
              size="small"
              variant="text"
              @click="emit('cancel')"
            >
              Cancel
            </v-btn>
          </div>
        </footer>
      </section>

      <p class="m-0 px-1 text-[11px] leading-4 text-ink/50">
        <template v-if="editing">
          Saved to the draft. Players get the change once it is published, and a window already on changes for them then.
        </template>
        <template v-else>
          A draft reaches nobody. An active event reaches players once the draft is published.
        </template>
      </p>
    </aside>

    <ConfirmDialog
      v-if="leaveOpen"
      v-model="leaveOpen"
      action="Leave without saving"
      description="What was changed here is not saved anywhere, and goes when you leave."
      title="Leave without saving?"
      @confirm="leave"
    />
  </form>
</template>

<style scoped>
/* A count is read as a figure; the browser's steppers are dropped for the field's own look. */
.ps-liveops-number :deep(input) {
  font-family: var(--font-mono);
  -moz-appearance: textfield;
}

.ps-liveops-number :deep(input::-webkit-inner-spin-button),
.ps-liveops-number :deep(input::-webkit-outer-spin-button) {
  -webkit-appearance: none;
  margin: 0;
}
</style>
