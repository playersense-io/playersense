import { aliases, materialSymbols } from '../icons/material-symbols'

// Installs the Material Symbols icon set. Here rather than in nuxt.config.ts because an
// icon set is a component and the config is serialised; `defaultSet: 'custom'` there is
// what stops vuetify-nuxt-module overwriting this. Runs before the module's own plugin
// (order 25), since Nuxt plugins default to 0.
export default defineNuxtPlugin({
  name: 'playersense:vuetify-icons',
  setup(nuxtApp) {
    nuxtApp.hook('vuetify:before-create', ({ vuetifyOptions }) => {
      vuetifyOptions.icons = { defaultSet: 'ms', aliases, sets: { ms: materialSymbols } }
    })
  }
})
