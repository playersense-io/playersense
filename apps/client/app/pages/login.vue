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

const PROVIDERS: { key: SignInProvider, label: string, icon: string }[] = [
  { key: 'google', label: 'Google', icon: 'mdi:google' },
  { key: 'apple', label: 'Apple', icon: 'mdi:apple' },
  { key: 'microsoft', label: 'Microsoft', icon: 'mdi:microsoft' },
  { key: 'github', label: 'GitHub', icon: 'mdi:github' }
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

const emailValid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value.trim()))

async function sendLink() {
  if (!emailValid.value) return
  problem.value = ''
  busy.value = 'link'
  try {
    await auth.sendLink(email.value.trim(), next.value)
    sentTo.value = email.value.trim()
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
  <div class="flex w-full max-w-[380px] flex-col gap-6">
    <div class="flex justify-center">
      <Logo :size="26" />
    </div>

    <v-card border class="flex flex-col gap-5 p-6" flat>
      <!-- Finishing a link opened here. -->
      <template v-if="busy === 'finish'">
        <div class="flex flex-col items-center gap-3 py-6 text-center">
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
      </template>

      <template v-else-if="confirming">
        <header class="flex flex-col gap-1">
          <h1 class="m-0 text-base font-semibold text-ink">
            Confirm your address
          </h1>
          <p class="m-0 text-xs leading-5 text-ink/60">
            This link was opened in a different browser from the one that asked for it. Type the address it was sent to.
          </p>
        </header>
        <form class="flex flex-col gap-3" @submit.prevent="completeLink(email.trim())">
          <div class="flex flex-col gap-1">
            <label class="text-xs font-medium text-ink" for="confirm-email">Email</label>
            <v-text-field
              id="confirm-email"
              v-model="email"
              autocomplete="email"
              autofocus
              hide-details
              type="email"
              variant="filled"
            />
          </div>
          <v-btn
            :disabled="!emailValid"
            block
            color="primary"
            type="submit"
            variant="flat"
          >
            Sign in
          </v-btn>
        </form>
      </template>

      <template v-else-if="sentTo">
        <div class="flex flex-col items-center gap-3 py-2 text-center">
          <Icon class="text-3xl text-primary" name="material-symbols:mark-email-unread-outline" />
          <h1 class="m-0 text-base font-semibold text-ink">
            Check your inbox
          </h1>
          <p class="m-0 text-xs leading-5 text-ink/60">
            We sent a sign-in link to <span class="font-medium text-ink">{{ sentTo }}</span>. It works once, for an hour.
          </p>
          <v-btn size="small" variant="text" @click="sentTo = ''">
            Use another address
          </v-btn>
        </div>
      </template>

      <template v-else>
        <header class="flex flex-col gap-1">
          <h1 class="m-0 text-base font-semibold text-ink">
            Sign in to PlayerSense
          </h1>
          <p class="m-0 text-xs leading-5 text-ink/60">
            New here? Signing in makes your account.
          </p>
        </header>

        <div class="grid grid-cols-2 gap-2">
          <v-btn
            v-for="provider in PROVIDERS"
            :key="provider.key"
            :disabled="busy !== null && busy !== provider.key"
            :loading="busy === provider.key"
            class="justify-start"
            variant="outlined"
            @click="withProvider(provider.key)"
          >
            <template #prepend>
              <Icon :name="provider.icon" class="text-lg" />
            </template>
            {{ provider.label }}
          </v-btn>
        </div>

        <div class="flex items-center gap-3 text-[11px] text-ink/45">
          <span class="h-px flex-1 bg-ink/10" />
          or with a link by email
          <span class="h-px flex-1 bg-ink/10" />
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
              variant="filled"
            />
          </div>
          <v-btn
            :disabled="!emailValid || busy !== null"
            :loading="busy === 'link'"
            block
            color="primary"
            type="submit"
            variant="flat"
          >
            Email me a link
          </v-btn>
        </form>
      </template>

      <p v-if="problem" class="m-0 text-xs leading-5 text-error">
        {{ problem }}
      </p>
    </v-card>

    <p class="m-0 text-center text-[11px] leading-4 text-ink/45">
      By signing in you agree to the terms of service and the privacy policy.
    </p>
  </div>
</template>
