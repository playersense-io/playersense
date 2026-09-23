<script setup lang="ts">
/**
 * The mark that says which series is which, beside a name in the legend and in the
 * table's head: a short stroke for a line, a small square for a bar or an area, so the
 * key mirrors the mark it stands for. The tooltip draws the same two shapes in its own
 * markup, and MetricChart.vue keeps those styles beside this file's.
 *
 * Coloured by an inline property rather than a class, because the colour is the theme's
 * chart slot read at runtime and a utility class has to be written out to exist.
 */
withDefaults(
  defineProps<{
    /** Any colour a canvas takes: the palette slot's hex, or the comparison grey. */
    color: string
    kind: 'line' | 'fill'
    dashed?: boolean
    /** The series is switched off; the key stays so the reader can switch it back. */
    muted?: boolean
  }>(),
  { dashed: false, muted: false }
)
</script>

<template>
  <span
    :class="[kind === 'line' ? 'metric-key--line' : 'metric-key--fill', dashed && kind === 'line' ? 'metric-key--dashed' : '', muted ? 'opacity-30' : '']"
    :style="{ '--metric-key': color }"
    aria-hidden="true"
    class="metric-key flex-none transition-opacity duration-150"
  />
</template>

<style scoped>
.metric-key--line {
  background: var(--metric-key);
  border-radius: 1px;
  height: 2px;
  width: 12px;
}

.metric-key--dashed {
  background: repeating-linear-gradient(90deg, var(--metric-key) 0 3px, transparent 3px 5px);
}

.metric-key--fill {
  background: var(--metric-key);
  border-radius: 2px;
  height: 8px;
  margin-inline: 2px;
  width: 8px;
}
</style>
