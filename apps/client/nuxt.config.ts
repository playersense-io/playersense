import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// The repository's one .env holds the Firebase web settings and the API's address, beside the
// backend's own settings; Nuxt reads only this app's folder, so it is loaded here too. Values
// already in the environment win, which is how a deployment sets them.
const rootEnv = fileURLToPath(new URL('../../.env', import.meta.url))
if (existsSync(rootEnv)) process.loadEnvFile(rootEnv)

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  // packages/dashboard brings Vuetify, Tailwind, icons, the theme, the shell and the
  // common components; packages/ui the brand marks and favicons.
  extends: ['../../packages/dashboard', '../../packages/ui'],

  modules: ['@nuxt/eslint', 'nuxt-shiki', '@pinia/nuxt'],

  // Behind Firebase auth, no public content to server-render.
  ssr: false,

  devtools: {
    enabled: true
  },

  css: ['~/assets/css/main.css'],

  // Public by nature: a browser app's Firebase settings name the project, and grant nothing
  // without a signed-in user. NUXT_PUBLIC_* overrides each at runtime.
  runtimeConfig: {
    public: {
      apiBase: process.env.API_PUBLIC_URL ?? 'http://localhost:21080',
      firebase: {
        apiKey: process.env.FIREBASE_API_KEY ?? '',
        authDomain: process.env.FIREBASE_AUTH_DOMAIN ?? '',
        projectId: process.env.FIREBASE_PROJECT_ID ?? '',
        storageBucket: process.env.FIREBASE_STORAGE_BUCKET ?? '',
        messagingSenderId: process.env.FIREBASE_MESSAGING_SENDER_ID ?? '',
        appId: process.env.FIREBASE_APP_ID ?? ''
      }
    }
  },

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
    bundledLangs: ['json', 'csharp', 'shellscript', 'javascript', 'python', 'go'],
    bundledThemes: ['min-dark'],
    defaultTheme: 'min-dark',
    defaultLang: 'json'
  }
})
