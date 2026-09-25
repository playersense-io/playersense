<script lang="ts">
export interface Kpi {
  key: string
  label: string
  /**
   * Already written, with its sign or percent: "$388.4k", "53.1k", "2.35%". Null is a figure
   * with nothing behind it yet (no rows came back, or they were held back), drawn as a dash
   * rather than a zero: a zero says the figure was taken and was nothing.
   */
  value: string | null
  /** Set after the figure, smaller: "min". */
  unit?: string
  /** The exact figure, for the tooltip on a compacted one. */
  exact?: string
  /** How far it moved; absent for a figure with nothing to compare against. */
  trend?: Trend
  /** What the figure counts, behind the info glyph. */
  info?: string
}
</script>

<script setup lang="ts">
// A row of figures that belong together, as one surface cut into equal cells by hairlines:
// the label, the figure and how far it moved. The period the change is measured against is
// said once, in the card's description, rather than under every figure.
//
// Five across from `lg`, two below it; with five cells the last one spans the pair so no
// lit square is left where a cell is missing.
defineProps<{
  title: string
  description?: string
  items: readonly Kpi[]
  /** While the figures are on their way: each cell keeps its label, and its figure is a bone. */
  loading?: boolean
}>()
</script>

<template>
  <SectionCard :description="description" :padded="false" :title="title">
    <div :aria-busy="loading || undefined" class="grid grid-cols-2 gap-px bg-line lg:grid-cols-5">
      <div
        v-for="item in items"
        :key="item.key"
        class="
          flex min-w-0 flex-col gap-2 bg-surface p-4
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

        <div v-if="loading" aria-hidden="true" class="flex h-8 items-center gap-2">
          <span class="ps-kpi-board__bone h-5 w-24" />
          <span class="ps-kpi-board__bone h-3 w-10" />
        </div>

        <div v-else class="flex min-w-0 items-center gap-2">
          <p v-if="item.value === null" :aria-label="`${item.label}: no data yet`" class="m-0 font-mono text-2xl leading-8 text-ink/30">
            &ndash;
          </p>
          <p v-else :title="item.exact" class="m-0 flex min-w-0 items-baseline gap-1">
            <span class="truncate font-mono text-2xl leading-8 text-ink tabular-nums">{{ item.value }}</span>
            <span v-if="item.unit" class="text-xs text-ink/55">{{ item.unit }}</span>
          </p>

          <span
            v-if="item.trend && item.value !== null"
            :class="TREND_TONES[item.trend.tone]"
            class="inline-flex flex-none items-center gap-0.5 font-mono text-xs tabular-nums"
          >
            <Icon :name="TREND_ICONS[item.trend.direction]" aria-hidden="true" class="text-sm" />
            {{ item.trend.text }}
          </span>
        </div>
      </div>
    </div>
  </SectionCard>
</template>

<style scoped>
/* The panel's bone, still, as MetricSkeleton draws its own. */
.ps-kpi-board__bone {
  background: var(--ps-skeleton-bone);
  border-radius: var(--ps-radius-x-small);
  display: block;
}
</style>
