<script setup lang="ts">
// A value a flag serves, drawn the same wherever it appears — a rule, a table cell, a preview,
// a diff — so `true` and `"true"` never look alike. A boolean is a dot and a word, a string is
// quoted, a number is bare, and JSON is its size with the whole document behind a tooltip.
// Null is the game's own default: nothing PlayerSense served.
const props = defineProps<{
  value: Json | null | undefined
}>()

const pretty = computed(() => (props.value !== null && typeof props.value === 'object' ? JSON.stringify(props.value, null, 2) : ''))
</script>

<template>
  <span v-if="value === null || value === undefined" class="text-xs text-ink/50 italic">Game default</span>

  <span v-else-if="typeof value === 'boolean'" class="inline-flex items-center gap-1.5 font-mono text-xs text-ink">
    <span :class="value ? 'bg-success' : 'bg-ink/30'" aria-hidden="true" class="size-1.5 flex-none rounded-full" />
    {{ value }}
  </span>

  <v-tooltip v-else-if="pretty" location="top" max-width="420">
    <template #activator="{ props: tip }">
      <span
        v-bind="tip"
        class="inline-flex max-w-full items-center gap-1 rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 font-mono text-[11px] leading-5 text-ink/80"
      >
        <Icon :aria-hidden="true" class="flex-none text-xs text-ink/45" name="material-symbols:data-object" />
        <span class="truncate">{{ writeValue(value) }}</span>
      </span>
    </template>
    <pre class="m-0 font-mono text-[11px] leading-4 whitespace-pre-wrap">{{ pretty }}</pre>
  </v-tooltip>

  <span v-else class="truncate font-mono text-xs text-ink">{{ writeValue(value) }}</span>
</template>
