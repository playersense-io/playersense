<script lang="ts">
/** A stretch of calendar days, both ends included: "2026-09-01" to "2026-09-24". */
export interface DayRange {
  from: string
  to: string
}
</script>

<script setup lang="ts">
// A stretch of days, chosen from the toolbar beside the filters: a button saying the range in
// words ("Last 30 days", "Sep 1 to Sep 24"), opening a menu with the ranges people ask for most
// down its side and Vuetify's month grid, set to the panel's scale, for any other. The choice is
// held until Apply, so picking the first day of a range does not redraw the page under the menu.
//
// Days are calendar dates, "2026-09-24", in UTC as the metrics query counts them, and the range
// includes both ends.

const model = defineModel<DayRange>({ required: true })

const open = ref(false)

const DAY = 86_400_000
const pad = (value: number) => String(value).padStart(2, '0')
const isoDay = (at: number) => new Date(at).toISOString().slice(0, 10)
const dayStart = (iso: string) => Date.parse(`${iso}T00:00:00Z`)

const today = computed(() => isoDay(Date.now()))

/* The ranges people ask for most, each ending today unless it names a finished month. */
const presets = computed(() => {
  const end = dayStart(today.value)
  const date = new Date(end)
  const monthStart = Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), 1)
  const lastMonthStart = Date.UTC(date.getUTCFullYear(), date.getUTCMonth() - 1, 1)
  return [
    { key: 'today', label: 'Today', from: today.value, to: today.value },
    { key: '7d', label: 'Last 7 days', from: isoDay(end - 6 * DAY), to: today.value },
    { key: '30d', label: 'Last 30 days', from: isoDay(end - 29 * DAY), to: today.value },
    { key: '90d', label: 'Last 90 days', from: isoDay(end - 89 * DAY), to: today.value },
    { key: 'month', label: 'This month', from: isoDay(monthStart), to: today.value },
    { key: 'last-month', label: 'Last month', from: isoDay(lastMonthStart), to: isoDay(monthStart - DAY) }
  ]
})

const presetOf = (range: DayRange) => presets.value.find(entry => entry.from === range.from && entry.to === range.to) ?? null

const monthDay = (iso: string, withYear = false) => new Date(dayStart(iso)).toLocaleDateString('en', { month: 'short', day: 'numeric', timeZone: 'UTC', ...(withYear ? { year: 'numeric' } : {}) })

/** The range in words: a preset's name, or its two ends. */
function words(range: DayRange): string {
  const preset = presetOf(range)
  if (preset) return preset.label
  if (range.from === range.to) return monthDay(range.from, true)
  const sameYear = range.from.slice(0, 4) === range.to.slice(0, 4) && range.to.slice(0, 4) === today.value.slice(0, 4)
  return `${monthDay(range.from, !sameYear)} to ${monthDay(range.to, !sameYear)}`
}

const length = (range: DayRange) => Math.round((dayStart(range.to) - dayStart(range.from)) / DAY) + 1

// --- The choice, held until Apply -----------------------------------------------------------

const draft = ref<DayRange>({ ...model.value })

watch(open, (value) => {
  if (value) draft.value = { ...model.value }
})

/* The month grid works in local dates; a day is carried across as its year, month and day. */
const toLocal = (iso: string) => {
  const [year, month, day] = iso.split('-').map(Number) as [number, number, number]
  return new Date(year, month - 1, day)
}
const fromLocal = (date: Date) => `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`

const picked = computed({
  get: () => {
    const days: Date[] = []
    for (let at = dayStart(draft.value.from); at <= dayStart(draft.value.to); at += DAY) days.push(toLocal(isoDay(at)))
    return days
  },
  set: (value: unknown) => {
    const dates = (Array.isArray(value) ? value : [value]).filter((entry): entry is Date => entry instanceof Date).sort((a, b) => a.getTime() - b.getTime())
    if (!dates.length) return
    draft.value = { from: fromLocal(dates[0]!), to: fromLocal(dates.at(-1)!) }
  }
})

const maxDate = computed(() => toLocal(today.value))

function choosePreset(preset: DayRange) {
  draft.value = { from: preset.from, to: preset.to }
}

function apply() {
  model.value = { ...draft.value }
  open.value = false
}
</script>

<template>
  <v-menu
    v-model="open"
    :close-on-content-click="false"
    location="bottom end"
    offset="6"
  >
    <template #activator="{ props: menu }">
      <v-btn
        v-bind="menu"
        class="ps-toolbar-control text-xs font-medium"
        color="neutral"
        prepend-icon="material-symbols:calendar-month-outline"
        size="small"
        variant="text"
      >
        {{ words(model) }}
      </v-btn>
    </template>

    <v-card border color="surface" flat>
      <div class="flex">
        <!-- The ranges asked for most. -->
        <ul aria-label="Ranges" class="m-0 flex w-40 flex-none list-none flex-col gap-0.5 border-r border-line p-2">
          <li v-for="preset in presets" :key="preset.key">
            <button
              :aria-pressed="presetOf(draft)?.key === preset.key"
              :class="presetOf(draft)?.key === preset.key ? 'bg-ink/6 text-ink' : 'text-ink/70 hover:bg-ink/4 hover:text-ink'"
              class="w-full cursor-pointer rounded-(--ps-radius-small) px-2.5 py-1.5 text-left text-xs transition-colors duration-100 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary"
              type="button"
              @click="choosePreset(preset)"
            >
              {{ preset.label }}
            </button>
          </li>
        </ul>

        <v-date-picker
          v-model="picked"
          :first-day-of-week="1"
          :max="maxDate"
          bg-color="transparent"
          class="ps-date-picker"
          color="primary"
          hide-header
          multiple="range"
          show-adjacent-months
          width="280"
        />
      </div>

      <footer class="flex items-center justify-between gap-3 border-t border-line px-3 py-2">
        <span class="text-[11px] text-ink/55">
          {{ words(draft) }} · {{ length(draft) }} {{ length(draft) === 1 ? 'day' : 'days' }}
        </span>
        <span class="flex items-center gap-1">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Cancel
          </v-btn>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="apply"
          >
            Apply
          </v-btn>
        </span>
      </footer>
    </v-card>
  </v-menu>
</template>
