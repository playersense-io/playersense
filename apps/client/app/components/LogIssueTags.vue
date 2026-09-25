<script setup lang="ts">
/**
 * Where an issue happens: its events split by release, platform, OS, device and country, one row
 * each, read the way a person triages a crash. The row leads with what it means ("Only in release
 * 1.4.2") and shows the split under it as one bar of the whole, so a bug confined to one build or
 * one phone is visible before any number is read.
 *
 * The bar is one hue in steps, commonest first and darkest, with everything past the five named
 * values as "Other" in grey. Shades rather than hues because the values are a ranking within one
 * row, not identities that recur across rows: a colour per value would suggest that the blue in
 * Release and the blue in Device are the same thing.
 */
const props = defineProps<{
  breakdown: Readonly<Record<string, readonly { value: string, count: number }[]>>
  /** The events the breakdown split, counted by the same query: the whole each bar is a share of. */
  total: number
}>()

const emit = defineEmits<{ filter: [clause: string] }>()

const palette = useMetricPalette()

const DIMENSIONS = [
  { key: 'release', label: 'Release', field: 'release', only: 'Only in release', mostly: 'Mostly release', noun: ['release', 'releases'] },
  { key: 'platform', label: 'Platform', field: 'platform', only: 'Only on', mostly: 'Mostly on', noun: ['platform', 'platforms'] },
  { key: 'os', label: 'OS version', field: 'os', only: 'Only on OS', mostly: 'Mostly OS', noun: ['OS version', 'OS versions'] },
  { key: 'device', label: 'Device', field: 'device', only: 'Only on', mostly: 'Mostly on', noun: ['device', 'devices'] },
  { key: 'country', label: 'Country', field: 'country', only: 'Only in', mostly: 'Mostly in', noun: ['country', 'countries'] }
] as const

/* The steps of the one hue, commonest first. */
const STEPS = [1, 0.72, 0.52, 0.36, 0.24]

interface Segment { value: string, count: number, share: number, color: string, other: boolean }

const rows = computed(() => DIMENSIONS.map((dimension) => {
  const values = [...(props.breakdown[dimension.key] ?? [])].sort((a, b) => b.count - a.count)
  const accent = palette.value.series[0] ?? palette.value.ink
  const named = values.reduce((sum, entry) => sum + entry.count, 0)
  // The API names the five commonest and counts the whole in the same query, so what the five do
  // not cover is exactly the rest: the Other.
  const whole = Math.max(props.total, named, 1)
  const rest = whole - named
  const segments: Segment[] = values.map((entry, index) => ({
    value: entry.value || 'Unknown',
    count: entry.count,
    share: entry.count / whole,
    // A value the SDK never sent is grey, like the Other: nothing to read into it.
    color: entry.value ? alpha(accent, STEPS[index] ?? 0.2) : alpha(palette.value.ink, 0.12),
    other: false
  }))
  if (rest > 0) {
    segments.push({ value: 'Other', count: rest, share: rest / whole, color: alpha(palette.value.ink, 0.12), other: true })
  }
  const top = segments[0]
  let finding = ''
  let tone: 'strong' | 'soft' | 'none' = 'none'
  if (top && top.value === 'Unknown' && top.share >= 0.999) {
    // Nothing to conclude from a value the SDK never sent.
    finding = 'Not reported'
  } else if (top && !top.other && top.share >= 0.999) {
    finding = `${dimension.only} ${top.value}`
    tone = 'strong'
  } else if (top && !top.other && top.share >= 0.75) {
    finding = `${dimension.mostly} ${top.value}, ${Math.round(top.share * 100)}%`
    tone = 'soft'
  } else if (values.length) {
    finding = `Across ${values.length}${rest > 0 ? '+' : ''} ${dimension.noun[values.length === 1 && rest === 0 ? 0 : 1]}`
  }
  return { ...dimension, segments, finding, tone }
}))

const percent = (share: number) => (share >= 0.995 ? '100%' : share < 0.005 ? '<1%' : `${Math.round(share * 100)}%`)
</script>

<template>
  <ul class="m-0 flex list-none flex-col p-0">
    <li
      v-for="row in rows"
      :key="row.key"
      class="grid grid-cols-1 gap-x-6 gap-y-2 border-b border-line px-5 py-4 last:border-b-0 md:grid-cols-[120px_minmax(0,1fr)]"
    >
      <div class="flex flex-col gap-1">
        <span class="text-xs font-medium text-ink">{{ row.label }}</span>
        <span
          v-if="row.finding"
          :class="row.tone === 'strong' ? 'text-primary' : row.tone === 'soft' ? 'text-ink/70' : 'text-ink/45'"
          class="text-[11px] leading-4"
        >
          {{ row.finding }}
        </span>
      </div>

      <div v-if="row.segments.length" class="flex min-w-0 flex-col gap-2.5">
        <!-- The whole, in one bar. -->
        <div :aria-label="`${row.label}: ${row.segments.map(segment => `${segment.value} ${percent(segment.share)}`).join(', ')}`" class="flex h-2 overflow-hidden rounded-full bg-ink/6" role="img">
          <span
            v-for="segment in row.segments"
            :key="segment.value"
            :style="{ width: `${segment.share * 100}%`, background: segment.color }"
            class="h-full border-r-2 border-surface last:border-r-0"
          />
        </div>
        <!-- Each value, as a filter on the issue's records. -->
        <ul class="m-0 flex list-none flex-wrap gap-x-5 gap-y-1.5 p-0">
          <li v-for="segment in row.segments" :key="segment.value" class="flex min-w-0 items-center gap-1.5 text-xs">
            <span :style="{ background: segment.color }" aria-hidden="true" class="size-2 flex-none rounded-[2px]" />
            <button
              v-if="!segment.other"
              :title="`Every record of this issue where ${row.label.toLowerCase()} is ${segment.value}`"
              class="max-w-48 truncate font-mono text-[11px] text-ink/85 hover:text-primary hover:underline"
              type="button"
              @click="emit('filter', clause(row.field, segment.value))"
            >
              {{ segment.value }}
            </button>
            <span v-else class="text-[11px] text-ink/50">Other</span>
            <span class="font-mono text-[11px] text-ink/45 tabular-nums">{{ writeFigure(segment.count) }} · {{ percent(segment.share) }}</span>
          </li>
        </ul>
      </div>
      <span v-else class="text-[11px] text-ink/40">Nothing reported</span>
    </li>
  </ul>
</template>
