<script setup lang="ts">
/**
 * One cohort's row of a `CohortTable`: its name, its size and its cells, already worked out
 * by the table. A component of its own so the table can redraw its header on every move
 * of the pointer while Vue leaves each row alone unless its own props changed — the row
 * that gains or loses the tab stop, and none of the ninety others.
 *
 * The table's stylesheet draws it: its classes are the table's, and it adds none.
 */
import type { CohortDrawnCell } from '../utils/cohort-table'

defineProps<{
  /** The row's place in the grid: 1 is the newest cohort, under "All cohorts". */
  index: number
  cells: readonly CohortDrawnCell[]
  label: string
  /** "Tue", for a day's cohort; empty otherwise. */
  weekday: string
  size: string
  withheldSize: boolean
  threshold: number
  /** The offset of this row's cell that holds the tab stop, or -1 if none does. */
  tabStop: number
  /** What a screen reader hears on that cell. */
  spoken?: string
}>()
</script>

<template>
  <tr>
    <th class="cohort-grid__label" scope="row">
      <span class="cohort-grid__name">{{ label }}</span>
      <span v-if="weekday" class="cohort-grid__weekday">{{ weekday }}</span>
    </th>
    <td class="cohort-grid__size" :class="{ 'cohort-grid__size--withheld': withheldSize }">
      {{ size }}
    </td>
    <td
      v-for="cell in cells"
      :key="cell.offset"
      :aria-label="cell.offset === tabStop ? spoken : undefined"
      :data-offset="cell.offset"
      :data-row="index"
      :tabindex="cell.offset === tabStop ? 0 : -1"
      role="gridcell"
    >
      <span v-if="cell.state === 'immature'" class="cohort-grid__cell cohort-grid__cell--immature"><span class="cohort-grid__empty">–</span></span>
      <span v-else-if="cell.state === 'withheld'" class="cohort-grid__cell cohort-grid__cell--withheld">&lt;{{ threshold }}</span>
      <span
        v-else
        :class="{ 'cohort-grid__cell--plain': !cell.shade }"
        :style="cell.shade ?? undefined"
        class="cohort-grid__cell"
      >
        <span class="cohort-grid__figure">{{ cell.text }}</span>
        <span v-if="cell.detail" class="cohort-grid__detail">{{ cell.detail }}</span>
      </span>
    </td>
  </tr>
</template>
