<script lang="ts">
export interface ShareRow {
  key: string
  label: string
  value: number
  /** The figure as written: "$84.7k". */
  display: string
  /** A quieter figure after it: "27% · 3,210 payers". */
  note?: string
  mark?: SegmentMarkValue
}
</script>

<script setup lang="ts">
// A ranked list where each row's size against the others is the point: revenue by
// country, by store, refunds by reason. Each row has its mark, its name, a bar drawn
// against the largest row and its figure, and, when given, a quieter second figure.
//
// Bars against the largest rather than the total, so the leader fills the width and the
// rest read as fractions of it; the share of the total, when it matters, is the caller's
// to write as the second figure.
const props = withDefaults(defineProps<{
  rows: readonly ShareRow[]
  /** Names the list for a screen reader. */
  label: string
  /** A theme colour for the bars; the accent when absent. */
  tone?: string
  /** While the rows are on their way: this many rows of bones, each at a row's height. */
  loading?: boolean
  /** How many rows to draw while loading. */
  skeletonRows?: number
}>(), {
  tone: 'primary',
  loading: false,
  skeletonRows: 6
})

const largest = computed(() => Math.max(0, ...props.rows.map(row => row.value)) || 1)
</script>

<template>
  <ul
    v-if="loading"
    :aria-label="`Loading ${label}`"
    aria-busy="true"
    class="m-0 flex list-none flex-col p-0"
  >
    <li
      v-for="index in skeletonRows"
      :key="index"
      aria-hidden="true"
      class="grid min-w-0 grid-cols-[minmax(0,11rem)_minmax(3rem,1fr)_auto] items-center gap-3 py-1.5 text-[13px]"
    >
      <!-- A line of the row's own text high, so a bone row is as tall as the row it stands for. -->
      <span class="flex h-lh items-center gap-2">
        <span class="ps-bone size-3.5 rounded-full!" />
        <span :style="{ width: `${4 + (index * 37) % 4}rem` }" class="ps-bone h-3" />
      </span>
      <span :style="{ width: `${Math.max(14, 92 - index * 9)}%` }" class="ps-bone h-1.5 rounded-full!" />
      <span class="ps-bone h-3 w-14" />
    </li>
  </ul>
  <ul v-else :aria-label="label" class="m-0 flex list-none flex-col p-0">
    <li
      v-for="row in rows"
      :key="row.key"
      class="grid min-w-0 grid-cols-[minmax(0,11rem)_minmax(3rem,1fr)_auto] items-center gap-3 py-1.5 text-[13px]"
    >
      <span class="flex min-w-0 items-center gap-2 text-ink">
        <SegmentMark v-if="row.mark" :mark="row.mark" />
        <span class="truncate">{{ row.label }}</span>
      </span>
      <span class="h-1.5 overflow-hidden rounded-full bg-ink/6">
        <span
          :style="{ width: `${Math.max(1, row.value / largest * 100)}%`, background: `rgb(var(--v-theme-${tone}))` }"
          class="block h-full rounded-full"
        />
      </span>
      <span class="flex items-baseline justify-end gap-2 text-right whitespace-nowrap">
        <span class="font-mono text-ink">{{ row.display }}</span>
        <span v-if="row.note" class="min-w-20 font-mono text-[11px] text-ink/50">{{ row.note }}</span>
      </span>
    </li>
  </ul>
</template>
