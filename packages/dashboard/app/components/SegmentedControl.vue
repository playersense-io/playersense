<script setup lang="ts">
import type { SegmentedOption } from '../utils/components'
/**
 * A row of choices where exactly one is on: the period a chart covers, or which of two
 * views of it is showing. The chosen segment is lifted onto the surface and the rest sit
 * in the track, and the track is `--ps-segmented-height`, a step lower than the
 * `--ps-toolbar-height` of the search and the Filter and Columns menus it can sit beside
 * over a table; the row centres them on one line.
 *
 * A radio group, in ARIA's terms and the keyboard's: one tab stop, arrows to move, which
 * is what tells a screen reader these are one choice rather than three buttons, and what
 * `v-btn-toggle` does not give. Selection follows the arrow keys, as it does in a native
 * radio group, and a disabled segment is skipped over rather than landed on.
 */
const props = defineProps<{
  options: readonly SegmentedOption[]
  /** What is being chosen, for a screen reader: "Period". */
  label: string
  /** The whole control, while a choice is being saved or cannot be made yet. */
  disabled?: boolean
  /**
   * Fill the container and share it equally between the segments, for a control that
   * is a row of its own rather than one sitting beside a button.
   */
  stretch?: boolean
}>()

const model = defineModel<string>({ required: true })

const group = useTemplateRef<HTMLElement>('group')

const KEYS: Record<string, 'next' | 'previous' | 'first' | 'last'> = {
  ArrowRight: 'next',
  ArrowDown: 'next',
  ArrowLeft: 'previous',
  ArrowUp: 'previous',
  Home: 'first',
  End: 'last'
}

/**
 * Wraps at either end, as a native radio group does. Looked up from the row's own
 * buttons rather than a ref array, because Vue does not promise the order of refs
 * collected from a `v-for`.
 */
function move(event: KeyboardEvent, from: number) {
  const step = KEYS[event.key]
  if (!step) return
  event.preventDefault()

  const enabled = props.options
    .map((option, index) => ({ option, index }))
    .filter(({ option }) => !option.disabled)
  if (enabled.length === 0) return

  const at = enabled.findIndex(({ index }) => index === from)
  const target
    = step === 'first'
      ? enabled[0]
      : step === 'last'
        ? enabled[enabled.length - 1]
        : enabled[(at + (step === 'next' ? 1 : -1) + enabled.length) % enabled.length]
  if (!target) return

  model.value = target.option.value
  group.value?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[target.index]?.focus()
}
</script>

<template>
  <div
    ref="group"
    :aria-disabled="disabled || undefined"
    :aria-label="label"
    :class="[disabled ? 'opacity-50' : '', stretch ? 'flex w-full' : 'inline-flex']"
    class="segmented items-center gap-0.5 rounded-(--ps-radius) bg-ink/5 p-0.5 transition-opacity duration-150"
    role="radiogroup"
  >
    <button
      v-for="(option, index) in options"
      :key="option.value"
      :aria-checked="option.value === model"
      :aria-label="option.title"
      :class="[
        option.value === model ? 'text-ink' : 'text-ink/60 hover:text-ink',
        option.label ? 'px-3' : 'w-8',
        stretch ? 'min-w-0 flex-1' : ''
      ]"
      :disabled="disabled || option.disabled"
      :tabindex="option.value === model ? 0 : -1"
      class="segmented__option relative inline-flex items-center justify-center gap-1 text-xs font-medium whitespace-nowrap transition-colors duration-150 focus-visible:z-10 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary disabled:text-ink/30"
      role="radio"
      type="button"
      @click="model = option.value"
      @keydown="move($event, index)"
    >
      <Icon
        v-if="option.icon"
        :name="option.icon"
        :aria-hidden="true"
        class="text-base"
      />
      <span v-if="option.label">{{ option.label }}</span>
      <v-tooltip
        v-if="!option.label && option.title"
        :text="option.title"
        activator="parent"
        location="top"
        open-delay="300"
      />
    </button>
  </div>
</template>

<style scoped>
/* The track's height is its own token, a step under the toolbar's. */
.segmented {
  height: var(--ps-segmented-height);
}

/*
 * The segment fills the track, and turns concentrically with it.
 *
 * Both come off the track less its two pixels of padding: the height less two of them, the
 * corner less one. Any other corner leaves a wedge of track showing where the raised segment
 * turns while its edges sit flush, which reads as a segment that does not quite fit.
 *
 * The track is on `--ps-radius` now, the same corner every button and field in the panel
 * takes, so the row over a table turns at one radius across the segmented control, the search
 * box and the two menu buttons rather than the control alone sitting a step tighter. The
 * segment follows it down: the one corner here that is not a token, because it is not a
 * choice — it is whatever the track is, less the padding between them.
 *
 * Both are calculations rather than answers, so the pair stays true the next time the row's
 * height or the radius ladder moves.
 */
.segmented__option {
  border-radius: calc(var(--ps-radius) - 2px);
  height: calc(var(--ps-segmented-height) - 4px);
}

/*
 * The chosen segment is raised: the surface, a hairline of the theme's line, and a
 * shadow light enough to read as a lift rather than a smudge. In the dark theme the
 * surface is the darkest thing in the track, so the raised segment takes a tint of the
 * ink instead, which is the same idea with the values the other way up.
 */
.segmented__option[aria-checked="true"] {
  background: rgb(var(--v-theme-surface));
  box-shadow:
    0 1px 2px rgba(0, 0, 0, 0.08),
    0 0 0 1px rgba(var(--v-border-color), var(--v-border-opacity));
}

:where(.v-theme--dark) .segmented__option[aria-checked="true"] {
  background: color-mix(in srgb, rgb(var(--v-theme-on-surface)) 10%, rgb(var(--v-theme-surface)));
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.24);
}
</style>
