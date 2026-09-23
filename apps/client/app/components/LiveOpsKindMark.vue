<script setup lang="ts">
// An event's kind as a square of its colour with its glyph, and the kind's name beside it when
// there is room. The same colour is the event's bar on the calendar and its wash on a chart, so
// "the orange ones" means the same thing on every screen.
withDefaults(defineProps<{
  kind: string
  /** The glyph alone, for a row with no room for the word. */
  bare?: boolean
  size?: 'small' | 'large'
}>(), {
  bare: false,
  size: 'small'
})
</script>

<template>
  <span class="inline-flex min-w-0 items-center gap-2">
    <span
      :class="size === 'large' ? 'size-9 rounded-(--ps-radius-large) text-lg' : 'size-6 rounded-(--ps-radius-small) text-sm'"
      :style="{ background: `color-mix(in srgb, ${toneColour(kindLook(kind).tone)} 14%, transparent)`, color: toneColour(kindLook(kind).tone) }"
      aria-hidden="true"
      class="flex flex-none items-center justify-center"
    >
      <Icon :name="kindLook(kind).icon" />
    </span>
    <span v-if="!bare" class="truncate text-xs text-ink/70">{{ kindLook(kind).label }}</span>
  </span>
</template>
