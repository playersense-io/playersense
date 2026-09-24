<script setup lang="ts">
// One delivery, the way somebody debugging their server reads it: did it arrive, every try
// with what the server answered and how fast, and for the try chosen, the request exactly as
// it was sent and the response as it came back. Resend makes one more try now, with the same
// event id, so a server that keeps the ids it has handled sees a repeat and not a new event.
const props = defineProps<{
  delivery: WebhookDelivery
  destination: WebhookDestination
}>()

const open = defineModel<boolean>({ required: true })

const { resend } = useWebhooks()

const now = useNow({ interval: 30_000 })

/* The try being read: the last one, until another is chosen. */
const chosen = ref(props.delivery.attempts.length - 1)
watch(() => props.delivery.attempts.length, (count) => {
  chosen.value = count - 1
})
const attempt = computed(() => props.delivery.attempts[chosen.value] ?? null)

const TABS: PageTab[] = [
  { value: 'request', label: 'Request' },
  { value: 'response', label: 'Response' }
]
const tab = ref('response')

const when = (at: number) => new Date(at).toLocaleString('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

const body = computed(() => JSON.stringify(eventDocument(props.delivery.type, {
  id: props.delivery.eventId,
  created: props.delivery.created,
  environment: props.delivery.environment,
  version: props.destination.version
}), null, 2))

const headers = computed(() => (attempt.value ? requestHeaders(props.delivery, props.destination, attempt.value) : []))

/* A JSON answer is shown as JSON; anything else, an nginx page say, as the text it was. */
const response = computed(() => {
  const text = attempt.value?.body ?? ''
  try {
    return { json: true, text: JSON.stringify(JSON.parse(text), null, 2) }
  } catch {
    return { json: false, text }
  }
})

/* Where the delivery stands, in a sentence. */
const standing = computed(() => {
  const entry = props.delivery
  const count = entry.attempts.length
  const last = entry.attempts.at(-1)
  if (entry.status === 'succeeded' && last) return `Delivered on try ${count}, answered in ${msWords(last.ms)}.`
  if (entry.status === 'failed') return `Gave up after ${count} tries over three days. Resend it once your server is fixed.`
  if (!count) return 'Waiting for its first try.'
  return `Try ${count} of ${WEBHOOK_TRIES} failed. The next is ${entry.next ? relativeWords(entry.next, now.value.getTime()) : 'soon'}${entry.next ? `, at ${new Date(entry.next).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}` : ''}.`
})

const resending = ref(false)
const lastResend = ref<DeliveryAttempt | null>(null)

async function resendNow() {
  resending.value = true
  await new Promise(resolve => setTimeout(resolve, 600))
  lastResend.value = resend(props.destination, props.delivery)
  resending.value = false
}

const disabled = computed(() => props.destination.status === 'disabled')
</script>

<template>
  <v-dialog v-model="open" max-width="820" scrollable>
    <v-card border flat>
      <header class="flex flex-wrap items-start justify-between gap-4 border-b border-line p-5">
        <div class="flex min-w-0 flex-col gap-1">
          <span class="flex flex-wrap items-center gap-2">
            <h2 class="m-0 truncate font-mono text-sm font-medium text-ink">
              {{ delivery.type }}
            </h2>
            <StatusBadge :label="DELIVERY_STATUS[delivery.status].label" :tone="DELIVERY_STATUS[delivery.status].tone" />
            <span v-if="delivery.test" class="rounded-full bg-ink/6 px-1.5 text-[10px] leading-4 font-medium text-ink/60">Test</span>
          </span>
          <span class="text-xs text-ink/60">{{ standing }}</span>
        </div>
        <div class="flex flex-none flex-col items-end gap-0.5 text-right">
          <span class="text-xs text-ink">{{ when(delivery.created) }}</span>
          <span class="text-[11px] text-ink/45">{{ ENVIRONMENT_LABELS[delivery.environment] }} · to {{ destination.name }}</span>
        </div>
      </header>

      <div class="overflow-y-auto">
        <div class="flex flex-col gap-5 p-5">
          <FactGrid
            :columns="2"
            :facts="[
              { key: 'event', label: 'Event id', value: delivery.eventId, mono: true, copy: delivery.eventId, info: 'The same on every try, and in the X-PlayerSense-Id header. Keep the ones your server has handled and ignore a repeat.' },
              { key: 'delivery', label: 'Delivery id', value: delivery.id, mono: true, copy: delivery.id }
            ]"
            label="Identifiers"
          />

          <!-- Every try: choose one to read its request and response. -->
          <div class="flex flex-col gap-2">
            <span class="text-xs font-medium text-ink">Tries</span>
            <ol class="m-0 flex list-none flex-col overflow-hidden rounded-(--ps-radius-large) border border-line p-0">
              <li v-for="(entry, index) in delivery.attempts" :key="index" class="border-b border-line/70 last:border-b-0">
                <button
                  :aria-pressed="chosen === index"
                  :class="chosen === index ? 'bg-primary/5' : 'hover:bg-ink/2'"
                  class="grid w-full cursor-pointer grid-cols-[1.5rem_minmax(0,1fr)_auto_auto] items-center gap-3 px-3 py-2 text-left"
                  type="button"
                  @click="chosen = index"
                >
                  <Icon
                    :class="answerOk(entry) ? 'text-success' : 'text-error'"
                    :name="answerOk(entry) ? 'material-symbols:check-circle-outline' : 'material-symbols:cancel-outline'"
                    aria-hidden="true"
                    class="text-base"
                  />
                  <span class="flex min-w-0 items-baseline gap-2">
                    <span :class="answerOk(entry) ? 'text-ink' : 'text-error'" class="font-mono text-xs">{{ answerWords(entry) }}</span>
                    <span class="truncate text-[11px] text-ink/45">Try {{ index + 1 }}{{ entry.manual ? ', resent by hand' : '' }}</span>
                  </span>
                  <span class="font-mono text-[11px] text-ink/55">{{ msWords(entry.ms) }}</span>
                  <span class="w-32 text-right text-[11px] text-ink/55">{{ when(entry.at) }}</span>
                </button>
              </li>
              <li v-if="delivery.status === 'retrying' && delivery.next" class="grid grid-cols-[1.5rem_minmax(0,1fr)_auto] items-center gap-3 bg-ink/2 px-3 py-2">
                <Icon aria-hidden="true" class="text-base text-warning" name="material-symbols:schedule-outline" />
                <span class="text-[11px] text-ink/60">Try {{ delivery.attempts.length + 1 }}, on the schedule</span>
                <span class="w-32 text-right text-[11px] text-ink/55">{{ when(delivery.next) }}</span>
              </li>
            </ol>
          </div>

          <div v-if="attempt" class="flex flex-col gap-3">
            <PageTabs v-model="tab" :tabs="TABS" label="Try details" />

            <template v-if="tab === 'request'">
              <div class="flex flex-col gap-2">
                <span class="flex min-w-0 items-center gap-2 rounded-(--ps-radius) bg-ink/4 px-3 py-2 font-mono text-xs">
                  <span class="font-medium text-primary">POST</span>
                  <span class="truncate text-ink">{{ destination.url }}</span>
                </span>
                <table class="w-full border-collapse text-left">
                  <tbody>
                    <tr v-for="header in headers" :key="header.name" class="border-b border-line/60 last:border-b-0">
                      <th class="w-56 py-1.5 pr-3 align-top font-mono text-[11px] font-normal whitespace-nowrap text-ink/55">
                        {{ header.name }}
                      </th>
                      <td class="py-1.5 font-mono text-[11px] break-all text-ink">
                        {{ header.value }}
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <CodeBlock :code="body" title="Body" />
            </template>

            <template v-else>
              <div class="flex flex-wrap items-center gap-x-6 gap-y-1 rounded-(--ps-radius) bg-ink/4 px-3 py-2 text-xs">
                <span class="flex items-center gap-1.5">
                  <span class="text-ink/50">Status</span>
                  <span :class="answerOk(attempt) ? 'text-success' : 'text-error'" class="font-mono">{{ answerWords(attempt) }}</span>
                </span>
                <span class="flex items-center gap-1.5">
                  <span class="text-ink/50">Took</span>
                  <span class="font-mono text-ink">{{ msWords(attempt.ms) }}</span>
                </span>
              </div>
              <CodeBlock v-if="response.text && response.json" :code="response.text" title="Body, first 4 KB" />
              <div v-else-if="response.text" class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border border-line">
                <span class="border-b border-line bg-ink/3 px-3 py-2 text-[11px] font-medium text-ink/55">Body, first 4 KB</span>
                <pre class="m-0 overflow-x-auto p-3 font-mono text-[11px] leading-5 whitespace-pre-wrap text-ink/80">{{ response.text }}</pre>
              </div>
              <p v-else class="m-0 rounded-(--ps-radius-large) border border-dashed border-line px-4 py-6 text-center text-xs text-ink/50">
                {{ attempt.code === null ? 'No answer came back, so there is no body.' : 'The answer had no body.' }}
              </p>
            </template>
          </div>
        </div>
      </div>

      <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-line px-5 py-3">
        <span class="text-[11px] text-ink/55">
          <template v-if="lastResend">
            Resent: <span :class="answerOk(lastResend) ? 'text-success' : 'text-error'" class="font-mono">{{ answerWords(lastResend) }}</span> in {{ msWords(lastResend.ms) }}.
          </template>
          <template v-else-if="disabled">
            The destination is disabled. Enable it to resend.
          </template>
          <template v-else>
            Resending sends the same event id. Your server should handle it once.
          </template>
        </span>
        <span class="flex items-center gap-2">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Close
          </v-btn>
          <v-btn
            :disabled="disabled"
            :loading="resending"
            class="text-xs font-medium"
            color="primary"
            prepend-icon="material-symbols:send-outline"
            size="small"
            variant="flat"
            @click="resendNow"
          >
            Resend
          </v-btn>
        </span>
      </footer>
    </v-card>
  </v-dialog>
</template>
