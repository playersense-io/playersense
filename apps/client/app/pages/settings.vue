<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const links = [[{
  label: 'Project',
  icon: 'i-lucide-settings',
  to: '/settings',
  exact: true
}, {
  label: 'Data Retention',
  icon: 'i-lucide-database',
  to: '/settings/data-retention'
}, {
  label: 'Billing',
  icon: 'i-lucide-credit-card',
  to: '/settings/billing'
}, {
  label: 'Organization / Team',
  icon: 'i-lucide-users',
  to: '/settings/members'
}, {
  label: 'API Keys',
  icon: 'i-lucide-key',
  to: '/settings/api-keys'
}, {
  label: 'Audit Log',
  icon: 'i-lucide-scroll-text',
  to: '/settings/audit-log'
}]] satisfies NavigationMenuItem[][]
</script>

<template>
  <UDashboardPanel id="settings" :ui="{ body: 'lg:py-12' }">
    <template #header>
      <UDashboardNavbar title="Settings">
        <template #leading>
          <SidebarCollapse />
        </template>
      </UDashboardNavbar>

      <UDashboardToolbar>
        <!-- NOTE: The `-mx-1` class is used to align with the `SidebarCollapse` button here. -->
        <UNavigationMenu :items="links" highlight class="-mx-1 flex-1" />
      </UDashboardToolbar>
    </template>

    <template #body>
      <div class="flex flex-col gap-4 sm:gap-6 lg:gap-12 w-full lg:max-w-2xl mx-auto">
        <NuxtPage />
      </div>
    </template>
  </UDashboardPanel>
</template>
