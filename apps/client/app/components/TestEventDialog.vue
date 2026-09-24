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
const environment = ref<string>(props.destination.environments[0] ?? 'production')

const TYPE_OPTIONS = computed<GhostSelectOption[]>(() => types.value.map(value => ({ value, label: value, group: WEBHOOK_GROUP[WEBHOOK_EVENT[value]!.group].label })))
const ENVIRONMENT_OPTIONS = computed<SegmentedOption[]>(() => props.destination.environments.map(value => ({ value, label: ENVIRONMENT_LABELS[value] })))

const body = computed(() => JSON.stringify(eventDocument(type.value, {
  id: 'evt_test_…',
  created: Date.now(),
  environment: WEBHOOK_EVENT[type.value]?.environments ? environment.value as EnvironmentKind : 'production',
  version: props.destination.version,
  seed: `test:${type.value}`
}), null, 2))

const sending = ref(false)
const result = ref<WebhookDelivery | null>(null)

async function send() {
  sending.value = true
  result.value = null
  await new Promise(resolve => setTimeout(resolve, 700))
  result.value = sendTest(props.destination, type.value, WEBHOOK_EVENT[type.value]?.environments ? environment.value as EnvironmentKind : 'production')
  sending.value = false
}
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
          <!-- The event, and whose it is as if from. -->
          <div class="flex flex-wrap items-center justify-between gap-3">
            <span class="flex min-w-0 flex-col">
              <span class="text-[11px] text-ink/50">Event</span>
              <GhostSelect
                v-model="type"
                :options="TYPE_OPTIONS"
                class="font-mono"
                label="Event"
              />
            </span>
            <span v-if="destination.environments.length > 1 && WEBHOOK_EVENT[type]?.environments" class="flex flex-col items-end gap-1">
              <span class="text-[11px] text-ink/50">As if from</span>
              <SegmentedControl v-model="environment" :options="ENVIRONMENT_OPTIONS" label="As if from" />
            </span>
            <span v-else class="flex flex-col items-end">
              <span class="text-[11px] text-ink/50">As if from</span>
              <span class="text-xs text-ink/70">{{ ENVIRONMENT_LABELS[WEBHOOK_EVENT[type]?.environments ? environment as EnvironmentKind : 'production'] }}</span>
            </span>
          </div>
          <p class="-mt-2 mb-0 text-[11px] text-ink/55">
            {{ WEBHOOK_EVENT[type]?.summary }}
          </p>

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
