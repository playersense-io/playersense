// Which of the two themes the dashboard is drawn in, and the switch that changes it.
//
// `useDark` keeps the preference on `auto` — following `prefers-color-scheme` — until
// somebody presses the toggle. Per browser rather than per account: which colour a screen
// is comfortable in is a fact about the screen. Shared, so the toggle in the bar and the
// binding in DashboardShell read one ref rather than two that agree through a storage event.
export const useColorScheme = createSharedComposable(() => {
  const isDark = useDark({
    storageKey: 'playersense.theme',
    // The default handler writes a class on <html> that nothing reads: Vuetify already
    // marks its root with `v-theme--*` and sets `color-scheme` on :root.
    onChanged: () => {}
  })

  return { isDark, toggle: useToggle(isDark) }
})
