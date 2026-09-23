<script setup lang="ts">
// A table cell's figure with how far it moved beside it, in the same plain coloured text
// the Overview's boards use, so a row reads the way the headline above it does.
const props = defineProps<{
  /** The figure as the cell shows it. */
  written: string
  value: number
  previous: number
}>()

const trend = computed(() => trendOf(props.value, props.previous))
</script>

<template>
  <span class="inline-flex items-center justify-end gap-2">
    <span>{{ written }}</span>
    <span
      :class="TREND_TONES[trend.tone]"
      class="inline-flex w-14 flex-none items-center justify-end gap-0.5 text-[11px]"
    >
      <Icon :name="TREND_ICONS[trend.direction]" aria-hidden="true" class="text-xs" />
      {{ trend.text }}
    </span>
  </span>
</template>
