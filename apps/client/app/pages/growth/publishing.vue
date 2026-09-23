<script setup lang="ts">
// Where the draft becomes what players get. There is one draft for the project and a version
// history per environment; publishing compiles the draft — every flag, each rule with its
// segments written in — into the environment's next immutable version, and devices pick it up
// on their next refresh.
//
// So this page answers four questions in order: does the draft compile, what would publishing
// it change in each environment, what has each environment served before, and who did what.
// Rolling back is restoring an old version into the draft and publishing it, which is a set of
// ordinary writes rather than a mechanism of its own — and leaves a record like any other.
//
// Sample data (app/utils/flags-sample.ts). The API has no route yet to list an environment's
// versions or read an old document back; this is what that route will return.
const { project } = useWorkspace()
const { flags, segments, audit, latest, historyOf, draftCheck, restoreDraft } = useLiveConfig()

const check = computed(() => draftCheck())

// --- Publishing ---------------------------------------------------------------------------

const publishing = ref(false)
const publishTo = ref<EnvironmentKind>('production')

function openPublish(environment: EnvironmentKind) {
  publishTo.value = environment
  publishing.value = true
}

// --- What would change --------------------------------------------------------------------

const ENVIRONMENT_OPTIONS: SegmentedOption[] = ENVIRONMENTS.map(environment => ({ value: environment, label: ENVIRONMENT_LABELS[environment] }))

const compareWith = useQueryChoice<EnvironmentKind>('compare', ENVIRONMENTS, 'production')
const changes = computed(() => draftChanges(latest(compareWith.value), flags.value, segments.value))

const CHANGE_ICONS: Record<DraftChange['change'], { icon: string, tone: string, word: string }> = {
  added: { icon: 'material-symbols:add-circle-outline', tone: 'text-success', word: 'Added' },
  changed: { icon: 'material-symbols:edit-outline', tone: 'text-warning', word: 'Changed' },
  removed: { icon: 'material-symbols:remove-circle-outline', tone: 'text-error', word: 'Removed' }
}

const flagName = (key: string) => flags.value.find(flag => flag.key === key)?.name ?? key
const segmentLabel = (key: string) => segments.value.find(segment => segment.key === key)?.name ?? key

// --- Versions -----------------------------------------------------------------------------

const historyFor = useQueryChoice<EnvironmentKind>('history', ENVIRONMENTS, 'production')
const versionRows = computed(() => {
  const list = historyOf(historyFor.value)
  return list.map((version, index) => {
    const previous = list[index + 1] ?? null
    const changed = previous
      ? version.flags.filter(flag => !sameJson(flag, previous.flags.find(entry => entry.key === flag.key))).length
      + previous.flags.filter(flag => !version.flags.some(entry => entry.key === flag.key)).length
      : null
    return { ...version, changed, current: index === 0 }
  })
})

const VERSION_COLUMNS: RecordColumn[] = [
  { key: 'version', label: 'Version', width: '84px' },
  { key: 'note', label: 'Note', width: 'minmax(0, 2fr)' },
  { key: 'by', label: 'Published', width: 'minmax(0, 1.2fr)' },
  { key: 'flags', label: 'Flags', width: '96px', align: 'end' },
  { key: 'actions', label: '', width: '72px', align: 'end' }
]

const now = useNow({ interval: 60_000 })

const viewing = shallowRef<ConfigVersionDef | null>(null)
const viewOpen = computed({
  get: () => viewing.value !== null,
  set: (value) => {
    if (!value) viewing.value = null
  }
})
const documentText = computed(() => (viewing.value ? JSON.stringify(compileDocument(viewing.value.version, viewing.value.flags, viewing.value.segments), null, 2) : ''))

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
  return `Sets the draft back to what ${ENVIRONMENT_LABELS[version.environment]} served in v${version.version}: ${differences} ${differences === 1 ? 'difference' : 'differences'} undone, flags added since then archived. Nothing reaches players until the draft is published again.`
})

// --- Activity -----------------------------------------------------------------------------

const ACTIVITY_FILTERS: SegmentedOption[] = [
  { value: 'all', label: 'Everything' },
  { value: 'publishes', label: 'Publishes' },
  { value: 'edits', label: 'Edits' }
]
const activityFilter = useQueryChoice('activity', ['all', 'publishes', 'edits'], 'all')
const activity = computed(() => audit.value.filter(entry => activityFilter.value === 'all'
  || (activityFilter.value === 'publishes' ? entry.action === 'liveops.publish' || entry.action === 'draft.restore' : entry.action !== 'liveops.publish' && entry.action !== 'draft.restore')).slice(0, 40))
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Publishing">
      <template #description>
        What each of <span class="text-ink">{{ project.name }}</span>'s environments serves, how far each is
        behind the draft, and every version before.
      </template>
      <template #actions>
        <v-btn
          :disabled="!check.compiles"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:publish"
          size="small"
          variant="flat"
          @click="openPublish('development')"
        >
          Publish the draft
        </v-btn>
      </template>
    </PageHeader>

    <EnvironmentPipeline @publish="openPublish" />

    <div class="grid gap-5 xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
      <SectionCard :padded="false" description="What publishing the draft would add, change and remove there." title="Waiting to publish">
        <template #actions>
          <SegmentedControl v-model="compareWith" :options="ENVIRONMENT_OPTIONS" label="Compare the draft with" />
        </template>

        <EmptyState
          v-if="!changes.length"
          :description="`${ENVIRONMENT_LABELS[compareWith]} already serves exactly what the draft says.`"
          icon="material-symbols:done-all"
          title="Nothing waiting"
        />
        <ul v-else class="m-0 list-none p-0">
          <li v-for="change in changes" :key="`${change.kind}:${change.key}`" class="flex gap-3 border-b border-line px-5 py-3 last:border-b-0">
            <Icon
              :class="CHANGE_ICONS[change.change].tone"
              :name="CHANGE_ICONS[change.change].icon"
              aria-hidden="true"
              class="mt-0.5 flex-none text-base"
            />
            <div class="flex min-w-0 flex-1 flex-col gap-1">
              <div class="flex flex-wrap items-baseline gap-x-2">
                <NuxtLink
                  v-if="change.kind === 'flag' && change.change !== 'removed'"
                  :to="`/growth/flags/${encodeURIComponent(change.key)}`"
                  class="text-xs font-medium text-ink no-underline hover:text-primary"
                >
                  {{ flagName(change.key) }}
                </NuxtLink>
                <span v-else class="text-xs font-medium text-ink">{{ change.kind === 'flag' ? flagName(change.key) : segmentLabel(change.key) }}</span>
                <span class="font-mono text-[11px] text-ink/45">{{ change.key }}</span>
                <span class="text-[11px] text-ink/45">{{ CHANGE_ICONS[change.change].word }} {{ change.kind }}</span>
              </div>
              <span v-for="line in change.lines" :key="line" class="text-[11px] leading-4 text-ink/60">{{ line }}</span>
            </div>
          </li>
        </ul>
      </SectionCard>

      <SectionCard description="What a publish would carry, checked as the server checks it." title="The draft">
        <template #badge>
          <StatusBadge :label="check.compiles ? 'Compiles' : 'Does not compile'" :tone="check.compiles ? 'success' : 'error'" />
        </template>
        <div class="flex flex-col gap-4">
          <InfoNote v-if="!check.compiles" icon="material-symbols:error-outline" tone="warning">
            {{ check.problem }}. Nothing can be published until it is fixed.
          </InfoNote>
          <dl class="m-0 grid grid-cols-2 gap-px overflow-hidden rounded-(--ps-radius) border border-line bg-line">
            <div class="flex flex-col gap-1 bg-surface p-3">
              <dt class="text-[11px] text-ink/50">
                Flags
              </dt>
              <dd class="m-0 font-mono text-lg text-ink">
                {{ check.flags }}
              </dd>
            </div>
            <div class="flex flex-col gap-1 bg-surface p-3">
              <dt class="text-[11px] text-ink/50">
                Segments
              </dt>
              <dd class="m-0 font-mono text-lg text-ink">
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
                  <Icon aria-hidden="true" class="flex-none text-sm text-ink/40" :name="entry.kind === 'flag' ? 'material-symbols:flag-outline' : 'material-symbols:group-outline'" />
                  <span class="truncate font-mono text-ink/80">{{ entry.key }}</span>
                </span>
                <span class="flex-none text-[11px] text-ink/45">{{ entry.reason }}</span>
              </li>
            </ul>
          </div>
        </div>
      </SectionCard>
    </div>

    <SectionCard :padded="false" description="Every version published there, newest first. Restore one into the draft to roll back to it." title="Versions">
      <template #actions>
        <SegmentedControl v-model="historyFor" :options="ENVIRONMENT_OPTIONS" label="Environment" />
      </template>
      <div class="px-5 py-2">
        <RecordTable :columns="VERSION_COLUMNS" label="Versions">
          <li v-for="version in versionRows" :key="version.version" class="record-row">
            <span class="record-cell record-cell--primary flex items-center gap-2" data-label="Version">
              <span class="font-mono text-xs text-ink">v{{ version.version }}</span>
              <StatusBadge v-if="version.current" label="Live" tone="success" />
            </span>
            <span class="record-cell text-xs text-ink/80" data-label="Note">{{ version.note || '—' }}</span>
            <span class="record-cell flex flex-col" data-label="Published">
              <span class="text-xs text-ink/80">{{ sinceWords(version.published_at, now) }}</span>
              <span class="truncate text-[11px] text-ink/45">{{ version.published_by }}</span>
            </span>
            <span class="record-cell record-cell--end flex flex-col" data-label="Flags">
              <span class="font-mono text-xs">{{ version.flags.length }}</span>
              <span v-if="version.changed !== null" class="text-[11px] text-ink/45">{{ version.changed }} changed</span>
            </span>
            <span class="record-cell record-cell--end record-cell--action">
              <v-menu location="bottom end">
                <template #activator="{ props: activator }">
                  <v-btn
                    v-bind="activator"
                    :aria-label="`v${version.version} actions`"
                    color="neutral"
                    icon="material-symbols:more-horiz"
                    size="small"
                    variant="text"
                  />
                </template>
                <v-list min-width="200" nav>
                  <v-list-item prepend-icon="material-symbols:data-object" title="View the document" @click="viewing = version" />
                  <v-list-item prepend-icon="material-symbols:history" title="Restore into the draft" @click="restoring = version" />
                </v-list>
              </v-menu>
            </span>
          </li>
          <template #empty>
            <p v-if="!versionRows.length" class="m-0 px-3 py-8 text-center text-xs text-ink/55">
              Nothing has been published to {{ ENVIRONMENT_LABELS[historyFor] }} yet. Until it is, every flag there answers with the game's own default.
            </p>
          </template>
        </RecordTable>
      </div>
    </SectionCard>

    <SectionCard description="Edits, publishes and restores, as the audit log records them." title="Activity">
      <template #actions>
        <SegmentedControl v-model="activityFilter" :options="ACTIVITY_FILTERS" label="Show" />
      </template>
      <ConfigHistory :entries="activity" show-target />
    </SectionCard>

    <PublishDialog v-if="publishing" v-model="publishing" :environment="publishTo" />

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
              The compiled document the server evaluates: segments written into each rule. Devices never receive it —
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
      :title="restoring ? `Restore v${restoring.version} into the draft?` : ''"
      action="Restore into the draft"
      tone="primary"
      @confirm="restoring && restoreDraft(restoring)"
    />
  </div>
</template>
