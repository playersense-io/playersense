/** A row of a menu the shell draws (the account menu at the foot of the drawer). */
export interface MenuItem {
  title: string
  /** Written out in full so @nuxt/icon bundles it. */
  icon: string
  to?: string
  onSelect?: () => void
}
