<script setup lang="ts">
// What production players were actually served by this flag, read from the exposures the SDK
// records the first time a flag is used in a session: each value, day by day, and how many reads
// found no configuration at all and fell to the game's own default. The last is the number that
// says whether the fallback in the code matters — on a first launch offline it is the only answer.
import type { Kpi } from '~/components/KpiBoard.vue'

const props = defineProps<{
  flag: FlagDef
}>()

const { latest } = useLiveConfig()

const RANGES: SegmentedOption[] = [
  { value: '7', label: '7 days' },
  { value: '14', label: '14 days' },
  { value: '30', label: '30 days' }
]
const range = useQueryChoice('range', ['7', '14', '30'], '14')

const production = computed(() => latest('production'))
const published = computed(() => production.value?.flags.find(entry => entry.key === props.flag.key) ?? null)

const { data, pending } = useSampleQuery(
  () => ({ key: props.flag.key, days: Number(range.value), document: published.value, version: production.value?.version ?? 0 }),
  question => (question.document ? sampleFlagExposure(question.document, question.days, question.version) : null)
)

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const categories = computed(() => data.value?.days.map(at => dayFormat.format(at)) ?? [])

const series = computed<MetricSeries[]>(() => (data.value?.lines ?? []).map((line, index) => ({
  // A JSON value is its size, which tells two documents apart no better than the rule that served them.
  name: line.source === 'default' ? 'Game default' : line.value !== null && typeof line.value === 'object' ? line.label : `${writeValue(line.value)} · ${line.label}`,
  data: line.daily,
  type: 'bar',
  stack: 'values',
  tone: line.source === 'default' ? 'muted' : ((index % 6) + 1) as MetricTone
})))

const board = computed<Kpi[]>(() => {
  const sample = data.value
  if (!sample) return []
  const defaults = sample.lines.find(line => line.source === 'default')
  const defaultReads = defaults?.daily.reduce((sum, value) => sum + value, 0) ?? 0
  return [
    { key: 'exposures', label: 'Exposures', value: writeFigure(sample.exposures, { compact: true }), exact: writeFigure(sample.exposures), trend: trendOf(sample.exposures, sample.previousExposures), info: 'Sessions in which the game read this flag. A read is counted once per session and config version.' },
    { key: 'players', label: 'Players', value: writeFigure(sample.players, { compact: true }), info: 'Distinct installs that read it in the range.' },
    { key: 'values', label: 'Values served', value: String(sample.lines.filter(line => line.source !== 'default').length), info: 'Distinct values PlayerSense served for it.' },
    { key: 'default', label: 'On the game default', value: sample.exposures ? formatShare(defaultReads / sample.exposures) : '–', info: 'Reads that found no configuration on the device — a first launch offline — and used the value written in the code.' },
    { key: 'last', label: 'Last read', value: sample.lastSeen === null ? 'Never' : sample.lastSeen < 1 ? 'Just now' : `${sample.lastSeen}m ago`, info: 'When a production device last read it.' }
  ]
})

const COLUMNS: RecordColumn[] = [
  { key: 'value', label: 'Value', width: 'minmax(0, 1.2fr)' },
  { key: 'source', label: 'From', width: 'minmax(0, 1fr)' },
  { key: 'exposures', label: 'Exposures', width: '110px', align: 'end' },
  { key: 'share', label: 'Share', width: 'minmax(0, 1fr)' }
]

const lines = computed(() => {
  const sample = data.value
  if (!sample) return []
  return sample.lines.map((line) => {
    const total = line.daily.reduce((sum, value) => sum + value, 0)
    return { ...line, total, share: sample.exposures ? total / sample.exposures : 0 }
  }).sort((a, b) => b.total - a.total)
})

const readByNobody = computed(() => data.value !== null && data.value.exposures === 0)
</script>

<template>
  <div class="flex flex-col gap-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="m-0 text-xs text-ink/60">
        Production, as the SDK reported it.
        <span v-if="production" class="text-ink/45">Serving v{{ production.version }}.</span>
      </p>
      <SegmentedControl v-model="range" :options="RANGES" label="Range" />
    </div>

    <SectionCard v-if="!published" :padded="true" title="Not in production">
      <EmptyState
        description="Production serves no version with this flag in it, so no player has been served a value by PlayerSense. Publish the draft to production to start."
        icon="material-symbols:visibility-off-outline"
        title="Nothing served yet"
      />
    </SectionCard>

    <template v-else>
      <InfoNote v-if="readByNobody" icon="material-symbols:warning-outline" tone="warning">
        Production serves this flag, and no device has read it in {{ range }} days. The shipped build
        probably does not call it yet — or no longer does, and the flag can be archived.
      </InfoNote>

      <KpiBoard
        :items="board"
        :loading="pending"
        description="Against the range before."
        title="Exposures"
      />

      <MetricCard
        v-if="!readByNobody"
        :categories="categories"
        :decimals="0"
        :loading="pending"
        :series="series"
        category-label="Day"
        compact
        description="Each value served, by the rule that served it, and the reads that used the game's own default."
        title="Served, day by day"
        type="bar"
      />

      <SectionCard v-if="!readByNobody" description="Every value, over the whole range." title="By value">
        <RecordTable :columns="COLUMNS" :loading="pending" label="Values served">
          <li v-for="line in lines" :key="line.label" class="record-row">
            <span class="record-cell record-cell--primary" data-label="Value"><FlagValue :value="line.value" /></span>
            <span class="record-cell text-ink/60" data-label="From">{{ line.source === 'default' ? 'No configuration on the device' : line.label }}</span>
            <span class="record-cell record-cell--end font-mono" data-label="Exposures">{{ writeFigure(line.total, { compact: true }) }}</span>
            <span class="record-cell" data-label="Share">
              <span class="flex items-center gap-2">
                <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6">
                  <span :class="line.source === 'default' ? 'bg-ink/30' : 'bg-primary'" :style="{ width: `${line.share * 100}%` }" class="block h-full rounded-full" />
                </span>
                <span class="w-12 text-right font-mono text-[11px] text-ink/60">{{ formatShare(line.share) }}</span>
              </span>
            </span>
          </li>
        </RecordTable>
      </SectionCard>
    </template>
  </div>
</template>
