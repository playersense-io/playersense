<template>
  <DashboardShell :navigation="NAV" product="PlayerSense Admin">
    <!-- No switcher: the console spans every organization, so the top of the drawer is
         the brand and nothing to choose. -->
    <template #switcher="{ rail }">
      <NuxtLink
        to="/"
        aria-label="PlayerSense Admin — overview"
        class="flex w-full items-center text-ink no-underline"
        :class="rail ? 'justify-center' : 'px-2.5'"
      >
        <LogoMark v-if="rail" :size="20" />
        <Logo v-else :size="20" />
      </NuxtLink>
    </template>

    <template #account="{ rail }">
      <SidebarStaffMenu :rail="rail" />
    </template>

    <slot />
  </DashboardShell>
</template>
