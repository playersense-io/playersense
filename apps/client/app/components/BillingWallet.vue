<script setup lang="ts">
// Billing > Wallet: the credit the balance is made of, lot by lot, and every movement of it.
// A lot is a top-up or a promotion; usage is taken from the oldest first, and only a
// promotion can expire, so the lot that expires soonest is the one worth seeing first.
const { billing, balance } = useBilling()

defineEmits<{ topUp: [] }>()

const now = Date.now()
const DAY = 86_400_000

const lots = computed(() => [...billing.value.lots]
  .filter(lot => lot.remaining_micros > 0)
  .sort((a, b) => (a.expires_at ? Date.parse(a.expires_at) : Infinity) - (b.expires_at ? Date.parse(b.expires_at) : Infinity) || Date.parse(a.granted_at) - Date.parse(b.granted_at)))

const spentLots = computed(() => billing.value.lots.filter(lot => lot.remaining_micros <= 0).length)

function expiry(lot: CreditLot) {
  if (!lot.expires_at) return { text: 'Never expires', soon: false }
  const days = Math.ceil((Date.parse(lot.expires_at) - now) / DAY)
  return { text: days <= 0 ? 'Expires today' : `Expires in ${days} ${days === 1 ? 'day' : 'days'}`, soon: days <= 30 }
}

// --- The ledger ------------------------------------------------------------------------------

const COLUMNS: DataTableColumn<LedgerEntry>[] = [
  { key: 'at', title: 'Date', fixed: true, mono: true, width: 170, value: entry => Date.parse(entry.at) },
  { key: 'kind', title: 'Kind', width: 140, skeleton: 'pill', value: entry => LEDGER_KINDS[entry.kind].label },
  { key: 'description', title: 'Description', sortable: false },
  { key: 'amount_micros', title: 'Amount', align: 'end', mono: true, width: 140 },
  { key: 'balance_after_micros', title: 'Balance after', align: 'end', mono: true, width: 150 }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'at', order: 'desc' }])

const query = ref('')
const filters = ref<Record<string, string[]>>({ kind: [] })

const FILTER_GROUPS: FilterGroup[] = [
  { key: 'kind', label: 'Kind', options: (Object.keys(LEDGER_KINDS) as LedgerKind[]).map(kind => ({ value: kind, label: LEDGER_KINDS[kind].label })) }
]

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.kind ?? []).map(kind => ({ key: `kind:${kind}`, label: `Kind: ${LEDGER_KINDS[kind as LedgerKind].label}` })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') query.value = ''
  else filters.value = { kind: (filters.value.kind ?? []).filter(kind => `kind:${kind}` !== key) }
}

function clearFilters() {
  query.value = ''
  filters.value = { kind: [] }
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const kinds = filters.value.kind ?? []
  return billing.value.ledger.filter(entry => (!kinds.length || kinds.includes(entry.kind))
    && (!needle || entry.description.toLowerCase().includes(needle)))
})

function exportLedger() {
  downloadCsv('wallet-ledger', [
    ['Date', 'Kind', 'Description', 'Amount (USD)', 'Balance after (USD)'],
    ...matched.value.map(entry => [entry.at, LEDGER_KINDS[entry.kind].label, entry.description, entry.amount_micros / MICROS, entry.balance_after_micros / MICROS])
  ])
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard description="What the balance is made of. Usage is taken from the oldest credit first; bought credit never expires, a promotion can." title="Credit">
      <template #actions>
        <span class="font-mono text-sm text-ink">{{ writeMicros(balance) }}</span>
      </template>

      <ul v-if="lots.length" class="m-0 flex list-none flex-col divide-y divide-line p-0">
        <li v-for="lot in lots" :key="lot.id" class="flex flex-wrap items-center gap-x-6 gap-y-3 py-3.5 first:pt-0 last:pb-0">
          <span class="flex min-w-48 flex-1 items-center gap-3">
            <span
              :style="{ color: `rgb(var(--v-theme-${lot.kind === 'promo' ? 'chart-6' : 'primary'}))`, background: `rgba(var(--v-theme-${lot.kind === 'promo' ? 'chart-6' : 'primary'}), 0.1)` }"
              aria-hidden="true"
              class="flex size-8 flex-none items-center justify-center rounded-(--ps-radius) text-base"
            >
              <Icon :name="lot.kind === 'promo' ? 'material-symbols:featured-seasonal-and-gifts-outline' : 'material-symbols:account-balance-wallet-outline'" />
            </span>
            <span class="flex min-w-0 flex-col">
              <span class="truncate text-sm text-ink">{{ lot.description ?? (lot.kind === 'promo' ? 'Promotion' : 'Top-up') }}</span>
              <span class="text-[11px] text-ink/55">{{ lot.kind === 'promo' ? 'Granted' : 'Bought' }} {{ formatDate(lot.granted_at) }}</span>
            </span>
          </span>

          <span class="flex w-56 flex-col gap-1.5">
            <span class="flex items-baseline justify-between gap-2 text-[11px] text-ink/55">
              <span><span class="font-mono text-xs text-ink">{{ writeMicros(lot.remaining_micros) }}</span> left</span>
              <span class="font-mono">of {{ writeMicros(lot.amount_micros) }}</span>
            </span>
            <span class="h-1.5 overflow-hidden rounded-full bg-ink/8">
              <span :style="{ width: `${Math.round((lot.remaining_micros / lot.amount_micros) * 100)}%` }" class="block h-full rounded-full bg-primary" />
            </span>
          </span>

          <span :class="expiry(lot).soon ? 'text-warning' : 'text-ink/55'" class="flex w-36 items-center justify-end gap-1 text-[11px]">
            <Icon
              v-if="lot.expires_at"
              aria-hidden="true"
              class="text-sm"
              name="material-symbols:schedule-outline"
            />
            {{ expiry(lot).text }}
          </span>
        </li>
      </ul>

      <EmptyState
        v-else
        description="Top up to keep collecting past the plan's allowance. Usage is taken from the wallet every day."
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

      <p v-if="spentLots" class="m-0 mt-4 border-t border-line pt-3 text-[11px] text-ink/50">
        {{ spentLots }} earlier {{ spentLots === 1 ? 'lot is' : 'lots are' }} spent in full, and in the ledger below.
      </p>
    </SectionCard>

    <SectionCard :padded="false" description="Every movement of the wallet: top-ups, promotions, usage taken each day, the plan, refunds and credit that expired." title="Ledger">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search the ledger"
          placeholder="Description"
        />
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
          <Tag :label="LEDGER_KINDS[item.kind].label" :tone="LEDGER_KINDS[item.kind].tone" />
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
    </SectionCard>
  </div>
</template>
