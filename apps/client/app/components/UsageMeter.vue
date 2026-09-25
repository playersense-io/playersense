<script setup lang="ts">
// One billing meter against the plan's allowance: what was used this month, what is
// included, and how full that is. Past the allowance is not an error — usage beyond it is
// billed from the wallet — so the bar turns the warning ink as it nears the line and says
// in words what happens after it, rather than going red. A meter with nothing counted yet
// (null) is a dash and an empty bar, not a zero.
const props = withDefaults(defineProps<{
  label: string
  used: number | null
  included: number
  format?: 'number' | 'bytes'
  /** What the meter counts, behind the info glyph. */
  info?: string
  loading?: boolean
}>(), {
  format: 'number',
  info: '',
  loading: false
})

const share = computed(() => (props.included > 0 && props.used !== null ? props.used / props.included : 0))
const percent = computed(() => Math.round(share.value * 100))

const write = (value: number) => writeFigure(value, { format: props.format, compact: props.format === 'number' })

const tone = computed(() => (share.value >= 0.85 ? 'bg-warning' : 'bg-primary'))
</script>

<template>
  <div class="flex min-w-0 flex-col gap-2.5">
    <div class="flex items-center gap-1.5 text-xs text-ink/60">
      {{ label }}
      <v-tooltip
        v-if="info"
        :text="info"
        location="top"
        max-width="280"
      >
        <template #activator="{ props: activator }">
          <Icon
            v-bind="activator"
            :aria-hidden="true"
            class="text-sm text-ink/40"
            name="material-symbols:info-outline"
          />
        </template>
      </v-tooltip>
    </div>

    <div v-if="loading" aria-hidden="true" class="h-6 w-32 animate-pulse rounded-full bg-ink/8" />
    <p v-else class="m-0 flex items-baseline gap-1.5">
      <span v-if="used === null" :aria-label="`${label}: no data yet`" class="font-mono text-xl text-ink/30">&ndash;</span>
      <span v-else class="font-mono text-xl text-ink tabular-nums">{{ write(used) }}</span>
      <span class="text-xs text-ink/55">of {{ write(included) }} included</span>
    </p>

    <div
      :aria-label="`${label}: ${percent}% of the allowance`"
      :aria-valuenow="percent"
      aria-valuemax="100"
      aria-valuemin="0"
      class="h-1.5 overflow-hidden rounded-full bg-ink/8"
      role="progressbar"
    >
      <div
        :class="tone"
        :style="{ width: `${Math.min(percent, 100)}%` }"
        class="h-full rounded-full transition-[width] duration-300"
      />
    </div>

    <p class="m-0 text-xs text-ink/55">
      <template v-if="used === null">
        No data yet
      </template>
      <template v-else-if="share > 1">
        {{ write(used - included) }} over, billed from the wallet
      </template>
      <template v-else>
        {{ percent }}% used · {{ write(included - (used ?? 0)) }} left
      </template>
    </p>
  </div>
</template>
