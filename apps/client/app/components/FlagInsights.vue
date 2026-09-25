<script setup lang="ts">
// What production players were actually served by this flag, read from the exposures the SDK
// records the first time a flag is used in a session: each value, day by day, and how many reads
// found no configuration at all and fell to the game's own default. The last is the number that
// says whether the fallback in the code matters — on a first launch offline it is the only answer.
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'

const props = defineProps<{
  flag: FlagDef
}>()

const { latest } = useLiveConfig()
const workspace = useWorkspaceStore()

const RANGES: SegmentedOption[] = [
  { value: '7', label: '7 days' },
  { value: '14', label: '14 days' },
  { value: '30', label: '30 days' }
]
const range = useQueryChoice('range', ['7', '14', '30'], '14')

const production = computed(() => latest('production'))
const published = computed(() => production.value?.flags.find(entry => entry.key === props.flag.key) ?? null)

const isoDay = (at: number) => new Date(at).toISOString().slice(0, 10)
const window = computed(() => {
  const to = Date.now()
  return { from: isoDay(to - (Number(range.value) - 1) * 86_400_000), to: isoDay(to) }
})

const read = useApiGet<Schema<'FlagExposureOut'>>(() => workspace.projectPath && published.value
  ? withQuery(`${workspace.projectPath}/analytics/production/flags/${encodeURIComponent(props.flag.key)}/exposures`, window.value)
  : null)
const pending = computed(() => read.isFetching.value)
const data = computed(() => read.data.value)

/** A value as text, read back as the value it was where it parses. */
function valueOf(text: string): Json {
  try {
    return JSON.parse(text) as Json
  } catch {
    return text
  }
}

const lineKey = (published: boolean, value: string) => (published ? `v:${value}` : 'default')

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const days = computed(() => [...new Set((data.value?.days ?? []).map(entry => entry.day))].sort())
const categories = computed(() => days.value.map(day => dayFormat.format(Date.parse(day))))

const series = computed<MetricSeries[]>(() => (data.value?.values ?? []).map((line, index) => {
  const key = lineKey(line.published, line.value)
  const byDay = new Map((data.value?.days ?? []).filter(entry => lineKey(entry.published, entry.value) === key).map(entry => [entry.day, entry.exposures]))
  const value = valueOf(line.value)
  return {
    // A JSON value is its size, which tells two documents apart no better than its text does.
    name: !line.published ? 'Game default' : value !== null && typeof value === 'object' ? `Value ${index + 1}` : writeValue(value),
    // A day with no row for this value is no data, drawn as a gap rather than a zero.
    data: days.value.map(day => byDay.get(day) ?? null),
    type: 'line',
    tone: !line.published ? 'muted' : ((index % 6) + 1) as MetricTone
  }
}))

/* Nothing read it in the range: every figure is no data, drawn as a dash, not a zero. */
const board = computed<Kpi[]>(() => {
  const out = data.value
  if (!out) return []
  const read = out.exposures > 0
  return [
    { key: 'exposures', label: 'Exposures', value: read ? writeFigure(out.exposures, { compact: true }) : null, exact: read ? writeFigure(out.exposures) : undefined, info: 'Sessions in which the game read this flag. A read is counted once per session and config version.' },
    { key: 'values', label: 'Values served', value: read ? String(out.values.filter(line => line.published).length) : null, info: 'Distinct values PlayerSense served for it.' },
    { key: 'default', label: 'On the game default', value: read ? formatShare(out.default_share) : null, info: 'Reads that found no configuration on the device, such as a first launch offline, and used the value written in the code.' },
    { key: 'daily', label: 'Exposures per day', value: read && days.value.length ? writeFigure(out.exposures / days.value.length, { compact: true }) : null, info: 'Exposures on an average day of the range, over the days the flag was read.' },
    { key: 'versions', label: 'Config versions', value: read ? String(out.versions.length) : null, info: 'Published versions devices held while reading it.' }
  ]
})

const COLUMNS: RecordColumn[] = [
  { key: 'value', label: 'Value', width: 'minmax(0, 1.2fr)' },
  { key: 'source', label: 'From', width: 'minmax(0, 1fr)' },
  { key: 'exposures', label: 'Exposures', width: '110px', align: 'end' },
  { key: 'share', label: 'Share', width: 'minmax(0, 1fr)' }
]

const lines = computed(() => {
  const out = data.value
  if (!out) return []
  return out.values.map(line => ({
    label: lineKey(line.published, line.value),
    value: line.published ? valueOf(line.value) : null,
    source: line.published ? 'published' : 'default',
    total: line.exposures,
    share: out.exposures ? line.exposures / out.exposures : 0
  })).sort((a, b) => b.total - a.total)
})

const readByNobody = computed(() => Boolean(data.value) && data.value!.exposures === 0)
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
        probably does not call it yet. If it no longer does, the flag can be archived.
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
        description="Each value served, and the reads that used the game's own default."
        title="Served, day by day"
        type="line"
      />

      <SectionCard v-if="!readByNobody" description="Every value, over the whole range." title="By value">
        <RecordTable :columns="COLUMNS" :loading="pending" label="Values served">
          <li v-for="line in lines" :key="line.label" class="record-row">
            <span class="record-cell record-cell--primary" data-label="Value"><FlagValue :value="line.value" /></span>
            <span class="record-cell text-ink/60" data-label="From">{{ line.source === 'default' ? 'No configuration on the device' : 'Served by PlayerSense' }}</span>
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
