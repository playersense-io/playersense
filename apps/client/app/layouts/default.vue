<template>
  <DashboardShell :navigation="NAV" product="PlayerSense">
    <template #switcher="{ rail }">
      <SidebarProjectSwitcher :rail="rail" />
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
