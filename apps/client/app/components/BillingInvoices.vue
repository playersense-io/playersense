<script setup lang="ts">
// Billing > Receipts: every Stripe receipt (a top-up's, the plan's), an invoiced organization's
// monthly invoices, and the details Stripe prints on them. Those details are edited in the
// customer portal, not here: this page shows what Stripe holds, so the two never disagree.
import type { ContractInvoice } from '~/stores/billing'

const store = useBillingStore()
const { receipts, receiptsNext, invoices, invoiced, details: held } = storeToRefs(store)

const details = computed<Fact[]>(() => {
  const value = held.value
  if (!value) return []
  return [
    { key: 'name', label: 'Billed to', value: value.name || 'Not set' },
    { key: 'email', label: 'Sent to', value: value.email || 'Not set', info: 'Where Stripe emails each invoice and receipt.' },
    { key: 'address', label: 'Address', value: value.address[0] ?? 'Not set', note: [...value.address.slice(1), value.country ?? ''].filter(Boolean).join(', ') },
    { key: 'tax', label: 'Tax ID', value: value.tax_ids.join(', ') || 'None', mono: value.tax_ids.length > 0 }
  ]
})

const editing = ref(false)
const portal = () => store.portalUrl()

const loadingMore = ref(false)
async function more() {
  loadingMore.value = true
  try {
    await store.moreReceipts()
  } finally {
    loadingMore.value = false
  }
}

// --- An invoiced organization's monthly invoices -------------------------------------------

const CONTRACT_STATUS: Record<string, { label: string, tone: BadgeTone }> = {
  draft: { label: 'Draft', tone: 'neutral' },
  open: { label: 'Open', tone: 'warning' },
  paid: { label: 'Paid', tone: 'success' },
  void: { label: 'Void', tone: 'neutral' },
  uncollectible: { label: 'Uncollectible', tone: 'error' }
}

const contractStatus = (invoice: ContractInvoice) => invoice.overdue
  ? { label: 'Overdue', tone: 'error' as BadgeTone }
  : CONTRACT_STATUS[invoice.status] ?? { label: invoice.status, tone: 'neutral' as BadgeTone }

// --- The list --------------------------------------------------------------------------------

const COLUMNS: DataTableColumn<Invoice>[] = [
  { key: 'number', title: 'Invoice', fixed: true, mono: true, width: 160 },
  { key: 'issued_at', title: 'Issued', mono: true, width: 140, value: invoice => Date.parse(invoice.issued_at) },
  { key: 'description', title: 'Description', sortable: false },
  { key: 'kind', title: 'For', width: 130, skeleton: 'pill' },
  { key: 'status', title: 'Status', width: 140, skeleton: 'pill', value: invoice => INVOICE_STATUS[invoice.status]?.label ?? invoice.status },
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
  return receipts.value.filter(invoice => (!statuses.length || statuses.includes(invoice.status))
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

      <FactGrid v-if="details.length" :facts="details" label="Invoice details" />
      <p v-else class="m-0 text-xs text-ink/55">
        Nothing yet. Stripe holds them from the first payment, and they can be set in the portal.
      </p>
    </SectionCard>

    <SectionCard
      v-if="invoiced"
      :padded="false"
      description="Your contract's monthly invoices: what the month's usage and fees came to, and when each is due."
      title="Monthly invoices"
    >
      <p v-if="!invoices.length" class="m-0 px-5 py-4 text-xs text-ink/55">
        No invoice yet. The first is issued when the current period closes.
      </p>
      <ul v-else class="m-0 flex list-none flex-col divide-y divide-line p-0">
        <li v-for="invoice in invoices" :key="invoice.id" class="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3">
          <span class="flex min-w-0 flex-1 basis-56 flex-col">
            <span class="font-mono text-xs text-ink">{{ invoice.number || 'Not numbered yet' }}</span>
            <span class="text-[11px] text-ink/50">{{ formatDate(invoice.period_start) }} to {{ formatDate(invoice.period_end) }}<template v-if="invoice.due_at">, due {{ formatDate(invoice.due_at) }}</template></span>
          </span>
          <StatusBadge :label="contractStatus(invoice).label" :tone="contractStatus(invoice).tone" />
          <span class="w-28 text-right font-mono text-xs text-ink">{{ writeMicros(invoice.total_micros) }}</span>
          <span class="flex items-center gap-0.5">
            <v-btn
              v-if="invoice.pdf_url"
              :aria-label="`Download ${invoice.number} as a PDF`"
              :href="invoice.pdf_url"
              icon
              rel="noopener"
              size="small"
              target="_blank"
              variant="text"
            >
              <Icon aria-hidden="true" class="text-lg" name="material-symbols:download" />
            </v-btn>
            <v-btn
              v-if="invoice.hosted_url"
              :aria-label="`Open ${invoice.number} on Stripe`"
              :href="invoice.hosted_url"
              icon
              rel="noopener"
              size="small"
              target="_blank"
              variant="text"
            >
              <Icon aria-hidden="true" class="text-lg" name="material-symbols:open-in-new" />
            </v-btn>
          </span>
        </li>
      </ul>
    </SectionCard>

    <SectionCard :padded="false" description="A receipt for every top-up and every plan payment, issued by Stripe." title="Receipts">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search receipts"
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
        :empty-description="receipts.length ? 'No receipt matches these filters.' : 'A receipt appears here after the first top-up or plan payment.'"
        :empty-title="receipts.length ? 'Nothing matches.' : 'No receipts yet.'"
        label="Receipts"
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
          <StatusBadge :label="INVOICE_STATUS[item.status]?.label ?? item.status" :tone="INVOICE_STATUS[item.status]?.tone ?? 'neutral'" />
        </template>
        <template #cell.total_micros="{ item }">
          <span :class="item.status === 'void' ? 'text-ink/45 line-through' : 'text-ink'">{{ writeMicros(item.total_micros) }}</span>
        </template>

        <template #actions="{ item }">
          <span class="flex items-center justify-end gap-0.5">
            <v-btn
              v-if="item.pdf_url"
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
              v-if="item.hosted_url"
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

      <div v-if="receiptsNext" class="flex justify-center border-t border-line px-5 py-3">
        <v-btn
          :loading="loadingMore"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="more"
        >
          Load older receipts
        </v-btn>
      </div>
    </SectionCard>

    <StripeHandoff
      v-if="editing"
      v-model="editing"
      :points="[
        'The Stripe customer portal opens on your billing information.',
        'Change the name, email, address or tax ID printed on invoices.',
        'Back here, this page shows what Stripe now holds.'
      ]"
      :run="portal"
      action="Open the customer portal"
      intro="Stripe holds the invoice details, so they are edited there and nowhere else."
      place="portal"
      title="Edit invoice details"
    />
  </div>
</template>
