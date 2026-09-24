<script setup lang="ts">
// A made-up event sent to one destination now, signed with its real secret, so a new handler
// can be tried before real events reach it. The body is what the event type really carries,
// with made-up ids; the answer is shown here, and the delivery is kept in the log, marked Test.
const props = defineProps<{
  destination: WebhookDestination
}>()

const open = defineModel<boolean>({ required: true })

const { sendTest } = useWebhooks()

const types = computed(() => eventsOf(props.destination.events))
const type = ref(types.value[0] ?? WEBHOOK_EVENTS[0]!.type)
const environment = ref<EnvironmentKind>(props.destination.environments[0] ?? 'production')

const TYPE_ITEMS = computed(() => types.value.map(value => ({ value, title: value, subtitle: WEBHOOK_EVENT[value]?.summary })))
const ENVIRONMENT_ITEMS = computed(() => props.destination.environments.map(value => ({ value, title: ENVIRONMENT_LABELS[value] })))

const body = computed(() => JSON.stringify(eventDocument(type.value, {
  id: 'evt_test_…',
  created: Date.now(),
  environment: WEBHOOK_EVENT[type.value]?.environments ? environment.value : 'production',
  version: props.destination.version,
  seed: `test:${type.value}`
}), null, 2))

const sending = ref(false)
const result = ref<WebhookDelivery | null>(null)

async function send() {
  sending.value = true
  result.value = null
  await new Promise(resolve => setTimeout(resolve, 700))
  result.value = sendTest(props.destination, type.value, WEBHOOK_EVENT[type.value]?.environments ? environment.value : 'production')
  sending.value = false
}

const ids = { type: useId(), environment: useId() }
</script>

<template>
  <v-dialog v-model="open" max-width="680" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          Send a test event
        </h2>
        <p class="m-0 text-xs text-ink/60">
          To <span class="font-mono text-ink">{{ destination.url }}</span>, signed with its secret. Your server cannot tell it from a real one but by its id, which starts evt_test.
        </p>
      </header>

      <div class="overflow-y-auto">
        <div class="flex flex-col gap-4 p-5">
          <div class="grid gap-4 sm:grid-cols-[minmax(0,1fr)_12rem]">
            <div class="flex flex-col gap-1.5">
              <label :for="ids.type" class="text-xs font-medium text-ink">Event</label>
              <v-select
                :id="ids.type"
                v-model="type"
                :items="TYPE_ITEMS"
                class="font-mono"
                hide-details
                item-props
                variant="outlined"
              />
            </div>
            <div class="flex flex-col gap-1.5">
              <label :for="ids.environment" class="text-xs font-medium text-ink">As if from</label>
              <v-select
                :id="ids.environment"
                v-model="environment"
                :disabled="!WEBHOOK_EVENT[type]?.environments || destination.environments.length < 2"
                :items="ENVIRONMENT_ITEMS"
                hide-details
                variant="outlined"
              />
            </div>
          </div>

          <!-- The answer, once it is in. -->
          <div
            v-if="result"
            :class="answerOk(result.attempts[0]!) ? 'bg-success/8 ring-success/20' : 'bg-error/8 ring-error/20'"
            class="flex flex-col gap-1 rounded-(--ps-radius) px-3 py-2.5 ring-1 ring-inset"
            role="status"
          >
            <span class="flex items-center gap-2 text-xs font-medium text-ink">
              <Icon
                :class="answerOk(result.attempts[0]!) ? 'text-success' : 'text-error'"
                :name="answerOk(result.attempts[0]!) ? 'material-symbols:check-circle-outline' : 'material-symbols:cancel-outline'"
                aria-hidden="true"
                class="text-base"
              />
              {{ answerOk(result.attempts[0]!) ? 'Delivered' : 'Not delivered' }}:
              <span class="font-mono">{{ answerWords(result.attempts[0]!) }}</span>
              <span class="font-mono font-normal text-ink/50">in {{ msWords(result.attempts[0]!.ms) }}</span>
            </span>
            <span class="text-[11px] text-ink/60">{{ answerOk(result.attempts[0]!) ? 'Your server took it. It is in the log below, marked Test.' : 'Your server refused it. It is in the log below, and will be retried like a real one.' }}</span>
            <code v-if="result.attempts[0]!.body" class="mt-1 truncate font-mono text-[11px] text-ink/70">{{ result.attempts[0]!.body }}</code>
          </div>

          <CodeBlock :code="body" title="Body" />
        </div>
      </div>

      <footer class="flex justify-end gap-2 border-t border-line px-5 py-3">
        <v-btn
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="open = false"
        >
          {{ result ? 'Done' : 'Cancel' }}
        </v-btn>
        <v-btn
          :disabled="destination.status === 'disabled'"
          :loading="sending"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:send-outline"
          size="small"
          variant="flat"
          @click="send"
        >
          {{ result ? 'Send another' : 'Send test event' }}
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>
