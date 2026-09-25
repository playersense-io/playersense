<script setup lang="ts">
import type { LogPreset, LogRange } from '~/composables/logs'

/**
 * The stretch of time a Cloud Logging page reads: the range picker, to the minute, with a step
 * back, a step forward and a zoom out beside it, which are how a person walks along a timeline
 * once the chart has put them somewhere.
 */
const props = defineProps<{
  range: LogRange
  /** The oldest moment the retention still holds. */
  earliest?: number
}>()

const emit = defineEmits<{
  preset: [preset: LogPreset]
  absolute: [from: number, to: number]
  shift: [direction: -1 | 1]
  zoomOut: []
}>()

const atPresent = computed(() => props.range.preset !== null || Date.now() - props.range.to < 60_000)
</script>

<template>
  <div class="flex items-center gap-0.5">
    <v-tooltip location="top" text="Earlier">
      <template #activator="{ props: tip }">
        <v-btn
          v-bind="tip"
          aria-label="Earlier"
          color="neutral"
          density="comfortable"
          icon="material-symbols:chevron-left"
          size="small"
          variant="text"
          @click="emit('shift', -1)"
        />
      </template>
    </v-tooltip>
    <DateTimeRangePicker
      :earliest="earliest"
      :from="range.from"
      :preset="range.preset"
      :presets="LOG_TIME_PRESETS"
      :to="range.to"
      @apply="(from, to) => emit('absolute', from, to)"
      @preset="(value) => emit('preset', value as LogPreset)"
    />
    <v-tooltip location="top" text="Later">
      <template #activator="{ props: tip }">
        <v-btn
          v-bind="tip"
          :disabled="atPresent"
          aria-label="Later"
          color="neutral"
          density="comfortable"
          icon="material-symbols:chevron-right"
          size="small"
          variant="text"
          @click="emit('shift', 1)"
        />
      </template>
    </v-tooltip>
    <v-tooltip location="top" text="Zoom out">
      <template #activator="{ props: tip }">
        <v-btn
          v-bind="tip"
          aria-label="Zoom out"
          color="neutral"
          density="comfortable"
          icon="material-symbols:zoom-out"
          size="small"
          variant="text"
          @click="emit('zoomOut')"
        />
      </template>
    </v-tooltip>
  </div>
</template>
