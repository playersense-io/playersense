<script setup lang="ts">
defineProps<{
  rail: boolean
}>()

// Placeholder until staff authentication is wired in (§104) — a shared operator token
// today, a verified staff identity once §56's console exists.
const name = 'Staff member'

const items: MenuItem[] = [
  { title: 'Staff access', icon: 'material-symbols:key-outline', to: '/settings/staff-access' },
  { title: 'Sign out', icon: 'material-symbols:logout' }
]
</script>

<template>
  <SidebarAccountMenu :items="items" :name="name" :rail="rail" />
</template>
