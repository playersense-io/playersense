<script setup lang="ts">
definePageMeta({ title: 'Overview' })
</script>

<template>
  <UDashboardPanel id="home">
    <template #header>
      <UDashboardNavbar title="Overview">
        <template #leading>
          <SidebarCollapse />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <PagePlaceholder
        title="Overview"
        description="Headline metrics for this project — active players, sessions, revenue — from /analytics/{environment}/metrics/query."
      />
    </template>
  </UDashboardPanel>
</template>
