// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  // packages/dashboard brings Vuetify, Tailwind, icons, the theme, the shell and the
  // common components; packages/ui the brand marks and favicons.
  extends: ['../../packages/dashboard', '../../packages/ui'],

  modules: ['@nuxt/eslint'],

  // Internal staff console, behind its own auth — no public content to
  // server-render (§56).
  ssr: false,

  devtools: {
    enabled: true
  },

  // Space Grotesk is the wordmark's face (packages/ui Logo.vue).
  css: ['@fontsource/space-grotesk/600.css', '~/assets/css/main.css'],

  routeRules: {
    '/api/**': {
      cors: true
    }
  },

  compatibilityDate: '2026-06-30',

  eslint: {
    config: {
      stylistic: {
        commaDangle: 'never',
        braceStyle: '1tbs'
      }
    }
  }
})
