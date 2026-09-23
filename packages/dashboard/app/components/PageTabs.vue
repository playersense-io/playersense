<script setup lang="ts">
import type { PageTab } from '../utils/components'
/**
 * The views of one screen, side by side, swapping the panel under them in place: the
 * funnel, its trend, its timing. Vuetify's tabs for what a tab list has to do — one tab
 * stop, the arrow keys, `role="tab"` and the selected state a screen reader reads — and
 * the panel's own type and ink for how it looks.
 *
 * Tabs rather than the settings rail's links because nothing navigates: the panel
 * changes and the page stays. The choice belongs in the URL all the same, so a reload
 * keeps it and a link carries it: pair this with `useQueryChoice`.
 *
 * A rule runs the width of the row under the tabs, and the chosen one sits on it in the
 * accent. A count, where a tab has one, is written after its label, quieter.
 */
defineProps<{
  tabs: readonly PageTab[]
  /** Names the tab list for a screen reader: "Funnel views". */
  label: string
}>()

const model = defineModel<string>({ required: true })
</script>

<template>
  <v-tabs
    v-model="model"
    :aria-label="label"
    class="ps-page-tabs"
    color="primary"
    density="compact"
    show-arrows
    slider-color="primary"
  >
    <v-tab
      v-for="tab in tabs"
      :key="tab.value"
      :ripple="false"
      :value="tab.value"
      class="ps-page-tabs__tab"
    >
      {{ tab.label }}
      <span v-if="tab.count !== undefined" class="ml-1.5 font-mono text-[11px] text-ink/45">{{ tab.count }}</span>
    </v-tab>
  </v-tabs>
</template>

<style scoped>
.ps-page-tabs {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  height: 40px;
}

/* The labels carry no padding of their own and stand apart by a gap instead, so the
   slider runs exactly under the words and the first tab starts on the page's edge. */
.ps-page-tabs :deep(.v-slide-group__content) {
  gap: 20px;
}

/* The panel's type, at the size of the headings the tabs sit among, and no chip or box:
   the ink says which is chosen, the slider says where. */
.ps-page-tabs :deep(.ps-page-tabs__tab) {
  color: rgba(var(--v-theme-on-surface), 0.6);
  font-size: 13px;
  font-weight: 500;
  height: 40px;
  min-width: 0;
  padding: 0;
}

.ps-page-tabs :deep(.ps-page-tabs__tab:hover) {
  color: rgb(var(--v-theme-on-surface));
}

.ps-page-tabs :deep(.ps-page-tabs__tab.v-tab--selected) {
  color: rgb(var(--v-theme-on-surface));
}

/* Vuetify's hover wash would box the label; the ink change is the hover. */
.ps-page-tabs :deep(.ps-page-tabs__tab .v-btn__overlay) {
  opacity: 0;
}

.ps-page-tabs :deep(.ps-page-tabs__tab:focus-visible) {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: -2px;
}

.ps-page-tabs :deep(.v-tab__slider) {
  height: 2px;
}
</style>
