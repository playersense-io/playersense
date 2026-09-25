<script setup lang="ts">
// The steps a funnel is made of, edited in place: each one an event from the catalogue,
// narrowed by any number of property conditions, moved up or down, or taken out. A funnel
// keeps between two and ten steps, so the controls that would take it past either end are
// off rather than refused after the fact.
//
// Choosing a different event clears the step's conditions, since they name properties of
// the event it was.
import type { EventDefinition } from '~/utils/events'

const props = defineProps<{
  catalogue: readonly EventDefinition[]
}>()

const steps = defineModel<FunnelStep[]>({ required: true })

/** The most steps the funnel query takes. */
const MOST_STEPS = 8

function update(index: number, change: Partial<FunnelStep>) {
  steps.value = steps.value.map((step, at) => (at === index ? { ...step, ...change } : step))
}

function choose(index: number, event: string) {
  if (steps.value[index]?.event !== event) update(index, { event, where: [] })
}

function move(index: number, direction: -1 | 1) {
  const next = [...steps.value]
  const target = index + direction
  if (target < 0 || target >= next.length) return
  ;[next[index], next[target]] = [next[target]!, next[index]!]
  steps.value = next
}

function remove(index: number) {
  if (steps.value.length <= MIN_STEPS) return
  steps.value = steps.value.filter((_, at) => at !== index)
}

function add() {
  if (steps.value.length >= MOST_STEPS) return
  const last = steps.value[steps.value.length - 1]
  steps.value = [...steps.value, { event: last?.event ?? props.catalogue[0]?.name ?? '', where: [] }]
}
</script>

<template>
  <ol class="m-0 list-none p-0">
    <li
      v-for="(step, index) in steps"
      :key="index"
      class="group flex min-h-12 flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-line px-5 py-2"
    >
      <span class="flex size-5 flex-none items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">
        {{ index + 1 }}
      </span>

      <EventPicker
        :catalogue="catalogue"
        :label="`Step ${index + 1} event`"
        :model-value="step.event"
        @update:model-value="choose(index, $event)"
      />

      <EventConditions
        :catalogue="catalogue"
        :event="step.event"
        :model-value="step.where"
        @update:model-value="update(index, { where: $event })"
      />

      <span class="ml-auto flex items-center opacity-60 transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100">
        <button
          :aria-label="`Move step ${index + 1} up`"
          :disabled="index === 0"
          class="ps-funnel-steps__control"
          type="button"
          @click="move(index, -1)"
        >
          <Icon aria-hidden="true" class="text-base" name="material-symbols:keyboard-arrow-up" />
        </button>
        <button
          :aria-label="`Move step ${index + 1} down`"
          :disabled="index === steps.length - 1"
          class="ps-funnel-steps__control"
          type="button"
          @click="move(index, 1)"
        >
          <Icon aria-hidden="true" class="text-base" name="material-symbols:keyboard-arrow-down" />
        </button>
        <button
          :aria-label="`Remove step ${index + 1}`"
          :disabled="steps.length <= MIN_STEPS"
          class="ps-funnel-steps__control"
          type="button"
          @click="remove(index)"
        >
          <Icon aria-hidden="true" class="text-base" name="material-symbols:close" />
        </button>
      </span>
    </li>

    <li class="flex items-center justify-between px-5 py-2">
      <v-btn
        :disabled="steps.length >= MAX_STEPS"
        class="text-xs font-medium"
        color="neutral"
        prepend-icon="material-symbols:add"
        size="small"
        variant="text"
        @click="add"
      >
        Add step
      </v-btn>
      <span class="text-[11px] text-ink/45">{{ steps.length }} of {{ MOST_STEPS }} steps</span>
    </li>
  </ol>
</template>

<style scoped>
.ps-funnel-steps__control {
  align-items: center;
  border-radius: var(--ps-radius-x-small);
  color: rgba(var(--v-theme-on-surface), 0.6);
  display: inline-flex;
  height: 26px;
  justify-content: center;
  width: 26px;
}

.ps-funnel-steps__control:hover:not(:disabled) {
  background: rgba(var(--v-theme-on-surface), 0.06);
  color: rgb(var(--v-theme-on-surface));
}

.ps-funnel-steps__control:disabled {
  color: rgba(var(--v-theme-on-surface), 0.2);
}
</style>
