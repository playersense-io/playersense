<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const open = ref(false)

const links = [[{
  label: 'Overview',
  icon: 'i-lucide-house',
  to: '/',
  onSelect: () => { open.value = false }
}, {
  label: 'Realtime',
  icon: 'i-lucide-activity',
  to: '/realtime',
  onSelect: () => { open.value = false }
}, {
  label: 'Events',
  icon: 'i-lucide-list',
  to: '/events',
  onSelect: () => { open.value = false }
}, {
  label: 'Players',
  icon: 'i-lucide-user',
  to: '/players',
  onSelect: () => { open.value = false }
}, {
  label: 'Sessions',
  icon: 'i-lucide-clock',
  to: '/sessions',
  onSelect: () => { open.value = false }
}, {
  label: 'Retention',
  icon: 'i-lucide-repeat',
  to: '/retention',
  onSelect: () => { open.value = false }
}, {
  label: 'Funnels',
  icon: 'i-lucide-filter',
  to: '/funnels',
  onSelect: () => { open.value = false }
}, {
  label: 'Cohorts',
  icon: 'i-lucide-users',
  to: '/cohorts',
  onSelect: () => { open.value = false }
}, {
  label: 'Revenue',
  icon: 'i-lucide-line-chart',
  to: '/revenue',
  onSelect: () => { open.value = false }
}, {
  label: 'Feature Flags',
  icon: 'i-lucide-flag',
  to: '/flags',
  onSelect: () => { open.value = false }
}, {
  label: 'Experiments',
  icon: 'i-lucide-flask-conical',
  to: '/experiments',
  onSelect: () => { open.value = false }
}, {
  label: 'LiveOps',
  icon: 'i-lucide-calendar-clock',
  to: '/liveops',
  onSelect: () => { open.value = false }
}, {
  label: 'Purchases',
  icon: 'i-lucide-credit-card',
  to: '/purchases',
  onSelect: () => { open.value = false }
}, {
  label: 'Webhooks',
  icon: 'i-lucide-webhook',
  to: '/webhooks',
  onSelect: () => { open.value = false }
}, {
  label: 'Exports',
  icon: 'i-lucide-download',
  to: '/exports',
  onSelect: () => { open.value = false }
}, {
  label: 'Settings',
  to: '/settings',
  icon: 'i-lucide-settings',
  defaultOpen: false,
  type: 'trigger',
  children: [{
    label: 'Project Settings',
    to: '/settings',
    exact: true,
    onSelect: () => { open.value = false }
  }, {
    label: 'Data Retention',
    to: '/settings/data-retention',
    onSelect: () => { open.value = false }
  }, {
    label: 'Billing',
    to: '/settings/billing',
    onSelect: () => { open.value = false }
  }, {
    label: 'Organization / Team',
    to: '/settings/members',
    onSelect: () => { open.value = false }
  }, {
    label: 'API Keys',
    to: '/settings/api-keys',
    onSelect: () => { open.value = false }
  }, {
    label: 'Audit Log',
    to: '/settings/audit-log',
    onSelect: () => { open.value = false }
  }]
}]] satisfies NavigationMenuItem[][]

const groups = computed(() => [{
  id: 'links',
  label: 'Go to',
  items: links.flat()
}])
</script>

<template>
  <UDashboardGroup unit="rem">
    <UDashboardSidebar
      id="default"
      v-model:open="open"
      collapsible
      resizable
      class="bg-elevated/25"
      :ui="{ footer: 'lg:border-t lg:border-default' }"
    >
      <template #header="{ collapsed }">
        <TeamsMenu :collapsed="collapsed" />
      </template>

      <template #default="{ collapsed }">
        <UDashboardSearchButton :collapsed="collapsed" class="bg-transparent ring-default" />

        <UNavigationMenu
          :collapsed="collapsed"
          :items="links[0]"
          orientation="vertical"
          tooltip
          popover
        />
      </template>

      <template #footer="{ collapsed }">
        <UserMenu :collapsed="collapsed" />
      </template>
    </UDashboardSidebar>

    <UDashboardSearch :groups="groups" />

    <slot />
  </UDashboardGroup>
</template>
