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
// The definition is the page's, edited there; this reads it and asks the API what it comes to
// (useFunnelAnswer), again a moment after each edit.
const props = defineProps<{
  definition: ProgressionFunnel
  range: FunnelRange
}>()

const RANGE_WORDS: Record<FunnelRange, string> = { '7d': 'the last 7 days', '30d': 'the last 30 days', '90d': 'the last 90 days' }

const answer = useFunnelAnswer(() => props.definition, () => props.range)

const data = computed<ProgressionResult>(() => answer.progression.value ?? { rows: [], previous: { first: null, last: null }, medianToLast: null })
const threshold = computed(() => answer.threshold.value)

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

/* Nobody reached the first value (or too few to show): the progression has nothing behind it,
   so every figure is a dash and the pictures show the no-data mark. */
const nobody = computed(() => !data.value.rows[0]?.players)

const board = computed<Kpi[]>(() => {
  const { rows, previous, medianToLast } = data.value
  const empty = nobody.value
  const first = empty ? null : rows[0]?.players ?? null
  const last = empty ? null : rows[rows.length - 1]?.players ?? null
  const held = ` Held back when fewer than ${threshold.value} players, too few to show.`
  const completion = first && last !== null ? last / first : null
  const previousCompletion = previous.first && previous.last !== null ? previous.last / previous.first : null
  const { from, to } = props.definition
  return [
    {
      key: 'started',
      label: `Reached ${property.value} ${from}`,
      value: first === null ? null : writeFigure(first, { compact: true }),
      exact: first === null ? undefined : writeFigure(first),
      trend: first !== null && previous.first ? trendOf(first, previous.first) : undefined,
      info: `Players who did ${props.definition.event} at ${property.value} ${from} in the range.${held}`
    },
    {
      key: 'finished',
      label: `Reached ${property.value} ${to}`,
      value: last === null ? null : writeFigure(last, { compact: true }),
      exact: last === null ? undefined : writeFigure(last),
      trend: last !== null && previous.last ? trendOf(last, previous.last) : undefined,
      info: `Of those, the players who went on through every ${property.value} to ${to}, ${windowPhrase(props.definition.window)} of the first.${held}`
    },
    {
      key: 'completion',
      label: 'Completion',
      value: completion === null ? null : formatShare(completion),
      trend: completion !== null && previousCompletion !== null ? trendOf(completion, previousCompletion) : undefined,
      info: `Reached ${property.value} ${to} over reached ${property.value} ${from}.`
    },
    {
      key: 'time',
      label: `Median time to ${property.value} ${to}`,
      value: empty || medianToLast === null ? null : formatSeconds(medianToLast),
      info: `From ${property.value} ${from} to ${property.value} ${to}, for the players who got there.`
    },
    {
      key: 'hardest',
      label: `Hardest ${property.value}`,
      value: !empty && hardest.value ? String(hardest.value.value) : null,
      unit: !empty && hardest.value ? `${formatShare(hardest.value.loss)} lost` : undefined,
      info: `The ${property.value} the largest share of players got to and never got past.`
    }
  ]
})

// --- Where they stop ----------------------------------------------------------------------

const categories = computed(() => data.value.rows.map(row => String(row.value)))
const titles = computed(() => data.value.rows.map(row => `${Property.value} ${row.value}`))
const lossSeries = computed<MetricSeries[]>(() => [{
  name: 'Lost here',
  data: losses.value.map(loss => (nobody.value || loss === null ? null : Math.round(loss * 1000) / 10)),
  type: 'bar',
  tone: 2
}])

// --- Every value --------------------------------------------------------------------------

const ORDERS: SegmentedOption[] = [
  { value: 'value', label: 'In order' },
  { value: 'hardest', label: 'Hardest first' }
]

const order = ref<'value' | 'hardest'>('value')

// --- Which view ---------------------------------------------------------------------------

const tabs = computed<PageTab[]>(() => [
  { value: 'stops', label: 'Where players stop' },
  { value: 'values', label: `Every ${property.value}`, count: data.value.rows.length }
])

/** In the URL, so a reload or a link opens the same view. */
const tab = useQueryChoice('tab', ['stops', 'values'], 'stops')

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
    <p v-if="answer.error.value" class="m-0 flex items-center gap-3 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      <span class="flex-1">{{ answer.error.value.message }}</span>
      <v-btn
        class="text-xs"
        color="error"
        size="small"
        variant="text"
        @click="answer.reload()"
      >
        Try again
      </v-btn>
    </p>
    <KpiBoard
      :description="`Players who reached ${property} ${definition.from} in ${RANGE_WORDS[range]}, against the range before.`"
      :items="board"
      :loading="answer.loading.value && !answer.progression.value"
      title="Completion"
    />

    <div class="flex flex-col gap-5">
      <PageTabs v-model="tab" :tabs="tabs" label="Progression views" />

      <MetricCard
        v-if="tab === 'stops'"
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
        v-else
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

        <div v-if="nobody" class="p-5">
          <NoDataPlaceholder :height="200" :seed="definition.to - definition.from + 1" label="No data yet" />
        </div>
        <ProgressionTable
          v-else
          :order="order"
          :property="definition.property"
          :rows="data.rows"
          :threshold="threshold"
        />
      </SectionCard>
    </div>
  </div>
</template>
