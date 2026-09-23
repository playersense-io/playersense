<script setup lang="ts">
/**
 * An inset for a sample of something real: what a user receives, what an app shows, what
 * a token carries. A title, a figure or a qualifier on the right, one sentence of context,
 * and the sample itself in the slot.
 *
 * It sits a step lighter than the card rather than a step darker, and carries a hairline
 * ring rather than a border, so it reads as something laid on the surface and not as a
 * hole cut into it. The sample is set in full ink: it is the point of the panel, and the
 * one thing in it that must not read as a caption.
 */
withDefaults(
  defineProps<{
    title: string
    /** A figure or a qualifier beside the title: "1 segment · 77 of 160", "After a step-up". */
    meta?: string
    /** One sentence under the title. A slot for the sentence that has to carry a link. */
    note?: string
  }>(),
  { meta: '', note: '' }
)
</script>

<template>
  <section class="rounded-(--ps-radius-small) bg-ink/4 px-4 py-3 ring-1 ring-ink/6 ring-inset">
    <div class="flex items-baseline justify-between gap-4">
      <h3 class="m-0 text-xs font-medium text-ink">
        {{ title }}
      </h3>
      <div v-if="meta || $slots.meta" class="text-[11px] text-ink/50">
        <slot name="meta">
          {{ meta }}
        </slot>
      </div>
    </div>
    <p v-if="note || $slots.note" class="m-0 mt-0.5 text-[11px] leading-4 text-ink/55">
      <slot name="note">
        {{ note }}
      </slot>
    </p>
    <div :class="note || $slots.note ? 'mt-3' : 'mt-2'" class="text-xs leading-5 text-ink">
      <slot />
    </div>
  </section>
</template>
