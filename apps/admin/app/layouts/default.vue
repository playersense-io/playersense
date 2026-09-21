<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const open = ref(false)

const links = [[{
  label: 'Organizations',
  icon: 'i-lucide-building-2',
  to: '/organizations',
  onSelect: () => { open.value = false }
}, {
  label: 'Projects',
  icon: 'i-lucide-folder',
  to: '/projects',
  onSelect: () => { open.value = false }
}, {
  label: 'Entitlements',
  icon: 'i-lucide-shield-check',
  to: '/entitlements',
  onSelect: () => { open.value = false }
}, {
  label: 'Billing',
  icon: 'i-lucide-credit-card',
  to: '/billing',
  onSelect: () => { open.value = false }
}, {
  label: 'Ingestion Volume',
  icon: 'i-lucide-activity',
  to: '/ingestion',
  onSelect: () => { open.value = false }
}, {
  label: 'Queue Backlog',
  icon: 'i-lucide-list-ordered',
  to: '/queue',
  onSelect: () => { open.value = false }
}, {
  label: 'ClickHouse Health',
  icon: 'i-lucide-database',
  to: '/clickhouse',
  onSelect: () => { open.value = false }
}, {
  label: 'Storage',
  icon: 'i-lucide-hard-drive',
  to: '/storage',
  onSelect: () => { open.value = false }
}, {
  label: 'Retention Jobs',
  icon: 'i-lucide-timer',
  to: '/retention-jobs',
  onSelect: () => { open.value = false }
}, {
  label: 'Webhook Failures',
  icon: 'i-lucide-webhook',
  to: '/webhooks',
  onSelect: () => { open.value = false }
}, {
  label: 'Purchase Verification',
  icon: 'i-lucide-receipt',
  to: '/purchases',
  onSelect: () => { open.value = false }
}, {
  label: 'Abuse / Rate Limits',
  icon: 'i-lucide-octagon-alert',
  to: '/abuse',
  onSelect: () => { open.value = false }
}, {
  label: 'Support Tooling',
  icon: 'i-lucide-life-buoy',
  to: '/support',
  onSelect: () => { open.value = false }
}, {
  label: 'Audit Access',
  icon: 'i-lucide-scroll-text',
  to: '/audit',
  onSelect: () => { open.value = false }
}, {
  label: 'Settings',
  to: '/settings',
  icon: 'i-lucide-settings',
  defaultOpen: false,
  type: 'trigger',
  children: [{
    label: 'Staff Access',
    to: '/settings/staff-access',
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
        <div class="flex items-center px-1 py-1" :class="collapsed ? 'justify-center' : ''">
          <Logo v-if="!collapsed" :size="20" />
          <LogoMark v-else :size="20" />
        </div>
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
