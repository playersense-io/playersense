<script setup lang="ts">
// Adds a channel of one type, or edits one: its name, where it sends, and a test that sends a
// real message there now, so a channel is known to work before a report counts on it. Each
// type asks for what it needs and says where to find it: a Slack or Discord webhook to make,
// an integration key to copy out of PagerDuty.
//
// A destination's secret is never read back. Editing, the URL shows masked and the fields are
// blank: left blank, the channel keeps what it has.
const props = defineProps<{
  /** The channel being edited; absent, a new one of `type` is added. */
  channel?: NotificationChannel | null
  type: ChannelType
}>()

const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ saved: [id: string] }>()

const { create, update, test: testSaved, testDraft } = useChannels()

const info = computed(() => CHANNEL_TYPES[props.type])
const editing = computed(() => Boolean(props.channel))

const name = ref(props.channel?.name ?? '')
const addresses = ref<string[]>([...(props.channel?.config.addresses ?? [])])
const url = ref('')
const routingKey = ref('')
const severity = ref<string>(props.channel?.config.severity ?? 'error')
const headerName = ref(props.channel?.config.header_name ?? '')
const headerValue = ref('')

const config = computed<ChannelConfigInput>(() => {
  switch (props.type) {
    case 'email': return { addresses: addresses.value }
    case 'pagerduty': return { routing_key: routingKey.value.trim() || undefined, severity: severity.value as Severity }
    case 'webhook': return {
      url: url.value.trim() || undefined,
      ...(headerName.value.trim() ? { header_name: headerName.value.trim(), header_value: headerValue.value.trim() || undefined } : {})
    }
    default: return { url: url.value.trim() || undefined }
  }
})

const problem = computed(() => channelProblem(props.type, name.value, config.value, editing.value))
/* What stands between the channel and a test: its destination, whatever it is called. */
const destinationProblem = computed(() => channelProblem(props.type, 'named', config.value, editing.value))
/* A test of what is typed, or of the saved channel when nothing about where it sends changed. */
const destinationChanged = computed(() => {
  const input = config.value
  if (!props.channel) return true
  return Boolean(input.url || input.routing_key || input.header_value)
    || !sameJson(input.addresses ?? [], props.channel.config.addresses ?? [])
    || (input.header_name ?? '') !== (props.channel.config.header_name ?? '')
})

// --- Testing ------------------------------------------------------------------------------

const testing = ref(false)
const tested = ref<ChannelSendAnswer | null>(null)
const failure = ref('')

async function test() {
  if (destinationProblem.value) return
  testing.value = true
  tested.value = null
  failure.value = ''
  try {
    tested.value = props.channel && !destinationChanged.value
      ? await testSaved(props.channel)
      : await testDraft(props.type, config.value)
  } catch (caught) {
    failure.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    testing.value = false
  }
}

// --- Saving -------------------------------------------------------------------------------

const saving = ref(false)
/** A new webhook channel's signing secret, shown this once. */
const secret = ref('')

async function save() {
  if (problem.value || saving.value) return
  saving.value = true
  failure.value = ''
  try {
    if (props.channel) {
      await update(props.channel, { name: name.value.trim(), config: config.value })
      emit('saved', props.channel.id)
      open.value = false
      return
    }
    const made = await create({ type: props.type, name: name.value.trim(), config: config.value })
    emit('saved', made.id)
    if (made.signingSecret) secret.value = made.signingSecret
    else open.value = false
  } catch (caught) {
    failure.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

const SEVERITY_OPTIONS: SegmentedOption[] = (Object.keys(SEVERITIES) as Severity[]).map(value => ({ value, label: SEVERITIES[value].label }))

const ids = { name: useId(), url: useId(), key: useId(), header: useId(), headerValue: useId() }
</script>

<template>
  <v-dialog
    v-model="open"
    :persistent="Boolean(secret)"
    max-width="560"
    scrollable
  >
    <v-card border flat>
      <header class="flex items-start gap-3 border-b border-line p-5">
        <ChannelMark :type="type" size="large" />
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ editing ? `Edit ${channel!.name}` : `Add a ${info.label} channel` }}
          </h2>
          <p class="m-0 text-xs text-ink/60">
            {{ info.note }}
          </p>
        </div>
      </header>

      <div v-if="secret" class="flex flex-col gap-3 p-5">
        <p class="m-0 text-xs leading-5 text-ink/70">
          {{ name }} is added. Its messages are signed with this secret: copy it now into whatever checks the signature. It is not shown again.
        </p>
        <CopyField :value="secret" label="Signing secret" />
        <div class="flex justify-end">
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="open = false"
          >
            Done
          </v-btn>
        </div>
      </div>

      <div v-if="!secret" class="overflow-y-auto">
        <form class="flex flex-col gap-5 p-5" @submit.prevent="save">
          <div class="flex flex-col gap-1.5">
            <label :for="ids.name" class="text-xs text-ink/60">Name <span class="text-ink/40">what reports and alerts list it as</span></label>
            <v-text-field
              :id="ids.name"
              v-model="name"
              :placeholder="{ email: 'Finance team', slack: 'Live ops channel', discord: 'Community mods', teams: 'Leadership', pagerduty: 'Backend on call', webhook: 'Warehouse loader' }[type]"
              autocomplete="off"
              autofocus
              hide-details
              maxlength="60"
              variant="outlined"
            />
          </div>

          <!-- Email. -->
          <div v-if="type === 'email'" class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">Addresses</span>
            <TagInput v-model="addresses" label="Addresses" placeholder="name@yourstudio.com, then Enter" />
            <span class="text-[11px] leading-4 text-ink/50">Sent from reports@playersense.io. A list address reaches everyone on it; nobody has to sign up here.</span>
          </div>

          <!-- PagerDuty. -->
          <template v-else-if="type === 'pagerduty'">
            <div class="flex flex-col gap-1.5">
              <label :for="ids.key" class="text-xs text-ink/60">Integration key</label>
              <v-text-field
                :id="ids.key"
                v-model="routingKey"
                :placeholder="channel?.config.has_routing_key ? 'Set. Leave blank to keep it' : '32 letters and digits'"
                autocomplete="off"
                class="ps-channel-mono"
                hide-details
                spellcheck="false"
                variant="outlined"
              />
              <span class="text-[11px] leading-4 text-ink/50">In PagerDuty: the service, then Integrations, then add Events API v2 and copy its key.</span>
            </div>
            <div class="flex flex-col gap-1.5">
              <span class="text-xs text-ink/60">Opens incidents as</span>
              <SegmentedControl
                v-model="severity"
                :options="SEVERITY_OPTIONS"
                label="Severity"
                stretch
              />
            </div>
          </template>

          <!-- Slack, Discord, Teams, a webhook: a URL. -->
          <template v-else>
            <div class="flex flex-col gap-1.5">
              <label :for="ids.url" class="text-xs text-ink/60">{{ type === 'webhook' ? 'URL' : 'Webhook URL' }}</label>
              <v-text-field
                :id="ids.url"
                v-model="url"
                :placeholder="channel?.config.url ? `${channel.config.url} (leave blank to keep it)` : type === 'slack' ? 'https://hooks.slack.com/services/…' : type === 'discord' ? 'https://discord.com/api/webhooks/…' : type === 'teams' ? 'https://….logic.azure.com/workflows/…' : 'https://api.yourstudio.com/playersense/reports'"
                autocomplete="off"
                class="ps-channel-mono"
                hide-details
                spellcheck="false"
                variant="outlined"
              />
              <span class="text-[11px] leading-4 text-ink/50">
                {{ type === 'slack' ? 'In Slack: add the Incoming Webhooks app to the channel, then copy its Webhook URL.' : type === 'discord' ? 'In Discord: the channel\'s settings, Integrations, Webhooks, New Webhook, then Copy Webhook URL.' : type === 'teams' ? 'In Teams: the channel\'s … menu, Workflows, then "Post to a channel when a webhook request is received".' : 'POSTed as JSON and signed; the signing secret is shown once, when the channel is added.' }}
              </span>
            </div>
            <div v-if="type === 'webhook'" class="grid grid-cols-2 gap-3">
              <div class="flex flex-col gap-1.5">
                <label :for="ids.header" class="text-xs text-ink/60">Header <span class="text-ink/40">optional</span></label>
                <v-text-field
                  :id="ids.header"
                  v-model="headerName"
                  autocomplete="off"
                  class="ps-channel-mono"
                  hide-details
                  placeholder="Authorization"
                  spellcheck="false"
                  variant="outlined"
                />
              </div>
              <div class="flex flex-col gap-1.5">
                <label :for="ids.headerValue" class="text-xs text-ink/60">Value</label>
                <v-text-field
                  :id="ids.headerValue"
                  v-model="headerValue"
                  :disabled="!headerName.trim()"
                  :placeholder="channel?.config.header_name ? 'Set. Leave blank to keep it' : 'Bearer …'"
                  autocomplete="off"
                  class="ps-channel-mono"
                  hide-details
                  spellcheck="false"
                  variant="outlined"
                />
              </div>
            </div>
          </template>

          <!-- The test's answer. -->
          <div
            v-if="tested"
            :class="tested.ok ? 'bg-success/8 ring-success/20' : 'bg-error/8 ring-error/20'"
            class="flex items-start gap-2 rounded-(--ps-radius) px-3 py-2.5 ring-1 ring-inset"
            role="status"
          >
            <Icon
              :class="tested.ok ? 'text-success' : 'text-error'"
              :name="tested.ok ? 'material-symbols:check-circle-outline' : 'material-symbols:cancel-outline'"
              :aria-hidden="true"
              class="mt-0.5 flex-none text-base"
            />
            <span class="flex flex-col gap-0.5">
              <span class="text-xs font-medium text-ink">{{ tested.ok ? 'The test arrived' : 'The test did not arrive' }}</span>
              <span class="text-[11px] leading-4 text-ink/65">{{ tested.detail }}{{ tested.ms ? `, in ${tested.ms} ms` : '' }}.</span>
            </span>
          </div>
          <p v-if="failure" class="m-0 text-xs text-error">
            {{ failure }}
          </p>
        </form>
      </div>

      <footer v-if="!secret" class="flex items-center justify-between gap-3 border-t border-line px-5 py-3">
        <v-btn
          :disabled="Boolean(destinationProblem)"
          :loading="testing"
          class="text-xs"
          color="neutral"
          prepend-icon="material-symbols:send-outline"
          size="small"
          variant="outlined"
          @click="test"
        >
          Send test
        </v-btn>
        <span class="flex items-center gap-2">
          <span v-if="problem" class="hidden max-w-56 truncate text-[11px] text-ink/50 sm:inline">{{ problem }}</span>
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
            @click="save"
          >
            {{ editing ? 'Save' : 'Add channel' }}
          </v-btn>
        </span>
      </footer>
    </v-card>
  </v-dialog>
</template>

<style scoped>
.ps-channel-mono :deep(input) {
  font-family: var(--font-mono);
}
</style>
