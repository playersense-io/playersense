<script setup lang="ts">
// A progression as a table, a row per value: how many players reached it, what share of
// the first value that is, how many of those at the value before were lost on it, how
// often they tried it, and how long the ones who went on took to reach the next. Read in
// order of the values, or hardest first, where the levels that lose the most players lead.
//
// "Lost here" is shaded in the error colour when it is above the typical value's, by how
// near the worst it comes, so a hard level stands out in a column of easy ones. The header stays put over
// a long run of values.
const props = defineProps<{
  rows: readonly ProgressionRow[]
  /** What the values are: "level". */
  property: string
  threshold: number
  order: 'value' | 'hardest'
}>()

interface Line extends ProgressionRow {
  share: number | null
  lost: number | null
}

/** The share of the players who reached the value before and never reached this one. */
const lines = computed<Line[]>(() => {
  const first = props.rows[0]?.players ?? null
  return props.rows.map((row, index) => {
    const before = props.rows[index - 1]
    return {
      ...row,
      share: first && row.players !== null ? row.players / first : null,
      lost: before?.players && row.players !== null ? 1 - row.players / before.players : null
    }
  })
})

const worst = computed(() => Math.max(0, ...lines.value.map(line => line.lost ?? 0)))

/** The loss of an ordinary value: the median. Only the values above it are shaded. */
const typical = computed(() => {
  const losses = lines.value.map(line => line.lost).filter((lost): lost is number => lost !== null).sort((a, b) => a - b)
  return losses[Math.floor(losses.length / 2)] ?? 0
})

const ordered = computed(() => (props.order === 'value'
  ? lines.value
  : [...lines.value].sort((a, b) => (b.lost ?? -1) - (a.lost ?? -1))))

/**
 * The error colour by how far a value's loss rises from the typical one towards the worst,
 * kept light enough to read on. An ordinary value is left plain: shaded too, every row
 * would be a little red and the hard ones would stop standing out.
 */
function heat(lost: number | null) {
  const span = worst.value - typical.value
  if (lost === null || span <= 0 || lost <= typical.value) return undefined
  const alpha = 0.08 + 0.5 * ((lost - typical.value) / span) ** 1.2
  return { background: `rgba(var(--v-theme-error), ${alpha.toFixed(3)})` }
}

const label = computed(() => props.property.charAt(0).toUpperCase() + props.property.slice(1).replaceAll('_', ' '))
</script>

<template>
  <div class="ps-progression-table__scroller">
    <table class="ps-progression-table">
      <thead>
        <tr>
          <th>{{ label }}</th>
          <th>Reached</th>
          <th class="w-52">
            Of the first
          </th>
          <th>Lost here</th>
          <th>Tries</th>
          <th>To the next</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="line in ordered" :key="line.value">
          <td class="font-mono text-ink">
            {{ line.value }}
          </td>
          <td class="font-mono text-ink">
            {{ line.players === null ? `<${threshold}` : writeFigure(line.players) }}
          </td>
          <td>
            <span class="flex items-center justify-end gap-2.5">
              <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6">
                <span :style="{ width: `${(line.share ?? 0) * 100}%` }" class="block h-full rounded-full bg-primary" />
              </span>
              <span class="w-12 font-mono text-ink">{{ line.share === null ? '–' : formatShare(line.share) }}</span>
            </span>
          </td>
          <td>
            <span :style="heat(line.lost)" class="ps-progression-table__loss">
              {{ line.lost === null ? '–' : formatShare(line.lost) }}
            </span>
          </td>
          <td class="font-mono text-ink/80">
            {{ line.tries === null ? '–' : writeFigure(line.tries, { decimals: 1 }) }}
          </td>
          <td class="font-mono text-ink/80">
            {{ line.toNext === null ? '–' : formatSeconds(line.toNext) }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.ps-progression-table__scroller {
  max-height: 36rem;
  overflow: auto;
}

.ps-progression-table {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-progression-table th {
  background: rgb(var(--v-theme-surface));
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  position: sticky;
  text-align: right;
  top: 0;
  white-space: nowrap;
  z-index: 1;
}

.ps-progression-table td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 6px 12px;
  text-align: right;
  white-space: nowrap;
}

.ps-progression-table :is(th, td):first-child {
  padding-left: 20px;
  text-align: left;
}

.ps-progression-table :is(th, td):last-child {
  padding-right: 20px;
}

.ps-progression-table tbody tr:last-child td {
  border-bottom: 0;
}

.ps-progression-table tbody tr:hover td {
  background: rgba(var(--v-theme-on-surface), 0.03);
}

.ps-progression-table__loss {
  border-radius: var(--ps-radius-x-small);
  color: rgb(var(--v-theme-on-surface));
  display: inline-block;
  font-family: var(--font-mono);
  min-width: 4rem;
  padding: 2px 6px;
  text-align: right;
}
</style>
