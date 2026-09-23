<script setup lang="ts">
/**
 * A series as one line under a figure, filled faintly to the base. No axes, no labels,
 * no pointer: the figure above it is the reading and the line says how it got there.
 * Drawn in the accent, at a stroke that stays one and a half pixels however the box
 * is stretched.
 */
import { areaPath, sparklinePath } from '../utils/sparkline'

const props = withDefaults(
  defineProps<{
    values: readonly number[]
    height?: number
  }>(),
  { height: 48 }
)

const WIDTH = 400
const gradient = useId()
const line = computed(() => sparklinePath(props.values, WIDTH, props.height, 4))
const area = computed(() => areaPath(line.value, WIDTH, props.height))
</script>

<template>
  <div :style="{ height: `${height}px` }" class="overflow-hidden text-primary">
    <svg
      :viewBox="`0 0 ${WIDTH} ${height}`"
      aria-hidden="true"
      class="block h-full w-full"
      preserveAspectRatio="none"
    >
      <defs>
        <linearGradient
          :id="gradient"
          x1="0"
          x2="0"
          y1="0"
          y2="1"
        >
          <stop offset="0%" stop-color="currentColor" stop-opacity="0.22" />
          <stop offset="100%" stop-color="currentColor" stop-opacity="0" />
        </linearGradient>
      </defs>
      <path :d="area" :fill="`url(#${gradient})`" />
      <path
        :d="line"
        fill="none"
        stroke="currentColor"
        stroke-linejoin="round"
        stroke-width="1.5"
        vector-effect="non-scaling-stroke"
      />
    </svg>
  </div>
</template>
