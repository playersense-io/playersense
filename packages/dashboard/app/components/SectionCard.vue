<script setup lang="ts">
// A section of a page as one plain card: a heading, a sentence on what it holds, the
// controls that change it, and the content — the same surface and heading as `MetricCard`,
// so a page of charts and tables reads as one set.
//
// Not `LayerCard`, which sets its content on an inner panel inside a tray. `padded` is off
// for content that is its own layout (a table, a grid of cells); that content then starts
// under a rule, edge to edge.
//
// The `title` slot is for a heading that is also a control — the funnel a card edits,
// chosen from a menu in the heading's place. `title` is still given, as the text a
// screen reader names the card by.
withDefaults(defineProps<{
  title: string
  description?: string
  padded?: boolean
}>(), {
  description: '',
  padded: true
})

const titleId = useId()
</script>

<template>
  <v-card
    :aria-labelledby="titleId"
    border
    flat
    role="group"
  >
    <header class="flex flex-col gap-1 p-5" :class="padded ? 'pb-0' : ''">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h2 :id="titleId" class="m-0 max-w-full shrink-0 text-sm leading-7 font-medium text-ink">
          <slot name="title">
            {{ title }}
          </slot>
        </h2>
        <slot name="badge" />
        <div v-if="$slots.actions" class="ml-auto flex flex-wrap items-center gap-2">
          <slot name="actions" />
        </div>
      </div>
      <p v-if="description || $slots.description" class="m-0 text-sm text-ink/60">
        <slot name="description">
          {{ description }}
        </slot>
      </p>
    </header>

    <div v-if="padded" class="p-5">
      <slot />
    </div>
    <div v-else class="border-t border-line">
      <slot />
    </div>
  </v-card>
</template>
