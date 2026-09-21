// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  extends: ['../../packages/ui'],

  modules: [
    '@nuxt/eslint',
    '@nuxt/icon',
    '@nuxt/ui',
    '@vueuse/nuxt',
    'vuetify-nuxt-module'
  ],

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
  },

  // Vuetify powers the dashboard sidebar only (v-navigation-drawer, v-list-group);
  // everything else stays on @nuxt/ui + Tailwind. `main.css` pre-declares the
  // cascade-layer order so Tailwind utilities always win over Vuetify's own
  // component styles — see the comment there before touching either import.
  vuetify: {
    moduleOptions: {
      // Vuetify's `useLayout` collides with Nuxt's own built-in composable
      // of the same name — prefix Vuetify's to `useVLayout` so both resolve.
      prefixComposables: ['useLayout']
    },
    vuetifyOptions: {
      // Icons come from @nuxt/icon's lucide set (`<Icon name="lucide:...">`),
      // not Vuetify's own icon system — no need for its default mdi font.
      icons: false
    }
  }
})
