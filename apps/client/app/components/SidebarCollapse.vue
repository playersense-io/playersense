<script setup lang="ts">
// Replaces @nuxt/ui's `UDashboardSidebarCollapse`, which drives itself off
// `UDashboardGroup`'s provide/inject context. The sidebar is now a Vuetify
// v-navigation-drawer (see layouts/default.vue), so this toggles our own
// shared state instead: the drawer's open flag on mobile, its rail (icon-only)
// width on desktop.
const { drawer, rail } = useSidebar()
const { mobile } = useDisplay()

function toggle() {
  if (mobile.value) drawer.value = !drawer.value
  else rail.value = !rail.value
}
</script>

<template>
  <UButton
    color="neutral"
    variant="ghost"
    icon="i-lucide-panel-left"
    aria-label="Toggle sidebar"
    @click="toggle"
  />
</template>
