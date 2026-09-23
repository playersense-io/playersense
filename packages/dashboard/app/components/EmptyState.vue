<script setup lang="ts">
/**
 * What a list or a section shows before it has anything: a mark, a heading that says
 * what is missing, a sentence that says what to do about it, and the control that does
 * it. Centred and roomy, because the state is the whole content and not a note in a
 * corner of it.
 *
 * The mark is the glyph on a small panel set in a tray, two more panels fanned behind
 * it: the same layering the cards use, so the empty state and the card that will
 * replace it are one family.
 */
withDefaults(
  defineProps<{
    /** Written out in full so @nuxt/icon bundles it. */
    icon: string
    title: string
    description?: string
  }>(),
  { description: '' }
)
</script>

<template>
  <div class="flex flex-col items-center gap-5 px-6 py-14 text-center">
    <span aria-hidden="true" class="relative flex h-16 w-16 items-center justify-center">
      <span
        class="absolute inset-x-3 -top-2 h-16 rounded-(--ps-radius) border border-line bg-canvas opacity-40"
      />
      <span
        class="absolute inset-x-1.5 -top-1 h-16 rounded-(--ps-radius) border border-line bg-canvas opacity-70"
      />
      <span
        class="relative flex size-16 items-center justify-center rounded-(--ps-radius-large) border border-line bg-canvas p-1"
      >
        <span
          class="flex size-full items-center justify-center rounded-(--ps-radius) border border-line bg-surface text-ink/70"
        >
          <Icon :name="icon" class="text-2xl" />
        </span>
      </span>
    </span>

    <div class="flex flex-col gap-1.5">
      <h2 class="m-0 text-lg font-semibold tracking-tight text-ink">
        {{ title }}
      </h2>
      <p v-if="description || $slots.description" class="m-0 max-w-md text-sm text-ink/60">
        <slot name="description">
          {{ description }}
        </slot>
      </p>
    </div>

    <div v-if="$slots.actions" class="flex flex-wrap items-center justify-center gap-2">
      <slot name="actions" />
    </div>
  </div>
</template>
