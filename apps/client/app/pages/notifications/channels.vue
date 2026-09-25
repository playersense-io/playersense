<script setup lang="ts">
// Where the project can tell people something, set up once and chosen by name from a
// notification rule or a report's Notify step. Laid out the way Google Cloud lays out its
// notification channels: a section a type, each with its channels and an Add button, so what
// exists and what does not is read at a glance. Every channel can be tested from here, and the
// last test is kept beside it: a channel that stopped working says so before it is counted on.
// What was sent to them is on the Activity page.
const { channels, loading, error, reload, setEnabled, remove, test: sendTest } = useChannels()
const { reports } = useReports()
const { rules } = useNotificationRules('project')

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

/* Which rules and reports notify each channel, by name. */
const usedBy = computed(() => {
  const out = new Map<string, string[]>()
  const add = (id: string, name: string) => out.set(id, [...new Set([...(out.get(id) ?? []), name])])
  for (const rule of rules.value) {
    for (const id of rule.channels) add(id, rule.name)
  }
  for (const report of reports.value) {
    for (const node of report.nodes) {
      if (node.data.kind !== 'notify') continue
      for (const id of node.data.config.channels ?? []) add(id, report.name)
    }
  }
  return out
})

const byType = computed(() => CHANNEL_TYPE_ORDER.map(type => ({ type, channels: channels.value.filter(channel => channel.type === type) })))

const counts = computed(() => ({
  all: channels.value.length,
  failing: channels.value.filter(channel => statusOf(channel) === 'failing').length
}))

// --- Adding, editing, testing -------------------------------------------------------------

const dialog = ref<{ type: ChannelType, channel: NotificationChannel | null } | null>(null)
const dialogOpen = computed({
  get: () => dialog.value !== null,
  set: (value) => {
    if (!value) dialog.value = null
  }
})

const testing = ref<string | null>(null)
const problem = ref('')

async function attempt(change: () => Promise<unknown>) {
  problem.value = ''
  try {
    await change()
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  }
}

async function test(channel: NotificationChannel) {
  testing.value = channel.id
  await attempt(() => sendTest(channel))
  testing.value = null
}

const deleting = ref<NotificationChannel | null>(null)
const deleteOpen = computed({
  get: () => deleting.value !== null,
  set: (value) => {
    if (!value) deleting.value = null
  }
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Channels">
      <template #description>
        Where rules and reports tell people something: an email list, a Slack channel, the one on call. Add as many of each as you need, one per destination; rules choose them by name.
      </template>
      <template #actions>
        <v-menu location="bottom end">
          <template #activator="{ props: menu }">
            <v-btn
              v-bind="menu"
              class="text-xs font-medium"
              color="primary"
              prepend-icon="material-symbols:add"
              size="small"
              variant="flat"
            >
              Add channel
            </v-btn>
          </template>
          <v-list min-width="220" nav>
            <v-list-item
              v-for="type in CHANNEL_TYPE_ORDER"
              :key="type"
              :title="CHANNEL_TYPES[type].label"
              @click="dialog = { type, channel: null }"
            >
              <template #prepend>
                <ChannelMark :type="type" class="mr-3" size="small" />
              </template>
            </v-list-item>
          </v-list>
        </v-menu>
      </template>
    </PageHeader>

    <p v-if="problem" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ problem }}
    </p>

    <LoadState
      :error="error"
      :loading="loading"
      what="the channels"
      @retry="reload"
    >
      <p v-if="counts.failing" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius-large) bg-error/6 px-4 py-3 text-xs leading-5 text-ink/80 ring-1 ring-error/20 ring-inset">
        <Icon :aria-hidden="true" class="mt-0.5 flex-none text-base text-error" name="material-symbols:error-outline" />
        <span>{{ counts.failing === 1 ? 'One channel failed' : `${counts.failing} channels failed` }} its last test. Rules and reports that notify it keep trying and say so in the activity log. Fix it below and send a test again.</span>
      </p>

      <!-- A section a type. -->
      <section
        v-for="{ type, channels: list } in byType"
        :key="type"
        :aria-label="CHANNEL_TYPES[type].label"
        class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border border-line bg-surface"
      >
        <header class="flex items-center gap-3 px-4 py-3">
          <ChannelMark :type="type" />
          <span class="flex min-w-0 flex-1 flex-col">
            <span class="text-sm font-medium text-ink">{{ CHANNEL_TYPES[type].label }}</span>
            <span class="truncate text-[11px] text-ink/55">{{ CHANNEL_TYPES[type].note }}</span>
          </span>
          <v-btn
            class="flex-none text-xs"
            color="neutral"
            prepend-icon="material-symbols:add"
            size="small"
            variant="text"
            @click="dialog = { type, channel: null }"
          >
            Add new
          </v-btn>
        </header>

        <ul v-if="list.length" class="m-0 flex list-none flex-col border-t border-line p-0">
          <li v-for="channel in list" :key="channel.id" class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line/70 px-4 py-3 first:border-t-0">
            <span class="flex min-w-0 flex-1 basis-60 flex-col gap-0.5">
              <span :class="channel.enabled ? 'text-ink' : 'text-ink/45'" class="truncate text-[13px]">{{ channel.name }}</span>
              <span class="truncate font-mono text-[11px] text-ink/45">{{ targetWords(channel) }}</span>
            </span>
            <span class="flex w-44 flex-none flex-col items-start gap-0.5">
              <StatusBadge :label="CHANNEL_STATUS[statusOf(channel)].label" :tone="CHANNEL_STATUS[statusOf(channel)].tone" />
              <span class="text-[11px] text-ink/45">{{ channel.last_test ? `Tested ${relativeWords(Date.parse(channel.last_test.at), time)}` : 'Never tested' }}</span>
            </span>
            <span class="w-40 flex-none text-[11px] text-ink/55">
              <template v-if="usedBy.get(channel.id)?.length">
                Used by <span class="text-ink/80">{{ usedBy.get(channel.id)!.length === 1 ? usedBy.get(channel.id)![0] : `${usedBy.get(channel.id)!.length} rules and reports` }}</span>
              </template>
              <template v-else>Not used yet</template>
            </span>
            <span class="flex flex-none items-center gap-1">
              <v-btn
                :disabled="!channel.enabled"
                :loading="testing === channel.id"
                class="text-xs"
                color="neutral"
                prepend-icon="material-symbols:send-outline"
                size="small"
                variant="text"
                @click="test(channel)"
              >
                Test
              </v-btn>
              <v-switch
                :aria-label="`${channel.name} on`"
                :model-value="channel.enabled"
                color="primary"
                density="compact"
                hide-details
                inset
                @update:model-value="attempt(() => setEnabled(channel, Boolean($event)))"
              />
              <v-menu location="bottom end">
                <template #activator="{ props: menu }">
                  <v-btn
                    v-bind="menu"
                    :aria-label="`${channel.name} options`"
                    color="neutral"
                    icon="material-symbols:more-horiz"
                    size="small"
                    variant="text"
                  />
                </template>
                <v-list min-width="180" nav>
                  <v-list-item prepend-icon="material-symbols:edit-outline" title="Edit" @click="dialog = { type, channel }" />
                  <v-list-item
                    base-color="error"
                    prepend-icon="material-symbols:delete-outline"
                    title="Delete"
                    @click="deleting = channel"
                  />
                </v-list>
              </v-menu>
            </span>
            <p v-if="statusOf(channel) === 'failing'" class="m-0 basis-full text-[11px] leading-4 text-error">
              {{ channel.last_test!.detail }}
            </p>
          </li>
        </ul>
        <p v-else class="m-0 border-t border-line px-4 py-3 text-xs text-ink/45">
          No {{ CHANNEL_TYPES[type].label }} channels.
        </p>
      </section>
    </LoadState>

    <ChannelDialog
      v-if="dialog"
      v-model="dialogOpen"
      :channel="dialog.channel"
      :type="dialog.type"
      @saved="reload"
    />

    <ConfirmDialog
      v-if="deleting"
      v-model="deleteOpen"
      :description="usedBy.get(deleting.id)?.length ? `${usedBy.get(deleting.id)!.join(', ')} ${usedBy.get(deleting.id)!.length === 1 ? 'notifies' : 'notify'} it, and will skip it from now on. Its sent notifications stay in the log.` : 'Nothing notifies it. Its sent notifications stay in the log.'"
      :title="`Delete ${deleting.name}?`"
      action="Delete channel"
      tone="error"
      @confirm="attempt(() => remove(deleting!))"
    />
  </div>
</template>
