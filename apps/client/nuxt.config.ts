// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  // packages/dashboard brings Vuetify, Tailwind, icons, the theme, the shell and the
  // common components; packages/ui the brand marks and favicons.
  extends: ['../../packages/dashboard', '../../packages/ui'],

  modules: ['@nuxt/eslint', 'nuxt-shiki'],

  // Behind Firebase auth, no public content to server-render.
  ssr: false,

  devtools: {
    enabled: true
  },

  css: ['~/assets/css/main.css'],

  routeRules: {
    '/api/**': {
      cors: true
    },
    // Growth's screens moved under /growth; links and bookmarks to the old paths still land.
    '/flags': { redirect: '/growth/flags' },
    '/experiments': { redirect: '/growth/experiments' },
    '/liveops': { redirect: '/growth/liveops' }
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

  // Highlighting for the code the dashboard shows: a flag's compiled document and a player's
  // snapshot (JSON), and how a game reads a flag (C#, and curl for an engine without an SDK).
  // CodeBlock.vue colours it with the panel's own theme (utils/code-theme.ts), loaded at first
  // use; the module needs one bundled theme to start from, and the smallest is named here.
  shiki: {
    bundledLangs: ['json', 'csharp', 'shellscript'],
    bundledThemes: ['min-dark'],
    defaultTheme: 'min-dark',
    defaultLang: 'json'
  }
})
