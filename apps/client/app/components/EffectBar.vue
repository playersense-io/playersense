<script setup lang="ts">
// An effect as a bar from zero: to the right and green for a sure gain, to the left and red for a
// sure loss, grey where it could be noise. Behind it, lighter, the range the true effect is in,
// so a long bar on a wide range reads as less certain than a short bar on a narrow one. Rows
// that share `domain` share a scale, which is what makes a column of them a chart; a domain
// with no losses in it puts zero at the left edge rather than wasting half the width.
const props = defineProps<{
  comparison: Comparison
  /** The lowest and highest effect the rows around it reach. Zero is always inside it. */
  domain: readonly [number, number]
}>()

const low = computed(() => Math.min(0, props.domain[0]))
const high = computed(() => Math.max(0, props.domain[1], low.value + 0.01))

/* A lift as a share of the width, clamped to the ends. */
const x = (lift: number) => ((Math.max(low.value, Math.min(high.value, lift)) - low.value) / (high.value - low.value)) * 100
const zero = computed(() => x(0))

const outcome = computed(() => outcomeOf(props.comparison))

const bar = computed(() => {
  const end = x(props.comparison.lift)
  return { left: `${Math.min(zero.value, end)}%`, width: `${Math.max(Math.abs(end - zero.value), 0.8)}%` }
})

const range = computed(() => {
  const from = x(props.comparison.low)
  const to = x(props.comparison.high)
  return { left: `${from}%`, width: `${Math.max(to - from, 0.8)}%` }
})

const TONE = { better: 'bg-success', worse: 'bg-error', unclear: 'bg-ink/40' } as const
const RANGE = { better: 'bg-success/15', worse: 'bg-error/15', unclear: 'bg-ink/10' } as const
</script>

<template>
  <div
    :aria-label="`${(comparison.lift * 100).toFixed(1)}%, likely between ${(comparison.low * 100).toFixed(1)}% and ${(comparison.high * 100).toFixed(1)}%`"
    class="relative h-5 w-full"
    role="img"
  >
    <span
      :class="RANGE[outcome]"
      :style="range"
      aria-hidden="true"
      class="absolute top-1/2 h-3.5 -translate-y-1/2 rounded-full"
    />
    <span
      :class="TONE[outcome]"
      :style="bar"
      aria-hidden="true"
      class="absolute top-1/2 h-1.5 -translate-y-1/2 rounded-full"
    />
    <span :style="{ left: `${zero}%` }" aria-hidden="true" class="absolute inset-y-0 w-px bg-ink/30" />
  </div>
</template>
