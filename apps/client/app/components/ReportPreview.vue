<script setup lang="ts">
// What comes out of the selected node, under the canvas: its rows as a table, or, for a
// chart, the chart itself, from the first fifty rows the API previews; the count is of all.
const props = defineProps<{
  node: GraphNode
  result: NodeResult | null
}>()

const LIMIT = 100

const shown = computed(() => props.result?.rows.slice(0, LIMIT) ?? [])

const isChart = computed(() => props.node.data.kind === 'chart' && props.result && !props.result.error)

const chart = computed(() => {
  const result = props.result
  const config = props.node.data.config
  if (!result || !config.x) return null
  const y = (config.y ?? []).filter(key => result.columns.some(column => column.key === key))
  const money = y.every(key => result.columns.find(column => column.key === key)?.type === 'money')
  return {
    categories: result.rows.map(row => String(row[config.x!] ?? '–')),
    series: y.map((key, index) => ({
      name: result.columns.find(column => column.key === key)?.label ?? key,
      data: result.rows.map(row => (typeof row[key] === 'number' ? row[key] as number : null)),
      tone: ((index % 6) + 1) as MetricTone
    })),
    format: money ? { prefix: '$', compact: true } : { compact: true }
  }
})

const alignOf = (type: ColumnType) => (isNumeric(type) ? 'text-right' : 'text-left')
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div v-if="result?.error" class="flex flex-1 items-center justify-center p-6">
      <p class="m-0 flex max-w-md items-start gap-2 text-xs leading-5 text-ink/70">
        <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:error-outline" />
        {{ result.error }}
      </p>
    </div>

    <div v-else-if="isChart && chart" class="min-h-0 flex-1 px-4 py-3">
      <MetricChart
        :categories="chart.categories"
        :format="chart.format"
        :height="200"
        :label="`${node.data.title}: ${chart.series.map(entry => entry.name).join(', ')}`"
        :series="chart.series"
        :type="node.data.config.chart ?? 'line'"
      />
    </div>

    <div v-else-if="result && result.rows.length" class="min-h-0 flex-1 overflow-auto">
      <table class="w-full border-collapse text-left">
        <thead class="sticky top-0 z-1 bg-surface">
          <tr>
            <th
              v-for="column in result.columns"
              :key="column.key"
              :class="alignOf(column.type)"
              class="border-b border-line px-3 py-2 text-[11px] font-normal whitespace-nowrap text-ink/50"
              scope="col"
            >
              {{ column.label }}
              <span class="ml-1 font-mono text-[10px] text-ink/30">{{ column.key }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, index) in shown" :key="index" class="border-b border-line/60 last:border-b-0 hover:bg-ink/2">
            <td
              v-for="column in result.columns"
              :key="column.key"
              :class="[alignOf(column.type), isNumeric(column.type) || column.type === 'time' || column.key.endsWith('_id') ? 'font-mono' : '']"
              class="px-3 py-1.5 text-xs whitespace-nowrap text-ink/80"
            >
              {{ writeCell(row[column.key] ?? null, column.type) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else class="flex flex-1 items-center justify-center p-6">
      <p class="m-0 text-xs text-ink/50">
        No rows come out of it.
      </p>
    </div>
  </div>
</template>
