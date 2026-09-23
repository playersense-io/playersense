// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  // packages/dashboard brings Vuetify, Tailwind, icons, the theme, the shell and the
  // common components; packages/ui the brand marks and favicons.
  extends: ['../../packages/dashboard', '../../packages/ui'],

  modules: ['@nuxt/eslint'],

  // Behind Firebase auth, no public content to server-render.
  ssr: false,

  devtools: {
    enabled: true
  },

  css: ['~/assets/css/main.css'],

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
