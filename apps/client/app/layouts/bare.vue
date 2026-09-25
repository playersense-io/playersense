<template>
  <!-- A page with no shell: signing in, before there is a workspace to show. -->
  <v-app>
    <v-main class="flex min-h-dvh items-center justify-center bg-white px-4 py-10 dark:bg-surface">
      <slot />
    </v-main>
  </v-app>
</template>
