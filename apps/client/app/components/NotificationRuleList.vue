<script setup lang="ts">
// One scope's rules as a card of rows: what each hears, where it goes, how often, and when it
// last fired, with a test, a switch, a mute and the rest a menu away. The project's and the
// organization's sit on one page, each in a card of its own, because a studio looks for
// "who hears about what" in one place whichever of the two it is.
//
// The organization's rules are billing's: somebody who may not read them sees whose they are
// rather than an error.
const props = defineProps<{
  scope: RuleScope
}>()

const { kinds } = useNotificationKinds()
const { rules, error, forbidden, loading, reload, setEnabled, muteUntil, remove, test: sendTest } = useNotificationRules(props.scope)
const { channels } = useRuleChannels(props.scope)

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const channelOf = (id: string) => channels.value.find(channel => channel.id === id) ?? null

const dialog = ref<{ rule: NotificationRule | null } | null>(null)
const dialogOpen = computed({
  get: () => dialog.value !== null,
  set: (value) => {
    if (!value) dialog.value = null
  }
})

const problem = ref('')
const notice = ref('')

async function attempt(change: () => Promise<unknown>) {
  problem.value = ''
  notice.value = ''
  try {
    await change()
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  }
}

const testing = ref<string | null>(null)
async function test(rule: NotificationRule) {
  testing.value = rule.id
  await attempt(async () => {
    const out = await sendTest(rule)
    const failed = out.channels.filter(entry => !entry.ok)
    if (!out.channels.length) problem.value = `${rule.name} has no channel left to test: they were deleted.`
    else if (failed.length) problem.value = `${failed.map(entry => entry.name).join(', ')} did not get the test: ${failed[0]!.detail}`
    else notice.value = `A test of ${rule.name} reached ${out.channels.length === 1 ? out.channels[0]!.name : `all ${out.channels.length} channels`}.`
  })
  testing.value = null
}

const deleting = ref<NotificationRule | null>(null)
const deleteOpen = computed({
  get: () => deleting.value !== null,
  set: (value) => {
    if (!value) deleting.value = null
  }
})

const environmentWords = (rule: NotificationRule) => rule.environments.length
  ? rule.environments.map(env => env[0]!.toUpperCase() + env.slice(1)).join(', ')
  : 'Every environment'

const stamp = (at: number) => new Date(at).toLocaleString('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

const title = computed(() => props.scope === 'organization' ? 'The organization' : 'This project')
const description = computed(() => props.scope === 'organization'
  ? 'Its account and money: a failed top-up, a balance below zero, events refused and let back in. Managed by owners, admins and billing members.'
  : 'What happens in the game\'s project: purchases, publishing, experiments, members and keys.')
</script>

<template>
  <SectionCard :description="description" :padded="false" :title="title">
    <template #actions>
      <v-btn
        v-if="!forbidden"
        class="text-xs"
        color="neutral"
        prepend-icon="material-symbols:add"
        size="small"
        variant="text"
        @click="dialog = { rule: null }"
      >
        New rule
      </v-btn>
    </template>

    <p v-if="forbidden" class="m-0 flex items-start gap-2.5 px-5 py-4 text-xs leading-5 text-ink/60">
      <Icon :aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:lock-outline" />
      <span>The organization's rules are for the people who manage billing: owners, admins and billing members. Ask one of them to route its alerts to your channels.</span>
    </p>

    <LoadState
      v-else
      :error="error"
      :loading="loading"
      what="the rules"
      @retry="reload"
    >
      <div class="flex flex-col">
        <p v-if="problem" class="m-0 border-b border-line bg-error/6 px-5 py-2.5 text-xs text-error">
          {{ problem }}
        </p>
        <p v-if="notice" class="m-0 border-b border-line bg-success/8 px-5 py-2.5 text-xs text-ink/80" role="status">
          {{ notice }}
        </p>

        <p v-if="!rules.length" class="m-0 px-5 py-4 text-xs leading-5 text-ink/55">
          No rules yet.
          <template v-if="scope === 'organization'">
            The owners are still emailed about the account; a rule adds Slack, PagerDuty or a team list.
          </template>
          <template v-else>
            Make one to hear about a publish, a refund or a new member where your team already is.
          </template>
        </p>

        <ul v-else class="m-0 flex list-none flex-col p-0">
          <li
            v-for="rule in rules"
            :key="rule.id"
            class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-line/70 px-5 py-3.5 first:border-t-0"
          >
            <span class="flex min-w-0 flex-1 basis-72 flex-col gap-1">
              <span class="flex flex-wrap items-center gap-2">
                <span :class="rule.enabled ? 'text-ink' : 'text-ink/45'" class="truncate text-[13px] font-medium">{{ rule.name }}</span>
                <StatusBadge v-if="!rule.enabled" label="Off" />
                <StatusBadge
                  v-else-if="isMuted(rule, time)"
                  :detail="`until ${stamp(rule.mutedUntil!)}`"
                  label="Muted"
                  tone="warning"
                />
              </span>
              <span class="truncate text-[11px] text-ink/60">{{ ruleEventWords(rule, kinds) }}</span>
              <span class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-ink/45">
                <template v-if="scope === 'project'">
                  <span>{{ environmentWords(rule) }}</span>
                  <span aria-hidden="true">·</span>
                </template>
                <span>{{ rule.cooldownMinutes ? `At most every ${cooldownWords(rule.cooldownMinutes)}` : 'Every time' }}</span>
                <span aria-hidden="true">·</span>
                <span>{{ rule.lastFiredAt ? `Fired ${relativeWords(rule.lastFiredAt, time)}` : 'Never fired' }}</span>
              </span>
            </span>

            <span class="flex w-56 flex-none flex-wrap items-center gap-1.5">
              <template v-for="id in rule.channels" :key="id">
                <span
                  v-if="channelOf(id)"
                  :title="channelOf(id)!.project ? `${channelOf(id)!.name} (${channelOf(id)!.project})` : channelOf(id)!.name"
                  class="flex max-w-full items-center gap-1.5 rounded-full border border-line py-0.5 pr-2 pl-1"
                >
                  <ChannelMark :type="channelOf(id)!.type" size="small" />
                  <span :class="channelOf(id)!.enabled ? 'text-ink/80' : 'text-ink/40 line-through'" class="truncate text-[11px]">{{ channelOf(id)!.name }}</span>
                </span>
                <span v-else class="rounded-full border border-dashed border-line px-2 py-0.5 text-[11px] text-ink/40">Deleted channel</span>
              </template>
            </span>

            <span class="flex flex-none items-center gap-1">
              <v-btn
                :disabled="!rule.channels.length"
                :loading="testing === rule.id"
                class="text-xs"
                color="neutral"
                prepend-icon="material-symbols:send-outline"
                size="small"
                variant="text"
                @click="test(rule)"
              >
                Test
              </v-btn>
              <v-switch
                :aria-label="`${rule.name} on`"
                :model-value="rule.enabled"
                color="primary"
                density="compact"
                hide-details
                inset
                @update:model-value="attempt(() => setEnabled(rule, Boolean($event)))"
              />
              <v-menu location="bottom end">
                <template #activator="{ props: menu }">
                  <v-btn
                    v-bind="menu"
                    :aria-label="`${rule.name} options`"
                    color="neutral"
                    icon="material-symbols:more-horiz"
                    size="small"
                    variant="text"
                  />
                </template>
                <v-list min-width="200" nav>
                  <v-list-item prepend-icon="material-symbols:edit-outline" title="Edit" @click="dialog = { rule }" />
                  <v-list-item
                    v-if="isMuted(rule, time)"
                    prepend-icon="material-symbols:notifications-active-outline"
                    title="Unmute"
                    @click="attempt(() => muteUntil(rule, null))"
                  />
                  <v-menu v-else location="start" open-on-hover>
                    <template #activator="{ props: submenu }">
                      <v-list-item
                        v-bind="submenu"
                        append-icon="material-symbols:chevron-right"
                        prepend-icon="material-symbols:notifications-off-outline"
                        title="Mute for"
                      />
                    </template>
                    <v-list min-width="140" nav>
                      <v-list-item
                        v-for="option in MUTE_FOR"
                        :key="option.label"
                        :title="option.label"
                        @click="attempt(() => muteUntil(rule, Date.now() + option.ms))"
                      />
                    </v-list>
                  </v-menu>
                  <v-list-item
                    base-color="error"
                    prepend-icon="material-symbols:delete-outline"
                    title="Delete"
                    @click="deleting = rule"
                  />
                </v-list>
              </v-menu>
            </span>
          </li>
        </ul>
      </div>
    </LoadState>

    <NotificationRuleDialog
      v-if="dialog"
      v-model="dialogOpen"
      :rule="dialog.rule"
      :scope="scope"
      @saved="reload"
    />

    <ConfirmDialog
      v-if="deleting"
      v-model="deleteOpen"
      :title="`Delete ${deleting.name}?`"
      action="Delete rule"
      description="Its channels stop hearing about these events. What was already sent stays in their log."
      tone="error"
      @confirm="attempt(() => remove(deleting!))"
    />
  </SectionCard>
</template>
