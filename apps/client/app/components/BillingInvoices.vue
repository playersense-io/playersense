<script setup lang="ts">
// Billing > Invoices: every Stripe invoice (a top-up's receipt, the plan's), and the details
// Stripe prints on them. Those details are edited in the customer portal, not here: this page
// shows what Stripe holds, so the invoice and the screen can never disagree.
const { billing } = useBilling()

const details = computed<Fact[]>(() => {
  const value = billing.value.details
  return [
    { key: 'name', label: 'Billed to', value: value.name },
    { key: 'email', label: 'Sent to', value: value.email, info: 'Where Stripe emails each invoice and receipt.' },
    { key: 'address', label: 'Address', value: value.address[0] ?? '', note: [...value.address.slice(1), value.country].join(', ') },
    { key: 'tax', label: 'Tax ID', value: value.tax_id ?? 'None', mono: Boolean(value.tax_id) }
  ]
})

const editing = ref(false)

// --- The list --------------------------------------------------------------------------------

const COLUMNS: DataTableColumn<Invoice>[] = [
  { key: 'number', title: 'Invoice', fixed: true, mono: true, width: 160 },
  { key: 'issued_at', title: 'Issued', mono: true, width: 140, value: invoice => Date.parse(invoice.issued_at) },
  { key: 'description', title: 'Description', sortable: false },
  { key: 'kind', title: 'For', width: 130, skeleton: 'pill' },
  { key: 'status', title: 'Status', width: 140, skeleton: 'pill', value: invoice => INVOICE_STATUS[invoice.status].label },
  { key: 'total_micros', title: 'Total', align: 'end', mono: true, width: 130 }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'issued_at', order: 'desc' }])

const query = ref('')
const filters = ref<Record<string, string[]>>({ status: [] })

const FILTER_GROUPS: FilterGroup[] = [
  { key: 'status', label: 'Status', options: (Object.keys(INVOICE_STATUS) as InvoiceStatus[]).map(status => ({ value: status, label: INVOICE_STATUS[status].label })) }
]

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.status ?? []).map(status => ({ key: `status:${status}`, label: `Status: ${INVOICE_STATUS[status as InvoiceStatus].label}` })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') query.value = ''
  else filters.value = { status: (filters.value.status ?? []).filter(status => `status:${status}` !== key) }
}

function clearFilters() {
  query.value = ''
  filters.value = { status: [] }
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const statuses = filters.value.status ?? []
  return billing.value.invoices.filter(invoice => (!statuses.length || statuses.includes(invoice.status))
    && (!needle || invoice.number.toLowerCase().includes(needle) || invoice.description.toLowerCase().includes(needle)))
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <SectionCard description="What Stripe prints on every invoice. Change them in the Stripe customer portal: the next invoice carries the change, and those already issued keep theirs." title="Invoice details">
      <template #actions>
        <v-btn
          append-icon="material-symbols:open-in-new"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="editing = true"
        >
          Edit on Stripe
        </v-btn>
      </template>

      <FactGrid :facts="details" label="Invoice details" />
    </SectionCard>

    <SectionCard :padded="false" description="A receipt for every top-up and every plan payment, issued by Stripe." title="Invoices">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search invoices"
          placeholder="Number or description"
        />
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="matched"
        empty-description="No invoice matches these filters."
        empty-title="Nothing matches."
        label="Invoices"
      >
        <template #cell.number="{ item }">
          <span class="text-ink">{{ item.number }}</span>
        </template>
        <template #cell.issued_at="{ item }">
          {{ formatDate(item.issued_at) }}
        </template>
        <template #cell.description="{ item }">
          <span class="text-ink/80">{{ item.description }}</span>
        </template>
        <template #cell.kind="{ item }">
          <Tag :label="item.kind === 'top-up' ? 'Top-up' : 'Plan'" :tone="item.kind === 'top-up' ? 'success' : 'chart-1'" />
        </template>
        <template #cell.status="{ item }">
          <StatusBadge :label="INVOICE_STATUS[item.status].label" :tone="INVOICE_STATUS[item.status].tone" />
        </template>
        <template #cell.total_micros="{ item }">
          <span :class="item.status === 'void' ? 'text-ink/45 line-through' : 'text-ink'">{{ writeMicros(item.total_micros) }}</span>
        </template>

        <template #actions="{ item }">
          <span class="flex items-center justify-end gap-0.5">
            <v-btn
              :aria-label="`Download ${item.number} as a PDF`"
              :href="item.pdf_url"
              icon
              rel="noopener"
              size="small"
              target="_blank"
              variant="text"
            >
              <Icon aria-hidden="true" class="text-lg" name="material-symbols:download" />
              <v-tooltip activator="parent" location="top" text="PDF" />
            </v-btn>
            <v-btn
              :aria-label="`Open ${item.number} on Stripe`"
              :href="item.hosted_url"
              icon
              rel="noopener"
              size="small"
              target="_blank"
              variant="text"
            >
              <Icon aria-hidden="true" class="text-lg" name="material-symbols:open-in-new" />
              <v-tooltip activator="parent" location="top" text="Open on Stripe" />
            </v-btn>
          </span>
        </template>
      </DataTable>
    </SectionCard>

    <StripeHandoff
      v-if="editing"
      v-model="editing"
      :points="[
        'The Stripe customer portal opens on your billing information.',
        'Change the name, email, address or tax ID printed on invoices.',
        'Back here, this page shows what Stripe now holds.'
      ]"
      :run="() => 'Back from Stripe. The next invoice carries the details as they are now; issued invoices keep theirs.'"
      action="Open the customer portal"
      intro="Stripe holds the invoice details, so they are edited there and nowhere else."
      place="portal"
      title="Edit invoice details"
    />
  </div>
</template>
