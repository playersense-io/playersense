<script lang="ts">
export interface Kpi {
  key: string
  label: string
  /** Already written, with its sign or percent: "$388.4k", "53.1k", "2.35%". */
  value: string
  /** Set after the figure, smaller: "min". */
  unit?: string
  /** The exact figure, for the tooltip on a compacted one. */
  exact?: string
  trend: Trend
  /** One value per day of the period, oldest first. */
  series: readonly number[]
  /** What the figure counts, behind the info glyph. */
  info?: string
}
</script>

<script setup lang="ts">
// A row of figures that belong together, as one surface cut into equal cells by hairlines:
// the label, the figure, how far it moved, and the line of how it got there. The period
// the change is measured against is said once, in the card's description, rather than
// under every figure.
//
// Five across from `lg`, two below it; with five cells the last one spans the pair so no
// lit square is left where a cell is missing.
defineProps<{
  title: string
  description?: string
  items: readonly Kpi[]
}>()

const TONES: Record<TrendTone, string> = {
  good: 'bg-success/10 text-success',
  bad: 'bg-error/10 text-error',
  neutral: 'bg-ink/5 text-ink/60'
}
</script>

<template>
  <LayerCard :description="description" :padded="false" :title="title">
    <div class="grid grid-cols-2 gap-px bg-line lg:grid-cols-5">
      <div
        v-for="item in items"
        :key="item.key"
        class="
          flex min-w-0 flex-col gap-2 bg-surface px-4 pt-4
          last:col-span-2
          lg:last:col-span-1
        "
      >
        <div class="flex min-w-0 items-center gap-1.5 text-xs text-ink/55">
          <span class="truncate">{{ item.label }}</span>
          <v-tooltip
            v-if="item.info"
            :text="item.info"
            location="top"
            max-width="280"
          >
            <template #activator="{ props: activator }">
              <Icon
                v-bind="activator"
                aria-hidden="true"
                class="flex-none text-sm text-ink/35"
                name="material-symbols:info-outline"
              />
            </template>
          </v-tooltip>
        </div>

        <div class="flex min-w-0 items-center justify-between gap-2">
          <p :title="item.exact" class="m-0 flex min-w-0 items-baseline gap-1">
            <span class="truncate font-mono text-2xl leading-8 text-ink tabular-nums">{{ item.value }}</span>
            <span v-if="item.unit" class="text-xs text-ink/55">{{ item.unit }}</span>
          </p>

          <span
            :class="TONES[item.trend.tone]"
            class="
              inline-flex h-5 flex-none items-center gap-0.5 rounded-full px-1.5 font-mono
              text-[11px] tabular-nums
            "
          >
            <Icon :name="TREND_ICONS[item.trend.direction]" aria-hidden="true" class="text-xs" />
            {{ item.trend.text }}
          </span>
        </div>

        <Sparkline :height="36" :values="item.series" class="-mx-4 mt-auto" />
      </div>
    </div>
  </LayerCard>
</template>
