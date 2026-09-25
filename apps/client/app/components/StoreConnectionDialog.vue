<script setup lang="ts">
import type { StoreConnection } from '~/composables/revenue'

// Connects a store for purchase verification, or changes how it is reached: App Store Connect's
// In-App Purchase key, a Google Play service account, or a Steam publisher key, with the app it is
// for. A secret is written and never read back: editing, its field is blank, and left blank the
// store keeps the one it has.
const props = defineProps<{
  store: StoreConnection
  save: (store: string, body: Record<string, unknown>) => Promise<void>
}>()

const open = defineModel<boolean>({ required: true })

const connected = computed(() => props.store.connected)
const label = computed(() => storeLook(props.store.store).label)

const bundleId = ref(props.store.bundle_id ?? '')
const appAppleId = ref(props.store.app_apple_id ? String(props.store.app_apple_id) : '')
const issuerId = ref(props.store.issuer_id ?? '')
const keyId = ref(props.store.key_id ?? '')
const privateKey = ref('')
const packageName = ref(props.store.package_name ?? '')
const serviceAccount = ref('')
const appId = ref(props.store.app_id ?? '')
const publisherKey = ref('')

const body = computed<Record<string, unknown>>(() => {
  switch (props.store.store) {
    case 'app_store': return {
      bundle_id: bundleId.value.trim(),
      ...(appAppleId.value.trim() ? { app_apple_id: Number(appAppleId.value.trim()) } : {}),
      issuer_id: issuerId.value.trim(),
      key_id: keyId.value.trim(),
      ...(privateKey.value.trim() ? { private_key: privateKey.value.trim() } : {})
    }
    case 'google_play': return {
      package_name: packageName.value.trim(),
      ...(serviceAccount.value.trim() ? { service_account: serviceAccount.value.trim() } : {})
    }
    default: return {
      app_id: appId.value.trim(),
      ...(publisherKey.value.trim() ? { publisher_key: publisherKey.value.trim() } : {})
    }
  }
})

/* What has to be there before the API is asked: the app it is for. The API checks the rest. */
const problem = computed(() => {
  switch (props.store.store) {
    case 'app_store':
      if (!bundleId.value.trim()) return 'The app\'s bundle id'
      if (appAppleId.value.trim() && !/^\d+$/.test(appAppleId.value.trim())) return 'The Apple id is digits only'
      return null
    case 'google_play': return packageName.value.trim() ? null : 'The app\'s package name'
    default: return /^\d+$/.test(appId.value.trim()) ? null : 'The Steam app id, digits only'
  }
})

const saving = ref(false)
const failure = ref('')

async function submit() {
  if (problem.value || saving.value) return
  saving.value = true
  failure.value = ''
  try {
    await props.save(props.store.store, body.value)
    open.value = false
  } catch (caught) {
    failure.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

const kept = (has: boolean) => (connected.value && has ? 'Set. Leave blank to keep it' : '')
</script>

<template>
  <v-dialog v-model="open" max-width="560" scrollable>
    <v-card border flat>
      <header class="flex items-start gap-3 border-b border-line p-5">
        <PlatformIcon :platform="storeLook(store.store).platform" :size="20" />
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ connected ? `${label} connection` : `Connect ${label}` }}
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            Receipts from {{ label }} are then checked with it before they count, and its refunds come in on their own.
          </p>
        </div>
      </header>

      <div class="overflow-y-auto">
        <form class="flex flex-col gap-4 p-5" @submit.prevent="submit">
          <template v-if="store.store === 'app_store'">
            <div class="grid gap-4 sm:grid-cols-2">
              <div class="flex flex-col gap-1">
                <label class="text-xs font-medium text-ink" for="store-bundle">Bundle id</label>
                <v-text-field
                  id="store-bundle"
                  v-model="bundleId"
                  autocomplete="off"
                  class="font-mono"
                  hide-details
                  placeholder="com.studio.game"
                  variant="filled"
                />
              </div>
              <div class="flex flex-col gap-1">
                <label class="text-xs font-medium text-ink" for="store-apple-id">Apple id <span class="font-normal text-ink/45">for notifications</span></label>
                <v-text-field
                  id="store-apple-id"
                  v-model="appAppleId"
                  autocomplete="off"
                  class="font-mono"
                  hide-details
                  inputmode="numeric"
                  placeholder="1234567890"
                  variant="filled"
                />
              </div>
              <div class="flex flex-col gap-1">
                <label class="text-xs font-medium text-ink" for="store-issuer">Issuer id</label>
                <v-text-field
                  id="store-issuer"
                  v-model="issuerId"
                  autocomplete="off"
                  class="font-mono"
                  hide-details
                  variant="filled"
                />
              </div>
              <div class="flex flex-col gap-1">
                <label class="text-xs font-medium text-ink" for="store-key-id">Key id</label>
                <v-text-field
                  id="store-key-id"
                  v-model="keyId"
                  autocomplete="off"
                  class="font-mono"
                  hide-details
                  variant="filled"
                />
              </div>
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-xs font-medium text-ink" for="store-p8">In-App Purchase key <span class="font-normal text-ink/45">the whole .p8 file</span></label>
              <v-textarea
                id="store-p8"
                v-model="privateKey"
                :placeholder="kept(store.has_key) || '-----BEGIN PRIVATE KEY-----'"
                auto-grow
                class="font-mono"
                hide-details
                rows="3"
                spellcheck="false"
                variant="filled"
              />
              <p class="m-0 text-[11px] leading-4 text-ink/50">
                In App Store Connect: Users and Access, Integrations, In-App Purchase. Stored encrypted, never shown again.
              </p>
            </div>
          </template>

          <template v-else-if="store.store === 'google_play'">
            <div class="flex flex-col gap-1">
              <label class="text-xs font-medium text-ink" for="store-package">Package name</label>
              <v-text-field
                id="store-package"
                v-model="packageName"
                autocomplete="off"
                class="font-mono"
                hide-details
                placeholder="com.studio.game"
                variant="filled"
              />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-xs font-medium text-ink" for="store-account">Service account key <span class="font-normal text-ink/45">the JSON file</span></label>
              <v-textarea
                id="store-account"
                v-model="serviceAccount"
                :placeholder="kept(store.has_key) || '{ &quot;type&quot;: &quot;service_account&quot;, ... }'"
                auto-grow
                class="font-mono"
                hide-details
                rows="3"
                spellcheck="false"
                variant="filled"
              />
              <p class="m-0 text-[11px] leading-4 text-ink/50">
                A Google Cloud service account invited to the app in Play Console with access to financial data. Stored encrypted, never shown again.
              </p>
            </div>
          </template>

          <template v-else>
            <div class="flex flex-col gap-1">
              <label class="text-xs font-medium text-ink" for="store-app-id">App id</label>
              <v-text-field
                id="store-app-id"
                v-model="appId"
                autocomplete="off"
                class="font-mono"
                hide-details
                inputmode="numeric"
                placeholder="480"
                variant="filled"
              />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-xs font-medium text-ink" for="store-publisher">Publisher Web API key</label>
              <v-text-field
                id="store-publisher"
                v-model="publisherKey"
                :placeholder="kept(store.has_key)"
                autocomplete="off"
                class="font-mono"
                hide-details
                spellcheck="false"
                variant="filled"
              />
              <p class="m-0 text-[11px] leading-4 text-ink/50">
                From Steamworks, Users and Permissions, Manage Groups. Stored encrypted, never shown again.
              </p>
            </div>
          </template>

          <p v-if="failure" class="m-0 text-xs text-error">
            {{ failure }}
          </p>
        </form>
      </div>

      <footer class="flex items-center justify-end gap-2 border-t border-line px-5 py-3">
        <span v-if="problem" class="mr-auto text-[11px] text-ink/50">{{ problem }}</span>
        <v-btn
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="open = false"
        >
          Cancel
        </v-btn>
        <v-btn
          :disabled="Boolean(problem)"
          :loading="saving"
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="submit"
        >
          {{ connected ? 'Save' : 'Connect' }}
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>
