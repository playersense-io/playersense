<script setup lang="ts">
// The deliveries log: events sent, newest first, with where each stands, what the server last
// answered and how many tries it took. Narrowed by status in one click, since "what failed" is
// why most people open it; the status is asked of the API, the rest narrows what was read. A
// row opens the delivery; ticking rows resends them together, for the morning after an outage.
const props = withDefaults(defineProps<{
  deliveries: readonly WebhookDelivery[]
  /** Every destination the deliveries go to. With more than one, the table says which. */
  destinations: readonly WebhookDestination[]
  loading?: boolean
  /** Older deliveries can be read. */
  hasMore?: boolean
  loadingMore?: boolean
}>(), {
  loading: false,
  hasMore: false,
  loadingMore: false
})

/** The status the log is narrowed to, read by the page's query. */
const status = defineModel<'all' | DeliveryStatus>('status', { default: 'all' })

const emit = defineEmits<{ more: [], changed: [] }>()

const { resend } = useWebhookActions()

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const byId = computed(() => new Map(props.destinations.map(entry => [entry.id, entry])))
const several = computed(() => props.destinations.length > 1)

// --- Narrowing ----------------------------------------------------------------------------

const query = ref('')
const filters = ref<Record<string, string[]>>({ type: [], destination: [], environment: [] })

const STATUS_OPTIONS: SegmentedOption[] = [
  { value: 'all', label: 'All' },
  { value: 'succeeded', label: 'Delivered' },
  { value: 'retrying', label: 'Retrying' },
  { value: 'failed', label: 'Failed' }
]

const statusModel = computed({
  get: () => status.value as string,
  set: (value: string) => {
    status.value = value as 'all' | DeliveryStatus
  }
})

const FILTER_GROUPS = computed<FilterGroup[]>(() => [
  { key: 'type', label: 'Event', noun: 'event', options: [...new Set(props.deliveries.map(entry => entry.type))].sort().map(value => ({ value, label: value })) },
  ...(several.value ? [{ key: 'destination', label: 'Destination', options: props.destinations.map(entry => ({ value: entry.id, label: entry.name })) }] : []),
  { key: 'environment', label: 'Environment', options: ENVIRONMENTS.slice().reverse().map(value => ({ value, label: ENVIRONMENT_LABELS[value] })) }
])

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.type ?? []).map(value => ({ key: `type:${value}`, label: value })),
  ...(filters.value.destination ?? []).map(value => ({ key: `destination:${value}`, label: `To ${byId.value.get(value)?.name ?? value}` })),
  ...(filters.value.environment ?? []).map(value => ({ key: `environment:${value}`, label: ENVIRONMENT_LABELS[value as EnvironmentKind] })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') {
    query.value = ''
    return
  }
  const [group, value] = [key.slice(0, key.indexOf(':')), key.slice(key.indexOf(':') + 1)]
  filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(entry => entry !== value) }
}

function clearFilters() {
  query.value = ''
  filters.value = { type: [], destination: [], environment: [] }
}

const shown = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const { type = [], destination = [], environment = [] } = filters.value
  return props.deliveries.filter(entry => (!type.length || type.includes(entry.type))
    && (!destination.length || destination.includes(entry.destinationId))
    && (!environment.length || (entry.environment !== undefined && environment.includes(entry.environment)))
    && (!needle || entry.eventId.toLowerCase().includes(needle) || entry.id.toLowerCase().includes(needle) || entry.type.includes(needle)))
})

// --- The table ----------------------------------------------------------------------------

const COLUMNS = computed<DataTableColumn<WebhookDelivery>[]>(() => [
  { key: 'type', title: 'Event', fixed: true, minWidth: 240, skeleton: 'identity' },
  { key: 'status', title: 'Status', value: entry => DELIVERY_STATUSES.indexOf(entry.status), skeleton: 'pill' },
  ...(several.value ? [{ key: 'destination', title: 'Destination', minWidth: 180, value: (entry: WebhookDelivery) => byId.value.get(entry.destinationId)?.name ?? '' }] : []),
  { key: 'answer', title: 'Last answer', value: entry => entry.lastCode ?? 0 },
  { key: 'attempts', title: 'Tries', align: 'end', value: entry => entry.attempts },
  { key: 'environment', title: 'Environment', value: entry => (entry.environment ? ENVIRONMENT_LABELS[entry.environment] : ''), hidden: !several.value },
  { key: 'created', title: 'Created', align: 'end', value: entry => entry.created },
  { key: 'eventId', title: 'Event id', mono: true, hidden: true }
])

const columns = ref(defaultColumnState(COLUMNS.value))
watch(several, () => {
  columns.value = defaultColumnState(COLUMNS.value)
})
const sortBy = ref<DataTableSort[]>([{ key: 'created', order: 'desc' }])
const selected = ref<string[]>([])

const stamp = (at: number) => new Date(at).toLocaleString('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

// --- Opening one, and resending -----------------------------------------------------------

const openId = ref<string | null>(null)
const opened = computed(() => props.deliveries.find(entry => entry.id === openId.value) ?? null)
const openModel = computed({
  get: () => opened.value !== null,
  set: (value) => {
    if (!value) openId.value = null
  }
})

const selectedDeliveries = computed(() => props.deliveries.filter(entry => selected.value.includes(entry.id)))
const resendable = computed(() => selectedDeliveries.value.filter(entry => byId.value.get(entry.destinationId)?.status === 'enabled'))
const resent = ref<{ total: number, ok: number, refused: string } | null>(null)
const resending = ref(false)

/* One at a time: each is a try made now, and a server just back up should not get them all at once. */
async function resendSelected() {
  resending.value = true
  let ok = 0
  let refused = ''
  const batch = resendable.value
  for (const entry of batch) {
    try {
      if (answerOk(await resend(entry.destinationId, entry.id))) ok++
    } catch (caught) {
      refused ||= caught instanceof Error ? caught.message : String(caught)
    }
  }
  resent.value = { total: batch.length, ok, refused }
  selected.value = []
  resending.value = false
  emit('changed')
}
</script>

<template>
  <div class="flex flex-col">
    <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
      <SegmentedControl v-model="statusModel" :options="STATUS_OPTIONS" label="Status" />
      <SearchField
        v-model="query"
        class="w-56"
        label="Search deliveries"
        placeholder="Event id or type"
      />
      <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
      <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
    </TableToolbar>

    <p v-if="resent" class="m-0 flex items-center gap-2 border-b border-line bg-ink/2 px-4 py-2 text-xs text-ink/75">
      <Icon aria-hidden="true" class="text-sm text-primary" name="material-symbols:send-outline" />
      Resent {{ resent.total }}: {{ resent.ok }} delivered{{ resent.total - resent.ok ? `, ${resent.total - resent.ok} not` : '' }}.{{ resent.refused ? ` ${resent.refused}` : '' }}
      <button class="ml-auto cursor-pointer text-ink/50 hover:text-ink" type="button" @click="resent = null">
        Dismiss
      </button>
    </p>

    <DataTable
      v-model:column-state="columns"
      v-model:selected="selected"
      v-model:sort-by="sortBy"
      :columns="COLUMNS"
      :items="shown"
      :loading="loading"
      :selectable="true"
      :empty-description="deliveries.length ? 'No delivery matches these filters.' : status !== 'all' ? 'None with this status in the last 30 days.' : 'Events will be listed here as they are sent. Send a test event to see one now.'"
      :empty-title="deliveries.length ? 'No deliveries match.' : 'No deliveries yet.'"
      item-value="id"
      label="Deliveries"
    >
      <template #selection>
        <v-btn
          :disabled="!resendable.length"
          :loading="resending"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:send-outline"
          size="small"
          variant="text"
          @click="resendSelected"
        >
          Resend {{ resendable.length }}
        </v-btn>
      </template>
      <template #cell.type="{ item }">
        <button class="group flex min-w-0 cursor-pointer flex-col py-1 text-left" type="button" @click="openId = item.id">
          <span class="flex items-center gap-1.5">
            <span class="truncate font-mono text-xs text-ink transition-colors group-hover:text-primary">{{ item.type }}</span>
            <span v-if="item.test" class="rounded-full bg-ink/6 px-1.5 text-[10px] leading-4 font-medium text-ink/60">Test</span>
          </span>
          <span class="truncate font-mono text-[11px] text-ink/40">{{ item.eventId }}</span>
        </button>
      </template>
      <template #cell.status="{ item }">
        <span class="flex flex-col items-start gap-0.5">
          <StatusBadge :label="DELIVERY_STATUS[item.status].label" :tone="DELIVERY_STATUS[item.status].tone" />
          <span v-if="item.status === 'retrying' && item.next" class="text-[11px] text-ink/45">next {{ relativeWords(item.next, time) }}</span>
        </span>
      </template>
      <template #cell.destination="{ item }">
        <NuxtLink :to="`/webhooks/${item.destinationId}`" class="truncate text-xs text-ink/70 no-underline hover:text-primary">
          {{ byId.get(item.destinationId)?.name }}
        </NuxtLink>
      </template>
      <template #cell.answer="{ item }">
        <span v-if="item.attempts" class="flex items-baseline gap-2 whitespace-nowrap">
          <span :class="answerOk({ code: item.lastCode }) ? 'text-ink/75' : 'text-error'" class="font-mono text-xs">{{ answerWords({ code: item.lastCode, error: item.lastError }) }}</span>
        </span>
        <span v-else class="text-[11px] text-ink/40">Not tried yet</span>
      </template>
      <template #cell.attempts="{ item }">
        <span :class="item.attempts > 1 ? 'text-ink/75' : 'text-ink/40'" class="font-mono text-xs">{{ item.attempts }}</span>
      </template>
      <template #cell.environment="{ item }">
        <span class="text-xs text-ink/65">{{ item.environment ? ENVIRONMENT_LABELS[item.environment] : 'Project' }}</span>
      </template>
      <template #cell.created="{ item }">
        <span :title="stamp(item.created)" class="text-xs whitespace-nowrap text-ink/60">{{ relativeWords(item.created, time) }}</span>
      </template>
    </DataTable>

    <div v-if="hasMore" class="flex justify-center border-t border-line px-5 py-3">
      <v-btn
        :loading="loadingMore"
        class="text-xs font-medium"
        color="neutral"
        size="small"
        variant="text"
        @click="emit('more')"
      >
        Load older deliveries
      </v-btn>
    </div>

    <DeliveryDialog
      v-if="opened && byId.get(opened.destinationId)"
      v-model="openModel"
      :delivery="opened"
      :destination="byId.get(opened.destinationId)!"
      @changed="emit('changed')"
    />
  </div>
</template>
