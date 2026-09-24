<script setup lang="ts">
// A date, or a date and a time of day, chosen from a menu rather than typed into the browser's
// own control, which draws differently in every browser and reads its format from the device.
// The field is an outlined field like the others in a form, saying the value in words ("Thu,
// Oct 1, 2026 at 10:00"); the menu is Vuetify's month grid, and beside it, for a time, a column
// of hours and a column of minutes to pick from, the chosen ones scrolled into view.
//
// The value is the string a datetime-local input holds ("2026-10-01T10:00"), or for a date
// alone a date input's ("2026-10-01"), so a form can swap one in without changing its model.
const props = withDefaults(defineProps<{
  /** Names the field for a screen reader when there is no visible label. */
  label: string
  /** A date alone: no time column, and the value is "YYYY-MM-DD". */
  dateOnly?: boolean
  /** The first day that can be chosen, as "YYYY-MM-DD". */
  min?: string
  density?: 'default' | 'comfortable'
}>(), {
  dateOnly: false,
  min: undefined,
  density: 'default'
})

/* An id and a width are the field's, not the menu's: they go on the text field. */
defineOptions({ inheritAttrs: false })

const model = defineModel<string>({ required: true })

const open = ref(false)

const pad = (value: number) => String(value).padStart(2, '0')

/* The value's parts, read without going through Date so no zone can move them. */
const parts = computed(() => {
  const match = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2}))?/.exec(model.value ?? '')
  if (!match) return null
  return { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]), hour: Number(match[4] ?? 0), minute: Number(match[5] ?? 0) }
})

const write = (next: { year: number, month: number, day: number, hour: number, minute: number }) => {
  const date = `${next.year}-${pad(next.month)}-${pad(next.day)}`
  model.value = props.dateOnly ? date : `${date}T${pad(next.hour)}:${pad(next.minute)}`
}

const today = () => {
  const now = new Date()
  return { year: now.getFullYear(), month: now.getMonth() + 1, day: now.getDate(), hour: 0, minute: 0 }
}

// --- The date -----------------------------------------------------------------------------

const date = computed({
  get: () => (parts.value ? new Date(parts.value.year, parts.value.month - 1, parts.value.day) : null),
  set: (value: unknown) => {
    if (!(value instanceof Date)) return
    const current = parts.value ?? today()
    write({ ...current, year: value.getFullYear(), month: value.getMonth() + 1, day: value.getDate() })
    if (props.dateOnly) open.value = false
  }
})

const minDate = computed(() => (props.min ? new Date(`${props.min}T00:00`) : undefined))

// --- The time -----------------------------------------------------------------------------

const HOURS = Array.from({ length: 24 }, (_, hour) => hour)

/* Every five minutes, and the value's own minute if it is between them. */
const minutes = computed(() => {
  const list = Array.from({ length: 12 }, (_, index) => index * 5)
  const own = parts.value?.minute
  return own !== undefined && !list.includes(own) ? [...list, own].sort((a, b) => a - b) : list
})

function setTime(hour: number | null, minute: number | null) {
  const current = parts.value ?? today()
  write({ ...current, hour: hour ?? current.hour, minute: minute ?? current.minute })
}

const hoursList = useTemplateRef<HTMLElement>('hours')
const minutesList = useTemplateRef<HTMLElement>('minutes')

/* On opening, the chosen hour and minute are brought into the middle of their columns. */
watch(open, async (value) => {
  if (!value || props.dateOnly) return
  await nextTick()
  for (const list of [hoursList.value, minutesList.value]) {
    const chosen = list?.querySelector<HTMLElement>('[aria-pressed="true"]')
    if (list && chosen) list.scrollTop = chosen.offsetTop - list.clientHeight / 2 + chosen.clientHeight / 2
  }
})

// --- In words -----------------------------------------------------------------------------

const shown = computed(() => {
  if (!parts.value) return ''
  const day = new Date(parts.value.year, parts.value.month - 1, parts.value.day)
  const words = day.toLocaleDateString('en', { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' })
  return props.dateOnly ? words : `${words} at ${pad(parts.value.hour)}:${pad(parts.value.minute)}`
})
</script>

<template>
  <v-menu
    v-model="open"
    :close-on-content-click="false"
    location="bottom start"
    offset="4"
  >
    <template #activator="{ props: menu }">
      <v-text-field
        v-bind="{ ...menu, ...$attrs }"
        :aria-label="label"
        :density="density"
        :model-value="shown"
        class="ps-date-time-field cursor-pointer"
        hide-details
        placeholder="Choose"
        prepend-inner-icon="material-symbols:calendar-month-outline"
        readonly
        variant="outlined"
      />
    </template>

    <v-card
      border
      class="ps-date-time-field__menu"
      color="surface"
      flat
    >
      <div class="flex">
        <v-date-picker
          v-model="date"
          :first-day-of-week="1"
          :min="minDate"
          bg-color="transparent"
          color="primary"
          hide-header
          class="ps-date-picker"
          show-adjacent-months
          width="280"
        />

        <!-- The time: an hour and a minute, each a column to pick from. -->
        <div v-if="!dateOnly" class="flex w-36 flex-col border-l border-line">
          <div class="flex h-12 items-center justify-center border-b border-line font-mono text-lg text-ink">
            {{ parts ? `${pad(parts.hour)}:${pad(parts.minute)}` : '--:--' }}
          </div>
          <div class="flex min-h-0 flex-1">
            <ul ref="hours" aria-label="Hour" class="m-0 h-72 flex-1 list-none overflow-y-auto border-r border-line/60 p-1">
              <li v-for="hour in HOURS" :key="hour">
                <button
                  :aria-pressed="parts?.hour === hour"
                  :class="parts?.hour === hour ? 'bg-primary text-on-primary' : 'text-ink/75 hover:bg-ink/5'"
                  class="w-full cursor-pointer rounded-(--ps-radius-small) py-1.5 font-mono text-xs transition-colors duration-100 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary"
                  type="button"
                  @click="setTime(hour, null)"
                >
                  {{ pad(hour) }}
                </button>
              </li>
            </ul>
            <ul ref="minutes" aria-label="Minute" class="m-0 h-72 flex-1 list-none overflow-y-auto p-1">
              <li v-for="minute in minutes" :key="minute">
                <button
                  :aria-pressed="parts?.minute === minute"
                  :class="parts?.minute === minute ? 'bg-primary text-on-primary' : 'text-ink/75 hover:bg-ink/5'"
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
      </div>

      <footer v-if="!dateOnly" class="flex items-center justify-between gap-2 border-t border-line px-3 py-2">
        <span class="truncate text-[11px] text-ink/55">{{ shown || 'Nothing chosen yet' }}</span>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="open = false"
        >
          Done
        </v-btn>
      </footer>
    </v-card>
  </v-menu>
</template>

<style scoped>
/* The field reads as a choice, not a box to type in. */
.ps-date-time-field :deep(input) {
  cursor: pointer;
}
</style>
