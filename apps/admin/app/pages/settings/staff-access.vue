<script setup lang="ts">
import type { StaffAccessEntry } from '~/types'

const { data: entries, pending } = await useFetch<StaffAccessEntry[]>('/api/staff-access', { default: () => [] })

const COLUMNS: DataTableColumn<StaffAccessEntry>[] = [
  { key: 'staffName', title: 'Staff', fixed: true },
  { key: 'organization', title: 'Organization' },
  { key: 'ticketReference', title: 'Ticket', mono: true },
  { key: 'accessedAt', title: 'Accessed at', mono: true, align: 'end' }
]

const columns = ref(defaultColumnState(COLUMNS))

const dateTime = new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' })
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader
      title="Staff access"
      description="Every PlayerSense staff read of a customer's data, with a ticket reference — not the customer's own audit log (§104)."
    />

    <InfoNote icon="material-symbols:construction" tone="warning">
      Blocked on backend: the staff_access table doesn't exist yet — nothing writes it today.
      The rows below are placeholder shape only.
    </InfoNote>

    <v-card border flat>
      <div class="flex justify-end border-b border-line p-3">
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </div>

      <DataTable
        v-model:column-state="columns"
        :columns="COLUMNS"
        :items="entries"
        :loading="pending"
        empty-description="No staff member has read a customer's data."
        empty-title="No access recorded."
        item-value="ticketReference"
        label="Staff access"
      >
        <template #cell.accessedAt="{ item }">
          {{ dateTime.format(new Date(item.accessedAt)) }} UTC
        </template>
      </DataTable>
    </v-card>
  </div>
</template>
