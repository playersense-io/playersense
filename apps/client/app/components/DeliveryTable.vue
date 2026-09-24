<script setup lang="ts">
// The deliveries log: every event sent, newest first, with where it stands, what the server
// last answered and how many tries it took. Narrowed by status in one click, since "what
// failed" is why most people open it, and by event, destination and environment under Filter.
// A row opens the delivery; ticking rows resends them together, for the morning after an
// outage.
const props = withDefaults(defineProps<{
  deliveries: readonly WebhookDelivery[]
  /** Every destination the deliveries go to. With more than one, the table says which. */
  destinations: readonly WebhookDestination[]
  loading?: boolean
  /** The status the log opens on: "retrying", from a warning that links here. */
  initialStatus?: 'all' | DeliveryStatus
}>(), {
  loading: false,
  initialStatus: 'all'
})

const { resend } = useWebhooks()

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

const byId = computed(() => new Map(props.destinations.map(entry => [entry.id, entry])))
const several = computed(() => props.destinations.length > 1)

// --- Narrowing ----------------------------------------------------------------------------

const status = ref<'all' | DeliveryStatus>(props.initialStatus)
const query = ref('')
const filters = ref<Record<string, string[]>>({ type: [], destination: [], environment: [] })

const counts = computed(() => ({
  all: props.deliveries.length,
  succeeded: props.deliveries.filter(entry => entry.status === 'succeeded').length,
  retrying: props.deliveries.filter(entry => entry.status === 'retrying').length,
  failed: props.deliveries.filter(entry => entry.status === 'failed').length
}))

const STATUS_OPTIONS = computed<SegmentedOption[]>(() => [
  { value: 'all', label: `All ${writeFigure(counts.value.all, { compact: true })}` },
  { value: 'succeeded', label: `Delivered ${writeFigure(counts.value.succeeded, { compact: true })}` },
  { value: 'retrying', label: `Retrying ${counts.value.retrying}` },
  { value: 'failed', label: `Failed ${counts.value.failed}` }
])

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
  return props.deliveries.filter(entry => (status.value === 'all' || entry.status === status.value)
    && (!type.length || type.includes(entry.type))
    && (!destination.length || destination.includes(entry.destinationId))
    && (!environment.length || environment.includes(entry.environment))
    && (!needle || entry.eventId.toLowerCase().includes(needle) || entry.id.toLowerCase().includes(needle) || entry.type.includes(needle)))
})

// --- The table ----------------------------------------------------------------------------

const COLUMNS = computed<DataTableColumn<WebhookDelivery>[]>(() => [
  { key: 'type', title: 'Event', fixed: true, minWidth: 240, skeleton: 'identity' },
  { key: 'status', title: 'Status', value: entry => DELIVERY_STATUSES.indexOf(entry.status), skeleton: 'pill' },
  ...(several.value ? [{ key: 'destination', title: 'Destination', minWidth: 180, value: (entry: WebhookDelivery) => byId.value.get(entry.destinationId)?.name ?? '' }] : []),
  { key: 'answer', title: 'Last answer', value: entry => entry.attempts.at(-1)?.code ?? 0 },
  { key: 'attempts', title: 'Tries', align: 'end', value: entry => entry.attempts.length },
  { key: 'environment', title: 'Environment', value: entry => ENVIRONMENT_LABELS[entry.environment], hidden: !several.value },
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
const resent = ref<{ total: number, ok: number } | null>(null)

function resendSelected() {
  let ok = 0
  for (const entry of resendable.value) {
    const destination = byId.value.get(entry.destinationId)
    if (destination && answerOk(resend(destination, entry))) ok++
  }
  resent.value = { total: resendable.value.length, ok }
  selected.value = []
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
      Resent {{ resent.total }}: {{ resent.ok }} delivered{{ resent.total - resent.ok ? `, ${resent.total - resent.ok} failed again and stay on the schedule` : '' }}.
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
      :empty-description="deliveries.length ? 'No delivery matches these filters.' : 'Events will be listed here as they are sent. Send a test event to see one now.'"
      :empty-title="deliveries.length ? 'No deliveries match.' : 'No deliveries yet.'"
      item-value="id"
      label="Deliveries"
    >
      <template #selection>
        <v-btn
          :disabled="!resendable.length"
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
        <span v-if="item.attempts.length" class="flex items-baseline gap-2 whitespace-nowrap">
          <span :class="answerOk(item.attempts.at(-1)!) ? 'text-ink/75' : 'text-error'" class="font-mono text-xs">{{ answerWords(item.attempts.at(-1)!) }}</span>
          <span class="font-mono text-[11px] text-ink/40">{{ msWords(item.attempts.at(-1)!.ms) }}</span>
        </span>
        <span v-else class="text-[11px] text-ink/40">Not tried yet</span>
      </template>
      <template #cell.attempts="{ item }">
        <span :class="item.attempts.length > 1 ? 'text-ink/75' : 'text-ink/40'" class="font-mono text-xs">{{ item.attempts.length }}</span>
      </template>
      <template #cell.environment="{ item }">
        <span class="text-xs text-ink/65">{{ ENVIRONMENT_LABELS[item.environment] }}</span>
      </template>
      <template #cell.created="{ item }">
        <span :title="stamp(item.created)" class="text-xs whitespace-nowrap text-ink/60">{{ relativeWords(item.created, time) }}</span>
      </template>
    </DataTable>

    <DeliveryDialog
      v-if="opened && byId.get(opened.destinationId)"
      v-model="openModel"
      :delivery="opened"
      :destination="byId.get(opened.destinationId)!"
    />
  </div>
</template>
