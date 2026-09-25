<script setup lang="ts">
/**
 * The one sentence a screen has to say out loud: what is not running, what a tier does
 * not certify, what a control here does not reach.
 *
 * Set into the surface as a tint of the ink rather than in the page's own ground, which
 * is the step every inset here takes; `bg-canvas` cut into a card reads as a hole in it.
 * Twelve pixels rather than eleven because this is the text on a page most likely to be
 * the one that matters, and the blueprint asks for no long paragraph below that.
 *
 * For a statement, never for an explanation. A paragraph that merely explains a fact
 * belongs behind that fact's own glyph in `FactGrid`.
 */
withDefaults(
  defineProps<{
    text?: string
    icon?: string
    /** `warning` for a capability that is missing rather than merely worth knowing. */
    tone?: 'neutral' | 'warning'
  }>(),
  { text: '', icon: 'material-symbols:info-outline', tone: 'neutral' }
)
</script>

<template>
  <p
    :class="
      tone === 'warning'
        ? 'bg-warning/8 text-ink/80 ring-warning/20'
        : 'bg-ink/4 text-ink/70 ring-ink/6'
    "
    class="m-0 flex items-start gap-2.5 rounded-(--ps-radius-small) px-3 py-2.5 text-xs leading-5 ring-1 ring-inset"
  >
    <Icon
      :class="tone === 'warning' ? 'text-warning' : 'text-ink/40'"
      :name="icon"
      :aria-hidden="true"
      class="mt-0.5 flex-none text-sm"
    />
    <span><slot>{{ text }}</slot></span>
  </p>
</template>
