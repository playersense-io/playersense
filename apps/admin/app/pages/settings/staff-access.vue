<script setup lang="ts">
import type { StaffAccessEntry } from '~/types'

definePageMeta({ title: 'Staff Access' })

const { data: entries } = await useFetch<StaffAccessEntry[]>('/api/staff-access', { default: () => [] })
</script>

<template>
  <UDashboardPanel id="staff-access">
    <template #header>
      <UDashboardNavbar title="Staff Access">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UPageHeader
        title="Staff Access"
        description="Every PlayerSense staff read of a customer's data, with a ticket reference — not the customer's own audit log (§104)."
      />

      <UAlert
        color="warning"
        variant="subtle"
        icon="i-lucide-construction"
        title="Blocked on backend"
        description="The staff_access table doesn't exist yet — nothing writes it today. The rows below are placeholder shape only."
        class="my-6"
      />

      <UTable
        :data="entries"
        :columns="[
          { accessorKey: 'staffName', header: 'Staff' },
          { accessorKey: 'organization', header: 'Organization' },
          { accessorKey: 'ticketReference', header: 'Ticket' },
          { accessorKey: 'accessedAt', header: 'Accessed at' }
        ]"
      />
    </template>
  </UDashboardPanel>
</template>
