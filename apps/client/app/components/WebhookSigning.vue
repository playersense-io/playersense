<script setup lang="ts">
// How a destination's server knows a delivery is from PlayerSense: the signing secret, the
// three headers every delivery carries, and a handler that checks them, in the language the
// server is written in. The secret is read from the API only when asked for (each read is in
// the audit log) and can be rolled; while an old one still signs beside it, that is said, with
// when it stops.
const props = defineProps<{
  destination: WebhookDestination
}>()

const emit = defineEmits<{ changed: [] }>()

const { revealSecret } = useWebhookActions()

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

/* The secret, once read: kept here only while the tab is open. */
const secret = ref('')
const revealed = ref(false)
const reading = ref(false)
const problem = ref('')
const masked = '•'.repeat(32)

async function toggle() {
  if (revealed.value) {
    revealed.value = false
    return
  }
  if (!secret.value) {
    reading.value = true
    problem.value = ''
    try {
      secret.value = await revealSecret(props.destination)
    } catch (caught) {
      problem.value = caught instanceof Error ? caught.message : String(caught)
      return
    } finally {
      reading.value = false
    }
  }
  revealed.value = true
}

function rolled(value: string) {
  secret.value = value
  revealed.value = Boolean(value)
  emit('changed')
}

const previousUntil = computed(() => {
  const at = props.destination.secret.previous_expires_at
  return at && Date.parse(at) > time.value ? at : null
})

const rolling = ref(false)

const language = ref<VerifyLanguage>('javascript')
const LANGUAGES: SegmentedOption[] = VERIFY_LANGUAGES.map(entry => ({ value: entry.value, label: entry.label }))
const languageModel = computed({
  get: () => language.value as string,
  set: (value: string) => {
    language.value = value as VerifyLanguage
  }
})

const stamp = (iso: string) => new Date(iso).toLocaleString('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <!-- The secret. -->
      <div class="flex flex-col gap-3">
        <span class="flex flex-col gap-0.5">
          <span class="text-xs font-medium text-ink">Signing secret</span>
          <span class="text-[11px] leading-4 text-ink/55">Keep it on your server, in PLAYERSENSE_WEBHOOK_SECRET for example. Anyone with it can sign a request that passes for ours.</span>
        </span>
        <div class="flex items-center gap-1 rounded-(--ps-radius) border border-line bg-ink/2 py-1 pr-1 pl-3">
          <span class="min-w-0 flex-1 truncate font-mono text-xs text-ink">{{ revealed ? secret : masked }}</span>
          <v-btn
            :aria-label="revealed ? 'Hide the secret' : 'Show the secret'"
            :icon="revealed ? 'material-symbols:visibility-off-outline' : 'material-symbols:visibility-outline'"
            :loading="reading"
            color="neutral"
            size="small"
            variant="text"
            @click="toggle"
          />
          <CopyButton v-if="revealed && secret" :value="secret" label="Copy the signing secret" />
        </div>
        <span v-if="problem" class="text-[11px] text-error">{{ problem }}</span>
        <span v-else class="text-[11px] text-ink/50">Made {{ stamp(destination.secret.created_at) }}. Showing it is recorded in the audit log.</span>

        <p v-if="previousUntil" class="m-0 flex items-start gap-2 rounded-(--ps-radius) bg-primary/6 px-3 py-2.5 text-xs leading-5 text-ink/80 ring-1 ring-primary/15 ring-inset">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-primary" name="material-symbols:sync" />
          <span>
            The old secret signs too until {{ stamp(previousUntil) }}, {{ relativeWords(Date.parse(previousUntil), time) }}. Every delivery carries both signatures until then, so move your server to the new one at any point before.
          </span>
        </p>

        <div>
          <v-btn
            class="text-xs"
            color="neutral"
            prepend-icon="material-symbols:autorenew"
            size="small"
            variant="outlined"
            @click="rolling = true"
          >
            Roll secret
          </v-btn>
        </div>
      </div>

      <!-- The headers. -->
      <div class="flex flex-col gap-3">
        <span class="flex flex-col gap-0.5">
          <span class="text-xs font-medium text-ink">What every delivery carries</span>
          <span class="text-[11px] leading-4 text-ink/55">Check the signature on the raw body before you parse it, and refuse a delivery that fails.</span>
        </span>
        <ul class="m-0 flex list-none flex-col divide-y divide-line/70 overflow-hidden rounded-(--ps-radius-large) border border-line p-0">
          <li v-for="header in SIGNATURE_HEADERS" :key="header.name" class="flex flex-col gap-0.5 px-3 py-2.5">
            <span class="font-mono text-xs text-ink">{{ header.name }}</span>
            <span class="text-[11px] leading-4 text-ink/55">{{ header.note }}</span>
          </li>
        </ul>
      </div>
    </div>

    <!-- A handler that checks it all. -->
    <div class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <span class="flex flex-col gap-0.5">
          <span class="text-xs font-medium text-ink">Check it on your server</span>
          <span class="text-[11px] text-ink/55">The whole handler: signature, age, and handling each event once.</span>
        </span>
        <SegmentedControl v-model="languageModel" :options="LANGUAGES" label="Language" />
      </div>
      <CodeBlock :code="VERIFY_CODE[language]" :lang="language" />
    </div>

    <RollSecretDialog
      v-if="rolling"
      v-model="rolling"
      :destination="destination"
      @rolled="rolled"
    />
  </div>
</template>
