<script setup lang="ts">
/**
 * The action on a card: "Create a key", "Add an origin", "View".
 *
 * A card's action is a second-order thing: the card already said what it holds. So this one
 * is text — no ground, no edge, nothing to see until the pointer is on it — which is now
 * what every action in the panel is. What parts this from the header actions beside it is
 * the rule under the label rather than a fill, and that it is sized to a line of text
 * rather than to a control.
 *
 * What answers the pointer is a rule drawn under the label, and it is drawn rather than
 * revealed: it grows from the left edge to the right on the way in and retracts to that
 * same left edge on the way out, so the motion reverses instead of restarting. A single
 * `transform-origin: left` is what makes both halves true — scaling to 1 runs the end
 * rightward, scaling back to 0 brings it home. The other spelling of this effect flips the
 * origin on exit so the rule leaves through the right; it looks like a second underline
 * being swept away rather than the first one being taken back.
 *
 * `scaleX` on a pseudo-element rather than `width`, because transform is composited: a
 * width animation on a row of these relayouts the row on every frame, and the panel draws
 * one per card. `currentColor` rather than a token, so a destructive action coloured
 * `text-error` gets a rule that agrees with its label without being told.
 *
 * The rule follows focus as well as hover. A keyboard reaches these too, and an affordance
 * only a pointer can see is not one. The focus ring is offset past the rule rather than the
 * usual two: at four it ran along the rule's own edge and the two read as one thick line.
 *
 * Three elements, because a card's action is three different things and the difference has
 * to reach the accessibility tree rather than only the handler: `to` navigates inside the
 * panel and renders a `NuxtLink`, `href` leaves it and renders an `a`, and neither renders
 * a button, because "Add a return URL" opens a dialog. One that cannot be pressed is a
 * `span` that says so, since there is no honest way to disable an anchor.
 *
 * `loading` is `disabled` with a reason shown. These sit in card headers where the write
 * they start has no other sign of being in flight — no dialog opens, the row below simply
 * changes a moment later — so the spinner is the only thing between the press and the
 * result. It takes the place of a leading glyph rather than sitting beside one: two marks
 * on a control this size is a control with no room left for its label.
 */
import { NuxtLink } from '#components'

const props = withDefaults(
  defineProps<{
    /** An internal route. With one this is a link, without one a button. */
    to?: string
    /** Somewhere outside the panel. The caller brings its own `target` and `rel`. */
    href?: string
    disabled?: boolean
    loading?: boolean
  }>(),
  { to: '', href: '', disabled: false, loading: false }
)

/* One word for the two states that must not be pressed, so nothing can set only half. */
const inert = computed(() => props.disabled || props.loading)

const element = computed(() => {
  if (props.href !== '') return inert.value ? 'span' : 'a'
  if (props.to !== '') return inert.value ? 'span' : NuxtLink
  return 'button'
})

const attrs = computed(() => {
  if (props.href !== '') return inert.value ? { 'aria-disabled': 'true' } : { href: props.href }
  if (props.to !== '') return inert.value ? { 'aria-disabled': 'true' } : { to: props.to }
  return { type: 'button', disabled: inert.value }
})
</script>

<template>
  <component
    :is="element"
    v-bind="attrs"
    class="action relative inline-flex h-5 items-center gap-1 text-xs font-medium whitespace-nowrap text-ink no-underline focus-visible:outline-2 focus-visible:outline-offset-6 focus-visible:outline-primary disabled:pointer-events-none disabled:text-ink/30"
  >
    <Icon
      v-if="loading"
      aria-hidden="true"
      class="animate-spin text-sm"
      name="material-symbols:progress-activity"
    />
    <slot />
  </component>
</template>

<style scoped>
/*
 * The rule: the full width of the control, held at nothing until it is wanted.
 *
 * Drawn rather than a text decoration, because a decoration cannot be animated from one
 * end and sits at a thickness and a distance the browser chooses. Both are ours here: two
 * pixels of rule, three below the control rather than on its bottom edge, which is what
 * clears the descender in "a key" and still reads as belonging to the label.
 *
 * The gap is a negative offset and not padding on purpose. Padding would grow the control,
 * and these sit in rows centred against a title or a switch — a taller action would move
 * every one of them down half a gap to buy the rule some air.
 */
.action::after {
  background: currentColor;
  bottom: -3px;
  content: "";
  height: 2px;
  inset-inline: 0;
  position: absolute;
  transform: scaleX(0);
  transform-origin: left;
}

/* The travel is the effect, so a reader who asked for stillness gets the rule without it
   rather than the animation slowed down. */
@media (prefers-reduced-motion: no-preference) {
  .action::after {
    transition: transform 220ms ease-out;
  }
}

.action:hover::after,
.action:focus-visible::after {
  transform: scaleX(1);
}

/* Nothing to offer, so nothing is drawn. Both spellings, because a disabled link is a
   `span` with no `:disabled` of its own. */
.action:disabled::after,
.action[aria-disabled="true"]::after {
  content: none;
}

.action[aria-disabled="true"] {
  color: rgba(var(--v-theme-on-surface), 0.3);
  pointer-events: none;
}
</style>
