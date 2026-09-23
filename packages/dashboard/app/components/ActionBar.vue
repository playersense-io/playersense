<script setup lang="ts">
/**
 * The bar that rises from the foot of a screen while there is something to act on — an
 * edit not yet saved — with what is pending on the left and the actions on the right.
 *
 * Sticky to the bottom of the page's own column rather than fixed to the window, so it
 * sits under the content it is about, as wide as that content allows, and never over the
 * drawer. It arrives and leaves with a short rise, and without one for a reader who asked
 * for stillness. A region with a name, so a screen reader can find the actions without
 * hunting back up the page for them.
 *
 * Put it last in the page, after everything it floats over.
 */
defineProps<{
  open: boolean
  /** Names the region for a screen reader: "Unsaved funnel". */
  label: string
}>()
</script>

<template>
  <Transition name="ps-action-bar">
    <div
      v-if="open"
      :aria-label="label"
      class="ps-action-bar"
      role="region"
    >
      <div class="ps-action-bar__panel">
        <p class="m-0 flex min-w-0 items-center gap-2 text-xs text-ink/70">
          <span aria-hidden="true" class="size-1.5 flex-none rounded-full bg-primary" />
          <span class="truncate"><slot name="status" /></span>
        </p>
        <div class="flex flex-none items-center gap-2">
          <slot />
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
/* The column's full width, so the panel can centre in it; only the panel takes a click. */
.ps-action-bar {
  bottom: 16px;
  display: flex;
  justify-content: center;
  pointer-events: none;
  position: sticky;
  z-index: 5;
}

.ps-action-bar__panel {
  align-items: center;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: var(--ps-radius-large);
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.16), 0 2px 6px rgba(0, 0, 0, 0.08);
  display: flex;
  gap: 24px;
  justify-content: space-between;
  max-width: 100%;
  min-width: min(28rem, 100%);
  padding: 8px 8px 8px 16px;
  pointer-events: auto;
}

.ps-action-bar-enter-active,
.ps-action-bar-leave-active {
  transition: opacity 160ms ease, transform 160ms ease;
}

.ps-action-bar-enter-from,
.ps-action-bar-leave-to {
  opacity: 0;
  transform: translateY(12px);
}

@media (prefers-reduced-motion: reduce) {
  .ps-action-bar-enter-active,
  .ps-action-bar-leave-active {
    transition: opacity 120ms ease;
  }

  .ps-action-bar-enter-from,
  .ps-action-bar-leave-to {
    transform: none;
  }
}
</style>
