<script setup lang="ts">
// What was sent to the project's channels lately, newest first: by a rule, a report, or a test.
// Every send is here whether it arrived or not, with what the destination answered, so "why
// did nobody hear about it" has an answer on the page rather than in a support ticket. Kept
// ninety days. The owners' emails about the account are not channels, and are not listed.
const { deliveries, loading, error, reload, channelById } = useChannels()

const view = useQueryChoice<'all' | 'failed' | 'tests'>('show', ['all', 'failed', 'tests'], 'all')
const VIEWS: SegmentedOption[] = [
  { value: 'all', label: 'All' },
  { value: 'failed', label: 'Failed' },
  { value: 'tests', label: 'Tests' }
]

const shown = computed(() => deliveries.value.filter(entry => view.value === 'all'
  || (view.value === 'failed' ? !entry.ok : entry.test)))

const failedCount = computed(() => deliveries.value.filter(entry => !entry.ok).length)

const stamp = (at: number) => new Date(at).toLocaleString('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Activity">
      <template #description>
        Everything sent to this project's channels lately: by a rule, a report's schedule, or a test. Kept ninety days.
      </template>
      <template #actions>
        <v-btn
          class="text-xs"
          color="neutral"
          prepend-icon="material-symbols:refresh"
          size="small"
          variant="outlined"
          @click="reload"
        >
          Refresh
        </v-btn>
      </template>
    </PageHeader>

    <LoadState
      :error="error"
      :loading="loading"
      what="the activity"
      @retry="reload"
    >
      <SectionCard :padded="false" title="Sent notifications">
        <template #badge>
          <StatusBadge v-if="failedCount" :label="`${failedCount} failed`" tone="error" />
        </template>
        <template #actions>
          <SegmentedControl v-model="view" :options="VIEWS" label="Which notifications" />
        </template>

        <p v-if="!shown.length" class="m-0 px-5 py-4 text-xs text-ink/50">
          <template v-if="view === 'failed'">
            Nothing failed lately.
          </template>
          <template v-else-if="view === 'tests'">
            No tests lately.
          </template>
          <template v-else>
            Nothing sent yet. A rule's test, or a channel's, is the first.
          </template>
        </p>
        <ul v-else class="m-0 flex list-none flex-col p-0">
          <li v-for="entry in shown" :key="entry.id" class="flex items-center gap-3 border-t border-line/70 px-5 py-2.5 first:border-t-0">
            <Icon
              :class="entry.ok ? 'text-success' : 'text-error'"
              :name="entry.ok ? 'material-symbols:check-circle-outline' : 'material-symbols:cancel-outline'"
              :aria-hidden="true"
              class="flex-none text-base"
            />
            <ChannelMark v-if="entry.channelId && channelById(entry.channelId)" :type="channelById(entry.channelId)!.type" size="small" />
            <span class="flex min-w-0 flex-1 flex-col">
              <span class="truncate text-xs text-ink">{{ entry.title }}</span>
              <span class="truncate text-[11px] text-ink/50">
                {{ entry.channelName }}<template v-if="!entry.channelId"> (deleted)</template> · {{ entry.test ? entry.source : `from ${entry.source}` }} · <span :class="entry.ok ? '' : 'text-error'">{{ entry.detail }}</span>
              </span>
            </span>
            <Tag v-if="entry.test" label="Test" />
            <span class="flex-none text-[11px] whitespace-nowrap text-ink/45">{{ stamp(entry.at) }}</span>
          </li>
        </ul>
      </SectionCard>
    </LoadState>
  </div>
</template>
