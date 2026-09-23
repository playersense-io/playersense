<script setup lang="ts">
// The funnel as figures: a row per step with everything the chart draws, exact. Every
// player, or, broken down, a column per value holding its share of its own step 1 over
// the players behind it — the shape of a cohort cell, so the two tables read alike.
//
// A share is written with one decimal always, a count in full, and a withheld count as
// the threshold, never as zero.
const props = defineProps<{
  steps: readonly FunnelStep[]
  total: FunnelSeries
  breakdown: readonly FunnelSeries[]
  threshold: number
}>()

const grouped = computed(() => props.breakdown.length > 0)

const share = (series: FunnelSeries, step: number): number | null => {
  const first = series.players[0]
  const here = series.players[step]
  return first && here !== null && here !== undefined ? here / first : null
}

const onward = (series: FunnelSeries, step: number): number | null => {
  const before = series.players[step - 1]
  const here = series.players[step]
  return step > 0 && before && here !== null && here !== undefined ? here / before : null
}

const lost = (series: FunnelSeries, step: number): number | null => {
  const before = series.players[step - 1]
  const here = series.players[step]
  return step > 0 && before && here !== null && here !== undefined ? before - here : null
}

const withheld = computed(() => `<${props.threshold}`)

const count = (value: number | null | undefined) => (value === null || value === undefined ? withheld.value : writeFigure(value))
</script>

<template>
  <div class="overflow-x-auto">
    <table class="ps-funnel-table">
      <thead>
        <tr>
          <th class="w-10">
            #
          </th>
          <th>
            Step
          </th>
          <template v-if="!grouped">
            <th>Players</th>
            <th class="w-48">
              Of step 1
            </th>
            <th>From the step before</th>
            <th>Lost</th>
            <th>Median wait</th>
          </template>
          <template v-else>
            <th>All players</th>
            <th v-for="(series, index) in breakdown" :key="series.name">
              <span class="inline-flex items-center gap-1.5">
                <span :style="{ background: `rgb(var(--v-theme-chart-${(index % 6) + 1}))` }" aria-hidden="true" class="size-2 rounded-full" />
                {{ series.name }}
              </span>
            </th>
          </template>
        </tr>
      </thead>

      <tbody>
        <tr v-for="(step, index) in steps" :key="index">
          <td class="font-mono text-ink/50">
            {{ index + 1 }}
          </td>
          <td>
            <span class="block font-mono text-ink">{{ step.event }}</span>
            <span v-for="condition in step.where" :key="condition.property" class="block text-[11px] text-ink/55">
              {{ condition.property }} = {{ condition.value }}
            </span>
          </td>

          <template v-if="!grouped">
            <td class="font-mono text-ink">
              {{ count(total.players[index]) }}
            </td>
            <td>
              <span class="flex items-center justify-end gap-2.5">
                <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink/6">
                  <span :style="{ width: `${(share(total, index) ?? 0) * 100}%` }" class="block h-full rounded-full bg-primary" />
                </span>
                <span class="w-12 font-mono text-ink">{{ share(total, index) === null ? '–' : formatShare(share(total, index)!) }}</span>
              </span>
            </td>
            <td class="font-mono text-ink/80">
              {{ onward(total, index) === null ? '–' : formatShare(onward(total, index)!) }}
            </td>
            <td :class="lost(total, index) ? 'text-error' : 'text-ink/40'" class="font-mono">
              {{ lost(total, index) === null ? '–' : `−${writeFigure(lost(total, index)!)}` }}
            </td>
            <td class="font-mono text-ink/80">
              {{ total.medians[index] === null || total.medians[index] === undefined ? '–' : formatSeconds(total.medians[index]!) }}
            </td>
          </template>

          <template v-else>
            <td v-for="series in [total, ...breakdown]" :key="series.name">
              <span class="block font-mono text-ink">
                {{ series.players[index] === null ? withheld : share(series, index) === null ? '–' : formatShare(share(series, index)!) }}
              </span>
              <span class="block font-mono text-[10px] text-ink/50">
                {{ series.players[index] === null ? '' : count(series.players[index]) }}
              </span>
            </td>
          </template>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.ps-funnel-table {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-funnel-table th {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  text-align: right;
  white-space: nowrap;
}

.ps-funnel-table td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 8px 12px;
  text-align: right;
  vertical-align: middle;
  white-space: nowrap;
}

/* The step and its number read from the left, the figures from the right; the outer
   cells sit on the card's own inset. */
.ps-funnel-table :is(th, td):nth-child(-n + 2) {
  text-align: left;
}

.ps-funnel-table :is(th, td):first-child {
  padding-left: 20px;
}

.ps-funnel-table :is(th, td):last-child {
  padding-right: 20px;
}

.ps-funnel-table tbody tr:last-child td {
  border-bottom: 0;
}

.ps-funnel-table tbody tr:hover td {
  background: rgba(var(--v-theme-on-surface), 0.03);
}
</style>
