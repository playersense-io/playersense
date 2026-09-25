import { defineStore } from 'pinia'
import type { AuthProvider, User } from 'firebase/auth'
import {
  GithubAuthProvider,
  GoogleAuthProvider,
  OAuthProvider,
  isSignInWithEmailLink,
  onIdTokenChanged,
  sendSignInLinkToEmail,
  signInWithEmailLink,
  signInWithPopup,
  signOut as firebaseSignOut
} from 'firebase/auth'

// Who is signed in, and how they sign in: a link sent to their address, or Google, Apple,
// Microsoft or GitHub. Firebase keeps the session; the API is sent the user's ID token, which
// Firebase refreshes before it expires.

export type SignInProvider = 'google' | 'apple' | 'microsoft' | 'github'

/** Where the address a link was sent to waits for the link to come back, in this browser. */
const LINK_EMAIL = 'playersense:sign-in-email'

function providerOf(name: SignInProvider): AuthProvider {
  switch (name) {
    case 'google': return new GoogleAuthProvider()
    case 'github': {
      const github = new GithubAuthProvider()
      github.addScope('user:email')
      return github
    }
    case 'apple': {
      const apple = new OAuthProvider('apple.com')
      apple.addScope('email')
      apple.addScope('name')
      return apple
    }
    case 'microsoft': {
      const microsoft = new OAuthProvider('microsoft.com')
      microsoft.setCustomParameters({ prompt: 'select_account' })
      return microsoft
    }
  }
}

/** A Firebase error, in words a person signing in can act on. */
export function signInError(error: unknown): string {
  const code = (error as { code?: string })?.code ?? ''
  switch (code) {
    case 'auth/popup-closed-by-user':
    case 'auth/cancelled-popup-request':
      return ''
    case 'auth/popup-blocked':
      return 'The sign-in window was blocked. Allow pop-ups for this site and try again.'
    case 'auth/account-exists-with-different-credential':
      return 'This address already signs in another way. Use the method you used before, then add this one from your account.'
    case 'auth/invalid-action-code':
    case 'auth/expired-action-code':
      return 'That sign-in link has expired or was already used. Ask for a new one.'
    case 'auth/invalid-email':
      return 'That is not an email address.'
    case 'auth/operation-not-allowed':
      return 'This way of signing in is not turned on for this deployment.'
    case 'auth/network-request-failed':
      return 'Could not reach the sign-in service. Check the connection and try again.'
    case 'auth/too-many-requests':
      return 'Too many attempts. Wait a few minutes and try again.'
  }
  return (error as Error)?.message || 'Sign-in did not work. Try again.'
}

export const useAuthStore = defineStore('auth', () => {
  const user = shallowRef<User | null>(null)
  const ready = ref(false)
  let resolveReady: () => void = () => {}
  const readyPromise = new Promise<void>((resolve) => {
    resolveReady = resolve
  })

  const nuxtApp = useNuxtApp()
  const auth = nuxtApp.$firebaseAuth

  onIdTokenChanged(auth, (next) => {
    user.value = next
    if (!ready.value) {
      ready.value = true
      resolveReady()
    }
  })

  /** Resolves once Firebase has said whether anybody is signed in. */
  function whenReady(): Promise<void> {
    return readyPromise
  }

  const signedIn = computed(() => user.value !== null)
  const email = computed(() => user.value?.email ?? '')
  const displayName = computed(() => user.value?.displayName || user.value?.email || '')
  const emailVerified = computed(() => user.value?.emailVerified ?? false)

  /** The ID token for the API, refreshed by Firebase when it is near expiry. */
  async function token(): Promise<string | null> {
    await whenReady()
    return user.value ? await user.value.getIdToken() : null
  }

  async function signInWith(provider: SignInProvider) {
    await signInWithPopup(auth, providerOf(provider))
  }

  /** Sends a sign-in link to an address; following it signs in, in this browser or another. */
  async function sendLink(address: string, next = '/') {
    const url = new URL('/login', window.location.origin)
    url.searchParams.set('next', next)
    await sendSignInLinkToEmail(auth, address, { url: url.toString(), handleCodeInApp: true })
    window.localStorage.setItem(LINK_EMAIL, address)
  }

  /** Whether the page was opened from a sign-in link. */
  function isLink(url = window.location.href): boolean {
    return isSignInWithEmailLink(auth, url)
  }

  /** The address the link was sent to, when this browser sent it. */
  function linkEmail(): string {
    return window.localStorage.getItem(LINK_EMAIL) ?? ''
  }

  /** Signs in with the link the page was opened from. */
  async function completeLink(address: string, url = window.location.href) {
    await signInWithEmailLink(auth, address, url)
    window.localStorage.removeItem(LINK_EMAIL)
  }

  async function signOut() {
    await firebaseSignOut(auth)
  }

  return { user, ready, signedIn, email, displayName, emailVerified, whenReady, token, signInWith, sendLink, isLink, linkEmail, completeLink, signOut }
})
