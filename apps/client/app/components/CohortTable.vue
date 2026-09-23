<script setup lang="ts">
// The retention triangle: one row per cohort, one column per day offset, each cell the
// share of the cohort that came back, shaded in the accent by that share so the decay reads
// down and across at a glance. The first row is every cohort together, weighted by size.
//
// Two kinds of empty cell, never drawn alike: a cohort too young for the offset (nothing to
// know yet) and a cell withheld because it counts too few players to show (the suppression
// threshold). Neither is a zero, and a zero would be the one wrong way to draw them.
const props = defineProps<{
  cohorts: readonly Cohort[]
  offsets: readonly number[]
  grain: RetentionGrain
}>()

const dayLabel = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })

function cohortLabel(at: number): string {
  if (props.grain === 'day') return dayLabel.format(at)
  return `${dayLabel.format(at)} – ${dayLabel.format(at + 6 * 86_400_000)}`
}

// Every cohort together at each offset: the players who came back over the sizes of the
// cohorts old enough to have, so a young cohort never drags an average down.
const overall = computed(() => props.offsets.map((_, index) => {
  let players = 0
  let size = 0
  for (const cohort of props.cohorts) {
    const cell = cohort.cells[index]
    if (!cell || cell.rate === null || cell.withheld) continue
    players += cell.players ?? 0
    size += cohort.size
  }
  return size ? players / size : null
}))

const totalSize = computed(() => props.cohorts.reduce((sum, cohort) => sum + cohort.size, 0))

/** The accent at the strength of the rate; ink turns light once the fill is dark enough. */
function shade(rate: number) {
  const alpha = Math.min(1, 0.08 + rate * 1.6)
  return {
    background: `rgba(var(--v-theme-primary), ${alpha.toFixed(3)})`,
    color: alpha > 0.55 ? 'rgb(var(--v-theme-on-primary))' : undefined
  }
}

const percent = (rate: number) => writeFigure(rate * 100, { decimals: 1, unit: '%' })
</script>

<template>
  <div class="max-h-[34rem] overflow-auto">
    <table class="w-full border-separate border-spacing-0 text-[13px]">
      <thead class="sticky top-0 z-1 bg-surface">
        <tr>
          <th class="border-b border-line px-4 py-2.5 text-left text-xs font-normal text-ink/55">
            Cohort
          </th>
          <th class="border-b border-line px-3 py-2.5 text-left text-xs font-normal text-ink/55">
            Players
          </th>
          <th
            v-for="offset in offsets"
            :key="offset"
            class="border-b border-line px-1 py-2.5 text-center font-mono text-xs font-normal text-ink/55"
          >
            Day {{ offset }}
          </th>
        </tr>
      </thead>

      <tbody>
        <tr>
          <td class="border-b border-line px-4 py-2 text-ink">
            All cohorts
          </td>
          <td class="border-b border-line px-3 py-2 font-mono text-ink/70">
            {{ writeFigure(totalSize, { compact: true }) }}
          </td>
          <td v-for="(rate, index) in overall" :key="index" class="border-b border-line p-0.5">
            <span
              v-if="rate !== null"
              :style="shade(rate)"
              class="flex h-8 items-center justify-center rounded-(--ps-radius-x-small) font-mono text-xs text-ink"
            >
              {{ percent(rate) }}
            </span>
          </td>
        </tr>

        <tr v-for="cohort in cohorts" :key="cohort.at">
          <td class="px-4 py-1 whitespace-nowrap text-ink/80">
            {{ cohortLabel(cohort.at) }}
          </td>
          <td class="px-3 py-1 font-mono text-ink/60">
            {{ writeFigure(cohort.size) }}
          </td>
          <td v-for="cell in cohort.cells" :key="cell.offset" class="p-0.5">
            <span
              v-if="cell.rate === null"
              :title="`Not ${cell.offset} days old yet`"
              class="flex h-8 items-center justify-center rounded-(--ps-radius-x-small) bg-ink/2"
            />
            <span
              v-else-if="cell.withheld"
              :title="`Fewer than ${SUPPRESSION_THRESHOLD} players — withheld`"
              class="
                flex h-8 items-center justify-center rounded-(--ps-radius-x-small) bg-ink/4
                font-mono text-xs text-ink/40
              "
            >
              &lt;{{ SUPPRESSION_THRESHOLD }}
            </span>
            <span
              v-else
              :style="shade(cell.rate)"
              :title="`${writeFigure(cell.players ?? 0)} of ${writeFigure(cohort.size)} players`"
              class="flex h-8 items-center justify-center rounded-(--ps-radius-x-small) font-mono text-xs text-ink"
            >
              {{ percent(cell.rate) }}
            </span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
