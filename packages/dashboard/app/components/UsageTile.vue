<script setup lang="ts">
/**
 * A figure on a small card, the sidebar's unit: the label, the number in the mono face,
 * and under it either the series that produced it or the stand-in for one. The
 * stand-in is shown for a figure of zero with no series behind it, which is the state
 * of every tile before the first thing happens, and the one state a tile must not
 * present as a chart of nothing. A figure that is not zero and has no series, a count
 * of accounts say, gets neither: "no data" under a number is a contradiction.
 */
import { formatFigure } from '../utils/format'

const props = withDefaults(
  defineProps<{
    label: string
    value: number
    /** Daily values behind the figure. Absent or all zero, the tile draws the stand-in. */
    series?: readonly number[]
    /** Tells one tile's stand-in wave from the next one's. */
    seed?: number
    /** A sentence behind the info glyph: what the figure counts. */
    info?: string
  }>(),
  { series: () => [], seed: 1, info: '' }
)

const figure = computed(() => formatFigure(props.value, { compact: true }))
const hasSeries = computed(() => props.series.some(value => value > 0))
</script>

<template>
  <div class="flex flex-col gap-2 overflow-hidden rounded-(--ps-radius) border border-line bg-surface pt-3">
    <div class="flex items-center gap-1 px-3.5 text-xs text-ink/60">
      <span class="truncate">{{ label }}</span>
      <v-tooltip
        v-if="info"
        :text="info"
        location="top"
        max-width="260"
      >
        <template #activator="{ props: tip }">
          <Icon
            v-bind="tip"
            :aria-label="info"
            class="flex-none text-sm text-ink/40"
            name="material-symbols:info-outline"
            role="img"
          />
        </template>
      </v-tooltip>
    </div>
    <p class="m-0 px-3.5 font-mono text-2xl leading-none font-medium tracking-tight text-ink tabular-nums">
      {{ figure.digits }}{{ figure.magnitude }}
    </p>
    <Sparkline v-if="hasSeries" :height="40" :values="series" />
    <NoDataPlaceholder v-else-if="value === 0" :height="40" :seed="seed" />
    <!-- A figure with no series behind it, such as a count of accounts: the band stays,
         empty, so the tile keeps the height of its neighbours. -->
    <div v-else aria-hidden="true" class="h-10" />
  </div>
</template>
