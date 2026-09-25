<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'
import type { IssueStatus, LogIssue } from '~/composables/logs'

// Cloud Logging's issues: every exception, crash and error log in the range, grouped into bugs by
// fingerprint and ranked by how often each hit and how many installs it reached. One issue opens
// on its own page. The range, the status, the sort and the search are in the URL.
const path = useLogsPath()
const { range, setPreset, setAbsolute, zoomOut, shift } = useLogRange('24h')
const span = computed(() => ({ from: new Date(range.value.from).toISOString(), to: new Date(range.value.to).toISOString() }))

type IssueFilter = 'unresolved' | 'resolved' | 'ignored' | 'all'
const issueStatus = useQueryChoice<IssueFilter>('status', ['unresolved', 'resolved', 'ignored', 'all'], 'unresolved')
const issueSort = useQueryChoice('sort', ['events', 'installs', 'last_seen', 'first_seen'], 'events')
const issueText = ref('')
const issueNeedle = refDebounced(issueText, 300)

const STATUS_OPTIONS: SegmentedOption[] = [
  { value: 'unresolved', label: 'Open' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'ignored', label: 'Ignored' },
  { value: 'all', label: 'All' }
]
const SORTS = [
  { value: 'events', title: 'Most events' },
  { value: 'installs', title: 'Most installs' },
  { value: 'last_seen', title: 'Seen most recently' },
  { value: 'first_seen', title: 'Newest first seen' }
]

const issuesRead = useApiGet<Schema<'LogIssuesOut'>>(() => (path.value
  ? withQuery(`${path.value}/issues`, { ...span.value, status: issueStatus.value, sort: issueSort.value, q: issueNeedle.value.trim() })
  : null))
const issues = computed<readonly LogIssue[]>(() => issuesRead.data.value?.issues ?? [])
const issuesError = computed(() => issuesRead.error.value as ApiError | null)

const summaryRead = useApiGet<Schema<'LogSummaryOut'>>(() => path.value && withQuery(`${path.value}/summary`, span.value))

const board = computed<Kpi[]>(() => {
  const s = summaryRead.data.value
  const current = s?.current
  const previous = s?.previous
  const figure = (value: number | undefined) => (s && value !== undefined ? writeFigure(value, { compact: true }) : null)
  const move = (value: number, before: number) => (before > 0 ? trendOf(value, before, 'down') : undefined)
  return [
    { key: 'issues', label: 'Issues', value: figure(current?.issues), trend: current && previous ? move(current.issues, previous.issues) : undefined, info: 'Distinct bugs that happened in the range: the same exception from the same place, or the same error message.' },
    { key: 'new', label: 'New issues', value: figure(current?.new_issues), info: 'Issues first seen in the range.' },
    { key: 'crashes', label: 'Crashes', value: figure(current?.crashes), trend: current && previous ? move(current.crashes, previous.crashes) : undefined, info: 'Times the game died, as reported on the next launch.' },
    { key: 'exceptions', label: 'Exceptions', value: figure(current?.exceptions), trend: current && previous ? move(current.exceptions, previous.exceptions) : undefined, info: 'Exceptions the game caught and reported.' },
    { key: 'installs', label: 'Installs affected', value: figure(current?.affected_installs), info: 'Installs any issue reached in the range, approximately.' }
  ]
})

const now = useNow({ interval: 60_000 })
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Issues">
      <template #description>
        Every exception, crash and error log, grouped into the bugs behind them.
      </template>
      <template #actions>
        <LogStorageButton />
      </template>
    </PageHeader>

    <KpiBoard
      :description="`${rangeWords(range)}, against the stretch before it.`"
      :items="board"
      :loading="summaryRead.isFetching.value && !summaryRead.data.value"
      title="In this range"
    />

    <SectionCard :padded="false" title="Issues">
      <template #actions>
        <span class="text-[11px] text-ink/50">{{ rangeWords(range) }}</span>
      </template>
      <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 pt-3 pb-3">
        <SegmentedControl v-model="issueStatus" :options="STATUS_OPTIONS" label="Issue status" />
        <SearchField
          v-model="issueText"
          class="min-w-56 flex-1"
          label="Search issues"
          placeholder="Search by exception, message or where it was thrown"
        />
        <v-select
          v-model="issueSort"
          :items="SORTS"
          class="max-w-52"
          density="comfortable"
          aria-label="Sort issues"
          hide-details
          variant="outlined"
        />
        <LogTimeRange
          :range="range"
          @absolute="setAbsolute"
          @preset="setPreset"
          @shift="shift"
          @zoom-out="zoomOut"
        />
      </div>

      <LoadState
        :error="issuesError"
        :loading="issuesRead.isFetching.value && !issuesRead.data.value"
        what="the issues"
        @retry="issuesRead.execute()"
      >
        <ul v-if="issues.length" class="m-0 flex list-none flex-col p-0">
          <li v-for="issue in issues" :key="issue.id" class="border-b border-line last:border-b-0">
            <NuxtLink
              :to="{ path: `/logs/issues/${issue.id}`, query: range.preset ? { range: range.preset } : { from: span.from, to: span.to } }"
              class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-4 py-3 no-underline transition-colors hover:bg-ink/[0.03] md:grid-cols-[minmax(0,1fr)_120px_72px_72px_110px]"
            >
              <span class="flex min-w-0 items-start gap-3">
                <span :class="issue.kind === 'crash' ? 'bg-error/12 text-error' : issue.kind === 'exception' ? 'bg-warning/12 text-warning' : 'bg-ink/6 text-ink/60'" aria-hidden="true" class="mt-0.5 flex size-7 flex-none items-center justify-center rounded-(--ps-radius-small) text-base">
                  <Icon :name="ISSUE_KIND[issue.kind]?.icon ?? 'material-symbols:error-outline'" />
                </span>
                <span class="flex min-w-0 flex-col gap-0.5">
                  <span class="flex min-w-0 items-center gap-2">
                    <span class="truncate text-[13px] font-medium text-ink">{{ issue.exception_type || ISSUE_KIND[issue.kind]?.label }}</span>
                    <StatusBadge v-if="issue.status !== 'unresolved'" :label="ISSUE_STATUS[issue.status as IssueStatus]?.label ?? issue.status" :tone="ISSUE_STATUS[issue.status as IssueStatus]?.tone" />
                    <span v-if="Date.parse(issue.first_seen) >= range.from" class="rounded-sm bg-primary/10 px-1 text-[10px] leading-4 font-medium text-primary">NEW</span>
                  </span>
                  <span class="truncate font-mono text-xs text-ink/70">{{ issue.title }}</span>
                  <span class="truncate text-[11px] text-ink/45">
                    <template v-if="issue.culprit">in <span class="font-mono">{{ issue.culprit }}</span> · </template>
                    first seen {{ relativeWords(Date.parse(issue.first_seen), now.getTime()) }}<template v-if="issue.first_release"> in {{ issue.first_release }}</template>
                    · last {{ relativeWords(Date.parse(issue.last_seen), now.getTime()) }}
                  </span>
                </span>
              </span>
              <Sparkline :height="28" :values="issue.trend" class="hidden h-7 w-[120px] md:block" />
              <span class="hidden flex-col items-end md:flex">
                <span class="font-mono text-[13px] text-ink">{{ writeFigure(issue.events, { compact: true }) }}</span>
                <span class="text-[10px] text-ink/45">events</span>
              </span>
              <span class="hidden flex-col items-end md:flex">
                <span class="font-mono text-[13px] text-ink">{{ writeFigure(issue.installs, { compact: true }) }}</span>
                <span class="text-[10px] text-ink/45">installs</span>
              </span>
              <span class="flex flex-col items-end">
                <span class="font-mono text-[11px] text-ink/60">{{ issue.last_release || '–' }}</span>
                <span class="text-[10px] text-ink/45">latest release</span>
              </span>
            </NuxtLink>
          </li>
        </ul>
        <EmptyState
          v-else
          :description="issueStatus === 'unresolved' ? 'No exception, crash or error log in this range. Resolved and ignored issues are under their own filters.' : 'No issue in this range has that status.'"
          icon="material-symbols:check-circle-outline"
          :title="issueStatus === 'unresolved' ? 'No open issues' : 'Nothing here'"
        />
        <p v-if="issuesRead.data.value?.truncated" class="m-0 border-t border-line px-4 py-2 text-[11px] text-ink/50">
          The first 200 issues. Narrow the range or search to see the rest.
        </p>
      </LoadState>
    </SectionCard>
  </div>
</template>
