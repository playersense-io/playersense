<script setup lang="ts">
// Makes a notification rule, or changes one: which events, from which environments, to which
// channels, and how often at most. The events are the webhook picker's rows, so choosing what
// a person hears reads the same as choosing what a server hears.
//
// A rule of the organization hears its account's events and may point at any project's
// channels. Its owners, admins and billing members are emailed about those whatever a rule
// says, and the dialog says so, so nobody writes a rule to get an email they already get.
const props = defineProps<{
  scope: RuleScope
  /** The rule being changed; absent, a new one is made. */
  rule?: NotificationRule | null
}>()

const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ saved: [] }>()

const { kindsOf } = useNotificationKinds()
const { create, update } = useNotificationRules(props.scope)
const { channels } = useRuleChannels(props.scope)

const editing = computed(() => Boolean(props.rule))
const draft = ref<NotificationRuleDraft>(props.rule
  ? draftOf(props.rule)
  : { name: '', enabled: true, events: [], environments: [], channels: [], cooldownMinutes: 0, mutedUntil: null })

const kinds = computed(() => kindsOf(props.scope))
const pickerEvents = computed(() => kinds.value.map(pickerEvent))
const pickerGroups = computed<WebhookGroup[]>(() => props.scope === 'organization'
  ? [BILLING_GROUP]
  : WEBHOOK_GROUPS.filter(group => kinds.value.some(kind => kind.group === group.key)))

/* Environments only mean something for events that belong to one. */
const hearsEnvironments = computed(() => props.scope === 'project' && (draft.value.events.includes(EVERY_EVENT)
  || draft.value.events.some(type => kinds.value.find(kind => kind.type === type)?.environments)))

const ENVIRONMENTS: readonly { value: EnvironmentKind, label: string }[] = [
  { value: 'production', label: 'Production' },
  { value: 'staging', label: 'Staging' },
  { value: 'development', label: 'Development' }
]

function toggleEnvironment(env: EnvironmentKind) {
  const list = draft.value.environments
  draft.value.environments = list.includes(env) ? list.filter(entry => entry !== env) : [...list, env]
}

function toggleChannel(id: string) {
  const list = draft.value.channels
  draft.value.channels = list.includes(id) ? list.filter(entry => entry !== id) : [...list, id]
}

const cooldown = computed({
  get: () => String(draft.value.cooldownMinutes),
  set: (value: string) => {
    draft.value.cooldownMinutes = Number(value)
  }
})
const COOLDOWN_OPTIONS: SegmentedOption[] = COOLDOWNS.map(entry => ({ value: entry.value, label: entry.label }))
const cooldownNote = computed(() => COOLDOWNS.find(entry => entry.value === cooldown.value)?.note
  ?? `At most once every ${cooldownWords(draft.value.cooldownMinutes)} for each kind of event.`)

const problem = computed(() => notificationRuleProblem(draft.value))

const saving = ref(false)
const failure = ref('')

async function save() {
  if (problem.value || saving.value) return
  saving.value = true
  failure.value = ''
  try {
    const body = { ...draft.value, environments: hearsEnvironments.value ? draft.value.environments : [] }
    if (props.rule) await update(props.rule, body)
    else await create(body)
    emit('saved')
    open.value = false
  } catch (caught) {
    failure.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

const nameId = useId()
</script>

<template>
  <v-dialog
    v-model="open"
    max-width="720"
    scrollable
  >
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ editing ? `Edit ${rule!.name}` : scope === 'organization' ? 'New rule for the organization' : 'New notification rule' }}
        </h2>
        <p class="m-0 text-xs text-ink/60">
          {{ scope === 'organization'
            ? 'When something happens to the organization\'s account, tell these channels.'
            : 'When one of these things happens in this project, tell these channels.' }}
        </p>
      </header>

      <div class="overflow-y-auto">
        <form class="flex flex-col gap-6 p-5" @submit.prevent="save">
          <div class="flex flex-col gap-1.5">
            <label :for="nameId" class="text-xs text-ink/60">Name <span class="text-ink/40">what the channels' log lists it as</span></label>
            <v-text-field
              :id="nameId"
              v-model="draft.name"
              :placeholder="scope === 'organization' ? 'Billing problems to #finance' : 'Publishing to #live-ops'"
              autocomplete="off"
              autofocus
              hide-details
              maxlength="120"
              variant="outlined"
            />
          </div>

          <InfoNote
            v-if="scope === 'organization'"
            text="Owners, admins and billing members are always emailed about these, at most once a day for each kind, whatever the rules say. A rule adds more places to hear about them."
          />

          <div class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">When</span>
            <WebhookEventPicker
              v-model="draft.events"
              :events="pickerEvents"
              :every-note="scope === 'organization' ? 'Everything about the account, and new events as they are added.' : `All ${pickerEvents.length} of the project's events today, and new ones as they are added.`"
              :groups="pickerGroups"
              :payloads="false"
              :placeholder="scope === 'organization' ? 'top-up, suspended…' : 'published, refund, member…'"
              chosen-note="Only the ones ticked below. Recommended: a channel hears what it needs to act on."
            />
          </div>

          <div v-if="hearsEnvironments" class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">From <span class="text-ink/40">none ticked is every environment</span></span>
            <div class="flex flex-wrap gap-1.5" role="group" aria-label="Environments">
              <button
                v-for="env in ENVIRONMENTS"
                :key="env.value"
                :aria-pressed="draft.environments.includes(env.value)"
                :class="draft.environments.includes(env.value) ? 'border-primary bg-primary/6 text-ink' : 'border-line bg-surface text-ink/70 hover:bg-ink/3'"
                class="flex cursor-pointer items-center gap-1.5 rounded-full border px-3 py-1 text-xs transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
                type="button"
                @click="toggleEnvironment(env.value)"
              >
                <Icon
                  v-if="draft.environments.includes(env.value)"
                  :aria-hidden="true"
                  class="text-sm text-primary"
                  name="material-symbols:check"
                />
                {{ env.label }}
              </button>
            </div>
          </div>

          <div class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">Tell <span class="text-ink/40">{{ scope === 'organization' ? 'any project\'s channels' : 'this project\'s channels' }}</span></span>
            <div v-if="channels.length" class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border border-line">
              <button
                v-for="channel in channels"
                :key="channel.id"
                :aria-checked="draft.channels.includes(channel.id)"
                class="flex cursor-pointer items-center gap-3 border-t border-line/70 px-3 py-2.5 text-left first:border-t-0 hover:bg-ink/2 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary"
                role="checkbox"
                type="button"
                @click="toggleChannel(channel.id)"
              >
                <span
                  :class="draft.channels.includes(channel.id) ? 'border-primary bg-primary text-white' : 'border-ink/25'"
                  aria-hidden="true"
                  class="flex size-3.5 flex-none items-center justify-center rounded-(--ps-radius-x-small) border-2"
                >
                  <Icon v-if="draft.channels.includes(channel.id)" class="text-[11px]" name="material-symbols:check" />
                </span>
                <ChannelMark :type="channel.type" size="small" />
                <span class="flex min-w-0 flex-1 flex-col">
                  <span :class="channel.enabled ? 'text-ink' : 'text-ink/45'" class="truncate text-xs">{{ channel.name }}</span>
                  <span class="truncate text-[11px] text-ink/50">{{ CHANNEL_TYPES[channel.type].label }}<template v-if="channel.project"> · {{ channel.project }}</template></span>
                </span>
                <Tag v-if="!channel.enabled" label="Off" />
              </button>
            </div>
            <p v-else class="m-0 rounded-(--ps-radius-large) border border-dashed border-line px-4 py-3 text-xs text-ink/60">
              No channels yet.
              <NuxtLink class="text-primary" to="/notifications/channels">Set one up</NuxtLink>
              first: an email list, a Slack channel, PagerDuty.
            </p>
          </div>

          <div class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">How often at most</span>
            <SegmentedControl
              v-model="cooldown"
              :options="COOLDOWN_OPTIONS"
              label="How often at most"
              stretch
            />
            <span class="text-[11px] leading-4 text-ink/50">{{ cooldownNote }}</span>
          </div>

          <p v-if="failure" class="m-0 text-xs text-error">
            {{ failure }}
          </p>
        </form>
      </div>

      <footer class="flex items-center justify-between gap-3 border-t border-line px-5 py-3">
        <v-switch
          v-model="draft.enabled"
          class="flex-none"
          color="primary"
          density="compact"
          hide-details
          inset
          label="On"
        />
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
            {{ editing ? 'Save' : 'Make rule' }}
          </v-btn>
        </span>
      </footer>
    </v-card>
  </v-dialog>
</template>
