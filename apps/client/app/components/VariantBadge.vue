<script setup lang="ts">
// A variant as it is named everywhere on a test's pages: its letter in the colour its line takes
// on the charts, and its own name beside it when it has one. A is the control.
const props = defineProps<{
  experiment: Pick<ExperimentDef, 'variants'>
  variant: VariantDef
}>()

const index = computed(() => props.experiment.variants.findIndex(entry => entry.id === props.variant.id))
const letter = computed(() => variantLetter(index.value))
const named = computed(() => props.variant.name && props.variant.name !== letter.value)
</script>

<template>
  <span class="inline-flex min-w-0 items-center gap-2">
    <span
      :style="{ background: `rgb(var(--v-theme-chart-${(index % 6) + 1}))` }"
      aria-hidden="true"
      class="flex size-5 flex-none items-center justify-center rounded-(--ps-radius-x-small) font-mono text-[11px] font-medium text-white"
    >{{ letter }}</span>
    <span class="truncate">{{ named ? variant.name : `Variant ${letter}` }}</span>
  </span>
</template>
