<script setup lang="ts">
// The keys the game and the studio's servers reach PlayerSense with. Read top to bottom: the
// three kinds of key and where each goes, since choosing the wrong kind is the mistake that
// matters, then any key that wants a decision (one nobody has used in months, one rolled and
// never revoked), then every key.
//
// A key is shown once, when it is made (ApiKeyDialog). After that the list knows its public
// half, which is safe to show and says what the key is, and never the key.
const { project } = useWorkspace()
const { keys, revoke } = useApiKeys()

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const DAY = 86_400_000

const active = computed(() => keys.value.filter(key => !key.revoked_at))
const countOf = (kind: ApiKeyKind) => active.value.filter(key => key.kind === kind).length
const requestsOf = (key: ApiKeyDef) => key.hourly.reduce((sum, value) => sum + value, 0)

// --- Creating and rolling -----------------------------------------------------------------

const creating = ref(false)
const createKind = ref<ApiKeyKind | undefined>(undefined)
const rolling = ref<ApiKeyDef | null>(null)

function openCreate(kind?: ApiKeyKind) {
  rolling.value = null
  createKind.value = kind
  creating.value = true
}

function openRoll(key: ApiKeyDef) {
  rolling.value = key
  createKind.value = undefined
  creating.value = true
}

/* Drawn afresh each time it opens, so a second key does not start on the first one's secret. */
const dialogKey = ref(0)
watch(creating, (value) => {
  if (value) dialogKey.value += 1
})

// --- What wants a decision ----------------------------------------------------------------

interface Finding {
  key: ApiKeyDef
  icon: string
  text: string
}

const findings = computed<Finding[]>(() => {
  const out: Finding[] = []
  for (const key of active.value) {
    const replacement = key.replaced_by ? keys.value.find(entry => entry.id === key.replaced_by) : null
    if (replacement && !replacement.revoked_at) {
      out.push({ key, icon: 'material-symbols:autorenew', text: `${key.name} was replaced by ${replacement.name} and still works. It was last used ${key.last_used_at ? relativeWords(Date.parse(key.last_used_at), time.value) : 'never'}: revoke it once nothing uses it.` })
      continue
    }
    const age = time.value - Date.parse(key.created_at)
    if (!key.last_used_at && age > 14 * DAY) {
      out.push({ key, icon: 'material-symbols:schedule-outline', text: `${key.name} was made ${relativeWords(Date.parse(key.created_at), time.value)} and has never been used. A key nobody uses is only a key somebody can leak.` })
    } else if (key.last_used_at && time.value - Date.parse(key.last_used_at) > STALE_AFTER_DAYS * DAY) {
      out.push({ key, icon: 'material-symbols:schedule-outline', text: `${key.name} has not been used for ${Math.round((time.value - Date.parse(key.last_used_at)) / DAY)} days. If whatever used it is gone, revoke it.` })
    }
  }
  return out
})

// --- The list -----------------------------------------------------------------------------

const kindFilter = ref('all')
const KIND_FILTERS = computed<SegmentedOption[]>(() => [
  { value: 'all', label: `All ${active.value.length}` },
  ...API_KEY_KIND_ORDER.map(value => ({ value, label: `${API_KEY_KINDS[value].label} ${countOf(value)}` }))
])

const filters = ref<Record<string, string[]>>({ environment: [] })
const FILTER_GROUPS: FilterGroup[] = [
  { key: 'environment', label: 'Environment', options: ENVIRONMENTS.slice().reverse().map(value => ({ value, label: ENVIRONMENT_LABELS[value] })) }
]
const showRevoked = ref(false)
const query = ref('')

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.environment ?? []).map(value => ({ key: `environment:${value}`, label: ENVIRONMENT_LABELS[value as EnvironmentKind] })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') query.value = ''
  else filters.value = { environment: (filters.value.environment ?? []).filter(value => `environment:${value}` !== key) }
}

function clearFilters() {
  query.value = ''
  filters.value = { environment: [] }
}

const shown = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const environments = filters.value.environment ?? []
  return keys.value.filter(key => (showRevoked.value || !key.revoked_at)
    && (kindFilter.value === 'all' || key.kind === kindFilter.value)
    && (!environments.length || environments.includes(key.environment))
    && (!needle || key.name.toLowerCase().includes(needle) || key.public_id.includes(needle) || key.created_by.includes(needle)))
})

const revokedCount = computed(() => keys.value.length - active.value.length)

const COLUMNS: DataTableColumn<ApiKeyDef>[] = [
  { key: 'name', title: 'Key', fixed: true, minWidth: 250, skeleton: 'identity' },
  { key: 'kind', title: 'Kind', value: key => API_KEY_KIND_ORDER.indexOf(key.kind) },
  { key: 'environment', title: 'Environment', value: key => ENVIRONMENTS.indexOf(key.environment) },
  { key: 'used', title: 'Last used', value: key => (key.last_used_at ? Date.parse(key.last_used_at) : 0) },
  { key: 'requests', title: 'Requests, 24 h', align: 'end', value: key => requestsOf(key) },
  { key: 'created', title: 'Created', value: key => Date.parse(key.created_at) }
]

const columns = useLocalStorage('playersense.apikeys.columns', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'used', order: 'desc' }])

const stamp = (iso: string) => new Date(iso).toLocaleDateString('en', { month: 'short', day: 'numeric', year: 'numeric' })

// --- Revoking -----------------------------------------------------------------------------

const revoking = ref<ApiKeyDef | null>(null)
const revokeOpen = computed({
  get: () => revoking.value !== null,
  set: (value) => {
    if (!value) revoking.value = null
  }
})

function confirmRevoke() {
  if (revoking.value) revoke(revoking.value.id)
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="API keys">
      <template #description>
        The keys your game and your servers reach <span class="font-medium text-ink">{{ project.name }}</span> with. Each is one kind in one environment, and is shown once, when it is made.
      </template>
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          variant="flat"
          @click="openCreate()"
        >
          Create key
        </v-btn>
      </template>
    </PageHeader>

    <InfoNote text="Keys are sample data until this page is wired to the keys API, which exists: creating and revoking here work in this browser." />

    <!-- The three kinds, and where each goes: picking the wrong one is the mistake that matters. -->
    <ul class="m-0 grid list-none gap-3 p-0 md:grid-cols-3">
      <li v-for="kind in API_KEY_KIND_ORDER" :key="kind" class="flex flex-col gap-3 rounded-(--ps-radius-large) border border-line bg-surface p-4">
        <span class="flex items-center gap-2.5">
          <span aria-hidden="true" class="flex size-8 flex-none items-center justify-center rounded-(--ps-radius) bg-primary/10 text-base text-primary">
            <Icon :name="API_KEY_KINDS[kind].icon" />
          </span>
          <span class="flex min-w-0 flex-col">
            <span class="text-sm font-medium text-ink">{{ API_KEY_KINDS[kind].label }}</span>
            <span class="text-[11px] text-ink/50">{{ API_KEY_KINDS[kind].where }}</span>
          </span>
        </span>
        <span class="flex flex-1 flex-col gap-1.5 text-xs leading-5">
          <span class="text-ink/75">{{ API_KEY_KINDS[kind].can }}</span>
          <span :class="API_KEY_KINDS[kind].secret ? 'text-ink/55' : 'text-warning'" class="flex items-start gap-1.5 text-[11px] leading-4">
            <Icon :name="API_KEY_KINDS[kind].secret ? 'material-symbols:lock-outline' : 'material-symbols:lock-open-outline'" aria-hidden="true" class="mt-px flex-none text-sm" />
            {{ API_KEY_KINDS[kind].cannot }}
          </span>
        </span>
        <span class="flex items-center justify-between gap-2 border-t border-line/70 pt-3">
          <span class="text-[11px] text-ink/55"><span class="font-mono text-ink">{{ countOf(kind) }}</span> active</span>
          <v-btn
            class="text-xs"
            color="neutral"
            prepend-icon="material-symbols:add"
            size="small"
            variant="text"
            @click="openCreate(kind)"
          >
            Create
          </v-btn>
        </span>
      </li>
    </ul>

    <!-- Keys that want a decision. -->
    <SectionCard
      v-if="findings.length"
      :padded="false"
      description="Keys that work and may not need to. Every working key is one that can leak."
      title="Worth a look"
    >
      <ul class="m-0 flex list-none flex-col p-0">
        <li v-for="finding in findings" :key="finding.key.id" class="flex flex-wrap items-center gap-3 border-t border-line/70 px-5 py-3 first:border-t-0">
          <Icon :name="finding.icon" aria-hidden="true" class="flex-none text-base text-warning" />
          <span class="flex min-w-0 flex-1 basis-72 flex-col gap-0.5">
            <span class="text-xs leading-5 text-ink/80">{{ finding.text }}</span>
            <span class="font-mono text-[11px] text-ink/45">{{ finding.key.public_id }}</span>
          </span>
          <v-btn
            class="flex-none text-xs"
            color="error"
            size="small"
            variant="outlined"
            @click="revoking = finding.key"
          >
            Revoke
          </v-btn>
        </li>
      </ul>
    </SectionCard>

    <SectionCard :padded="false" description="Every key, with where it is used and when it last was. Only the public half is kept in view; the key itself was shown once." title="Keys">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <SegmentedControl v-model="kindFilter" :options="KIND_FILTERS" label="Kind" />
        <SearchField
          v-model="query"
          class="w-52"
          label="Search keys"
          placeholder="Name or public id"
        />
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
        <v-switch
          v-model="showRevoked"
          :label="`Revoked ${revokedCount}`"
          class="flex-none"
          color="primary"
          density="compact"
          hide-details
          inset
        />
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="shown"
        empty-description="No key matches these filters."
        empty-title="No keys match."
        item-value="id"
        label="API keys"
      >
        <template #cell.name="{ item }">
          <span :class="item.revoked_at ? 'opacity-55' : ''" class="flex min-w-0 items-center gap-3 py-1.5">
            <span aria-hidden="true" class="flex size-8 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/5 text-base text-ink/60">
              <Icon :name="item.revoked_at ? 'material-symbols:key-off-outline' : API_KEY_KINDS[item.kind].icon" />
            </span>
            <span class="flex min-w-0 flex-col">
              <span class="flex items-center gap-2">
                <span class="truncate text-[13px] text-ink">{{ item.name }}</span>
                <StatusBadge v-if="item.revoked_at" :label="`Revoked ${stamp(item.revoked_at)}`" tone="neutral" />
                <span v-else-if="item.replaced_by" class="rounded-full bg-warning/12 px-1.5 text-[10px] leading-4 font-medium text-warning">Replaced</span>
              </span>
              <span class="flex items-center gap-1">
                <span class="truncate font-mono text-[11px] text-ink/45">{{ item.public_id }}</span>
                <CopyButton :value="item.public_id" label="Copy the public id" />
              </span>
            </span>
          </span>
        </template>
        <template #cell.kind="{ item }">
          <span class="text-xs text-ink/70">{{ API_KEY_KINDS[item.kind].label }}</span>
        </template>
        <template #cell.environment="{ item }">
          <span :class="item.environment === 'production' ? 'bg-primary/10 text-primary' : 'bg-ink/5 text-ink/65'" class="rounded-full px-1.5 py-px text-[11px]">{{ ENVIRONMENT_LABELS[item.environment] }}</span>
        </template>
        <template #cell.used="{ item }">
          <span v-if="item.last_used_at" class="flex items-center gap-1.5 text-xs whitespace-nowrap text-ink/65">
            <span :class="time - Date.parse(item.last_used_at) < DAY ? 'bg-success' : time - Date.parse(item.last_used_at) > STALE_AFTER_DAYS * DAY ? 'bg-warning' : 'bg-ink/30'" aria-hidden="true" class="size-1.5 flex-none rounded-full" />
            {{ relativeWords(Date.parse(item.last_used_at), time) }}
          </span>
          <span v-else class="text-[11px] text-ink/40">Never</span>
        </template>
        <template #cell.requests="{ item }">
          <span v-if="requestsOf(item)" :title="`${writeFigure(requestsOf(item))} requests`" class="font-mono text-xs text-ink/75">{{ writeFigure(requestsOf(item), { compact: true }) }}</span>
          <span v-else class="font-mono text-xs text-ink/35">0</span>
        </template>
        <template #cell.created="{ item }">
          <span class="flex flex-col">
            <span class="text-xs whitespace-nowrap text-ink/65">{{ stamp(item.created_at) }}</span>
            <span class="truncate text-[11px] text-ink/40">{{ item.created_by }}</span>
          </span>
        </template>
        <template #actions="{ item }">
          <v-btn
            v-if="!item.revoked_at"
            :aria-label="`Actions for ${item.name}`"
            icon
            size="small"
            variant="text"
          >
            <Icon aria-hidden="true" class="text-lg" name="material-symbols:more-vert" />
            <v-menu activator="parent" location="bottom end" :offset="4">
              <v-list min-width="200" nav>
                <v-list-item prepend-icon="material-symbols:autorenew" title="Roll key" @click="openRoll(item)" />
                <v-list-item
                  base-color="error"
                  prepend-icon="material-symbols:block"
                  title="Revoke"
                  @click="revoking = item"
                />
              </v-list>
            </v-menu>
          </v-btn>
        </template>
      </DataTable>
    </SectionCard>

    <ApiKeyDialog
      v-if="creating"
      :key="dialogKey"
      v-model="creating"
      :kind="createKind"
      :replaces="rolling"
    />

    <ConfirmDialog
      v-if="revoking"
      v-model="revokeOpen"
      :description="`${revokeWords(revoking)} ${revoking.last_used_at ? `It was last used ${relativeWords(Date.parse(revoking.last_used_at), time)}.` : 'It has never been used.'} This cannot be undone.`"
      :title="`Revoke ${revoking.name}?`"
      action="Revoke key"
      tone="error"
      @confirm="confirmRevoke"
    />
  </div>
</template>
