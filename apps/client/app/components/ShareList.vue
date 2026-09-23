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
}>(), {
  tone: 'primary'
})

const largest = computed(() => Math.max(0, ...props.rows.map(row => row.value)) || 1)
</script>

<template>
  <ul :aria-label="label" class="m-0 flex list-none flex-col p-0">
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
