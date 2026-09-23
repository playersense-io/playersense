<script setup lang="ts">
const { isDark } = useColorScheme()

useHead({
  meta: [
    { charset: 'utf-8' },
    { name: 'viewport', content: 'width=device-width, initial-scale=1' },
    // The shell colour of each theme (packages/dashboard/nuxt.config.ts).
    { key: 'theme-color', name: 'theme-color', content: () => (isDark.value ? '#111111' : '#ffffff') },
    { name: 'robots', content: 'noindex, nofollow' }
  ],
  link: [
    { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
    { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' },
    { rel: 'manifest', href: '/site.webmanifest' }
  ],
  htmlAttrs: {
    lang: 'en'
  }
})
</script>

<template>
  <NuxtLoadingIndicator color="rgb(var(--v-theme-primary))" />

  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
</template>
