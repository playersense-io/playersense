<script setup lang="ts">
// How a variant did against the control, drawn as its range: a line from the 95% low to the
// 95% high, a dot at the best guess, and the control at zero down the middle. Wholly right of
// zero is a sure gain and green, wholly left a sure loss and red, across it not yet known and
// grey. It says "better, worse or can't tell yet" before a number is read.
//
// `extent` is the widest lift the rows around it reach, so several ranges share one scale.
const props = defineProps<{
  comparison: Comparison
  extent: number
}>()

const scale = computed(() => Math.max(props.extent, 0.01))
const x = (lift: number) => 50 + Math.max(-1, Math.min(1, lift / scale.value)) * 50

const tone = computed(() => {
  const outcome = outcomeOf(props.comparison)
  return outcome === 'better' ? 'bg-success' : outcome === 'worse' ? 'bg-error' : 'bg-ink/40'
})
</script>

<template>
  <div class="relative h-5 w-full" role="img" :aria-label="`From ${(comparison.low * 100).toFixed(1)}% to ${(comparison.high * 100).toFixed(1)}%`">
    <span aria-hidden="true" class="absolute inset-y-0 left-1/2 w-px bg-ink/25" />
    <span
      :class="tone"
      :style="{ left: `${x(comparison.low)}%`, width: `${Math.max(x(comparison.high) - x(comparison.low), 0.5)}%` }"
      aria-hidden="true"
      class="absolute top-1/2 h-1 -translate-y-1/2 rounded-full opacity-60"
    />
    <span
      :class="tone"
      :style="{ left: `${x(comparison.lift)}%` }"
      aria-hidden="true"
      class="absolute top-1/2 size-2.5 -translate-1/2 rounded-full ring-2 ring-surface"
    />
  </div>
</template>
