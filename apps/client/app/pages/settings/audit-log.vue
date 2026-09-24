<script setup lang="ts">
// Settings > Audit log: every change made in the organization, newest first. Append-only and
// read-only: nothing on this page changes anything, and there is no delete. Each row opens to
// the whole event, metadata included, as the API returns it. Sample events until the page
// reads GET /organizations/{org}/audit.
const events = ref<AuditEvent[]>(seedAudit())

const COLUMNS: DataTableColumn<AuditEvent>[] = [
  { key: 'at', title: 'When', fixed: true, mono: true, width: 170, value: event => Date.parse(event.at) },
  { key: 'actor', title: 'Who', width: 220, skeleton: 'identity', value: event => event.actor ?? '' },
  { key: 'action', title: 'What', value: event => auditAction(event.action).label },
  { key: 'area', title: 'Area', width: 130, skeleton: 'pill', value: event => AUDIT_AREAS[auditAction(event.action).area].label },
  { key: 'target', title: 'Target', width: 240, sortable: false },
  { key: 'ip', title: 'IP address', mono: true, width: 140, hidden: true }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'at', order: 'desc' }])

// --- Narrowing ---------------------------------------------------------------------------------

const query = ref('')
const filters = ref<Record<string, string[]>>({ area: [], actor: [] })

const actors = computed(() => [...new Set(events.value.map(event => event.actor ?? 'System'))].sort())

const FILTER_GROUPS = computed<FilterGroup[]>(() => [
  { key: 'area', label: 'Area', options: (Object.keys(AUDIT_AREAS) as AuditArea[]).map(area => ({ value: area, label: AUDIT_AREAS[area].label })) },
  { key: 'actor', label: 'Who', noun: 'person', options: actors.value.map(actor => ({ value: actor, label: actor })) }
])

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.area ?? []).map(area => ({ key: `area:${area}`, label: `Area: ${AUDIT_AREAS[area as AuditArea].label}` })),
  ...(filters.value.actor ?? []).map(actor => ({ key: `actor:${actor}`, label: `Who: ${actor}` })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') {
    query.value = ''
    return
  }
  const split = key.indexOf(':')
  const group = key.slice(0, split)
  const value = key.slice(split + 1)
  filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(entry => entry !== value) }
}

function clearFilters() {
  query.value = ''
  filters.value = { area: [], actor: [] }
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const areas = filters.value.area ?? []
  const people = filters.value.actor ?? []
  return events.value.filter((event) => {
    if (areas.length && !areas.includes(auditAction(event.action).area)) return false
    if (people.length && !people.includes(event.actor ?? 'System')) return false
    if (!needle) return true
    return [event.action, auditAction(event.action).label, event.actor ?? 'system', event.actor_email ?? '', event.target_type, event.target_id, event.ip ?? '']
      .some(text => text.toLowerCase().includes(needle))
  })
})

function exportLog() {
  downloadCsv('audit-log', [
    ['Time', 'Action', 'Actor', 'Actor email', 'Target type', 'Target ID', 'IP address', 'Metadata'],
    ...matched.value.map(event => [event.at, event.action, event.actor ?? 'System', event.actor_email, event.target_type, event.target_id, event.ip, JSON.stringify(event.metadata)])
  ])
}

const initials = (name: string) => name.split(' ').map(part => part[0]).join('').slice(0, 2).toUpperCase()

// --- One event, whole ----------------------------------------------------------------------------

const open = ref<AuditEvent | null>(null)
const opened = computed({
  get: () => open.value !== null,
  set: (value: boolean) => {
    if (!value) open.value = null
  }
})

const openFacts = computed<Fact[]>(() => {
  const event = open.value
  if (!event) return []
  return [
    { key: 'at', label: 'When', value: formatDateTime(event.at), mono: true },
    { key: 'who', label: 'Who', value: event.actor ?? 'System', note: event.actor_email ?? 'A webhook or a scheduled job' },
    { key: 'ip', label: 'From', value: event.ip ?? 'Inside PlayerSense', mono: Boolean(event.ip) },
    { key: 'target', label: 'Target', value: event.target_id, mono: true, note: event.target_type.replace(/_/g, ' '), copy: event.target_id }
  ]
})

const openCode = computed(() => (open.value ? JSON.stringify(open.value, null, 2) : ''))
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Audit log">
      <template #description>
        Every change made in this organization: members and roles, keys, flags and live config, billing, privacy requests. It is only ever added to, never edited.
      </template>
      <template #actions>
        <v-btn
          :disabled="!matched.length"
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:download"
          size="small"
          variant="text"
          @click="exportLog"
        >
          Export CSV
        </v-btn>
      </template>
    </PageHeader>

    <SectionCard :padded="false" description="Newest first. Open an event for everything recorded with it." title="Events">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search the audit log"
          placeholder="Action, person, target, IP"
        />
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="matched"
        empty-description="No event matches these filters."
        empty-title="Nothing matches."
        label="Audit events"
      >
        <template #cell.at="{ item }">
          {{ formatDateTime(item.at) }}
        </template>

        <template #cell.actor="{ item }">
          <span class="flex min-w-0 items-center gap-2.5">
            <span
              aria-hidden="true"
              class="flex size-6 flex-none items-center justify-center rounded-full border border-line bg-ink/4 text-[9px] font-medium text-ink/70"
            >
              <Icon v-if="!item.actor" class="text-sm" name="material-symbols:settings-outline" />
              <template v-else>{{ initials(item.actor) }}</template>
            </span>
            <span class="flex min-w-0 flex-col">
              <span class="truncate text-ink">{{ item.actor ?? 'System' }}</span>
              <span v-if="item.actor_email" class="truncate text-[11px] text-ink/50">{{ item.actor_email }}</span>
            </span>
          </span>
        </template>

        <template #cell.action="{ item }">
          <span class="flex min-w-0 flex-col">
            <span class="truncate text-ink">{{ auditAction(item.action).label }}</span>
            <span class="truncate font-mono text-[11px] text-ink/45">{{ item.action }}</span>
          </span>
        </template>

        <template #cell.area="{ item }">
          <Tag :label="AUDIT_AREAS[auditAction(item.action).area].label" :tone="AUDIT_AREAS[auditAction(item.action).area].tone" />
        </template>

        <template #cell.target="{ item }">
          <span class="flex min-w-0 flex-col">
            <span class="truncate font-mono text-xs text-ink/80">{{ item.target_id }}</span>
            <span class="truncate text-[11px] text-ink/45">{{ item.target_type.replace(/_/g, ' ') }}</span>
          </span>
        </template>

        <template #cell.ip="{ item }">
          <span class="text-ink/65">{{ item.ip ?? '' }}</span>
        </template>

        <template #actions="{ item }">
          <v-btn
            :aria-label="`Open the event: ${auditAction(item.action).label}`"
            icon
            size="small"
            variant="text"
            @click="open = item"
          >
            <Icon aria-hidden="true" class="text-lg" name="material-symbols:open-in-full" />
            <v-tooltip activator="parent" location="top" text="Details" />
          </v-btn>
        </template>
      </DataTable>
    </SectionCard>

    <v-dialog v-model="opened" max-width="640">
      <v-card v-if="open" border flat>
        <header class="flex items-start justify-between gap-3 border-b border-line p-5">
          <div class="flex min-w-0 flex-col gap-1.5">
            <span class="flex items-center gap-2">
              <h2 class="m-0 text-sm font-medium text-ink">
                {{ auditAction(open.action).label }}
              </h2>
              <Tag :label="AUDIT_AREAS[auditAction(open.action).area].label" :tone="AUDIT_AREAS[auditAction(open.action).area].tone" />
            </span>
            <span class="font-mono text-[11px] text-ink/50">{{ open.action }} · {{ open.id }}</span>
          </div>
          <v-btn
            aria-label="Close"
            icon
            size="small"
            variant="text"
            @click="open = null"
          >
            <Icon aria-hidden="true" class="text-lg" name="material-symbols:close" />
          </v-btn>
        </header>
        <div class="flex flex-col gap-4 p-5">
          <FactGrid :columns="2" :facts="openFacts" label="The event" />
          <CodeBlock :code="openCode" title="As the API returns it" />
        </div>
      </v-card>
    </v-dialog>
  </div>
</template>
