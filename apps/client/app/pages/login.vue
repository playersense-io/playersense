<script setup lang="ts">
import type { SignInProvider } from '~/stores/auth'
import { signInError } from '~/stores/auth'

// Signing in: a link sent to an address, or an account at Google, Apple, Microsoft or GitHub.
// Firebase does the signing in; the API is then sent the user's token. The first sign-in makes
// the account and a personal organization, on the API's side.
definePageMeta({ layout: 'bare' })
useHead({ title: 'Sign in · PlayerSense' })

const auth = useAuthStore()
const route = useRoute()
const next = computed(() => (typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'))

/* Apple's and GitHub's marks are black, so they are drawn in white on a dark page. */
const PROVIDERS: { key: SignInProvider, label: string, icon: string, ink?: boolean }[] = [
  { key: 'google', label: 'Google', icon: 'logos:google-icon' },
  { key: 'apple', label: 'Apple', icon: 'logos:apple', ink: true },
  { key: 'microsoft', label: 'Microsoft', icon: 'logos:microsoft-icon' },
  { key: 'github', label: 'GitHub', icon: 'logos:github-icon', ink: true }
]

const email = ref('')
const busy = ref<SignInProvider | 'link' | 'finish' | null>(null)
const problem = ref('')
const sentTo = ref('')
// Opened from a sign-in link in a browser that did not send it: the address is asked for.
const confirming = ref(false)

async function finish() {
  await navigateTo(next.value, { replace: true })
}

async function withProvider(provider: SignInProvider) {
  problem.value = ''
  busy.value = provider
  try {
    await auth.signInWith(provider)
    await finish()
  } catch (caught) {
    problem.value = signInError(caught)
  } finally {
    busy.value = null
  }
}

const isAddress = (text: string) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(text)

/* The address as the field holds it. A browser's autofill can fill the field without telling
   Vue, so a submit reads the field itself rather than trusting the model alone. */
function addressOf(event: Event): string {
  const field = (event.target as HTMLFormElement).querySelector('input[type="email"]') as HTMLInputElement | null
  const address = (field?.value || email.value).trim()
  email.value = address
  return address
}

async function sendLink(event: Event) {
  const address = addressOf(event)
  if (!isAddress(address)) {
    problem.value = 'Enter an email address, like you@studio.com.'
    return
  }
  problem.value = ''
  busy.value = 'link'
  try {
    await auth.sendLink(address, next.value)
    sentTo.value = address
  } catch (caught) {
    problem.value = signInError(caught)
  } finally {
    busy.value = null
  }
}

async function completeLink(address: string) {
  problem.value = ''
  busy.value = 'finish'
  try {
    await auth.completeLink(address)
    await finish()
  } catch (caught) {
    problem.value = signInError(caught)
    confirming.value = true
  } finally {
    busy.value = null
  }
}

function confirmLink(event: Event) {
  const address = addressOf(event)
  if (!isAddress(address)) {
    problem.value = 'Enter the email address the link was sent to.'
    return
  }
  void completeLink(address)
}

onMounted(() => {
  if (!auth.isLink()) return
  const stored = auth.linkEmail()
  if (stored) {
    email.value = stored
    void completeLink(stored)
  } else {
    confirming.value = true
  }
})
</script>

<template>
  <div class="flex w-full max-w-[360px] flex-col gap-8">
    <div class="flex justify-center">
      <Logo :size="28" />
    </div>

    <!-- Finishing a link opened here. -->
    <div v-if="busy === 'finish'" class="flex flex-col items-center gap-3 py-6 text-center">
      <v-progress-circular
        color="primary"
        indeterminate
        size="28"
        width="3"
      />
      <p class="m-0 text-sm text-ink/70">
        Signing you in...
      </p>
    </div>

    <template v-else-if="confirming">
      <header class="flex flex-col gap-1.5 text-center">
        <h1 class="m-0 text-xl font-semibold tracking-tight text-ink">
          Confirm your address
        </h1>
        <p class="m-0 text-sm leading-6 text-ink/60">
          This link was opened in a different browser from the one that asked for it. Type the address it was sent to.
        </p>
      </header>
      <form class="flex flex-col gap-3" @submit.prevent="confirmLink">
        <div class="flex flex-col gap-1">
          <label class="text-xs font-medium text-ink" for="confirm-email">Email</label>
          <v-text-field
            id="confirm-email"
            v-model="email"
            autocomplete="email"
            autofocus
            hide-details
            type="email"
            variant="underlined"
          />
        </div>
        <v-btn
          block
          class="ps-login-button ps-login-provider"
          size="large"
          type="submit"
          variant="flat"
        >
          Sign in
          <template #append>
            <Icon aria-hidden="true" class="text-base" name="material-symbols:arrow-forward" />
          </template>
        </v-btn>
      </form>
    </template>

    <div v-else-if="sentTo" class="flex flex-col items-center gap-3 text-center">
      <Icon class="text-4xl text-primary" name="material-symbols:mark-email-unread-outline" />
      <h1 class="m-0 text-xl font-semibold tracking-tight text-ink">
        Check your inbox
      </h1>
      <p class="m-0 text-sm leading-6 text-ink/60">
        We sent a sign-in link to <span class="font-medium text-ink">{{ sentTo }}</span>. It works once, for an hour.
      </p>
      <v-btn size="small" variant="text" @click="sentTo = ''">
        Use another address
      </v-btn>
    </div>

    <template v-else>
      <header class="flex flex-col gap-1.5 text-center">
        <h1 class="m-0 text-xl font-semibold tracking-tight text-ink">
          Sign in to PlayerSense
        </h1>
        <p class="m-0 text-sm text-ink/60">
          New here? Signing in makes your account.
        </p>
      </header>

      <div class="flex flex-col gap-2.5">
        <v-btn
          v-for="provider in PROVIDERS"
          :key="provider.key"
          :disabled="busy !== null && busy !== provider.key"
          :loading="busy === provider.key"
          block
          class="ps-login-button ps-login-provider"
          size="large"
          variant="flat"
          @click="withProvider(provider.key)"
        >
          <template #prepend>
            <Icon :class="provider.ink ? 'dark:invert' : ''" :name="provider.icon" class="text-lg" />
          </template>
          Sign in with {{ provider.label }}
        </v-btn>
      </div>

      <div class="flex items-center gap-3 text-xs text-ink/45">
        <span class="h-px flex-1 bg-ink/8" />
        or with a link by email
        <span class="h-px flex-1 bg-ink/8" />
      </div>

      <form class="flex flex-col gap-3" @submit.prevent="sendLink">
        <div class="flex flex-col gap-1">
          <label class="text-xs font-medium text-ink" for="link-email">Email</label>
          <v-text-field
            id="link-email"
            v-model="email"
            autocomplete="email"
            hide-details
            placeholder="you@studio.com"
            type="email"
            variant="underlined"
          />
        </div>
        <v-btn
          :disabled="busy !== null"
          :loading="busy === 'link'"
          block
          class="ps-login-button ps-login-provider"
          size="large"
          type="submit"
          variant="flat"
        >
          Email me a link
          <template #append>
            <Icon aria-hidden="true" class="text-base" name="material-symbols:arrow-forward" />
          </template>
        </v-btn>
      </form>
    </template>

    <p v-if="problem" class="m-0 text-center text-sm leading-6 text-error">
      {{ problem }}
    </p>

    <p class="m-0 text-center text-xs leading-5 text-ink/45">
      By signing in you agree to the terms of service and the privacy policy.
    </p>
  </div>
</template>

<style scoped>
/* Full-width rows with their words in the middle, and no outline: a soft fill says they can be
   pressed, as the email field's does. */
.ps-login-button {
  font-size: 13px;
  letter-spacing: 0;
  text-transform: none;
  font-weight: 500;
}

.ps-login-provider {
  background: rgba(var(--v-theme-on-surface), 0.05);
  color: rgb(var(--v-theme-on-surface));
}

/* Disabled, while another way of signing in is under way, keeps the same soft fill, fainter,
   rather than Vuetify's darker grey. */
.ps-login-provider.v-btn--disabled {
  background: rgba(var(--v-theme-on-surface), 0.05);
  color: rgb(var(--v-theme-on-surface));
  opacity: 0.5;
}

.ps-login-provider.v-btn--disabled :deep(.v-btn__overlay) {
  opacity: 0;
}

.ps-login-provider:hover {
  background: rgba(var(--v-theme-on-surface), 0.08);
}
</style>
