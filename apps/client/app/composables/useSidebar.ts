// Shared drawer/rail/search state for the Vuetify sidebar in layouts/default.vue,
// exposed here so page-level navbars (e.g. SidebarCollapse.vue) can toggle it
// without threading props through the layout.
export function useSidebar() {
  const drawer = useState('sidebar-drawer', () => true)
  const rail = useState('sidebar-rail', () => false)
  const searchOpen = useState('sidebar-search-open', () => false)

  return { drawer, rail, searchOpen }
}
