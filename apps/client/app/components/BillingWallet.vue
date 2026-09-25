<script setup lang="ts">
// Billing > Wallet: the credit the balance is made of, lot by lot, and every movement of it,
// each a table. A lot is a top-up or a promotion; usage is taken from the oldest first, and
// only a promotion can expire, so an expiry coming up is drawn in the warning ink.
const store = useBillingStore()
const { lots: allLots, ledger, ledgerNext, balance } = storeToRefs(store)
const loadingMore = ref(false)

async function more() {
  loadingMore.value = true
  try {
    await store.moreLedger()
  } finally {
    loadingMore.value = false
  }
}

defineEmits<{ topUp: [] }>()

const now = Date.now()
const DAY = 86_400_000

// --- Credit, lot by lot ----------------------------------------------------------------------

type LotState = 'available' | 'spent' | 'expired'

const LOT_STATES: Record<LotState, { label: string, tone: BadgeTone }> = {
  available: { label: 'Available', tone: 'success' },
  spent: { label: 'Spent', tone: 'neutral' },
  expired: { label: 'Expired', tone: 'warning' }
}

const LOT_KINDS: Record<CreditLot['kind'], { label: string, tone: string }> = {
  purchase: { label: 'Bought', tone: 'primary' },
  promo: { label: 'Promotion', tone: 'chart-6' }
}

function stateOf(lot: CreditLot): LotState {
  if (lot.expires_at && Date.parse(lot.expires_at) <= now) return 'expired'
  return lot.remaining_micros > 0 ? 'available' : 'spent'
}

function expiry(lot: CreditLot) {
  if (!lot.expires_at) return { text: 'Never', soon: false }
  const days = Math.ceil((Date.parse(lot.expires_at) - now) / DAY)
  if (days <= 0) return { text: `Expired ${formatDate(lot.expires_at)}`, soon: false }
  return { text: `In ${days} ${days === 1 ? 'day' : 'days'}`, soon: days <= 30 && lot.remaining_micros > 0 }
}

const LOT_COLUMNS: DataTableColumn<CreditLot>[] = [
  { key: 'description', title: 'Credit', fixed: true, value: lot => lot.description ?? '' },
  { key: 'state', title: 'Status', width: 130, skeleton: 'pill', value: lot => LOT_STATES[stateOf(lot)].label },
  { key: 'remaining_micros', title: 'Left', align: 'end', mono: true, width: 200 },
  { key: 'amount_micros', title: 'Of', align: 'end', mono: true, width: 130 },
  { key: 'granted_at', title: 'Added', mono: true, width: 140, value: lot => Date.parse(lot.granted_at) },
  { key: 'expires_at', title: 'Expires', width: 160, value: lot => (lot.expires_at ? Date.parse(lot.expires_at) : Number.MAX_SAFE_INTEGER) }
]

const lotColumns = ref(defaultColumnState(LOT_COLUMNS))
/* Oldest first: the order usage spends them in. */
const lotSortBy = ref<DataTableSort[]>([{ key: 'granted_at', order: 'asc' }])

const lotFilters = ref<Record<string, string[]>>({ kind: [], state: [] })

const LOT_FILTER_GROUPS: FilterGroup[] = [
  { key: 'kind', label: 'Kind', options: (Object.keys(LOT_KINDS) as CreditLot['kind'][]).map(kind => ({ value: kind, label: LOT_KINDS[kind].label })) },
  { key: 'state', label: 'Status', options: (Object.keys(LOT_STATES) as LotState[]).map(state => ({ value: state, label: LOT_STATES[state].label })) }
]

const lotActiveFilters = computed<ActiveFilter[]>(() => [
  ...(lotFilters.value.kind ?? []).map(kind => ({ key: `kind:${kind}`, label: `Kind: ${LOT_KINDS[kind as CreditLot['kind']].label}` })),
  ...(lotFilters.value.state ?? []).map(state => ({ key: `state:${state}`, label: `Status: ${LOT_STATES[state as LotState].label}` }))
])

function removeLotFilter(key: string) {
  const [group, value] = key.split(':') as [string, string]
  lotFilters.value = { ...lotFilters.value, [group]: (lotFilters.value[group] ?? []).filter(entry => entry !== value) }
}

function clearLotFilters() {
  lotFilters.value = { kind: [], state: [] }
}

const lots = computed(() => {
  const kinds = lotFilters.value.kind ?? []
  const states = lotFilters.value.state ?? []
  return allLots.value.filter(lot => (!kinds.length || kinds.includes(lot.kind))
    && (!states.length || states.includes(stateOf(lot))))
})

// --- The ledger ------------------------------------------------------------------------------

const COLUMNS: DataTableColumn<LedgerEntry>[] = [
  { key: 'at', title: 'Date', fixed: true, mono: true, width: 170, value: entry => Date.parse(entry.at) },
  { key: 'kind', title: 'Kind', width: 140, skeleton: 'pill', value: entry => LEDGER_KINDS[entry.kind]?.label ?? entry.kind },
  { key: 'description', title: 'Description', sortable: false },
  { key: 'amount_micros', title: 'Amount', align: 'end', mono: true, width: 140 },
  { key: 'balance_after_micros', title: 'Balance after', align: 'end', mono: true, width: 150 }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'at', order: 'desc' }])

const filters = ref<Record<string, string[]>>({ kind: [] })

const FILTER_GROUPS: FilterGroup[] = [
  { key: 'kind', label: 'Kind', options: (Object.keys(LEDGER_KINDS) as LedgerKind[]).map(kind => ({ value: kind, label: LEDGER_KINDS[kind].label })) }
]

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.kind ?? []).map(kind => ({ key: `kind:${kind}`, label: `Kind: ${LEDGER_KINDS[kind as LedgerKind].label}` }))
])

function removeFilter(key: string) {
  filters.value = { kind: (filters.value.kind ?? []).filter(kind => `kind:${kind}` !== key) }
}

function clearFilters() {
  filters.value = { kind: [] }
}

const matched = computed(() => {
  const kinds = filters.value.kind ?? []
  return ledger.value.filter(entry => !kinds.length || kinds.includes(entry.kind))
})

function exportLedger() {
  downloadCsv('wallet-ledger', [
    ['Date', 'Kind', 'Description', 'Amount (USD)', 'Balance after (USD)'],
    ...matched.value.map(entry => [entry.at, LEDGER_KINDS[entry.kind]?.label ?? entry.kind, entry.description, entry.amount_micros / MICROS, entry.balance_after_micros / MICROS])
  ])
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard :padded="false" description="What the balance is made of. Usage is taken from the oldest credit first; bought credit never expires, a promotion can." title="Credit">
      <template #actions>
        <span class="font-mono text-sm text-ink">{{ writeMicros(balance) }}</span>
      </template>

      <TableToolbar :filters="lotActiveFilters" @clear="clearLotFilters" @remove="removeLotFilter">
        <FilterMenu v-model="lotFilters" :groups="LOT_FILTER_GROUPS" />
        <DataTableColumnsMenu v-model="lotColumns" :columns="LOT_COLUMNS" />
      </TableToolbar>

      <DataTable
        v-model:column-state="lotColumns"
        v-model:sort-by="lotSortBy"
        :columns="LOT_COLUMNS"
        :items="lots"
        empty-description="No credit matches these filters."
        empty-title="Nothing matches."
        label="Credit"
      >
        <template v-if="!allLots.length" #empty>
          <EmptyState
            description="Top up to keep collecting past the plan's allowance. Usage is taken from the wallet as it happens."
            icon="material-symbols:account-balance-wallet-outline"
            title="The wallet is empty."
          >
            <template #actions>
              <v-btn
                class="text-xs font-medium"
                color="primary"
                size="small"
                variant="flat"
                @click="$emit('topUp')"
              >
                Top up
              </v-btn>
            </template>
          </EmptyState>
        </template>
        <template #cell.description="{ item }">
          <span class="flex min-w-0 items-center gap-3">
            <span
              :style="{ color: `rgb(var(--v-theme-${item.kind === 'promo' ? 'chart-6' : 'primary'}))`, background: `rgba(var(--v-theme-${item.kind === 'promo' ? 'chart-6' : 'primary'}), 0.1)` }"
              aria-hidden="true"
              class="flex size-7 flex-none items-center justify-center rounded-(--ps-radius-small) text-sm"
            >
              <Icon :name="item.kind === 'promo' ? 'material-symbols:featured-seasonal-and-gifts-outline' : 'material-symbols:account-balance-wallet-outline'" />
            </span>
            <span class="flex min-w-0 flex-col">
              <span class="truncate text-ink">{{ item.description ?? LOT_KINDS[item.kind].label }}</span>
              <span class="truncate text-[11px] text-ink/50">{{ LOT_KINDS[item.kind].label }}</span>
            </span>
          </span>
        </template>
        <template #cell.state="{ item }">
          <StatusBadge :label="LOT_STATES[stateOf(item)].label" :tone="LOT_STATES[stateOf(item)].tone" />
        </template>
        <template #cell.remaining_micros="{ item }">
          <span class="flex items-center justify-end gap-2.5">
            <span aria-hidden="true" class="h-1.5 w-16 overflow-hidden rounded-full bg-ink/8">
              <span :style="{ width: `${item.amount_micros ? Math.round((item.remaining_micros / item.amount_micros) * 100) : 0}%` }" class="block h-full rounded-full bg-primary" />
            </span>
            <span :class="item.remaining_micros > 0 ? 'text-ink' : 'text-ink/45'">{{ writeMicros(item.remaining_micros) }}</span>
          </span>
        </template>
        <template #cell.amount_micros="{ item }">
          <span class="text-ink/65">{{ writeMicros(item.amount_micros) }}</span>
        </template>
        <template #cell.granted_at="{ item }">
          {{ formatDate(item.granted_at) }}
        </template>
        <template #cell.expires_at="{ item }">
          <span :class="expiry(item).soon ? 'text-warning' : 'text-ink/60'" class="flex items-center gap-1 text-xs">
            <Icon
              v-if="expiry(item).soon"
              :aria-hidden="true"
              class="text-sm"
              name="material-symbols:schedule-outline"
            />
            {{ expiry(item).text }}
          </span>
        </template>
      </DataTable>
    </SectionCard>

    <SectionCard :padded="false" description="Every movement of the wallet: top-ups, promotions, usage as it is taken, the plan, refunds and credit that expired." title="Ledger">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
        <v-btn
          :disabled="!matched.length"
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:download"
          size="small"
          variant="text"
          @click="exportLedger"
        >
          CSV
        </v-btn>
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="matched"
        empty-description="No movement matches these filters."
        empty-title="Nothing matches."
        label="Wallet ledger"
      >
        <template #cell.at="{ item }">
          {{ formatDateTime(item.at) }}
        </template>
        <template #cell.kind="{ item }">
          <Tag :label="LEDGER_KINDS[item.kind]?.label ?? item.kind" :tone="LEDGER_KINDS[item.kind]?.tone ?? 'neutral'" />
        </template>
        <template #cell.description="{ item }">
          <span class="text-ink/80">{{ item.description }}</span>
        </template>
        <template #cell.amount_micros="{ item }">
          <span :class="item.amount_micros > 0 ? 'text-success' : 'text-ink'">{{ writeSignedMicros(item.amount_micros) }}</span>
        </template>
        <template #cell.balance_after_micros="{ item }">
          <span class="text-ink/65">{{ writeMicros(item.balance_after_micros) }}</span>
        </template>
      </DataTable>

      <div v-if="ledgerNext" class="flex justify-center border-t border-line px-5 py-3">
        <v-btn
          :loading="loadingMore"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="more"
        >
          Load older movements
        </v-btn>
      </div>
    </SectionCard>
  </div>
</template>
