<template>
  <!-- The default layout without the content column: for a page that is one workspace
       filling the window under the bar, Report Builder's canvas. -->
  <DashboardShell :navigation="NAV" fluid product="PlayerSense">
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
