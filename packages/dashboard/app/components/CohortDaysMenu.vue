<script setup lang="ts">
/**
 * The button and menu a reader picks a cohort table's days from: a preset, or any of the
 * days the server has, ticked one by one. The days sit seven to a row, so the first month
 * reads down like a calendar and a week is a line of it. Ticking a day makes the choice
 * Custom, unless it lands exactly on a preset again.
 */
import {
  COHORT_OFFSETS,
  COHORT_PRESETS,
  type CohortDays,
  type CohortPreset,
  CUSTOM_PRESET,
  choosePreset,
  defaultCohortDays,
  offsetLabel,
  shownOffsets,
  toggleOffset
} from '../utils/cohort-table'

const props = withDefaults(defineProps<{
  presets?: readonly CohortPreset[]
  available?: readonly number[]
}>(), {
  presets: () => COHORT_PRESETS,
  available: () => COHORT_OFFSETS
})

const days = defineModel<CohortDays>({ required: true })

const shown = computed(() => shownOffsets(days.value, props.presets, props.available))
const current = computed(() => props.presets.find(preset => preset.key === days.value.preset))
const pristine = computed(() => days.value.preset === defaultCohortDays(props.presets).preset)

/** The first month as a calendar, then the milestones after it on a row of their own. */
const month = computed(() => props.available.filter(offset => offset <= 30))
const later = computed(() => props.available.filter(offset => offset > 30))

const choices = computed(() => [
  ...props.presets.map(preset => ({
    key: preset.key,
    label: preset.label,
    days: summarize(preset.offsets.filter(offset => props.available.includes(offset)))
  })),
  { key: CUSTOM_PRESET, label: 'Custom', days: days.value.custom.length ? summarize(days.value.custom) : 'pick below' }
])

/** "1 · 3 · 7 · 14 · 30", or "0 – 30" for a run too long to list. */
function summarize(offsets: readonly number[]): string {
  const run = offsets.every((offset, index) => index === 0 || offset === offsets[index - 1]! + 1)
  if (run && offsets.length > 4) return `${offsets[0]} – ${offsets[offsets.length - 1]}`
  return offsets.join(' · ')
}

function choose(key: string) {
  days.value = choosePreset(days.value, key, props.presets, props.available)
}

function toggle(offset: number) {
  days.value = toggleOffset(days.value, offset, props.presets, props.available)
}

function reset() {
  days.value = { ...defaultCohortDays(props.presets), custom: days.value.custom }
}
</script>

<template>
  <v-menu :close-on-content-click="false" :offset="6" location="bottom end">
    <template #activator="{ props: activator }">
      <v-btn
        v-bind="activator"
        class="ps-toolbar-control text-xs font-medium"
        color="neutral"
        prepend-icon="material-symbols:calendar-view-week-outline"
        size="small"
        variant="text"
      >
        {{ current?.label ?? 'Custom' }}
        <span
          v-if="!pristine"
          aria-label="Days changed"
          class="ml-1.5 size-1.5 rounded-full bg-primary"
          role="img"
        />
      </v-btn>
    </template>

    <v-card class="w-76" flat>
      <div class="flex items-baseline justify-between px-3 pt-2.5 pb-1.5">
        <p class="m-0 text-xs font-medium text-ink">
          Days
        </p>
        <p class="m-0 text-[11px] text-ink/55">
          <span class="font-mono tabular-nums">{{ shown.length }}</span>
          shown
        </p>
      </div>

      <ul aria-label="Presets" class="m-0 list-none p-1" role="radiogroup">
        <li v-for="choice in choices" :key="choice.key">
          <button
            :aria-checked="days.preset === choice.key"
            class="flex h-8 w-full items-center gap-2 rounded-(--ps-radius-x-small) px-2 text-left transition-colors duration-150 hover:bg-ink/4 focus-visible:outline-2 focus-visible:outline-primary"
            role="radio"
            type="button"
            @click="choose(choice.key)"
          >
            <span
              :class="days.preset === choice.key ? 'border-primary' : 'border-ink/30'"
              class="flex size-3.5 flex-none items-center justify-center rounded-full border-[1.5px] transition-colors duration-150"
            >
              <span v-if="days.preset === choice.key" class="size-1.5 rounded-full bg-primary" />
            </span>
            <span class="flex-none text-xs whitespace-nowrap text-ink">{{ choice.label }}</span>
            <span class="min-w-0 flex-1 truncate text-right font-mono text-[11px] text-ink/50">{{ choice.days }}</span>
          </button>
        </li>
      </ul>

      <div class="border-t border-line px-3 pt-2.5 pb-3">
        <p class="m-0 mb-2 text-[11px] text-ink/55">
          Days since install
        </p>
        <div aria-label="Days" class="grid grid-cols-7 gap-1" role="group">
          <button
            v-for="offset in month"
            :key="offset"
            :aria-pressed="shown.includes(offset)"
            :class="shown.includes(offset) ? 'bg-primary text-on-primary' : 'bg-ink/4 text-ink/70 hover:bg-ink/8 hover:text-ink'"
            class="h-7 rounded-(--ps-radius-x-small) font-mono text-[11px] tabular-nums transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary"
            type="button"
            @click="toggle(offset)"
          >
            {{ offsetLabel(offset) }}
          </button>
        </div>
        <div v-if="later.length" class="mt-2 grid grid-cols-4 gap-1">
          <button
            v-for="offset in later"
            :key="offset"
            :aria-pressed="shown.includes(offset)"
            :class="shown.includes(offset) ? 'bg-primary text-on-primary' : 'bg-ink/4 text-ink/70 hover:bg-ink/8 hover:text-ink'"
            class="h-7 rounded-(--ps-radius-x-small) font-mono text-[11px] tabular-nums transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary"
            type="button"
            @click="toggle(offset)"
          >
            {{ offsetLabel(offset) }}
          </button>
        </div>
      </div>

      <div class="flex items-center justify-between border-t border-line px-3 py-2">
        <p class="m-0 text-[11px] text-ink/50">
          Kept for this table in this browser.
        </p>
        <button
          :disabled="pristine"
          class="text-xs font-medium text-ink/70 transition-colors duration-150 hover:text-ink disabled:text-ink/30"
          type="button"
          @click="reset"
        >
          Reset
        </button>
      </div>
    </v-card>
  </v-menu>
</template>
