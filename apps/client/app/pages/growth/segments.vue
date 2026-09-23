<script setup lang="ts">
// The audiences this project has named: whales, new players, testers, the markets with regional
// pricing. A flag's rule refers to a segment instead of repeating its conditions, so "who counts
// as a whale" is decided in one place and every flag follows it at the next publish.
//
// Each row says who asserts the keys it reads, because a segment built on what the game client
// sets is one a modified client can join (ADR 0027), and what it is used by, because that is
// what an edit to it will reach.
//
// Sample data (app/utils/flags-sample.ts). The reach is drawn, not counted, until the players
// table answers it.
const { project } = useWorkspace()
const { segments, segmentName, usesOf, setSegmentArchived } = useLiveConfig()

interface SegmentRow extends SegmentDef {
  reach: number
  authorities: Authority[]
  usedBy: FlagDef[]
  nested: SegmentDef[]
}

const rows = computed<SegmentRow[]>(() => segments.value.map((segment) => {
  const rules = new Map(segments.value.map(entry => [entry.key, entry.rule]))
  const keys = keysIn(inlineSegments(segment.rule, rules))
  const order: Authority[] = ['client', 'sdk', 'history', 'connection']
  const uses = usesOf(segment.key)
  return {
    ...segment,
    reach: sampleReach(segment.rule),
    authorities: order.filter(authority => keys.some(key => authorityOf(key) === authority)),
    usedBy: uses.flags,
    nested: uses.segments
  }
}))

const tabs = computed<PageTab[]>(() => [
  { value: 'active', label: 'Segments', count: rows.value.filter(row => !row.archived).length },
  { value: 'archived', label: 'Archived', count: rows.value.filter(row => row.archived).length }
])
const tab = useQueryChoice('view', ['active', 'archived'], 'active')

const query = ref('')

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return rows.value.filter(row => row.archived === (tab.value === 'archived')
    && (!needle || row.key.includes(needle) || row.name.toLowerCase().includes(needle) || row.description.toLowerCase().includes(needle)))
})

const active = computed<ActiveFilter[]>(() => (query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : []))

const COLUMNS: DataTableColumn<SegmentRow>[] = [
  { key: 'name', title: 'Segment', fixed: true, minWidth: 240, skeleton: 'identity' },
  { key: 'rule', title: 'Players who match', sortable: false, minWidth: 320 },
  { key: 'authorities', title: 'Reads', sortable: false },
  { key: 'reach', title: 'Reach', align: 'end', mono: true },
  { key: 'usedBy', title: 'Used by', value: row => row.usedBy.length + row.nested.length, align: 'end' },
  { key: 'updated_at', title: 'Updated', align: 'end', mono: true }
]

const columns = useLocalStorage('playersense.segments.columns', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'usedBy', order: 'desc' }])

const now = useNow({ interval: 60_000 })

// --- Editing ------------------------------------------------------------------------------

const editing = shallowRef<SegmentDef | null>(null)
const dialogOpen = ref(false)

function openSegment(segment: SegmentDef | null) {
  editing.value = segment
  dialogOpen.value = true
}

const archiving = shallowRef<SegmentRow | null>(null)
const archiveOpen = computed({
  get: () => archiving.value !== null,
  set: (value) => {
    if (!value) archiving.value = null
  }
})

const archiveWords = computed(() => {
  const row = archiving.value
  if (!row) return ''
  const users = [...row.usedBy.map(flag => flag.name), ...row.nested.map(segment => `segment ${segment.name}`)]
  if (!users.length) return 'It leaves the draft. Nothing refers to it, so nothing else changes.'
  return `${users.join(', ')} ${users.length === 1 ? 'refers' : 'refer'} to it. The draft will not compile, and nothing can be published, until ${users.length === 1 ? 'that rule stops' : 'those rules stop'} naming it.`
})

const { copy } = useClipboard({ legacy: true })
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Segments">
      <template #description>
        The audiences <span class="text-ink">{{ project.name }}</span>'s flags are written against,
        each defined once and referred to by name.
      </template>
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          variant="flat"
          @click="openSegment(null)"
        >
          New segment
        </v-btn>
      </template>
    </PageHeader>

    <div class="flex flex-col gap-5">
      <PageTabs v-model="tab" :tabs="tabs" label="Segment views" />

      <SectionCard
        :description="tab === 'archived' ? 'Left out of every publish. A rule naming one stops the draft compiling.' : 'Open one to change who is in it. Reach is the share of last week\'s players who would match.'"
        :padded="false"
        :title="tab === 'archived' ? 'Archived segments' : 'Segments'"
      >
        <TableToolbar :filters="active" @clear="query = ''" @remove="query = ''">
          <SearchField
            v-model="query"
            class="w-56"
            label="Search segments"
            placeholder="Name, key or description"
          />
          <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
        </TableToolbar>

        <DataTable
          v-model:column-state="columns"
          v-model:sort-by="sortBy"
          :columns="COLUMNS"
          :items="matched"
          :paged="false"
          empty-description="Nothing matches the search, or there are no segments here yet."
          empty-title="No segments."
          item-value="key"
          label="Segments"
        >
          <template #cell.name="{ item }">
            <button class="group flex min-w-0 items-center gap-3 text-left" type="button" @click="openSegment(item)">
              <span
                aria-hidden="true"
                class="
                  flex size-8 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/5
                  text-ink/60 transition-colors
                  group-hover:bg-primary/10 group-hover:text-primary
                "
              >
                <Icon class="text-base" name="material-symbols:group-outline" />
              </span>
              <span class="flex min-w-0 flex-col">
                <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.name }}</span>
                <span class="truncate font-mono text-[11px] text-ink/50">{{ item.key }}</span>
              </span>
            </button>
          </template>

          <template #cell.rule="{ item }">
            <span class="flex min-w-0 flex-col gap-0.5 py-1 whitespace-normal">
              <span class="line-clamp-2 text-xs text-ink/80">{{ describeRule(item.rule, segmentName) }}</span>
              <span v-if="item.description" class="line-clamp-1 text-[11px] text-ink/50">{{ item.description }}</span>
            </span>
          </template>

          <template #cell.authorities="{ item }">
            <span class="flex items-center gap-2">
              <AuthorityMark
                v-for="authority in item.authorities"
                :key="authority"
                :authority="authority"
                labelled
              />
              <span v-if="!item.authorities.length" class="text-[11px] text-ink/40">Everyone</span>
            </span>
          </template>

          <template #cell.reach="{ item }">
            <span class="inline-flex items-center gap-2">
              <span class="h-1.5 w-16 overflow-hidden rounded-full bg-ink/6">
                <span :style="{ width: `${item.reach * 100}%` }" class="block h-full rounded-full bg-primary" />
              </span>
              {{ formatShare(item.reach) }}
            </span>
          </template>

          <template #cell.usedBy="{ item }">
            <v-tooltip
              v-if="item.usedBy.length || item.nested.length"
              :text="[...item.usedBy.map(flag => flag.name), ...item.nested.map(segment => `segment ${segment.name}`)].join(', ')"
              location="top"
              max-width="320"
            >
              <template #activator="{ props: tip }">
                <span v-bind="tip" class="text-xs text-ink/80">
                  {{ item.usedBy.length }} {{ item.usedBy.length === 1 ? 'flag' : 'flags' }}<template v-if="item.nested.length">, {{ item.nested.length }} {{ item.nested.length === 1 ? 'segment' : 'segments' }}</template>
                </span>
              </template>
            </v-tooltip>
            <span v-else class="text-[11px] text-ink/40">Unused</span>
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
                <v-list-item prepend-icon="material-symbols:edit-outline" title="Edit" @click="openSegment(item)" />
                <v-list-item prepend-icon="material-symbols:content-copy-outline" title="Copy key" @click="copy(item.key)" />
                <v-list-item
                  v-if="item.archived"
                  prepend-icon="material-symbols:unarchive-outline"
                  title="Restore"
                  @click="setSegmentArchived(item.key, false)"
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

    <SegmentDialog v-if="dialogOpen" v-model="dialogOpen" :segment="editing" />

    <ConfirmDialog
      v-if="archiveOpen"
      v-model="archiveOpen"
      :description="archiveWords"
      :title="archiving ? `Archive ${archiving.name}?` : ''"
      action="Archive segment"
      @confirm="archiving && setSegmentArchived(archiving.key, true)"
    />
  </div>
</template>
