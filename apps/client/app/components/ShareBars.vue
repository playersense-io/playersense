<script setup lang="ts">
// A dimension's values as bars of their share: the name, the figure, and a bar scaled to
// the largest row so the order reads at a glance. Written out rather than charted — five
// rows of a breakdown are a list somebody reads, not a picture they decode.
const props = withDefaults(defineProps<{
  rows: readonly { value: string, figure: number }[]
  /** Names the list for a screen reader: "Active players by platform". */
  label: string
  loading?: boolean
}>(), {
  loading: false
})

const total = computed(() => props.rows.reduce((sum, row) => sum + row.figure, 0))
const largest = computed(() => Math.max(1, ...props.rows.map(row => row.figure)))
</script>

<template>
  <ul :aria-label="label" class="m-0 flex list-none flex-col gap-3 p-0">
    <template v-if="loading">
      <li
        v-for="row in 5"
        :key="row"
        aria-hidden="true"
        class="flex flex-col gap-1.5"
      >
        <span class="h-3 w-28 animate-pulse rounded-full bg-ink/8" />
        <span class="h-1.5 animate-pulse rounded-full bg-ink/6" />
      </li>
    </template>

    <li
      v-for="row in rows"
      v-else
      :key="row.value"
      class="flex flex-col gap-1.5"
    >
      <div class="flex items-baseline justify-between gap-3 text-[13px]">
        <span class="truncate text-ink">{{ row.value }}</span>
        <span class="flex flex-none items-baseline gap-2">
          <span class="font-mono text-ink tabular-nums">{{ writeFigure(row.figure, { compact: true }) }}</span>
          <span class="w-10 text-right font-mono text-xs text-ink/50 tabular-nums">
            {{ total ? Math.round(row.figure / total * 100) : 0 }}%
          </span>
        </span>
      </div>
      <div class="h-1.5 overflow-hidden rounded-full bg-ink/6">
        <div
          :style="{ width: `${row.figure / largest * 100}%` }"
          class="h-full rounded-full bg-primary/70 transition-[width] duration-300"
        />
      </div>
    </li>
  </ul>
</template>
