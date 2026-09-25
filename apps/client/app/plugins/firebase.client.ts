import { initializeApp } from 'firebase/app'
import { browserLocalPersistence, getAuth, setPersistence } from 'firebase/auth'

// Firebase Auth: who is signed in. The API trusts nothing else (every request carries the
// user's ID token), so this is the one place the app learns who its user is.
export default defineNuxtPlugin(() => {
  const config = useRuntimeConfig().public.firebase
  const app = initializeApp(config)
  const auth = getAuth(app)
  // Kept across tabs and restarts until signed out.
  void setPersistence(auth, browserLocalPersistence)
  return { provide: { firebaseAuth: auth } }
})
