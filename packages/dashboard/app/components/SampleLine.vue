<script setup lang="ts">
/**
 * One line of a sample: a glyph that says what kind of thing it is, then the thing. A
 * claim is drawn as its key and its value in two weights, the value in the accent,
 * because the value is what the reader came to check; anything else goes in the slot,
 * in the mono face when it is a string a machine reads and the text face when it is a
 * sentence a person does.
 */
withDefaults(
  defineProps<{
    /** Written out in full so @nuxt/icon bundles it. */
    icon?: string
    /** A JSON key. With `value`, the line is drawn as `"key": value`. */
    name?: string
    /** The value as JSON text, already quoted where a string. */
    value?: string
    /** For a line that is not a value: a sentence about why there is none. */
    tone?: 'ink' | 'muted' | 'warning' | 'error'
    mono?: boolean
  }>(),
  { icon: '', name: '', value: '', tone: 'ink', mono: false }
)

const TONES = {
  ink: 'text-ink',
  muted: 'text-ink/55',
  warning: 'text-warning',
  error: 'text-error'
} as const
</script>

<template>
  <p class="m-0 flex items-start gap-2 text-xs leading-5">
    <Icon
      v-if="icon"
      :name="icon"
      aria-hidden="true"
      class="mt-0.5 flex-none text-base text-ink/45"
    />
    <span
      :class="[TONES[tone], mono || name ? 'font-mono' : '']"
      class="min-w-0 break-words"
    >
      <template v-if="name">
        <span class="text-ink/60">"{{ name }}"</span><span class="text-ink/40">: </span><span class="text-primary">{{ value }}</span>
      </template>
      <slot v-else />
    </span>
  </p>
</template>
