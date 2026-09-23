<script setup lang="ts">
/**
 * The top of a screen: what it is, what it shows, and what can be done to it.
 *
 * One component rather than a heading each page styles for itself, so every screen
 * opens at the same size and the same distance from the bar, with its actions on the
 * same line. A screen carries no glyph of its own: the drawer already marks where the
 * reader is, and a tile repeating it here only moves the title off the page's edge.
 *
 * Actions are a slot because they are the page's; the header only puts them where they
 * go, centred on the title block, and under the text on a screen too narrow for the
 * row. The description is a slot as well as a prop, for the sentence that has to carry
 * a link.
 *
 * `mark` is for a screen about one thing rather than one topic: a person's picture
 * stands before the title, which is the one case a glyph here says something the drawer
 * does not. `badge` sits beside the title for the state that thing is in.
 */
withDefaults(
  defineProps<{
    title: string
    description?: string
  }>(),
  { description: '' }
)
</script>

<template>
  <header class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
    <div class="flex min-w-0 items-center gap-3.5">
      <slot name="mark" />

      <div class="flex min-w-0 flex-col gap-1">
        <!-- `m-0` on both: nothing here resets a heading's browser margins, and theme.css
             hands a paragraph's back, so without it the two sit 36px apart and the block
             no longer centres on a mark or on the actions beside it. -->
        <div class="flex min-w-0 items-center gap-2.5">
          <h1 class="m-0 truncate text-2xl leading-tight font-semibold tracking-tight">
            {{ title }}
          </h1>
          <span v-if="$slots.badge" class="flex-none"><slot name="badge" /></span>
        </div>
        <p
          v-if="description || $slots.description"
          class="m-0 text-sm text-ink/60 [&_a]:font-medium [&_a]:text-ink [&_a]:underline [&_a]:decoration-ink/30 [&_a]:underline-offset-2 [&_a:hover]:decoration-ink"
        >
          <slot name="description">
            {{ description }}
          </slot>
        </p>
      </div>
    </div>

    <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2 sm:justify-end">
      <slot name="actions" />
    </div>
  </header>
</template>
