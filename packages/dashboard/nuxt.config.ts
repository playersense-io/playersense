import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'

// Shared Nuxt layer for the two dashboards (apps/client, apps/admin): Vuetify 4 for
// components, Tailwind 4 for layout, Material Symbols through @nuxt/icon, the theme, the
// shell and the common components. Ported from idenzy's panel (vendor/idenzy/apps/client,
// docs/guide/panel.md there has the long-form reasoning). The landing page stays on
// packages/ui + @nuxt/ui and does not extend this.
//
// Each app still owns its Tailwind entry point (app/assets/css/main.css), which imports
// ./app/assets/css/dashboard.css from here and adds its own `@source`.
export default defineNuxtConfig({
  modules: [
    'vuetify-nuxt-module',
    '@vueuse/nuxt',
    '@nuxt/icon',
    'nuxt-echarts'
  ],

  // After the app's own main.css, which is the order the overrides need.
  css: [fileURLToPath(new URL('./app/assets/css/theme.css', import.meta.url))],

  // Tailwind 4 is a Vite plugin rather than a Nuxt module.
  vite: { plugins: [tailwindcss()] },

  // Registered explicitly so the bundle carries only the chart types drawn here; ECharts
  // otherwise imports its whole catalogue.
  echarts: {
    renderer: 'canvas',
    charts: ['BarChart', 'LineChart', 'PieChart'],
    components: [
      'DatasetComponent',
      'GridComponent',
      'LegendComponent',
      'TitleComponent',
      'TooltipComponent'
    ]
  },

  // Every icon is compiled in and nothing reaches api.iconify.design. `mode: 'svg'`
  // because CSS mode injects a style element at runtime. The scan runs once when the dev
  // server starts, so a new literal icon name needs a restart; a name assembled at
  // runtime draws nothing, and belongs in `clientBundle.icons` instead.
  icon: {
    mode: 'svg',
    provider: 'none',
    fallbackToApi: false,
    serverBundle: false,
    // Every layer's app/ is scanned; .ts is not by default, and nav trees and the Vuetify
    // alias map are .ts.
    clientBundle: { scan: { globInclude: ['app/**/*.{vue,ts}'] } }
  },

  vuetify: {
    moduleOptions: {
      // Vuetify's `useLayout` collides with Nuxt's built-in of the same name — prefix
      // Vuetify's to `useVLayout` so both resolve.
      prefixComposables: ['useLayout'],

      // Vuetify's utility classes are most of its CSS and collide by name with
      // Tailwind's (`text-center`, `d-flex`…). Off, so Tailwind is the only utility
      // system. Component styles don't depend on them.
      styles: { utilities: false },

      // The module's rules plugin imports `vuetify/labs/rules`, which moved to the root
      // export in Vuetify 4 and breaks the build. Revisit when the module leaves RC.
      enableRules: false
    },
    vuetifyOptions: {
      // "custom" is the only value that makes the module leave `icons` alone; the
      // Material Symbols set is installed by app/plugins/vuetify-icons.ts.
      icons: { defaultSet: 'custom' },

      // Every action is a text button, which has no fill of its own to change under a
      // finger; without the ripple a tap leaves only a hover overlay that looks stuck.
      defaults: { global: { ripple: true } },

      theme: {
        // The theme of the first paint, before app.vue pins the stored preference, so a
        // dark machine never flashes white.
        defaultTheme: 'system',
        themes: {
          // Three surfaces: `shell` is the drawer and the app bar, `background` the page,
          // `surface` a card. The line is one opaque colour, so edges never darken over
          // a fill. `neutral` is the ink for a control with no accent. Outcome colours
          // are Material 800s on light and 400s on dark: a 500 on white is under 3:1.
          // `chart-1`…`chart-6` are series colours in a validated order — adjacent pairs
          // were checked for separation under colour-vision deficiency, so reordering
          // one undoes that. Outcome colours stay out of the chart list.
          light: {
            dark: false,
            colors: {
              'background': '#fafafa',
              'surface': '#ffffff',
              'shell': '#ffffff',
              'primary': '#2979ff',
              'primary-sharp': '#0162ec',
              'neutral': '#212121',
              'success': '#2e7d32',
              'warning': '#ef6c00',
              'error': '#c62828',
              'chart-1': '#2979ff',
              'chart-2': '#eb6834',
              'chart-3': '#1baf7a',
              'chart-4': '#eda100',
              'chart-5': '#e87ba4',
              'chart-6': '#4a3aa7'
            },
            variables: { 'border-color': '#e4e4e3', 'border-opacity': 1 }
          },
          dark: {
            dark: true,
            colors: {
              'background': '#151515',
              'surface': '#20201f',
              'shell': '#111111',
              'primary': '#448aff',
              'primary-sharp': '#0162ec',
              'neutral': '#f5f5f5',
              'success': '#66bb6a',
              'warning': '#ffa726',
              'error': '#ef5350',
              'chart-1': '#448aff',
              'chart-2': '#d95926',
              'chart-3': '#199e70',
              'chart-4': '#c98500',
              'chart-5': '#d55181',
              'chart-6': '#9085e9'
            },
            variables: { 'border-color': '#363635', 'border-opacity': 1 }
          }
        }
      }
    }
  }
})
