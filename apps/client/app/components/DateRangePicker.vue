<script lang="ts">
/** A stretch of calendar days, both ends included: "2026-09-01" to "2026-09-24". */
export interface DayRange {
  from: string
  to: string
}
</script>

<script setup lang="ts">
/**
 * The days a page reads: the ranges asked for most, or a piece of the calendar. The design is
 * idenzy's usage range picker, carried over whole.
 *
 * One control and not two. The presets are what a visit almost always wants and the calendar
 * is what a preset cannot answer, and a picker beside a segmented control would put the range
 * in two places with only one of them right. So the button says what is on screen, whichever
 * kind it is, and the menu holds both ways of changing it with the presets first.
 *
 * **Nothing is applied until Apply.** Both halves stage rather than write: the rail holds a
 * preset, the calendar holds a span, naming one drops the other, and the model is untouched
 * until the button. A span has to work this way (it is two clicks, and between them the picker
 * holds a range a day long that nobody asked for), and the rail matches it, because a preset
 * that took effect on the click would shut the menu under a pointer on its way to the calendar.
 * Cancel puts both back to what the page is already showing.
 *
 * The span is bounded at both ends: `earliest` is the first day there is anything to read,
 * today the last, and `MAX_DAYS` the longest span the page draws; over it Apply says so rather
 * than going dark with no reason.
 *
 * **The layout is `VDateRangePicker`'s.** It draws the two months, the rule between them, the
 * navigation across the pair and the footer its `footer` slot fills; it is a labs component,
 * named in packages/dashboard/nuxt.config.ts.
 */
const props = withDefaults(defineProps<{
  /** The first day there is anything to read, as `YYYY-MM-DD`. Two years back when absent. */
  earliest?: string
  loading?: boolean
}>(), {
  earliest: '',
  loading: false
})

const model = defineModel<DayRange>({ required: true })

/** The longest span a page draws: a year and a day, so "last year" fits whatever the leap. */
const MAX_DAYS = 366

const DAY = 86_400_000
const isoDay = (at: number) => new Date(at).toISOString().slice(0, 10)
const dayStart = (iso: string) => Date.parse(`${iso}T00:00:00Z`)
const spanDays = (range: DayRange) => Math.round((dayStart(range.to) - dayStart(range.from)) / DAY) + 1

const today = computed(() => isoDay(Date.now()))

type PresetKey = 'today' | '7d' | '30d' | '90d' | 'month' | 'last-month'

/**
 * The ranges asked for most. `hint` is a tooltip on the row rather than a line under it: which
 * days a preset covers settles a question somebody has once, and a second line under every row
 * made the rail as tall as the calendar to say it six times.
 */
const presets = computed(() => {
  const end = dayStart(today.value)
  const date = new Date(end)
  const monthStart = Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), 1)
  const lastMonthStart = Date.UTC(date.getUTCFullYear(), date.getUTCMonth() - 1, 1)
  const entries: { value: PresetKey, title: string, hint: string, range: DayRange }[] = [
    { value: 'today', title: 'Today', hint: 'Since midnight UTC, so far.', range: { from: today.value, to: today.value } },
    { value: '7d', title: 'Last 7 days', hint: 'Today and the six days before it, against the week before.', range: { from: isoDay(end - 6 * DAY), to: today.value } },
    { value: '30d', title: 'Last 30 days', hint: 'Today and the 29 days before it, against the 30 before.', range: { from: isoDay(end - 29 * DAY), to: today.value } },
    { value: '90d', title: 'Last 90 days', hint: 'Today and the 89 days before it: a season.', range: { from: isoDay(end - 89 * DAY), to: today.value } },
    { value: 'month', title: 'This month', hint: 'From the 1st to today.', range: { from: isoDay(monthStart), to: today.value } },
    { value: 'last-month', title: 'Last month', hint: 'The whole of the month before this one.', range: { from: isoDay(lastMonthStart), to: isoDay(monthStart - DAY) } }
  ]
  return entries
})

const presetOf = (range: DayRange) => presets.value.find(entry => entry.range.from === range.from && entry.range.to === range.to) ?? null

const monthDay = (iso: string, withYear = false) => new Date(dayStart(iso)).toLocaleDateString('en', { month: 'short', day: 'numeric', timeZone: 'UTC', ...(withYear ? { year: 'numeric' } : {}) })

/** A span in words: "Sep 1 to Sep 24", with the years only when they are not this one. */
function spanWords(range: DayRange): string {
  if (range.from === range.to) return monthDay(range.from, true)
  const sameYear = range.from.slice(0, 4) === range.to.slice(0, 4) && range.to.slice(0, 4) === today.value.slice(0, 4)
  return `${monthDay(range.from, !sameYear)} to ${monthDay(range.to, !sameYear)}`
}

/* The label is the range's own, so a preset and a chosen span are described by one function
   and cannot drift apart. */
const label = computed(() => presetOf(model.value)?.title ?? spanWords(model.value))

const open = ref(false)

/*
 * The calendar's dates are local and the figures behind it are UTC, so the two are converted
 * at this boundary and nowhere else.
 *
 * Vuetify builds every date it hands back at local midnight and compares the ones it is given
 * the same way. A `YYYY-MM-DD` read off one of them with `toISOString` is therefore the day
 * before for every reader east of UTC: clicking the 3rd in Istanbul would give the 2nd. The day
 * a person clicked is the day they saw, so it is read from the local parts; the reverse puts it
 * back at local midnight so the cell it lands on is the one it came from.
 */
function toDay(date: Date): string {
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

function fromDay(day: string): Date {
  const [year, month, date] = day.split('-').map(Number)
  return new Date(year ?? 1970, (month ?? 1) - 1, date ?? 1)
}

const min = computed(() => fromDay(props.earliest || isoDay(dayStart(today.value) - 730 * DAY)))
const max = computed(() => fromDay(today.value))

/*
 * The range being built, which the calendar owns until Apply. Seeded from what is on the page
 * rather than left empty, so the calendar opens on the range already shown with it marked, and
 * on the months that range falls in.
 */
const draft = ref<Date[]>([])

/**
 * The preset the rail is holding, a draft in the same way the calendar's span is. The two are
 * exclusive: naming a preset replaces whatever was drawn, and drawing on the calendar drops the
 * preset, because the answer is one or the other and a rail showing a selection the calendar
 * disagrees with cannot say which.
 */
const pendingPreset = ref<PresetKey | null>(null)

const daysOf = (range: DayRange) => [fromDay(range.from), fromDay(range.to)]

function seed() {
  draft.value = daysOf(model.value)
  pendingPreset.value = presetOf(model.value)?.value ?? null
}

watch([model, open], seed, { immediate: true })

function choosePreset(value: PresetKey) {
  pendingPreset.value = value
  /* The calendar marks the preset's own days, so the rail and the months agree on what Apply
     would do before it is pressed. */
  const preset = presets.value.find(entry => entry.value === value)
  if (preset) draft.value = daysOf(preset.range)
}

function onCalendar(value: unknown) {
  pendingPreset.value = null
  draft.value = (Array.isArray(value) ? value : []).filter((entry): entry is Date => entry instanceof Date)
}

/** The draft as a span, or null while it has only one end. */
const pending = computed<DayRange | null>(() => {
  const preset = presets.value.find(entry => entry.value === pendingPreset.value)
  if (preset) return preset.range
  const first = draft.value[0]
  const last = draft.value.at(-1)
  if (draft.value.length < 2 || first === undefined || last === undefined) return null
  return { from: toDay(first), to: toDay(last) }
})

const pendingDays = computed(() => (pending.value ? spanDays(pending.value) : 0))
const tooLong = computed(() => pendingDays.value > MAX_DAYS)

/** Whether Apply would change anything, so a second press of it is not offered. */
const changed = computed(() => pending.value !== null && (pending.value.from !== model.value.from || pending.value.to !== model.value.to))
const canApply = computed(() => changed.value && !tooLong.value)

/** One line in the footer: what is staged, or what is stopping it. */
const note = computed(() => {
  const preset = presets.value.find(entry => entry.value === pendingPreset.value)
  if (preset) return preset.hint
  if (pending.value === null) return 'Pick the first day, then the last.'
  if (tooLong.value) return `${pendingDays.value} days is past the ${MAX_DAYS} this page reads.`
  return `${pendingDays.value} ${pendingDays.value === 1 ? 'day' : 'days'}, ${spanWords(pending.value)}.`
})

function apply() {
  if (!canApply.value || !pending.value) return
  model.value = { ...pending.value }
  open.value = false
}

function cancel() {
  seed()
  open.value = false
}
</script>

<template>
  <v-btn
    :aria-label="`Days shown: ${label}`"
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
      <!-- Vuetify's own arrangement for this control: the presets, a rule, and the range
           picker with its footer. -->
      <v-card
        border
        class="ps-range__card flex"
        color="surface"
        flat
      >
        <!-- The presets are the panel's ghost buttons, stacked and left-aligned; the one
             staged carries a ground, so the rail says which Apply would write. -->
        <div class="flex w-44 flex-none flex-col gap-0.5 p-1.5">
          <v-btn
            v-for="preset in presets"
            :key="preset.value"
            :aria-label="`${preset.title}. ${preset.hint}`"
            :aria-pressed="pendingPreset === preset.value"
            :class="pendingPreset === preset.value ? 'bg-ink/8 text-ink' : 'text-ink/70 hover:text-ink'"
            class="justify-start text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="choosePreset(preset.value)"
          >
            {{ preset.title }}

            <!-- Inside the button and after the label, so the thing it explains is the thing
                 it sits in; bound to this span rather than the button. -->
            <span aria-hidden="true" class="ml-1.5 inline-flex items-center text-ink/40">
              <Icon class="text-sm" name="material-symbols:info-outline" />
              <v-tooltip
                :text="preset.hint"
                activator="parent"
                location="top"
                max-width="260"
                open-delay="200"
              />
            </span>
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
            <p :class="tooLong ? 'text-warning' : 'text-ink/50'" class="m-0 text-[11px] leading-4">
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
              :disabled="!canApply"
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
      </v-card>
    </v-menu>
  </v-btn>
</template>

<style scoped>
/*
 * The picker keeps Vuetify's layout and takes the panel's type. The two months, the rule between
 * them, the navigation over the pair, the span, the preview of one under the pointer and every
 * hover overlay are the component's own; what changes is the size of the type, which arrives at
 * Material's 0.875rem and is larger than the figures on the page the menu opens over.
 */
.ps-range__card {
  max-width: calc(100vw - 2rem);
}

/*
 * One kind of rule inside the control. `.v-date-range-picker__divider` is a plain border in the
 * line colour; `VDivider` takes the same colour through the same opacity and then sets the
 * opacity on the element as well, so it came out the fainter of the two, and a pixel under the
 * rail. Both are undone, so the two rules are the same pixel.
 */
.ps-range__card :deep(.v-divider--vertical) {
  border-color: rgba(var(--v-border-color), var(--v-border-opacity));
  margin: 0;
  opacity: 1;
}

.ps-range__picker :deep(.v-date-picker) {
  background: transparent;
}

.ps-range__picker :deep(.v-date-picker-controls) {
  min-height: var(--ps-toolbar-height);
}

.ps-range__picker :deep(.v-date-picker-controls),
.ps-range__picker :deep(.v-date-picker-controls .v-btn),
.ps-range__picker :deep(.v-date-range-picker__header) {
  font-size: 0.75rem;
  font-weight: 500;
}

/* The weekday letters are a legend, not data: a step under the figures they head. */
.ps-range__picker :deep(.v-date-picker-month__weekday) {
  color: rgba(var(--v-theme-on-surface), 0.45);
  font-size: 0.6875rem;
  font-weight: 500;
  letter-spacing: 0;
  text-transform: none;
}

/* Vuetify's own knob for the figure on a day, and only that: 0.75rem is what the tables and
   the tiles use. */
.ps-range__picker :deep(.v-btn.v-date-picker-month__day-btn) {
  --v-btn-size: 0.75rem;

  font-weight: 500;
  letter-spacing: 0;
}

/*
 * The day is a circle, Vuetify's shape for it and the shape of its hover. theme.css rounds every
 * `.v-btn` to the standard radius and wins on load order, and `.v-btn` clips its overlay to that
 * radius, so the overlay Vuetify lights under the pointer came out a square inside a round span.
 * This is the button's radius, not the overlay's, so clipping gives the overlay its shape back.
 */
.ps-range__picker :deep(.v-btn.v-date-picker-month__day-btn) {
  border-radius: 50%;
  height: 36px;
  width: 36px;
}
</style>
