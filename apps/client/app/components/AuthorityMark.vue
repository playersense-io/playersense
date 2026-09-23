<script setup lang="ts">
// Who asserts a key, beside the key, wherever a rule reads one (ADR 0027): the server, the
// player's history, the SDK or the game itself. The glyph carries the tone and the tooltip the
// sentence; `labelled` adds the short word, for a list where the column has room for it.
const props = withDefaults(defineProps<{
  authority: Authority
  labelled?: boolean
}>(), {
  labelled: false
})

const about = computed(() => AUTHORITIES[props.authority])
</script>

<template>
  <v-tooltip location="top" max-width="300">
    <template #activator="{ props: tip }">
      <span
        v-bind="tip"
        :aria-label="about.label"
        :style="{ '--mark': `var(--v-theme-${about.tone})` }"
        class="ps-authority-mark inline-flex flex-none items-center gap-1 text-[11px] leading-4"
        role="img"
      >
        <Icon :name="about.icon" aria-hidden="true" class="text-sm" />
        <span v-if="labelled">{{ about.short }}</span>
      </span>
    </template>
    <p class="m-0 text-xs font-medium">
      {{ about.label }}
    </p>
    <p class="m-0 text-xs opacity-80">
      {{ about.note }}
    </p>
  </v-tooltip>
</template>

<style scoped>
.ps-authority-mark {
  color: color-mix(in srgb, rgb(var(--mark)) 70%, rgb(var(--v-theme-on-surface)));
}
</style>
