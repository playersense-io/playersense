<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'
import type { IssueStatus } from '~/composables/logs'

// One issue: every record of one bug. What it is and where it was thrown, how often it happened
// across the range and to how many installs, which releases, platforms, devices and countries it
// hits, and its newest record whole. Resolving says "fixed": the issue shows as regressed if it is
// seen again afterwards, which is the moment somebody needs to know.
const route = useRoute()
const path = useLogsPath()
const id = computed(() => String(route.params.id))

const { range, setPreset, setAbsolute, zoomOut, shift } = useLogRange('7d')
const span = computed(() => ({ from: new Date(range.value.from).toISOString(), to: new Date(range.value.to).toISOString() }))

const read = useApiGet<Schema<'LogIssueDetailOut'>>(() => path.value && withQuery(`${path.value}/issues/${id.value}`, span.value))
const detail = computed(() => read.data.value)
const issue = computed(() => detail.value?.issue)
const readError = computed(() => read.error.value as ApiError | null)

usePageCrumb(() => issue.value?.exception_type || issue.value?.title || 'Issue')

const now = useNow({ interval: 60_000 })

const board = computed<Kpi[]>(() => {
  const i = issue.value
  return [
    { key: 'events', label: 'Events', value: i ? writeFigure(i.events, { compact: true }) : null, info: `Records of this issue in the range. ${i ? writeFigure(i.total_events) : '–'} across everything kept.` },
    { key: 'installs', label: 'Installs', value: i ? writeFigure(i.installs, { compact: true }) : null, info: `Installs it reached in the range, approximately. ${i ? writeFigure(i.total_installs) : '–'} across everything kept.` },
    { key: 'first', label: 'First seen', value: i ? relativeWords(Date.parse(i.first_seen), now.value.getTime()) : null, info: i?.first_release ? `In release ${i.first_release}.` : 'The oldest record still kept.' },
    { key: 'last', label: 'Last seen', value: i ? relativeWords(Date.parse(i.last_seen), now.value.getTime()) : null, info: i?.last_release ? `In release ${i.last_release}.` : '' }
  ]
})

const tick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', hour: '2-digit', hour12: false })
const categories = computed(() => (detail.value?.series ?? []).map(point => tick.format(new Date(point.at))))
const series = computed<MetricSeries[]>(() => [{ name: 'Events', data: (detail.value?.series ?? []).map(point => point.value), tone: issue.value?.kind === 'crash' ? 2 : 1 }])

const BREAKDOWNS = [
  { key: 'release', label: 'Release' },
  { key: 'platform', label: 'Platform' },
  { key: 'os', label: 'OS version' },
  { key: 'device', label: 'Device' },
  { key: 'country', label: 'Country' }
]

const saving = ref(false)
const failure = ref('')

async function setStatus(status: 'unresolved' | 'resolved' | 'ignored') {
  if (!path.value) return
  saving.value = true
  failure.value = ''
  try {
    await setLogIssueStatus(path.value, id.value, status)
    await read.execute()
  } catch (caught) {
    failure.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

const status = computed(() => (issue.value?.status ?? 'unresolved') as IssueStatus)

const explorerLink = computed(() => ({ path: '/logs', query: { q: `issue:${id.value}`, ...(range.value.preset ? { range: range.value.preset } : { from: span.value.from, to: span.value.to }) } }))
</script>

<template>
  <div class="flex flex-col gap-6">
    <LoadState
      :error="readError"
      :loading="read.isFetching.value && !detail"
      what="the issue"
      @retry="read.execute()"
    >
      <template v-if="issue">
        <header class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div class="flex min-w-0 items-start gap-3.5">
            <span :class="issue.kind === 'crash' ? 'bg-error/12 text-error' : issue.kind === 'exception' ? 'bg-warning/12 text-warning' : 'bg-ink/6 text-ink/60'" aria-hidden="true" class="flex size-10 flex-none items-center justify-center rounded-(--ps-radius) text-xl">
              <Icon :name="ISSUE_KIND[issue.kind]?.icon ?? 'material-symbols:error-outline'" />
            </span>
            <div class="flex min-w-0 flex-col gap-1">
              <div class="flex flex-wrap items-center gap-2">
                <h1 class="m-0 text-lg font-medium text-ink">
                  {{ issue.exception_type || ISSUE_KIND[issue.kind]?.label }}
                </h1>
                <StatusBadge :label="ISSUE_STATUS[status].label" :tone="ISSUE_STATUS[status].tone" />
                <Tag :label="ISSUE_KIND[issue.kind]?.label ?? issue.kind" :tone="issue.kind === 'crash' ? 'error' : 'neutral'" />
              </div>
              <p class="m-0 font-mono text-sm break-words text-ink/75">
                {{ issue.title }}
              </p>
              <p v-if="issue.culprit" class="m-0 text-xs text-ink/50">
                in <span class="font-mono text-ink/70">{{ issue.culprit }}</span>
              </p>
              <p v-if="status === 'regressed' && issue.resolved_at" class="m-0 text-xs text-error">
                Resolved {{ relativeWords(Date.parse(issue.resolved_at), now.getTime()) }}{{ issue.updated_by ? ` by ${issue.updated_by}` : '' }}, and seen again since.
              </p>
            </div>
          </div>
          <div class="flex flex-none flex-wrap items-center gap-2">
            <span v-if="failure" class="text-xs text-error">{{ failure }}</span>
            <v-btn
              v-if="status !== 'resolved'"
              :loading="saving"
              class="text-xs font-medium"
              color="primary"
              prepend-icon="material-symbols:check"
              size="small"
              variant="flat"
              @click="setStatus('resolved')"
            >
              Resolve
            </v-btn>
            <v-btn
              v-if="status !== 'ignored'"
              :disabled="saving"
              class="text-xs font-medium"
              prepend-icon="material-symbols:visibility-off-outline"
              size="small"
              variant="outlined"
              @click="setStatus('ignored')"
            >
              Ignore
            </v-btn>
            <v-btn
              v-if="status === 'resolved' || status === 'ignored'"
              :disabled="saving"
              class="text-xs font-medium"
              prepend-icon="material-symbols:undo"
              size="small"
              variant="outlined"
              @click="setStatus('unresolved')"
            >
              Reopen
            </v-btn>
          </div>
        </header>

        <div class="flex flex-wrap items-center justify-between gap-2">
          <LogTimeRange
            :range="range"
            @absolute="setAbsolute"
            @preset="setPreset"
            @shift="shift"
            @zoom-out="zoomOut"
          />
          <v-btn
            :to="explorerLink"
            append-icon="material-symbols:arrow-forward"
            class="text-xs font-medium"
            size="small"
            variant="text"
          >
            Every record of this issue
          </v-btn>
        </div>

        <KpiBoard :description="rangeWords(range)" :items="board" title="This issue" />

        <MetricCard
          :categories="categories"
          :decimals="0"
          :height="200"
          :series="series"
          category-label="When"
          description="Records of this issue in each bar of the range."
          title="Events over time"
          type="bar"
        />

        <SectionCard description="The five commonest values of each, over the range." title="Where it happens">
          <div class="grid gap-6 sm:grid-cols-2 xl:grid-cols-3">
            <div v-for="entry in BREAKDOWNS" :key="entry.key" class="flex flex-col gap-2">
              <h3 class="m-0 text-xs font-medium text-ink/60">
                {{ entry.label }}
              </h3>
              <BreakdownList :label="`Events by ${entry.label.toLowerCase()}`" :rows="(detail?.breakdown[entry.key] ?? []).map(share => ({ value: share.value || 'Unknown', count: share.count }))" />
            </div>
          </div>
        </SectionCard>

        <SectionCard v-if="detail?.latest" :padded="false" title="Latest event">
          <LogRecordPanel :issue-link="false" :record="detail.latest" @filter="(clauseText) => navigateTo({ path: '/logs', query: { q: `issue:${id} ${clauseText}` } })" />
        </SectionCard>
      </template>
    </LoadState>
  </div>
</template>
