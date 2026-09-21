<script setup lang="ts">
interface NavLeaf {
  label: string
  to: string
  icon: string
  exact?: boolean
}

interface NavGroup {
  label: string
  icon: string
  items: NavLeaf[]
}

const overview: NavLeaf = { label: 'Overview', to: '/', icon: 'lucide:layout-grid', exact: true }

const groups: NavGroup[] = [{
  label: 'Analytics',
  icon: 'lucide:activity',
  items: [
    { label: 'Realtime', to: '/realtime', icon: 'lucide:activity' },
    { label: 'Events', to: '/events', icon: 'lucide:list' },
    { label: 'Players', to: '/players', icon: 'lucide:user' },
    { label: 'Sessions', to: '/sessions', icon: 'lucide:clock' }
  ]
}, {
  label: 'Insights',
  icon: 'lucide:line-chart',
  items: [
    { label: 'Retention', to: '/retention', icon: 'lucide:repeat' },
    { label: 'Funnels', to: '/funnels', icon: 'lucide:filter' },
    { label: 'Cohorts', to: '/cohorts', icon: 'lucide:users' },
    { label: 'Revenue', to: '/revenue', icon: 'lucide:circle-dollar-sign' }
  ]
}, {
  label: 'Growth',
  icon: 'lucide:rocket',
  items: [
    { label: 'Feature Flags', to: '/flags', icon: 'lucide:flag' },
    { label: 'Experiments', to: '/experiments', icon: 'lucide:flask-conical' },
    { label: 'LiveOps', to: '/liveops', icon: 'lucide:calendar-clock' }
  ]
}, {
  label: 'Data & Commerce',
  icon: 'lucide:package',
  items: [
    { label: 'Purchases', to: '/purchases', icon: 'lucide:credit-card' },
    { label: 'Webhooks', to: '/webhooks', icon: 'lucide:webhook' },
    { label: 'Exports', to: '/exports', icon: 'lucide:download' }
  ]
}, {
  label: 'Settings',
  icon: 'lucide:settings',
  items: [
    { label: 'Project Settings', to: '/settings', icon: 'lucide:sliders-horizontal', exact: true },
    { label: 'Data Retention', to: '/settings/data-retention', icon: 'lucide:database' },
    { label: 'Billing', to: '/settings/billing', icon: 'lucide:credit-card' },
    { label: 'Organization / Team', to: '/settings/members', icon: 'lucide:building-2' },
    { label: 'API Keys', to: '/settings/api-keys', icon: 'lucide:key' },
    { label: 'Audit Log', to: '/settings/audit-log', icon: 'lucide:scroll-text' }
  ]
}]

const { drawer, rail, searchOpen } = useSidebar()

const searchGroups = computed(() => [{
  id: 'links',
  label: 'Go to',
  items: [overview, ...groups.flatMap(group => group.items)].map(item => ({
    label: item.label,
    icon: `i-${item.icon.replace(':', '-')}`,
    to: item.to,
    onSelect: () => { searchOpen.value = false }
  }))
}])
</script>

<template>
  <v-app>
    <v-layout>
      <v-navigation-drawer
        v-model="drawer"
        :rail="rail"
        rail-width="72"
        width="256"
      >
        <template #prepend>
          <div class="p-2">
            <TeamsMenu :collapsed="rail" />
          </div>

          <UButton
            color="neutral"
            variant="ghost"
            icon="i-lucide-search"
            :label="rail ? undefined : 'Search'"
            :square="rail"
            block
            class="mx-2 mb-1"
            :class="[!rail && 'justify-start']"
            :ui="{ base: rail ? undefined : 'w-[calc(100%-1rem)]' }"
            @click="searchOpen = true"
          />

          <v-divider class="mt-2" />
        </template>

        <v-list density="compact" nav color="primary">
          <v-list-item :to="overview.to" :exact="overview.exact">
            <template #prepend>
              <Icon :name="overview.icon" mode="svg" class="size-4" />
            </template>
            <v-list-item-title v-if="!rail" class="text-xs">
              {{ overview.label }}
            </v-list-item-title>
          </v-list-item>

          <v-list-group v-for="group in groups" :key="group.label" :value="group.label">
            <template #activator="{ props: activatorProps, isOpen }">
              <v-list-item v-bind="activatorProps">
                <template #prepend>
                  <Icon :name="group.icon" mode="svg" class="size-4" />
                </template>
                <v-list-item-title v-if="!rail" class="text-xs">
                  {{ group.label }}
                </v-list-item-title>
                <template v-if="!rail" #append>
                  <Icon
                    name="lucide:chevron-down"
                    mode="svg"
                    class="size-3.5 transition-transform duration-200"
                    :class="{ 'rotate-180': isOpen }"
                  />
                </template>
              </v-list-item>
            </template>

            <v-list-item
              v-for="item in group.items"
              :key="item.to"
              :to="item.to"
              :exact="item.exact"
            >
              <template #prepend>
                <Icon :name="item.icon" mode="svg" class="size-4" />
              </template>
              <v-list-item-title v-if="!rail" class="text-xs">
                {{ item.label }}
              </v-list-item-title>
            </v-list-item>
          </v-list-group>
        </v-list>

        <template #append>
          <v-divider />
          <div class="p-2">
            <UserMenu :collapsed="rail" />
          </div>
        </template>
      </v-navigation-drawer>

      <v-main>
        <UDashboardSearch v-model:open="searchOpen" :groups="searchGroups" />

        <slot />
      </v-main>
    </v-layout>
  </v-app>
</template>
