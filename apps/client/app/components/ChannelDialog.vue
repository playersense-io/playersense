<script setup lang="ts">
// Adds a channel of one type, or edits one: its name, where it sends, and a test that sends
// a real-looking message there now, so a channel is known to work before a report counts on
// it. Each type asks for what it needs and says where to find it: a Slack workspace to
// connect, a Discord webhook to make, an integration key to copy out of PagerDuty.
const props = defineProps<{
  /** The channel being edited; absent, a new one of `type` is added. */
  channel?: NotificationChannel | null
  type: ChannelType
}>()

const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ saved: [id: string] }>()

const { create, update, send } = useChannels()

const info = computed(() => CHANNEL_TYPES[props.type])
const editing = computed(() => Boolean(props.channel))

const name = ref(props.channel?.name ?? '')
const addresses = ref<string[]>([...(props.channel?.config.addresses ?? [])])
const workspace = ref(props.channel?.config.workspace ?? '')
const slackChannel = ref(props.channel?.config.channel ?? '')
const url = ref(props.channel?.config.url ?? '')
const chat = ref(props.channel?.config.chat ?? '')
const routingKey = ref(props.channel?.config.routingKey ?? '')
const severity = ref<string>(props.channel?.config.severity ?? 'error')
const header = ref(props.channel?.config.header ?? '')

const config = computed<ChannelConfig>(() => {
  switch (props.type) {
    case 'email': return { addresses: addresses.value }
    case 'slack': return { workspace: workspace.value, channel: slackChannel.value }
    case 'telegram': return { chat: chat.value }
    case 'pagerduty': return { routingKey: routingKey.value.trim(), severity: severity.value as Severity }
    case 'webhook': return { url: url.value.trim(), ...(header.value.trim() ? { header: header.value.trim() } : {}) }
    default: return { url: url.value.trim() }
  }
})

const problem = computed(() => channelProblem(props.type, name.value, config.value))
/* What stands between the channel and a test: its destination, whatever it is called. */
const destinationProblem = computed(() => channelProblem(props.type, 'named', config.value))

/* Connecting is an OAuth round trip to Slack, or the bot seeing its start code: simulated. */
const connecting = ref(false)
async function connect() {
  connecting.value = true
  await new Promise(resolve => setTimeout(resolve, 900))
  if (props.type === 'slack') workspace.value = 'Starfall Games'
  else chat.value = TELEGRAM_CHATS[0]!
  connecting.value = false
}

const startCode = 'ps-7k2m'

// --- Testing ------------------------------------------------------------------------------

const testing = ref(false)
const tested = ref<(ChannelTest & { ms: number }) | null>(null)

async function test() {
  if (destinationProblem.value) return
  testing.value = true
  tested.value = null
  await new Promise(resolve => setTimeout(resolve, 700))
  const draft: NotificationChannel = { id: props.channel?.id ?? 'draft', type: props.type, name: name.value || info.value.label, enabled: true, config: config.value, created_at: '', created_by: '' }
  tested.value = props.channel && sameJson(props.channel.config, config.value)
    ? send(props.channel, 'Test notification from PlayerSense', 'Test from this page', true)
    : simulateSend(draft)
  testing.value = false
}

function save() {
  if (problem.value) return
  const draft: ChannelDraft = { type: props.type, name: name.value.trim(), config: config.value }
  let id: string
  if (props.channel) {
    update(props.channel.id, draft)
    id = props.channel.id
  } else {
    id = create(draft, tested.value ? { at: tested.value.at, ok: tested.value.ok, detail: tested.value.detail } : undefined)
  }
  emit('saved', id)
  open.value = false
}

const SEVERITY_OPTIONS: SegmentedOption[] = (Object.keys(SEVERITIES) as Severity[]).map(value => ({ value, label: SEVERITIES[value].label }))

const ids = { name: useId(), url: useId(), key: useId(), header: useId() }
</script>

<template>
  <v-dialog v-model="open" max-width="560" scrollable>
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

      <div class="overflow-y-auto">
        <form class="flex flex-col gap-5 p-5" @submit.prevent="save">
          <div class="flex flex-col gap-1.5">
            <label :for="ids.name" class="text-xs text-ink/60">Name <span class="text-ink/40">what reports and alerts list it as</span></label>
            <v-text-field
              :id="ids.name"
              v-model="name"
              :placeholder="{ email: 'Finance team', slack: 'Live ops channel', discord: 'Community mods', teams: 'Leadership', telegram: 'Ops group', pagerduty: 'Backend on call', webhook: 'Warehouse loader' }[type]"
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

          <!-- Slack. -->
          <template v-else-if="type === 'slack'">
            <div class="flex flex-col gap-1.5">
              <span class="text-xs text-ink/60">Workspace</span>
              <div v-if="workspace" class="flex items-center gap-2 rounded-(--ps-radius) border border-line px-3 py-2">
                <Icon aria-hidden="true" class="text-base" name="selfhst:slack" />
                <span class="text-[13px] text-ink">{{ workspace }}</span>
                <span class="text-[11px] text-success">Connected</span>
                <v-btn
                  class="ml-auto text-xs"
                  color="neutral"
                  size="small"
                  variant="text"
                  @click="workspace = ''; slackChannel = ''"
                >
                  Disconnect
                </v-btn>
              </div>
              <div v-else class="flex flex-col items-start gap-2">
                <v-btn
                  :loading="connecting"
                  class="text-xs font-medium"
                  color="neutral"
                  prepend-icon="selfhst:slack"
                  variant="outlined"
                  @click="connect"
                >
                  Connect Slack
                </v-btn>
                <span class="text-[11px] leading-4 text-ink/50">Opens Slack to add the PlayerSense app to your workspace. It can only post to channels it is invited to.</span>
              </div>
            </div>
            <div v-if="workspace" class="flex flex-col gap-1.5">
              <span class="text-xs text-ink/60">Channel</span>
              <v-select
                v-model="slackChannel"
                :items="SLACK_CHANNELS"
                v-bind="FIELD_MENU"
                aria-label="Channel"
                class="font-mono"
                hide-details
                placeholder="Choose a channel"
                variant="outlined"
              />
              <span class="text-[11px] leading-4 text-ink/50">A private channel is listed once /invite @PlayerSense has been run in it.</span>
            </div>
          </template>

          <!-- Telegram. -->
          <template v-else-if="type === 'telegram'">
            <div v-if="!chat" class="flex flex-col gap-2 rounded-(--ps-radius-large) bg-ink/3 p-4">
              <span class="text-xs font-medium text-ink">Add the bot to your group</span>
              <ol class="m-0 flex flex-col gap-1 pl-4 text-xs leading-5 text-ink/70">
                <li>Add <span class="font-mono text-ink">@PlayerSenseBot</span> to the group or chat.</li>
                <li>Send it <span class="font-mono text-ink">/start {{ startCode }}</span> there.</li>
              </ol>
              <div>
                <v-btn
                  :loading="connecting"
                  class="text-xs"
                  color="neutral"
                  size="small"
                  variant="outlined"
                  @click="connect"
                >
                  I have sent it
                </v-btn>
              </div>
            </div>
            <div v-else class="flex flex-col gap-1.5">
              <span class="text-xs text-ink/60">Chat</span>
              <v-select
                v-model="chat"
                :items="TELEGRAM_CHATS"
                v-bind="FIELD_MENU"
                aria-label="Chat"
                hide-details
                variant="outlined"
              />
            </div>
          </template>

          <!-- PagerDuty. -->
          <template v-else-if="type === 'pagerduty'">
            <div class="flex flex-col gap-1.5">
              <label :for="ids.key" class="text-xs text-ink/60">Integration key</label>
              <v-text-field
                :id="ids.key"
                v-model="routingKey"
                autocomplete="off"
                class="ps-channel-mono"
                hide-details
                placeholder="32 letters and digits"
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

          <!-- Discord, Teams, a webhook: a URL. -->
          <template v-else>
            <div class="flex flex-col gap-1.5">
              <label :for="ids.url" class="text-xs text-ink/60">{{ type === 'webhook' ? 'URL' : 'Webhook URL' }}</label>
              <v-text-field
                :id="ids.url"
                v-model="url"
                :placeholder="type === 'discord' ? 'https://discord.com/api/webhooks/…' : type === 'teams' ? 'https://….logic.azure.com/workflows/…' : 'https://api.yourstudio.com/playersense/reports'"
                autocomplete="off"
                class="ps-channel-mono"
                hide-details
                spellcheck="false"
                variant="outlined"
              />
              <span class="text-[11px] leading-4 text-ink/50">
                {{ type === 'discord' ? 'In Discord: the channel\'s settings, Integrations, Webhooks, New Webhook, then Copy Webhook URL.' : type === 'teams' ? 'In Teams: the channel\'s … menu, Workflows, then "Post to a channel when a webhook request is received".' : 'POSTed as JSON, signed with X-PlayerSense-Signature like every webhook.' }}
              </span>
            </div>
            <div v-if="type === 'webhook'" class="flex flex-col gap-1.5">
              <label :for="ids.header" class="text-xs text-ink/60">Header <span class="text-ink/40">optional</span></label>
              <v-text-field
                :id="ids.header"
                v-model="header"
                autocomplete="off"
                class="ps-channel-mono"
                hide-details
                placeholder="Authorization: Bearer …"
                spellcheck="false"
                variant="outlined"
              />
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
              aria-hidden="true"
              class="mt-0.5 flex-none text-base"
            />
            <span class="flex flex-col gap-0.5">
              <span class="text-xs font-medium text-ink">{{ tested.ok ? 'The test arrived' : 'The test did not arrive' }}</span>
              <span class="text-[11px] leading-4 text-ink/65">{{ tested.detail }}{{ tested.ms ? `, in ${tested.ms} ms` : '' }}.</span>
            </span>
          </div>
        </form>
      </div>

      <footer class="flex items-center justify-between gap-3 border-t border-line px-5 py-3">
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
