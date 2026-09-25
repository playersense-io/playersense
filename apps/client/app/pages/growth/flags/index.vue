<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Every flag the game reads: what it serves, to whom, where it is published and whether anybody
// is reading it. The list is also where a project's configuration is kept healthy — a release
// flag everybody has had for a month is code to delete, a flag no build reads is a rule doing
// nothing, and a value-granting flag reached through a key the client sets is a hole — so each
// row says when it needs somebody, and "Needs attention" gathers them.
//
// Flags, segments and published versions are the API's; reads are production's exposures over
// the last seven days.
const { project } = useWorkspace()
const { flags, segments, latest, setFlagArchived, loading, error, reload } = useLiveConfig()
const { readsOf, ready: readsReady, hasData: readsKnown } = useFlagExposures()

const problem = ref('')

async function archive(key: string, archived: boolean) {
  problem.value = (await setFlagArchived(key, archived)) ?? ''
}
const router = useRouter()

// --- Attention ----------------------------------------------------------------------------

type Attention = 'waiting' | 'unread' | 'remove' | 'decide' | 'forgeable'

const ATTENTION: Record<Attention, { label: string, tone: BadgeTone, note: string }> = {
  forgeable: { label: 'Forgeable audience', tone: 'error', note: 'Grants value to an audience read from keys the game client sets.' },
  unread: { label: 'Not read', tone: 'warning', note: 'Published to production, and no device read it in the last 7 days.' },
  remove: { label: 'Ready to remove', tone: 'warning', note: 'A release everybody has had for 30 days. Delete the check from the code, then archive it.' },
  decide: { label: 'Decide', tone: 'warning', note: 'An experiment running four weeks or more. Pick the winner and make it the fallback.' },
  waiting: { label: 'Unpublished', tone: 'neutral', note: 'The draft differs from what production serves: publish it there for players to get it.' }
}

const production = computed(() => latest('production'))

const DAY = 86_400_000

interface FlagRow extends FlagDef {
  document: FlagDocument
  reads: number
  previousReads: number
  daily: number[]
  states: Record<EnvironmentKind, FlagEnvironmentState>
  attention: Attention[]
}

const rows = computed<FlagRow[]>(() => flags.value.map((flag) => {
  const states = Object.fromEntries(ENVIRONMENTS.map(environment => [environment, flagStateIn(flag, latest(environment), segments.value)])) as Record<EnvironmentKind, FlagEnvironmentState>
  const published = production.value?.flags.find(entry => entry.key === flag.key)
  const weekly = published && !flag.archived ? readsOf(flag.key) : { reads: 0, previous: 0, daily: [] }
  const document = documentOf(flag)
  const attention: Attention[] = []
  if (!flag.archived) {
    if (flag.grantsValue && forgeableKeys(flag, segments.value).length) attention.push('forgeable')
    if (published && readsReady.value && weekly.reads === 0) attention.push('unread')
    const settled = Date.now() - Date.parse(flag.updated_at) > 30 * DAY
    if (flag.purpose === 'release' && settled && !isPartial(document) && servedValues(document).length === 1) attention.push('remove')
    if (flag.purpose === 'experiment' && Date.now() - Date.parse(flag.created_at) > 28 * DAY) attention.push('decide')
    if (states.production !== 'same') attention.push('waiting')
  }
  return { ...flag, document, reads: weekly.reads, previousReads: weekly.previous, daily: weekly.daily, states, attention }
}))

const active = computed(() => rows.value.filter(row => !row.archived))
const needing = computed(() => active.value.filter(row => row.attention.some(item => item !== 'waiting')))

// --- The board ----------------------------------------------------------------------------

const board = computed<Kpi[]>(() => {
  const live = active.value
  const partial = live.filter(row => isPartial(row.document)).length
  const waiting = draftChanges(production.value, flags.value, segments.value).length
  const reads = live.reduce((sum, row) => sum + row.reads, 0)
  const previous = live.reduce((sum, row) => sum + row.previousReads, 0)
  return [
    { key: 'flags', label: 'Flags', value: String(live.length), info: 'Flags in the draft, archived ones aside.' },
    { key: 'partial', label: 'Rolling out', value: String(partial), info: 'Flags holding some players back: a flag or a rule served to less than all of its audience.' },
    { key: 'waiting', label: 'Waiting for production', value: String(waiting), info: 'Flags the next publish to production would add, change or remove.' },
    {
      key: 'reads',
      label: 'Exposures, 7 days',
      // No device has read a flag yet: no data, not a zero. A trend needs a week before it too.
      value: readsKnown.value ? writeFigure(reads, { compact: true }) : null,
      exact: readsKnown.value ? writeFigure(reads) : undefined,
      trend: readsKnown.value && previous > 0 ? trendOf(reads, previous) : undefined,
      info: 'Sessions in which production devices read a flag, against the week before.'
    },
    { key: 'attention', label: 'Need attention', value: String(needing.value.length), info: 'Flags nobody reads, releases ready to remove, experiments to decide and value granted to a forgeable audience.' }
  ]
})

// --- Views and filters --------------------------------------------------------------------

const tabs = computed<PageTab[]>(() => [
  { value: 'all', label: 'All flags', count: active.value.length },
  { value: 'attention', label: 'Needs attention', count: needing.value.length },
  { value: 'archived', label: 'Archived', count: rows.value.length - active.value.length }
])
const tab = useQueryChoice('view', ['all', 'attention', 'archived'], 'all')

const TAGS = computed(() => [...new Set(flags.value.flatMap(flag => flag.tags))].sort())

const purposes = useQueryList('purpose', Object.keys(FLAG_PURPOSES))
const kinds = useQueryList('kind', Object.keys(FLAG_KIND_LABELS))
const tags = useQueryList('tag', () => TAGS.value)
const inProduction = useQueryList('production', ['same', 'changed', 'absent'])
const query = ref('')

const filters = computed<Record<string, string[]>>({
  get: () => ({ purpose: purposes.value, kind: kinds.value, tag: tags.value, production: inProduction.value }),
  set: (value) => {
    purposes.value = value.purpose ?? []
    kinds.value = value.kind ?? []
    tags.value = value.tag ?? []
    inProduction.value = value.production ?? []
  }
})

const PRODUCTION_WORDS: Record<string, string> = { same: 'Serves the draft', changed: 'Draft differs', absent: 'Not published' }

const FILTER_GROUPS = computed<FilterGroup[]>(() => [
  { key: 'purpose', label: 'Purpose', options: (Object.keys(FLAG_PURPOSES) as FlagPurpose[]).map(value => ({ value, label: FLAG_PURPOSES[value].label })) },
  { key: 'kind', label: 'Kind', options: (Object.keys(FLAG_KIND_LABELS) as FlagKind[]).map(value => ({ value, label: FLAG_KIND_LABELS[value] })) },
  { key: 'production', label: 'In production', options: Object.entries(PRODUCTION_WORDS).map(([value, label]) => ({ value, label })) },
  { key: 'tag', label: 'Tag', options: TAGS.value.map(value => ({ value, label: value })) }
])

const activeFilters = computed<ActiveFilter[]>(() => [
  ...FILTER_GROUPS.value.flatMap(group => (filters.value[group.key] ?? []).map(value => ({
    key: `${group.key}:${value}`,
    label: `${group.label}: ${group.options.find(option => option.value === value)?.label ?? value}`
  }))),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') {
    query.value = ''
    return
  }
  const [group = '', value] = key.split(':')
  filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(item => item !== value) }
}

function clearFilters() {
  query.value = ''
  filters.value = { purpose: [], kind: [], tag: [], production: [] }
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const { purpose = [], kind = [], tag = [], production = [] } = filters.value
  const inView = tab.value === 'archived' ? rows.value.filter(row => row.archived) : tab.value === 'attention' ? needing.value : active.value
  return inView.filter(row =>
    (!purpose.length || purpose.includes(row.purpose))
    && (!kind.length || kind.includes(row.kind))
    && (!tag.length || row.tags.some(entry => tag.includes(entry)))
    && (!production.length || production.includes(row.states.production === 'unpublished' ? 'absent' : row.states.production))
    && (!needle || row.key.toLowerCase().includes(needle) || row.name.toLowerCase().includes(needle) || row.description.toLowerCase().includes(needle)))
})

// --- The table ----------------------------------------------------------------------------

const COLUMNS: DataTableColumn<FlagRow>[] = [
  { key: 'name', title: 'Flag', fixed: true, minWidth: 260, skeleton: 'identity' },
  { key: 'kind', title: 'Kind', value: row => FLAG_KIND_LABELS[row.kind], skeleton: 'pill' },
  { key: 'serving', title: 'Serving', sortable: false, minWidth: 240 },
  { key: 'environments', title: 'Environments', sortable: false, skeleton: 'glyphs' },
  { key: 'reads', title: 'Exposures, 7d', align: 'end', mono: true },
  { key: 'attention', title: 'Attention', sortable: false, minWidth: 160 },
  { key: 'purpose', title: 'Purpose', value: row => FLAG_PURPOSES[row.purpose].label, hidden: true },
  { key: 'tags', title: 'Tags', sortable: false, hidden: true },
  { key: 'updated_at', title: 'Updated', align: 'end', mono: true }
]

const columns = useLocalStorage('playersense.flags.columns', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'updated_at', order: 'desc' }])

const now = useNow({ interval: 60_000 })

const ENV_MARKS: Record<FlagEnvironmentState, string> = {
  same: 'bg-success',
  changed: 'bg-warning',
  absent: 'bg-ink/15',
  unpublished: 'bg-ink/15'
}

const ENV_WORDS: Record<FlagEnvironmentState, string> = {
  same: 'serves the draft',
  changed: 'serves an older version of it',
  absent: 'does not have it',
  unpublished: 'has never been published'
}

function rolloutWords(row: FlagRow): string {
  if (row.rollout < FULL_ROLLOUT) return `${formatRollout(row.rollout)} of players`
  const partial = row.rules.find(rule => rule.rollout < FULL_ROLLOUT)
  return partial ? `${formatRollout(partial.rollout)} of rule ${row.rules.indexOf(partial) + 1}` : ''
}

// --- Actions ------------------------------------------------------------------------------

const creating = ref(false)
const duplicating = shallowRef<FlagDef | null>(null)
const duplicateOpen = computed({
  get: () => duplicating.value !== null,
  set: (value) => {
    if (!value) duplicating.value = null
  }
})

const previewing = ref(false)
const publishing = ref(false)
const publishTo = ref<EnvironmentKind>('production')

function openPublish(environment: EnvironmentKind) {
  publishTo.value = environment
  publishing.value = true
}

const { copy } = useClipboard({ legacy: true })

const archiving = shallowRef<FlagRow | null>(null)
const archiveOpen = computed({
  get: () => archiving.value !== null,
  set: (value) => {
    if (!value) archiving.value = null
  }
})

function onCreated(key: string) {
  void router.push(`/growth/flags/${encodeURIComponent(key)}`)
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Feature flags">
      <template #description>
        What <span class="text-ink">{{ project?.name }}</span> serves each player, decided on the server
        and changed without a release.
      </template>
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:person-search-outline"
          size="small"
          variant="outlined"
          @click="previewing = true"
        >
          Preview a player
        </v-btn>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          variant="flat"
          @click="creating = true"
        >
          New flag
        </v-btn>
      </template>
    </PageHeader>

    <p v-if="problem" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ problem }}
    </p>

    <LoadState
      :error="error"
      :loading="loading && !flags.length"
      what="the flags"
      @retry="reload"
    >
      <EnvironmentPipeline compact @publish="openPublish" />

      <KpiBoard :items="board" description="The draft, and production over the last 7 days." title="Summary" />

      <div class="flex flex-col gap-5">
        <PageTabs v-model="tab" :tabs="tabs" label="Flag views" />

        <SectionCard
          :description="tab === 'archived' ? 'Left out of every publish. Restoring one puts it back in the next.' : tab === 'attention' ? 'Flags that need a decision or a cleanup, with the reason on each.' : 'Every flag in the draft. Open one to change who gets what.'"
          :padded="false"
          :title="tab === 'archived' ? 'Archived flags' : tab === 'attention' ? 'Needs attention' : 'Flags'"
        >
          <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
            <SearchField
              v-model="query"
              class="w-56"
              label="Search flags"
              placeholder="Name, key or description"
            />
            <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
            <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
          </TableToolbar>

          <DataTable
            v-model:column-state="columns"
            v-model:sort-by="sortBy"
            :columns="COLUMNS"
            :items="matched"
            :paged="matched.length > 25"
            :empty-description="tab === 'attention' ? 'Every flag is read, current and safely targeted.' : 'No flag matches these filters. Remove one, or clear them all.'"
            :empty-title="tab === 'attention' ? 'Nothing needs attention.' : 'No flags match.'"
            item-value="key"
            label="Flags"
          >
            <template #cell.name="{ item }">
              <NuxtLink :to="`/growth/flags/${encodeURIComponent(item.key)}`" class="group flex min-w-0 items-center gap-3 no-underline">
                <span
                  aria-hidden="true"
                  class="
                  flex size-8 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/5
                  text-ink/60 transition-colors
                  group-hover:bg-primary/10 group-hover:text-primary
                "
                >
                  <Icon :name="FLAG_PURPOSES[item.purpose].icon" class="text-base" />
                </span>
                <span class="flex min-w-0 flex-col">
                  <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.name }}</span>
                  <span class="truncate font-mono text-[11px] text-ink/50">{{ item.key }}</span>
                </span>
              </NuxtLink>
            </template>

            <template #cell.kind="{ item }">
              <Tag :label="FLAG_KIND_LABELS[item.kind]" :tone="FLAG_KIND_TONES[item.kind]" />
            </template>

            <template #cell.serving="{ item }">
              <span class="flex min-w-0 flex-col gap-0.5">
                <span class="flex min-w-0 items-center gap-1.5">
                  <FlagValue :value="item.fallback" />
                  <span v-if="item.rules.length" class="flex-none text-[11px] text-ink/50">
                    + {{ item.rules.length }} {{ item.rules.length === 1 ? 'rule' : 'rules' }}
                  </span>
                </span>
                <span v-if="rolloutWords(item)" class="flex items-center gap-1 text-[11px] text-ink/50">
                  <Icon :aria-hidden="true" class="text-xs" name="material-symbols:donut-large" />
                  {{ rolloutWords(item) }}
                </span>
              </span>
            </template>

            <template #cell.environments="{ item }">
              <span class="flex items-center gap-2.5">
                <v-tooltip
                  v-for="environment in ENVIRONMENTS"
                  :key="environment"
                  :text="`${ENVIRONMENT_LABELS[environment]} ${ENV_WORDS[item.states[environment]]}`"
                  location="top"
                >
                  <template #activator="{ props: tip }">
                    <span v-bind="tip" class="inline-flex items-center gap-1 text-[11px] text-ink/55">
                      <span :class="ENV_MARKS[item.states[environment]]" aria-hidden="true" class="size-1.5 rounded-full" />
                      {{ ENVIRONMENT_SHORT[environment] }}
                    </span>
                  </template>
                </v-tooltip>
              </span>
            </template>

            <template #cell.reads="{ item }">
              <span v-if="item.states.production === 'absent' || item.states.production === 'unpublished' || item.archived || !readsKnown" class="text-ink/35">–</span>
              <ProjectFigure
                v-else
                :previous="item.previousReads"
                :value="item.reads"
                :written="writeFigure(item.reads, { compact: true })"
              />
            </template>

            <template #cell.attention="{ item }">
              <span class="flex flex-wrap gap-1">
                <v-tooltip
                  v-for="entry in item.attention"
                  :key="entry"
                  :text="ATTENTION[entry].note"
                  location="top"
                  max-width="260"
                >
                  <template #activator="{ props: tip }">
                    <span v-bind="tip"><StatusBadge :label="ATTENTION[entry].label" :tone="ATTENTION[entry].tone" /></span>
                  </template>
                </v-tooltip>
                <span v-if="!item.attention.length" class="text-[11px] text-ink/35">–</span>
              </span>
            </template>

            <template #cell.purpose="{ item }">
              {{ FLAG_PURPOSES[item.purpose].label }}
            </template>

            <template #cell.tags="{ item }">
              <span class="flex flex-wrap gap-1">
                <Tag v-for="entry in item.tags" :key="entry" :label="entry" />
              </span>
            </template>

            <template #cell.updated_at="{ item }">
              <v-tooltip :text="`${formatDateTime(item.updated_at)} by ${item.updated_by}`" location="top">
                <template #activator="{ props: tip }">
                  <span v-bind="tip">{{ relativeTime(item.updated_at, now) }}</span>
                </template>
              </v-tooltip>
            </template>

            <template #actions="{ item }">
              <v-menu location="bottom end">
                <template #activator="{ props: activator }">
                  <v-btn
                    v-bind="activator"
                    :aria-label="`${item.name} actions`"
                    color="neutral"
                    icon="material-symbols:more-horiz"
                    size="small"
                    variant="text"
                  />
                </template>
                <v-list min-width="200" nav>
                  <v-list-item :to="`/growth/flags/${encodeURIComponent(item.key)}`" prepend-icon="material-symbols:open-in-new" title="Open" />
                  <v-list-item prepend-icon="material-symbols:content-copy-outline" title="Copy key" @click="copy(item.key)" />
                  <v-list-item prepend-icon="material-symbols:library-add-outline" title="Duplicate" @click="duplicating = item" />
                  <v-list-item
                    v-if="item.archived"
                    prepend-icon="material-symbols:unarchive-outline"
                    title="Restore"
                    @click="archive(item.key, false)"
                  />
                  <v-list-item
                    v-else
                    prepend-icon="material-symbols:archive-outline"
                    title="Archive"
                    @click="archiving = item"
                  />
                </v-list>
              </v-menu>
            </template>
          </DataTable>
        </SectionCard>
      </div>
    </LoadState>

    <!-- Mounted only while open: see the note on the flag page. -->
    <NewFlagDialog v-if="creating" v-model="creating" @created="onCreated" />
    <NewFlagDialog
      v-if="duplicateOpen"
      v-model="duplicateOpen"
      :from="duplicating"
      @created="onCreated"
    />
    <FlagPreviewDialog v-if="previewing" v-model="previewing" />
    <PublishDialog v-if="publishing" v-model="publishing" :environment="publishTo" />

    <ConfirmDialog
      v-if="archiveOpen"
      v-model="archiveOpen"
      :description="archiving ? `${archiving.name} leaves the draft. Each environment keeps serving it until the next publish there; after that, devices fall back to the value written in the game's code.` : ''"
      :title="archiving ? `Archive ${archiving.name}?` : ''"
      action="Archive flag"
      @confirm="archiving && archive(archiving.key, true)"
    />
  </div>
</template>
