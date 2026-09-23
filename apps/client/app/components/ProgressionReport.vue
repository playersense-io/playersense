<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// What a progression comes to over a range: how many started and how many got to the end
// against the range before, the value that loses the most players, where along the way
// they are lost as a chart, and every value as a table, in order or hardest first, to take
// away as CSV.
//
// A value's loss is the players who reached the one before and never reached it: for a
// level, the players who got to it and were stuck there. That puts a hard level's loss on
// the hard level, where a designer looks for it.
//
// The definition is the page's, edited there; this reads it. Sample data until the funnel
// query exists (app/utils/funnel-sample.ts).
const props = defineProps<{
  definition: ProgressionFunnel
  range: FunnelRange
}>()

const RANGE_WORDS: Record<FunnelRange, string> = { '7d': 'the last 7 days', '30d': 'the last 30 days', '90d': 'the last 90 days' }

const data = computed(() => sampleProgression(props.definition, props.range, EVENT_CATALOGUE))

const property = computed(() => props.definition.property.replaceAll('_', ' '))
const Property = computed(() => property.value.charAt(0).toUpperCase() + property.value.slice(1))

/** Per value, the share of those who reached the one before that never reached it. */
const losses = computed(() => data.value.rows.map((row, index) => {
  const before = data.value.rows[index - 1]
  return before?.players && row.players !== null ? 1 - row.players / before.players : null
}))

const hardest = computed(() => {
  let at = -1
  losses.value.forEach((loss, index) => {
    if (loss !== null && (at === -1 || loss > losses.value[at]!)) at = index
  })
  return at === -1 ? null : { value: data.value.rows[at]!.value, loss: losses.value[at]! }
})

const board = computed<Kpi[]>(() => {
  const { rows, previous, medianToLast } = data.value
  const first = rows[0]?.players ?? null
  const last = rows[rows.length - 1]?.players ?? null
  const completion = first && last !== null ? last / first : null
  const previousCompletion = previous.first && previous.last !== null ? previous.last / previous.first : null
  const { from, to } = props.definition
  return [
    {
      key: 'started',
      label: `Reached ${property.value} ${from}`,
      value: first === null ? `<${FUNNEL_THRESHOLD}` : writeFigure(first, { compact: true }),
      exact: first === null ? undefined : writeFigure(first),
      trend: first !== null && previous.first ? trendOf(first, previous.first) : undefined,
      info: `Players who did ${props.definition.event} at ${property.value} ${from} in the range.`
    },
    {
      key: 'finished',
      label: `Reached ${property.value} ${to}`,
      value: last === null ? `<${FUNNEL_THRESHOLD}` : writeFigure(last, { compact: true }),
      exact: last === null ? undefined : writeFigure(last),
      trend: last !== null && previous.last ? trendOf(last, previous.last) : undefined,
      info: `Of those, the players who went on through every ${property.value} to ${to}, ${windowPhrase(props.definition.window)} of the first.`
    },
    {
      key: 'completion',
      label: 'Completion',
      value: completion === null ? '–' : formatShare(completion),
      trend: completion !== null && previousCompletion !== null ? trendOf(completion, previousCompletion) : undefined,
      info: `Reached ${property.value} ${to} over reached ${property.value} ${from}.`
    },
    {
      key: 'time',
      label: `Median time to ${property.value} ${to}`,
      value: medianToLast === null ? '–' : formatSeconds(medianToLast),
      info: `From ${property.value} ${from} to ${property.value} ${to}, for the players who got there.`
    },
    {
      key: 'hardest',
      label: `Hardest ${property.value}`,
      value: hardest.value ? String(hardest.value.value) : '–',
      unit: hardest.value ? `${formatShare(hardest.value.loss)} lost` : undefined,
      info: `The ${property.value} the largest share of players got to and never got past.`
    }
  ]
})

// --- Where they stop ----------------------------------------------------------------------

const categories = computed(() => data.value.rows.map(row => String(row.value)))
const titles = computed(() => data.value.rows.map(row => `${Property.value} ${row.value}`))
const lossSeries = computed<MetricSeries[]>(() => [{
  name: 'Lost here',
  data: losses.value.map(loss => (loss === null ? null : Math.round(loss * 1000) / 10)),
  type: 'bar',
  tone: 2
}])

// --- Every value --------------------------------------------------------------------------

const ORDERS: SegmentedOption[] = [
  { value: 'value', label: 'In order' },
  { value: 'hardest', label: 'Hardest first' }
]

const order = ref<'value' | 'hardest'>('value')

function download() {
  const { rows } = data.value
  const first = rows[0]?.players ?? null
  const header = [props.definition.property, 'players', 'share of first', 'lost here', 'tries per player', 'median to next (s)']
  const lines = rows.map((row, index) => [
    row.value,
    row.players,
    first && row.players !== null ? Math.round(row.players / first * 10_000) / 10_000 : null,
    losses.value[index] === null ? null : Math.round(losses.value[index]! * 10_000) / 10_000,
    row.tries,
    row.toNext === null ? null : Math.round(row.toNext)
  ])
  downloadCsv(`${fileSlug(props.definition.name)}-${props.range}`, [header, ...lines])
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <KpiBoard
      :description="`Players who reached ${property} ${definition.from} in ${RANGE_WORDS[range]}, against the range before.`"
      :items="board"
      title="Completion"
    />

    <MetricCard
      :categories="categories"
      :category-label="Property"
      :series="lossSeries"
      :titles="titles"
      :description="`Of the players who reached the ${property} before, the share who never got past this one. The tall bars are where the game loses them.`"
      title="Where players stop"
      type="bar"
      unit="%"
    />

    <SectionCard
      :description="`Every ${property} from ${definition.from} to ${definition.to}. Lost here is shaded against the worst of them; tries is how often each player did ${definition.event} there.`"
      :padded="false"
      :title="`Every ${property}`"
    >
      <template #actions>
        <SegmentedControl v-model="order" :options="ORDERS" label="Order" />
        <v-btn
          aria-label="Download as CSV"
          color="neutral"
          icon="material-symbols:download"
          size="small"
          variant="text"
          @click="download"
        />
      </template>

      <ProgressionTable
        :order="order"
        :property="definition.property"
        :rows="data.rows"
        :threshold="FUNNEL_THRESHOLD"
      />
    </SectionCard>
  </div>
</template>
