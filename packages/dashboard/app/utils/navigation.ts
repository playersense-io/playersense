import type { InjectionKey } from 'vue'

// Where a dashboard can go, in the order the drawer shows it. One tree per app
// (app/utils/nav.ts there), read by the drawer, the rail's flyouts, the breadcrumb, the
// tab title and the "Go to" search, so none of them can disagree about a name.
//
// Icon names are written out in full: @nuxt/icon bundles what it finds by scanning
// `app/**/*.ts`, and a name assembled at runtime draws nothing.

export interface NavLink {
  title: string
  to: string
  /** Top-level links carry one; a link inside a group is indented instead. */
  icon?: string
}

export interface NavGroup {
  /** What `v-list` tracks the open state by. Stable, because it is written to storage. */
  id: string
  title: string
  icon: string
  children: readonly NavLink[]
  /**
   * Set when the group is one drawer item and the page moves between its children
   * itself (a settings screen with tabs). The children still name the breadcrumb.
   */
  to?: string
}

export type NavEntry = NavLink | NavGroup

export interface NavSection {
  /** Absent on the first section, the one above every heading. */
  heading?: string
  entries: readonly NavEntry[]
}

export interface NavTree {
  sections: readonly NavSection[]
  /** Below the rule at the foot of the list: settings, billing — about the account. */
  footer?: readonly NavEntry[]
}

export interface Crumb {
  title: string
  /** Absent on a group, which is a place in the tree and not a screen. */
  to?: string
}

export function isGroup(entry: NavEntry): entry is NavGroup {
  return 'children' in entry
}

export interface DashboardNavigation extends NavTree {
  groups: readonly NavGroup[]
  links: readonly NavLink[]
  /** The group whose child is `path`, or null for a top-level screen. */
  groupOf: (path: string) => NavGroup | null
  /**
   * The screen at `path`. A path the tree does not name exactly resolves to the deepest
   * link it sits under, so one player's page reads as "Players" rather than as nothing.
   */
  linkOf: (path: string) => NavLink | null
  titleOf: (path: string) => string | null
  /** The way down the tree to `path`: its group, when it has one, then itself. */
  crumbsOf: (path: string) => Crumb[]
}

export function defineNavigation(tree: NavTree): DashboardNavigation {
  const entries = [...tree.sections.flatMap(section => section.entries), ...(tree.footer ?? [])]
  const groups = entries.filter(isGroup)
  const links = entries.flatMap(entry => (isGroup(entry) ? entry.children : [entry]))

  const groupOf = (path: string) =>
    groups.find(group => group.children.some(child => child.to === path)) ?? null

  const linkOf = (path: string) => {
    const exact = links.find(link => link.to === path)
    if (exact) return exact
    const above = links.filter(link => link.to !== '/' && path.startsWith(`${link.to}/`))
    above.sort((a, b) => b.to.length - a.to.length)
    return above[0] ?? null
  }

  const crumbsOf = (path: string): Crumb[] => {
    const link = linkOf(path)
    if (!link) return []
    const group = groups.find(candidate => candidate.children.includes(link))
    const last: Crumb = { title: link.title, to: link.to }
    return group ? [{ title: group.title, to: group.to }, last] : [last]
  }

  return {
    ...tree,
    groups,
    links,
    groupOf,
    linkOf,
    titleOf: path => linkOf(path)?.title ?? null,
    crumbsOf
  }
}

export const NAVIGATION_KEY: InjectionKey<DashboardNavigation> = Symbol('navigation')

/** The tree `DashboardShell` was given. Only inside the shell. */
export function useNavigation(): DashboardNavigation {
  const navigation = inject(NAVIGATION_KEY)
  if (!navigation) throw new Error('useNavigation() called outside <DashboardShell>')
  return navigation
}
