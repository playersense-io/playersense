<script setup lang="ts">
/**
 * A bar that stays in reach as the page scrolls under it: the filters of a long page of
 * figures, pinned just under the app bar so they can be changed from anywhere on it. It
 * sits in the page's flow until it reaches the top, and only then lifts off the page with
 * a shadow, so it looks like part of the page until it is floating over it.
 *
 * It sticks within its parent, so it belongs directly in the page's column, never in a
 * wrapper of its own. Whether it is stuck is read with an IntersectionObserver whose top
 * edge sits a pixel under where it sticks: stuck, a pixel of it is past the edge.
 */
const bar = useTemplateRef<HTMLElement>('bar')
const stuck = ref(false)

/** The app bar's height, which Vuetify's layout sets on the main area, and a little air. */
const GAP = 12

let observer: IntersectionObserver | undefined

onMounted(() => {
  const el = bar.value
  if (!el) return
  const top = (Number.parseFloat(getComputedStyle(el).getPropertyValue('--v-layout-top')) || 56) + GAP
  observer = new IntersectionObserver(
    ([entry]) => {
      stuck.value = !!entry && entry.intersectionRatio < 1 && entry.boundingClientRect.top <= top + 1
    },
    { rootMargin: `-${top + 1}px 0px 0px 0px`, threshold: [1] }
  )
  observer.observe(el)
})

onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <div ref="bar" :class="{ 'ps-sticky-bar--stuck': stuck }" class="ps-sticky-bar">
    <slot />
  </div>
</template>

<style scoped>
.ps-sticky-bar {
  border-radius: var(--ps-radius-large);
  position: sticky;
  top: calc(var(--v-layout-top, 56px) + 12px);
  transition: box-shadow 160ms ease;
  z-index: 5;
}

.ps-sticky-bar--stuck {
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.14), 0 2px 6px rgba(0, 0, 0, 0.06);
}

@media (prefers-reduced-motion: reduce) {
  .ps-sticky-bar {
    transition: none;
  }
}
</style>
