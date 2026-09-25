<template>
  <!-- A page with no shell: signing in, before there is a workspace to show. -->
  <v-app>
    <v-main class="flex min-h-dvh items-center justify-center bg-surface px-4 py-10">
      <slot />
    </v-main>
  </v-app>
</template>
