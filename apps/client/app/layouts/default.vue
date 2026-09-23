<script setup lang="ts">
// Placeholder until control-api's /organizations + project-access list are wired in (§55).
const organization = 'Acme Games'
</script>

<template>
  <DashboardShell :navigation="NAV" :root="{ title: organization, to: '/' }" product="PlayerSense">
    <template #switcher="{ rail }">
      <SidebarOrganizationSwitcher :organization="organization" :rail="rail" />
    </template>

    <template #account="{ rail }">
      <SidebarUserMenu :rail="rail" />
    </template>

    <template #actions>
      <HeaderActions />
    </template>

    <slot />
  </DashboardShell>
</template>
