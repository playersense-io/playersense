<script setup lang="ts">
/**
 * A value drawn as a tag: the word in its own colour, on a wash of the same colour.
 *
 * `tone` is a theme colour name, so an outcome takes `success`, `warning` or `error`, a
 * nominal value takes one of the six chart slots, and anything that means nothing in
 * particular takes `neutral`, which is near-black on the light theme and near-white on
 * the dark one and therefore reads as ink in both.
 *
 * The label is mixed towards the theme's ink rather than drawn in the tone itself: three
 * of the chart slots sit under 3:1 on white and this is 11px text. Three parts tone to
 * two of ink keeps the hue and buys the contrast in both themes off one number.
 *
 * Not `StatusBadge`. That one is an outlined pill with a coloured dot, for the state a
 * section or a page is in. A tag colours the whole word, which is what a list of unlike
 * values wants.
 */
withDefaults(
  defineProps<{
    label: string
    /** A theme colour: `success`, `warning`, `error`, `chart-1` … `chart-6`, `neutral`. */
    tone?: string
  }>(),
  { tone: 'neutral' }
)
</script>

<template>
  <span :style="{ '--tag': `var(--v-theme-${tone})` }" class="tag">{{ label }}</span>
</template>

<style scoped>
.tag {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  padding: 2px 8px;
  border-radius: 999px;
  background: color-mix(in srgb, rgb(var(--tag)) 14%, transparent);
  color: color-mix(in srgb, rgb(var(--tag)) 60%, rgb(var(--v-theme-on-surface)));
  font-size: 11px;
  line-height: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
