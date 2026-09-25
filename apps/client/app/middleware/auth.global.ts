import { useAuthStore } from '~/stores/auth'
import { useWorkspaceStore } from '~/stores/workspace'

// Every page but /login needs somebody signed in, and a workspace to show: who they are, their
// organizations and the chosen organization's projects, read once before the first page draws.
// Everything else in the dashboard is a project's, so with no project the only page is
// /getting-started, which makes the first one; with one, that page has nothing left to do.
export default defineNuxtRouteMiddleware(async (to) => {
  if (import.meta.server) return
  const auth = useAuthStore()
  await auth.whenReady()

  if (to.path === '/login') {
    // A sign-in link is finished on the login page itself, whoever is signed in.
    if (auth.signedIn && !auth.isLink()) return navigateTo(typeof to.query.next === 'string' ? to.query.next : '/')
    return
  }
  if (!auth.signedIn) {
    return navigateTo({ path: '/login', query: to.fullPath === '/' ? {} : { next: to.fullPath } })
  }
  const workspace = useWorkspaceStore()
  await workspace.load()
  // Only on an answer: a workspace that failed to load is not one with no projects.
  if (!workspace.loaded) return
  const starting = to.path === '/getting-started'
  if (!workspace.projects.length && !starting) return navigateTo('/getting-started')
  if (workspace.projects.length && starting) return navigateTo('/')
})
