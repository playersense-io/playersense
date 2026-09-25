<script setup lang="ts">
import type { StoreConnection } from '~/composables/revenue'

// Settings > Stores: the App Store, Google Play and Steam connections that make this project's
// purchases verified. A connected store checks every receipt before it counts, its revenue
// becomes its verified purchases from then on, and its refunds come in by themselves: App Store
// and Google Play send notifications to the URL shown here, Steam's are swept.
const { stores, loading, error, reload, save, disconnect, rotateUrl, test } = useStoreConnections()

const STATUS: Record<string, { label: string, tone: BadgeTone }> = {
  not_connected: { label: 'Not connected', tone: 'neutral' },
  off: { label: 'Off', tone: 'neutral' },
  untested: { label: 'Not tested', tone: 'neutral' },
  working: { label: 'Working', tone: 'success' },
  failing: { label: 'Failing', tone: 'error' }
}

const statusOf = (store: StoreConnection) => STATUS[store.status] ?? { label: store.status, tone: 'neutral' as BadgeTone }

const now = useNow({ interval: 60_000 })

const problem = ref('')
const busy = ref('')

async function attempt(store: string, change: () => Promise<unknown>) {
  busy.value = store
  problem.value = ''
  try {
    await change()
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    busy.value = ''
  }
}

const tested = ref<Record<string, { ok: boolean, detail: string }>>({})

async function runTest(store: StoreConnection) {
  await attempt(store.store, async () => {
    const out = await test(store.store)
    tested.value = { ...tested.value, [store.store]: { ok: out.ok, detail: out.detail } }
  })
}

const identity = (store: StoreConnection) => {
  switch (store.store) {
    case 'app_store': return store.bundle_id ? `${store.bundle_id}${store.key_id ? `, key ${store.key_id}` : ''}` : ''
    case 'google_play': return store.package_name ? `${store.package_name}${store.service_account_email ? `, as ${store.service_account_email}` : ''}` : ''
    default: return store.app_id ? `App ${store.app_id}` : ''
  }
}

const outcomes = (store: StoreConnection) => {
  const day = store.last_day
  const parts = [
    day.verified ? `${writeFigure(day.verified)} verified` : '',
    day.rejected ? `${writeFigure(day.rejected)} turned away` : '',
    day.duplicate ? `${writeFigure(day.duplicate)} sent again` : '',
    day.unavailable ? `${writeFigure(day.unavailable)} store unavailable` : '',
    day.credentials ? `${writeFigure(day.credentials)} refused our key` : ''
  ].filter(Boolean)
  return parts.length ? parts.join(', ') : 'No receipts in the last 24 hours'
}

// --- Dialogs ----------------------------------------------------------------------------------

const editing = ref<StoreConnection | null>(null)
const editOpen = computed({
  get: () => editing.value !== null,
  set: (value) => {
    if (!value) editing.value = null
  }
})

const confirming = ref<{ store: StoreConnection, action: 'disconnect' | 'rotate' } | null>(null)
const confirmOpen = computed({
  get: () => confirming.value !== null,
  set: (value) => {
    if (!value) confirming.value = null
  }
})

function confirmed() {
  const current = confirming.value
  if (!current) return
  const { store, action } = current
  void attempt(store.store, () => (action === 'disconnect' ? disconnect(store.store) : rotateUrl(store.store)))
}

async function setEnabled(store: StoreConnection, enabled: boolean) {
  await attempt(store.store, () => save(store.store, { enabled }))
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader description="The stores this project verifies purchases with. A connected store checks every receipt before it counts, and its refunds come in by themselves." title="Stores" />

    <p v-if="problem" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ problem }}
    </p>

    <LoadState
      :error="error"
      :loading="loading"
      what="the stores"
      @retry="reload"
    >
      <section
        v-for="store in stores"
        :key="store.store"
        :aria-label="storeLook(store.store).label"
        class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border border-line bg-surface"
      >
        <header class="flex flex-wrap items-center gap-3 px-4 py-3">
          <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/4">
            <PlatformIcon :platform="storeLook(store.store).platform" :size="18" />
          </span>
          <span class="flex min-w-0 flex-1 flex-col">
            <span class="flex items-center gap-2 text-sm font-medium text-ink">
              {{ storeLook(store.store).label }}
              <StatusBadge :label="statusOf(store).label" :tone="statusOf(store).tone" />
            </span>
            <span class="truncate font-mono text-[11px] text-ink/50">{{ identity(store) || 'Not connected' }}</span>
          </span>
          <span class="flex flex-none items-center gap-1">
            <template v-if="store.connected">
              <v-btn
                :disabled="!store.enabled"
                :loading="busy === store.store"
                class="text-xs"
                color="neutral"
                prepend-icon="material-symbols:send-outline"
                size="small"
                variant="text"
                @click="runTest(store)"
              >
                Test
              </v-btn>
              <v-switch
                :aria-label="`Verify ${storeLook(store.store).label} purchases`"
                :model-value="store.enabled"
                color="primary"
                density="compact"
                hide-details
                inset
                @update:model-value="setEnabled(store, Boolean($event))"
              />
              <v-menu location="bottom end">
                <template #activator="{ props: menu }">
                  <v-btn
                    v-bind="menu"
                    :aria-label="`${storeLook(store.store).label} options`"
                    color="neutral"
                    icon="material-symbols:more-horiz"
                    size="small"
                    variant="text"
                  />
                </template>
                <v-list min-width="220" nav>
                  <v-list-item prepend-icon="material-symbols:edit-outline" title="Edit connection" @click="editing = store" />
                  <v-list-item
                    v-if="store.notification_url"
                    prepend-icon="material-symbols:autorenew"
                    title="New notification URL"
                    @click="confirming = { store, action: 'rotate' }"
                  />
                  <v-list-item
                    base-color="error"
                    prepend-icon="material-symbols:link-off"
                    title="Disconnect"
                    @click="confirming = { store, action: 'disconnect' }"
                  />
                </v-list>
              </v-menu>
            </template>
            <v-btn
              v-else
              class="text-xs font-medium"
              color="primary"
              prepend-icon="material-symbols:add-link"
              size="small"
              variant="flat"
              @click="editing = store"
            >
              Connect
            </v-btn>
          </span>
        </header>

        <div v-if="store.connected" class="flex flex-col gap-3 border-t border-line px-4 py-3">
          <div v-if="store.notification_url" class="flex flex-col gap-1">
            <span class="text-[11px] text-ink/55">{{ store.store === 'app_store' ? 'App Store Server Notifications V2 URL, in App Store Connect, App Information' : 'Push endpoint for the Real-time Developer Notifications Pub/Sub subscription' }}</span>
            <CopyField :value="store.notification_url" label="Copy the notification URL" />
          </div>
          <p v-else-if="store.store === 'steam'" class="m-0 text-[11px] text-ink/55">
            Steam sends no notifications: its refunds are swept<template v-if="store.swept_at">
              , last {{ relativeWords(Date.parse(store.swept_at), now.getTime()) }}
            </template>.
          </p>

          <dl class="m-0 grid gap-x-6 gap-y-1.5 text-xs sm:grid-cols-2">
            <div class="flex gap-2">
              <dt class="text-ink/50">
                Last 24 hours
              </dt>
              <dd class="m-0 text-ink/80">
                {{ outcomes(store) }}
              </dd>
            </div>
            <div class="flex gap-2">
              <dt class="text-ink/50">
                Verified since
              </dt>
              <dd class="m-0 text-ink/80">
                {{ store.verified_since ? formatDate(store.verified_since) : 'Not yet' }}
              </dd>
            </div>
            <div class="flex gap-2">
              <dt class="text-ink/50">
                Last receipt
              </dt>
              <dd class="m-0 text-ink/80">
                {{ store.last_verified_at ? relativeWords(Date.parse(store.last_verified_at), now.getTime()) : 'None yet' }}
              </dd>
            </div>
            <div v-if="store.store !== 'steam'" class="flex gap-2">
              <dt class="text-ink/50">
                Last notification
              </dt>
              <dd class="m-0 text-ink/80">
                {{ store.last_notification_at ? relativeWords(Date.parse(store.last_notification_at), now.getTime()) : 'None yet' }}
              </dd>
            </div>
          </dl>

          <p v-if="store.missing?.length" class="m-0 text-[11px] leading-4 text-warning">
            Not set, and some checks need it: {{ store.missing.join(', ') }}.
          </p>
          <p
            v-if="tested[store.store]"
            :class="tested[store.store]!.ok ? 'text-success' : 'text-error'"
            class="m-0 text-[11px] leading-4"
            role="status"
          >
            Test: {{ tested[store.store]!.detail }}
          </p>
          <p v-else-if="store.last_error" class="m-0 text-[11px] leading-4 text-error">
            {{ store.last_error }}<template v-if="store.last_error_at">
              , {{ relativeWords(Date.parse(store.last_error_at), now.getTime()) }}
            </template>
          </p>
        </div>
      </section>
    </LoadState>

    <StoreConnectionDialog
      v-if="editing"
      v-model="editOpen"
      :save="save"
      :store="editing"
    />

    <ConfirmDialog
      v-if="confirming"
      v-model="confirmOpen"
      :action="confirming.action === 'disconnect' ? 'Disconnect' : 'Make a new URL'"
      :description="confirming.action === 'disconnect'
        ? `Receipts from ${storeLook(confirming.store.store).label} stop being checked, and its revenue goes back to what the game reports. The key is deleted; purchases already verified stay.`
        : `The URL in ${storeLook(confirming.store.store).label} stops being accepted at once. Paste the new one there straight away, or refunds are missed until you do.`"
      :title="confirming.action === 'disconnect' ? `Disconnect ${storeLook(confirming.store.store).label}?` : 'Make a new notification URL?'"
      :tone="confirming.action === 'disconnect' ? 'error' : undefined"
      @confirm="confirmed"
    />
  </div>
</template>
