<script setup lang="ts">
definePageMeta({ title: 'Overview' })
</script>

<template>
  <UDashboardPanel id="home">
    <template #header>
      <UDashboardNavbar title="Overview">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <PagePlaceholder
        title="Overview"
        description="Fleet-wide health at a glance: ingestion volume, queue backlog, ClickHouse and storage — see the nav for each in detail."
      />
    </template>
  </UDashboardPanel>
</template>
