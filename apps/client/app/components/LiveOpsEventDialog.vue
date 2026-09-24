<script setup lang="ts">
// A new event, or an edit to one, in the order somebody thinks it through: what it is, when it
// is on, who gets it, what the game receives, and what it should move.
//
// The schedule is the part people get wrong, so it is set the way it is said ("on for 3 days,
// every 7 days", "every Friday and Saturday", "every year, ending after 5 times") and read back
// as a sentence and as the actual dates of the next windows, which is the only check anybody
// trusts. One event with a repeat stands for every New Year there will ever be: nobody should
// have to make twelve events a year by hand, and a studio that has to will eventually miss one.
const open = defineModel<boolean>({ required: true })

const props = defineProps<{
  /** The event being edited. Absent is a new one. */
  event?: LiveOpsEventDef | null
  /** For a new event: the day it starts on, as a day on the calendar was chosen. */
  startDay?: number | null
}>()

const emit = defineEmits<{ saved: [key: string] }>()

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

/* The first day, at 10:00. */
const nextHour = () => {
  const at = new Date(now + 24 * 3_600_000)
  at.setHours(10, 0, 0, 0)
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
const LENGTH_ITEMS = computed(() => (['minutes', 'hours', 'days'] as const).map(value => ({ value, title: repeat.value.length === 1 ? value.slice(0, -1) : value })))

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

function save(status: LiveOpsStatus) {
  if (!ready.value) return
  if (props.event) update(props.event.key, draft(props.event.status))
  else create(draft(status))
  emit('saved', key.value)
  open.value = false
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
  <v-dialog v-model="open" max-width="780" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ editing ? `Edit ${event!.name}` : 'New event' }}
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          <template v-if="editing">
            Saved to the draft. Players get the change once it is published, and a window already on changes for them then.
          </template>
          <template v-else>
            Save it as a draft to finish later, or activate it: either way it reaches players once the draft is published.
          </template>
        </p>
      </header>

      <form class="flex flex-col overflow-y-auto" @submit.prevent="save('draft')">
        <!-- 1. What it is. -->
        <section :aria-labelledby="`section-${SECTIONS[0]!.id}`" class="flex flex-col gap-5 border-b border-line p-5">
          <h3 :id="`section-${SECTIONS[0]!.id}`" class="m-0 flex items-center gap-2 text-xs font-semibold tracking-wide text-ink/55 uppercase">
            <span class="flex size-5 items-center justify-center rounded-full bg-ink/8 font-mono text-[11px] text-ink/70">1</span>
            What it is
          </h3>

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
        <section :aria-labelledby="`section-${SECTIONS[1]!.id}`" class="flex flex-col gap-5 border-b border-line p-5">
          <h3 :id="`section-${SECTIONS[1]!.id}`" class="m-0 flex items-center gap-2 text-xs font-semibold tracking-wide text-ink/55 uppercase">
            <span class="flex size-5 items-center justify-center rounded-full bg-ink/8 font-mono text-[11px] text-ink/70">2</span>
            When it is on
          </h3>

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
              <Icon :name="preset.icon" aria-hidden="true" class="text-sm" />
              {{ preset.label }}
            </button>
          </div>

          <div class="grid gap-4 sm:grid-cols-[1.3fr_1fr_1fr]">
            <div class="flex flex-col gap-1.5">
              <label :for="ids.start" class="text-xs font-medium text-ink">{{ repeat.repeats ? 'First starts' : 'Starts' }}</label>
              <v-text-field
                :id="ids.start"
                :model-value="repeat.start"
                class="font-mono"
                hide-details
                type="datetime-local"
                variant="outlined"
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
                <v-text-field
                  v-if="repeat.ends === 'on'"
                  :id="ids.until"
                  :model-value="repeat.until"
                  aria-label="Last day a window may start"
                  class="w-44 flex-none font-mono"
                  density="comfortable"
                  hide-details
                  type="date"
                  variant="outlined"
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

          <!-- Read back: the sentence, and the dates it means. -->
          <div :class="problem ? 'bg-error/6 ring-error/25' : 'bg-primary/5 ring-primary/15'" class="flex flex-col gap-3 rounded-(--ps-radius-large) p-4 ring-1 ring-inset">
            <div class="flex items-start gap-2.5">
              <Icon
                :class="problem ? 'text-error' : 'text-primary'"
                :name="problem ? 'material-symbols:error-outline' : 'material-symbols:event-repeat-outline'"
                aria-hidden="true"
                class="mt-0.5 flex-none text-base"
              />
              <div class="flex min-w-0 flex-col gap-0.5">
                <span class="text-[13px] font-medium text-ink">{{ problem ?? scheduleWords(schedule) }}</span>
                <span v-if="!problem" class="text-[11px] text-ink/55">
                  {{ repeat.timezone }} clock. A window is on up to the minute its length runs out: three days from Jan 1, 00:00 are on until Jan 3, 23:59, and the break is counted from Jan 4, 00:00.
                </span>
              </div>
            </div>
            <ol v-if="preview.length" class="m-0 grid list-none gap-1 p-0 sm:grid-cols-2">
              <li v-for="(window, index) in preview" :key="window.start" class="flex items-center gap-2 rounded-(--ps-radius-small) bg-surface px-2.5 py-1.5 ring-1 ring-line ring-inset">
                <span class="w-4 flex-none text-right font-mono text-[10px] text-ink/40">{{ index + 1 }}</span>
                <span class="truncate text-xs text-ink/80">{{ windowWords(window) }}</span>
              </li>
            </ol>
            <span v-if="preview.length === 6 && schedule.mode !== 'once'" class="text-[11px] text-ink/50">
              {{ schedule.until ? `Last window starts by ${whenAt(Date.parse(schedule.until)).split(',')[0]}.` : 'And on, until somebody stops it.' }}
            </span>
          </div>
        </section>

        <!-- 3. Who gets it. -->
        <section :aria-labelledby="`section-${SECTIONS[2]!.id}`" class="flex flex-col gap-5 border-b border-line p-5">
          <h3 :id="`section-${SECTIONS[2]!.id}`" class="m-0 flex items-center gap-2 text-xs font-semibold tracking-wide text-ink/55 uppercase">
            <span class="flex size-5 items-center justify-center rounded-full bg-ink/8 font-mono text-[11px] text-ink/70">3</span>
            Who gets it
          </h3>

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
            <div class="mt-1 flex flex-wrap items-center gap-3">
              <RolloutField v-model="rollout" label="Event rollout" class="min-w-0 flex-1" />
              <v-btn
                v-if="rollout === FULL_ROLLOUT"
                class="text-xs"
                color="neutral"
                size="small"
                variant="outlined"
                @click="rollout = 9000"
              >
                Hold back 10%
              </v-btn>
            </div>
            <span class="text-[11px] text-ink/55">
              <template v-if="rollout === FULL_ROLLOUT">Everybody in the audience gets it; its effect is read against their own weeks before.</template>
              <template v-else>{{ formatRollout(rollout) }} get it and {{ formatRollout(FULL_ROLLOUT - rollout) }} are held back to measure it against.</template>
            </span>
          </div>
        </section>

        <!-- 4. What the game gets. -->
        <section :aria-labelledby="`section-${SECTIONS[3]!.id}`" class="flex flex-col gap-5 border-b border-line p-5">
          <h3 :id="`section-${SECTIONS[3]!.id}`" class="m-0 flex items-center gap-2 text-xs font-semibold tracking-wide text-ink/55 uppercase">
            <span class="flex size-5 items-center justify-center rounded-full bg-ink/8 font-mono text-[11px] text-ink/70">4</span>
            What the game gets
          </h3>

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
        <section :aria-labelledby="`section-${SECTIONS[4]!.id}`" class="flex flex-col gap-5 p-5">
          <h3 :id="`section-${SECTIONS[4]!.id}`" class="m-0 flex items-center gap-2 text-xs font-semibold tracking-wide text-ink/55 uppercase">
            <span class="flex size-5 items-center justify-center rounded-full bg-ink/8 font-mono text-[11px] text-ink/70">5</span>
            What it should move
          </h3>

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
      </form>

      <footer class="flex items-center justify-between gap-2 border-t border-line px-5 py-3">
        <span class="truncate text-[11px] text-ink/50">{{ problem ?? (ready ? scheduleWords(schedule) : 'A name, a key and a kind are all it needs to be saved.') }}</span>
        <div class="flex flex-none items-center gap-2">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Cancel
          </v-btn>
          <template v-if="editing">
            <v-btn
              :disabled="!ready"
              class="text-xs font-medium"
              color="primary"
              size="small"
              variant="flat"
              @click="save(event!.status)"
            >
              Save changes
            </v-btn>
          </template>
          <template v-else>
            <v-btn
              :disabled="!ready"
              class="text-xs font-medium"
              color="neutral"
              size="small"
              variant="outlined"
              @click="save('draft')"
            >
              Save as draft
            </v-btn>
            <v-btn
              :disabled="!ready"
              class="text-xs font-medium"
              color="primary"
              size="small"
              variant="flat"
              @click="save('active')"
            >
              Save and activate
            </v-btn>
          </template>
        </div>
      </footer>
    </v-card>
  </v-dialog>
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
