<script setup lang="ts">
// One dimension's values as a list: the value, its count, its share, and a bar scaled to
// the largest so the order reads at a glance. A list rather than a chart — a handful of
// platforms or countries is something to read, not a picture to decode.
const props = defineProps<{
  rows: readonly { value: string, count: number }[]
  /** Names the list for a screen reader: "Occurrences by platform". */
  label: string
}>()

const total = computed(() => props.rows.reduce((sum, row) => sum + row.count, 0))
const largest = computed(() => Math.max(1, ...props.rows.map(row => row.count)))
</script>

<template>
  <!-- Nothing to list yet: the watermark every chart uses, at the height of a short list. -->
  <NoDataPlaceholder
    v-if="!rows.length || !total"
    :height="96"
    :seed="label.length"
    label="No data yet"
  />
  <ul v-else :aria-label="label" class="m-0 flex list-none flex-col gap-3 p-0">
    <li v-for="row in rows" :key="row.value" class="flex flex-col gap-1.5">
      <div class="flex items-baseline justify-between gap-3 text-[13px]">
        <span class="truncate text-ink">{{ row.value }}</span>
        <span class="flex flex-none items-baseline gap-2 font-mono tabular-nums">
          <span class="text-ink">{{ writeFigure(row.count, { compact: true }) }}</span>
          <span class="w-9 text-right text-xs text-ink/50">
            {{ total ? Math.round(row.count / total * 100) : 0 }}%
          </span>
        </span>
      </div>
      <div class="h-1.5 overflow-hidden rounded-full bg-ink/6">
        <div
          :style="{ width: `${row.count / largest * 100}%` }"
          class="h-full rounded-full bg-primary/70 transition-[width] duration-300"
        />
      </div>
    </li>
  </ul>
</template>
