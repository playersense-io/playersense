<script setup lang="ts">
// Where the draft becomes what players get, laid out as the one question people bring here:
// which of my changes has reached whom?
//
// There is one draft for the project and a version history per environment. Publishing
// compiles the draft (every flag, each rule with its segments written in) into an
// environment's next immutable version, and devices pick it up on their next refresh. The page
// answers in that order: a sentence saying where things stand, the three environments as steps
// with the one action each needs, every change not yet everywhere with a column per
// environment, and the history of what was published and edited. What the draft compiles to
// in detail is folded away at the foot, for whoever needs it.
//
// Rolling back is one step: an earlier version of an environment published again as its next
// one, with a reason, leaving the draft alone so what was pulled back is still there to fix.
// Restoring an old version into the draft is the other way back, for when the draft itself
// should start again from it.
//
// Sample data (app/utils/flags-sample.ts). The API has no route yet to list an environment's
// versions or read an old document back; this is what that route will return.
const { flags, segments, versions, audit, latest, draftCheck, restoreDraft } = useLiveConfig()

const check = computed(() => draftCheck())

const now = useNow({ interval: 60_000 })

// --- Where things stand -------------------------------------------------------------------

const behind = computed(() => ENVIRONMENTS.map(environment => ({
  environment,
  version: latest(environment),
  waiting: draftChanges(latest(environment), flags.value, segments.value).length
})))

const list = (names: string[]) => (names.length > 1 ? `${names.slice(0, -1).join(', ')} and ${names.at(-1)}` : names[0] ?? '')

/* The answer in a sentence, the one a founder would give if asked "is it live?". */
const status = computed(() => {
  if (!check.value.compiles) {
    return { tone: 'error' as const, icon: 'material-symbols:error-outline', title: 'Your draft has a problem', detail: `${check.value.problem}. Nothing can be published until it is fixed.` }
  }
  const late = behind.value.filter(entry => entry.waiting || !entry.version)
  if (!late.length) {
    return { tone: 'success' as const, icon: 'material-symbols:check-circle-outline', title: 'Everything is live', detail: 'Every environment serves your latest changes.' }
  }
  const production = behind.value.find(entry => entry.environment === 'production')!
  const names = late.map(entry => ENVIRONMENT_LABELS[entry.environment])
  // Straight after a rollback, that is the news: players are on an older version on purpose.
  if (production.version?.rollback_of && production.waiting) {
    return {
      tone: 'warning' as const,
      icon: 'material-symbols:undo',
      title: `Production was rolled back to v${production.version.rollback_of}`,
      detail: `Players are on v${production.version.rollback_of} again. The ${production.waiting} ${production.waiting === 1 ? 'change' : 'changes'} rolled back ${production.waiting === 1 ? 'is' : 'are'} still in your draft: fix what went wrong, then publish again.`
    }
  }
  if (production.waiting) {
    return {
      tone: 'warning' as const,
      icon: 'material-symbols:schedule-outline',
      title: `${production.waiting} ${production.waiting === 1 ? 'change is' : 'changes are'} not live for players yet`,
      detail: `${list(names)} ${names.length === 1 ? 'is' : 'are'} behind your draft. Publish to test it first, then to Production to release it.`
    }
  }
  return {
    tone: 'neutral' as const,
    icon: 'material-symbols:info-outline',
    title: 'Players have your latest changes',
    detail: `Production is up to date. ${list(names)} ${names.length === 1 ? 'is' : 'are'} behind, which only your team and testers see.`
  }
})

const STATUS_LOOK = {
  error: 'bg-error/12 text-error',
  success: 'bg-success/12 text-success',
  warning: 'bg-warning/12 text-warning',
  neutral: 'bg-ink/6 text-ink/60'
} as const

// --- Publishing ---------------------------------------------------------------------------

const publishing = ref(false)
const publishTo = ref<EnvironmentKind>('development')

function openPublish(environment: EnvironmentKind) {
  publishTo.value = environment
  publishing.value = true
}

/* The header's Publish goes to the first environment behind, in the order a change travels. */
const firstBehind = computed(() => behind.value.find(entry => entry.waiting || !entry.version)?.environment ?? null)

// --- Changes not everywhere yet -----------------------------------------------------------

interface ChangeRow extends DraftChange {
  /** Environments that do not serve this change yet. */
  waiting: EnvironmentKind[]
}

/* Every flag or segment the draft has changed that some environment does not serve yet, once,
   with a column per environment rather than a switch between them. */
const changeRows = computed<ChangeRow[]>(() => {
  const rows = new Map<string, ChangeRow>()
  for (const environment of [...ENVIRONMENTS].reverse()) {
    for (const change of draftChanges(latest(environment), flags.value, segments.value)) {
      const id = `${change.kind}:${change.key}`
      const row = rows.get(id)
      if (row) row.waiting.push(environment)
      else rows.set(id, { ...change, waiting: [environment] })
    }
  }
  return [...rows.values()]
})

const CHANGE_LOOK: Record<DraftChange['change'], { icon: string, tone: string, word: string }> = {
  added: { icon: 'material-symbols:add-circle-outline', tone: 'text-success', word: 'New' },
  changed: { icon: 'material-symbols:edit-outline', tone: 'text-warning', word: 'Edited' },
  removed: { icon: 'material-symbols:remove-circle-outline', tone: 'text-error', word: 'Removed' }
}

const flagName = (key: string) => flags.value.find(flag => flag.key === key)?.name ?? key
const segmentLabel = (key: string) => segments.value.find(segment => segment.key === key)?.name ?? key

// --- History ------------------------------------------------------------------------------

const HISTORY_VIEWS: SegmentedOption[] = [
  { value: 'publishes', label: 'Publishes' },
  { value: 'edits', label: 'Edits' }
]
const historyView = useQueryChoice('history', ['publishes', 'edits'], 'publishes')

const ENVIRONMENT_FILTERS: SegmentedOption[] = [
  { value: 'all', label: 'All' },
  ...ENVIRONMENTS.map(environment => ({ value: environment, label: ENVIRONMENT_LABELS[environment] }))
]
const environmentFilter = useQueryChoice('environment', ['all', ...ENVIRONMENTS], 'all')

/* Every publish to every environment, newest first, each marked if it is what that environment serves now. */
const publishes = computed(() => [...versions.value]
  .filter(version => environmentFilter.value === 'all' || version.environment === environmentFilter.value)
  .sort((a, b) => Date.parse(b.published_at) - Date.parse(a.published_at))
  .map(version => ({ ...version, current: latest(version.environment)?.version === version.version })))

const VERSION_COLUMNS: RecordColumn[] = [
  { key: 'where', label: 'Where', width: '200px' },
  { key: 'note', label: 'What it was for', width: 'minmax(0, 2fr)' },
  { key: 'by', label: 'Published', width: 'minmax(0, 1.2fr)' },
  { key: 'actions', label: '', width: '48px', align: 'end' }
]

const ENVIRONMENT_TONE: Record<EnvironmentKind, string> = {
  development: 'chart-3',
  staging: 'chart-4',
  production: 'chart-1'
}

const edits = computed(() => audit.value.filter(entry => !['liveops.publish', 'liveops.rollback', 'draft.restore'].includes(entry.action)).slice(0, 40))

/* A version to put its environment back on: the one-step undo for something that went out wrong. */
const rollingBack = shallowRef<ConfigVersionDef | null>(null)
const rollbackOpen = computed({
  get: () => rollingBack.value !== null,
  set: (value) => {
    if (!value) rollingBack.value = null
  }
})

const viewing = shallowRef<ConfigVersionDef | null>(null)
const viewOpen = computed({
  get: () => viewing.value !== null,
  set: (value) => {
    if (!value) viewing.value = null
  }
})
const documentText = computed(() => (viewing.value ? JSON.stringify(compileDocument(viewing.value.version, viewing.value.flags, viewing.value.segments), null, 2) : ''))

/* The draft's technical side, folded away until somebody asks for it. */
const detailsOpen = ref(false)

const restoring = shallowRef<ConfigVersionDef | null>(null)
const restoreOpen = computed({
  get: () => restoring.value !== null,
  set: (value) => {
    if (!value) restoring.value = null
  }
})

const restoreWords = computed(() => {
  const version = restoring.value
  if (!version) return ''
  const differences = draftChanges(version, flags.value, segments.value).length
  return `Sets your draft back to what ${ENVIRONMENT_LABELS[version.environment]} served in v${version.version}: ${differences} ${differences === 1 ? 'difference' : 'differences'} undone, flags added since then archived. Nothing reaches players until you publish again.`
})
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader description="Your changes are saved to a draft. Publishing sends the draft to an environment, and its devices pick it up within a minute." title="Publishing">
      <template #actions>
        <v-btn
          :disabled="!check.compiles || !firstBehind"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:publish"
          size="small"
          variant="flat"
          @click="firstBehind && openPublish(firstBehind)"
        >
          Publish
        </v-btn>
      </template>
    </PageHeader>

    <!-- Where things stand, in a sentence, then the three places a change goes. -->
    <section class="flex flex-col gap-4">
      <div class="flex items-start gap-4">
        <span :class="STATUS_LOOK[status.tone]" aria-hidden="true" class="flex size-11 flex-none items-center justify-center rounded-(--ps-radius-large)">
          <Icon :name="status.icon" class="text-2xl" />
        </span>
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-lg leading-tight font-semibold tracking-tight text-ink">
            {{ status.title }}
          </h2>
          <p class="m-0 text-sm leading-6 text-ink/65">
            {{ status.detail }}
          </p>
        </div>
      </div>

      <EnvironmentPipeline @publish="openPublish" />
    </section>

    <!-- Every change not everywhere yet: a row a change, a column a place. -->
    <SectionCard :padded="false" description="What you have changed that some environment does not have yet." title="Your changes">
      <EmptyState
        v-if="!changeRows.length"
        description="Every environment already has everything in your draft."
        icon="material-symbols:done-all"
        title="Nothing to publish"
      />
      <div v-else class="overflow-x-auto">
        <table class="w-full min-w-[640px] border-collapse text-left">
          <thead>
            <tr class="border-b border-line text-[11px] text-ink/45">
              <th class="px-5 py-2.5 font-normal">
                Change
              </th>
              <th v-for="environment in ENVIRONMENTS" :key="environment" class="w-32 px-3 py-2.5 text-center font-normal">
                {{ ENVIRONMENT_LABELS[environment] }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in changeRows" :key="`${row.kind}:${row.key}`" class="border-b border-line/70 last:border-b-0">
              <td class="px-5 py-3">
                <div class="flex gap-3">
                  <Icon
                    :class="CHANGE_LOOK[row.change].tone"
                    :name="CHANGE_LOOK[row.change].icon"
                    aria-hidden="true"
                    class="mt-0.5 flex-none text-base"
                  />
                  <div class="flex min-w-0 flex-col gap-0.5">
                    <span class="flex flex-wrap items-baseline gap-x-2">
                      <NuxtLink
                        v-if="row.kind === 'flag' && row.change !== 'removed'"
                        :to="`/growth/flags/${encodeURIComponent(row.key)}`"
                        class="text-[13px] text-ink no-underline hover:text-primary"
                      >
                        {{ flagName(row.key) }}
                      </NuxtLink>
                      <span v-else class="text-[13px] text-ink">{{ row.kind === 'flag' ? flagName(row.key) : segmentLabel(row.key) }}</span>
                      <span class="text-[11px] text-ink/45">{{ CHANGE_LOOK[row.change].word }} {{ row.kind }}</span>
                    </span>
                    <span v-for="line in row.lines" :key="line" class="text-[11px] leading-4 text-ink/55">{{ line }}</span>
                  </div>
                </div>
              </td>
              <td v-for="environment in ENVIRONMENTS" :key="environment" class="px-3 py-3 text-center">
                <span v-if="row.waiting.includes(environment)" class="inline-flex items-center gap-1 rounded-full bg-warning/12 px-2 py-0.5 text-[11px] text-warning">
                  <Icon aria-hidden="true" class="text-xs" name="material-symbols:schedule-outline" />
                  Waiting
                </span>
                <span v-else class="inline-flex items-center gap-1 text-[11px] text-success">
                  <Icon aria-hidden="true" class="text-sm" name="material-symbols:check-circle-outline" />
                  Live
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </SectionCard>

    <!-- What was published and edited, everywhere, newest first. -->
    <SectionCard :padded="false" description="Every publish to every environment, and every edit to the draft. Something went out wrong? Roll the environment back to an earlier version from its menu." title="History">
      <template #actions>
        <SegmentedControl v-model="historyView" :options="HISTORY_VIEWS" label="Show" />
      </template>

      <template v-if="historyView === 'publishes'">
        <div class="border-b border-line px-5 py-2.5">
          <SegmentedControl v-model="environmentFilter" :options="ENVIRONMENT_FILTERS" label="Environment" />
        </div>
        <div class="px-5 py-2">
          <RecordTable :columns="VERSION_COLUMNS" label="Publishes">
            <li v-for="version in publishes" :key="`${version.environment}:${version.version}`" class="record-row">
              <span class="record-cell record-cell--primary flex items-center gap-2" data-label="Where">
                <Tag :label="ENVIRONMENT_LABELS[version.environment]" :tone="ENVIRONMENT_TONE[version.environment]" />
                <span class="font-mono text-[11px] text-ink/55">v{{ version.version }}</span>
                <StatusBadge v-if="version.current" label="Live" tone="success" />
                <v-tooltip v-if="version.rollback_of" :text="`A rollback: v${version.rollback_of}, published again`" location="top">
                  <template #activator="{ props: tip }">
                    <Icon
                      v-bind="tip"
                      aria-label="Rollback"
                      class="text-sm text-warning"
                      name="material-symbols:undo"
                    />
                  </template>
                </v-tooltip>
              </span>
              <span class="record-cell text-xs text-ink/80" data-label="What it was for">{{ version.note || 'No note' }}</span>
              <span class="record-cell flex flex-col" data-label="Published">
                <span class="text-xs text-ink/80">{{ sinceWords(version.published_at, now) }}</span>
                <span class="truncate text-[11px] text-ink/45">{{ version.published_by }}</span>
              </span>
              <span class="record-cell record-cell--end record-cell--action">
                <v-menu location="bottom end">
                  <template #activator="{ props: activator }">
                    <v-btn
                      v-bind="activator"
                      :aria-label="`${ENVIRONMENT_LABELS[version.environment]} v${version.version} actions`"
                      color="neutral"
                      icon="material-symbols:more-horiz"
                      size="small"
                      variant="text"
                    />
                  </template>
                  <v-list min-width="220" nav>
                    <v-list-item
                      v-if="!version.current"
                      base-color="warning"
                      prepend-icon="material-symbols:undo"
                      :title="`Roll back ${ENVIRONMENT_LABELS[version.environment]} to v${version.version}`"
                      @click="rollingBack = version"
                    />
                    <v-list-item prepend-icon="material-symbols:history" title="Restore into the draft" @click="restoring = version" />
                    <v-list-item prepend-icon="material-symbols:data-object" title="View what it contained" @click="viewing = version" />
                  </v-list>
                </v-menu>
              </span>
            </li>
            <template #empty>
              <p v-if="!publishes.length" class="m-0 px-3 py-8 text-center text-xs text-ink/55">
                Nothing has been published here yet.
              </p>
            </template>
          </RecordTable>
        </div>
      </template>
      <div v-else class="p-5">
        <ConfigHistory :entries="edits" show-target />
      </div>
    </SectionCard>

    <!-- The technical side of the draft, for whoever needs it. -->
    <LayerCard
      v-model:open="detailsOpen"
      description="What a publish would carry, checked the way the server checks it."
      expandable
      title="Draft details"
    >
      <div class="flex flex-col gap-4 p-3">
        <dl class="m-0 grid grid-cols-2 gap-px overflow-hidden rounded-(--ps-radius) border border-line bg-line sm:grid-cols-3">
          <div class="flex flex-col gap-1 bg-surface p-3">
            <dt class="text-[11px] text-ink/50">
              Compiles
            </dt>
            <dd :class="check.compiles ? 'text-success' : 'text-error'" class="m-0 text-sm">
              {{ check.compiles ? 'Yes' : 'No' }}
            </dd>
          </div>
          <div class="flex flex-col gap-1 bg-surface p-3">
            <dt class="text-[11px] text-ink/50">
              Flags
            </dt>
            <dd class="m-0 font-mono text-sm text-ink">
              {{ check.flags }}
            </dd>
          </div>
          <div class="flex flex-col gap-1 bg-surface p-3">
            <dt class="text-[11px] text-ink/50">
              Segments
            </dt>
            <dd class="m-0 font-mono text-sm text-ink">
              {{ segments.filter(segment => !segment.archived).length }}
            </dd>
          </div>
        </dl>
        <p class="m-0 flex items-start gap-2 text-xs leading-5 text-ink/65">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-ink/45" name="material-symbols:history" />
          <span v-if="check.needsPlayerFacts">
            Some rules read players' purchases or activity, so serving it costs a history lookup per request,
            cached for an hour per player.
          </span>
          <span v-else>No rule reads players' history, so a request is answered from the document alone.</span>
        </p>
        <div v-if="check.excluded.length" class="flex flex-col gap-1.5">
          <span class="text-[11px] text-ink/50">Left out on purpose</span>
          <ul class="m-0 flex list-none flex-col gap-1 p-0">
            <li v-for="entry in check.excluded" :key="`${entry.kind}:${entry.key}`" class="flex items-center justify-between gap-2 text-xs">
              <span class="flex min-w-0 items-center gap-2">
                <Icon :name="entry.kind === 'flag' ? 'material-symbols:flag-outline' : 'material-symbols:group-outline'" aria-hidden="true" class="flex-none text-sm text-ink/40" />
                <span class="truncate font-mono text-ink/80">{{ entry.key }}</span>
              </span>
              <span class="flex-none text-[11px] text-ink/45">{{ entry.reason }}</span>
            </li>
          </ul>
        </div>
      </div>
    </LayerCard>

    <PublishDialog v-if="publishing" v-model="publishing" :environment="publishTo" />

    <RollbackDialog v-if="rollbackOpen && rollingBack" v-model="rollbackOpen" :version="rollingBack" />

    <v-dialog
      v-if="viewOpen"
      v-model="viewOpen"
      max-width="760"
      scrollable
    >
      <v-card border flat>
        <header class="flex items-start justify-between gap-3 border-b border-line p-5">
          <div class="flex flex-col gap-1">
            <h2 class="m-0 text-sm font-medium text-ink">
              {{ viewing ? `${ENVIRONMENT_LABELS[viewing.environment]} v${viewing.version}` : '' }}
            </h2>
            <p class="m-0 text-xs leading-5 text-ink/60">
              The compiled document the server evaluates: segments written into each rule. Devices never receive it,
              only the values it decides for them.
            </p>
          </div>
        </header>
        <div class="overflow-auto p-5">
          <CodeBlock :code="documentText" :title="viewing ? `${viewing.environment}-v${viewing.version}.json` : ''" />
        </div>
        <footer class="flex justify-end border-t border-line px-5 py-3">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="viewing = null"
          >
            Close
          </v-btn>
        </footer>
      </v-card>
    </v-dialog>

    <ConfirmDialog
      v-if="restoreOpen"
      v-model="restoreOpen"
      :description="restoreWords"
      :title="restoring ? `Restore ${ENVIRONMENT_LABELS[restoring.environment]} v${restoring.version} into your draft?` : ''"
      action="Restore into the draft"
      tone="primary"
      @confirm="restoring && restoreDraft(restoring)"
    />
  </div>
</template>
