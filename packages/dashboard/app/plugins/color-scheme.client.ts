// The stored colour preference, handed to Vuetify. A plugin rather than a watcher in a
// layout, so it holds on every screen — error page included — and runs before the first
// paint rather than correcting it a frame later. `theme.change` rather than writing
// `theme.global.name`, which Vuetify 4 deprecates.
export default defineNuxtPlugin({
  name: 'playersense:color-scheme',
  setup(nuxtApp) {
    nuxtApp.hook('vuetify:ready', (vuetify) => {
      const { isDark } = useColorScheme()
      watch(isDark, dark => vuetify.theme.change(dark ? 'dark' : 'light'), { immediate: true })
    })
  }
})
