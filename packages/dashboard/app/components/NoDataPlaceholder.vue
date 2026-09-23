<script setup lang="ts">
/**
 * The band under a figure when there is no series to draw yet: a faint wave with a
 * fainter fill, and a pill in the middle saying so in words. The wave is a stand-in for
 * the line that will be there, at the strength of a watermark, so the tile keeps the
 * height and the shape of a tile with data and the words carry the meaning.
 *
 * The wave is seeded so it holds still between visits and differs between neighbours,
 * which is what stops a row of tiles reading as one picture repeated.
 */
import { areaPath, noDataPath } from '../utils/sparkline'

const props = withDefaults(
  defineProps<{
    seed?: number
    label?: string
    height?: number
  }>(),
  { seed: 1, label: 'No data', height: 48 }
)

const WIDTH = 400
const gradient = useId()
const line = computed(() => noDataPath(props.seed, WIDTH, props.height))
const area = computed(() => areaPath(line.value, WIDTH, props.height))
</script>

<template>
  <div :style="{ height: `${height}px` }" class="relative overflow-hidden text-ink">
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
          <stop offset="0%" stop-color="currentColor" stop-opacity="0.07" />
          <stop offset="100%" stop-color="currentColor" stop-opacity="0" />
        </linearGradient>
      </defs>
      <path :d="area" :fill="`url(#${gradient})`" />
      <path
        :d="line"
        fill="none"
        stroke="currentColor"
        stroke-opacity="0.28"
        stroke-width="1"
        vector-effect="non-scaling-stroke"
      />
    </svg>
    <span
      class="absolute top-[40%] left-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full border border-line bg-surface px-2 py-0.5 text-[11px] leading-4 font-medium whitespace-nowrap text-ink/55"
    >
      {{ label }}
    </span>
  </div>
</template>
