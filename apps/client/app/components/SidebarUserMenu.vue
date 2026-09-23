<script setup lang="ts">
defineProps<{
  rail: boolean
}>()

// Placeholder until Firebase sign-in is wired in (§55); "Sign out" then calls it.
const name = 'Signed-in member'

const items: MenuItem[] = [
  { title: 'Billing', icon: 'material-symbols:receipt-long-outline', to: '/settings/billing' },
  { title: 'Settings', icon: 'material-symbols:settings-outline', to: '/settings' },
  { title: 'Sign out', icon: 'material-symbols:logout' }
]
</script>

<template>
  <SidebarAccountMenu :items="items" :name="name" :rail="rail" />
</template>
