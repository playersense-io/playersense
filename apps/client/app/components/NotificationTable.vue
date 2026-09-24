<script setup lang="ts">
// The rows a notification carries in its body: the first few, the way a chat or an email
// shows a small table. Figures right-aligned in the mono face, as everywhere else.
withDefaults(defineProps<{
  table: { columns: readonly ReportColumn[], rows: readonly ReportRow[] }
  /** A chat's narrower post: smaller type and padding. */
  compact?: boolean
}>(), {
  compact: false
})
</script>

<template>
  <div class="overflow-x-auto rounded-(--ps-radius-small) border border-line">
    <table class="w-full border-collapse text-left">
      <thead>
        <tr class="bg-ink/3">
          <th
            v-for="column in table.columns"
            :key="column.key"
            :class="[isNumeric(column.type) ? 'text-right' : '', compact ? 'px-2 py-1' : 'px-3 py-1.5']"
            class="text-[10px] font-medium whitespace-nowrap text-ink/55"
            scope="col"
          >
            {{ column.label }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(row, index) in table.rows" :key="index" class="border-t border-line/70">
          <td
            v-for="column in table.columns"
            :key="column.key"
            :class="[isNumeric(column.type) ? 'text-right font-mono' : '', compact ? 'px-2 py-1 text-[11px]' : 'px-3 py-1.5 text-xs']"
            class="whitespace-nowrap text-ink/80"
          >
            {{ writeCell(row[column.key] ?? null, column.type) }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
