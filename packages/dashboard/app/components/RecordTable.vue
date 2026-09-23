<script setup lang="ts">
import type { RecordColumn, RecordTab } from '../utils/components'
/**
 * The compact record list a detail page is made of: an optional tab bar, a row of column
 * labels, and rows on one grid.
 *
 * It exists so that two lists sitting one under the other on the same page are the same
 * list. The track widths, the inset, the row padding, the separator and the hover all live
 * here once, which is the only way the right-hand columns of two different tables line up
 * to the pixel and stay lined up after somebody edits one of them.
 *
 * Not `DataTable`. That one is the full list surface: sorting, paging, a column
 * menu, an arrangement the reader keeps. This is a handful of rows about one subject,
 * where a pager would say "1-6 of 6" and a column menu would be furniture.
 *
 * The 12px inset is paid back by a negative margin on the root, so a label and the text
 * under it start on the section's own left edge while the hover wash and the rules run
 * wider than the text. Below 900px the labels move onto the cells themselves, through
 * `data-label`, because a column heading somewhere above a stacked row explains nothing.
 */

import { cycleSort, directionOf, type TableSort } from '../utils/table-sort'

const props = withDefaults(
  defineProps<{
    columns: readonly RecordColumn[]
    /** Names the list for a screen reader: "Sessions". */
    label: string
    tabs?: readonly RecordTab[]
    /** Counts read as a dash until the rows have arrived. */
    loading?: boolean
    /**
     * Drawn as a table of its own: a bordered frame, a shaded head, solid rules. For a
     * list that sits beside the activity feed and has to read as its twin. Flush is still
     * the default, because a section heading above a list already says what it is.
     */
    framed?: boolean
  }>(),
  { tabs: () => [], loading: false, framed: false }
)

/** The active tab's key. Owned by the caller, which is what filters the rows. */
const tab = defineModel<string>('tab', { default: '' })

/** The column in force, or null for the list's own order. The caller does the sorting. */
const sort = defineModel<TableSort | null>('sort', { default: null })

const tracks = computed(() => props.columns.map(column => column.width).join(' '))

/* A head of plain labels is furniture and stays hidden from a screen reader, which is
   what the row's own `data-label` is for. A head that sorts holds controls, and a control
   inside `aria-hidden` is one a keyboard reaches and a reader cannot hear. */
const sortable = computed(() => props.columns.some(column => column.sortable === true))
</script>

<template>
  <div :class="framed ? 'record--framed' : ''" :style="{ '--record-columns': tracks }" class="record">
    <div v-if="tabs.length > 0 || $slots.toolbar" class="record__toolbar">
      <div
        v-if="tabs.length > 0"
        :aria-label="`Filter ${label.toLowerCase()}`"
        class="record__tabs"
        role="group"
      >
        <button
          v-for="entry in tabs"
          :key="entry.key"
          :aria-pressed="tab === entry.key"
          type="button"
          @click="tab = entry.key"
        >
          {{ entry.label }}
          <span>{{ loading ? "–" : entry.count }}</span>
        </button>
      </div>
      <div class="record__toolbar-end">
        <slot name="toolbar" />
      </div>
    </div>

    <div :aria-hidden="sortable ? undefined : 'true'" class="record__head">
      <span
        v-for="column in columns"
        :key="column.key"
        :class="column.align === 'end' ? 'record__head-cell--end' : ''"
      >
        <SortHeader
          v-if="column.sortable"
          :align="column.align"
          :direction="directionOf(sort, column.key)"
          :label="column.label"
          @toggle="sort = cycleSort(sort, column.key)"
        />
        <template v-else>{{ column.label }}</template>
      </span>
    </div>

    <ul :aria-label="label" class="record__rows">
      <slot />
    </ul>

    <slot name="empty" />
  </div>
</template>

<style scoped>
.record {
  --record-inset: 12px;
  --record-line: rgba(var(--v-theme-on-surface), 0.09);

  margin-inline: calc(var(--record-inset) * -1);
  min-width: 0;
}

/* The framed variant. The border supplies the inset, so the root stops paying it back
   with a negative margin, and the head takes a band of ink instead of standing on the
   section's own ground. Every rule inside is the frame's own line, which is what makes
   this list and the activity feed read as one pair. */
.record--framed {
  --record-inset: 14px;
  --record-line: rgba(var(--v-border-color), var(--v-border-opacity));

  margin-inline: 0;
  border: 1px solid var(--record-line);
  border-radius: 6px;
  overflow: hidden;
}

.record--framed .record__head {
  padding-block: 9px;
  background: rgba(var(--v-theme-on-surface), 0.025);
  color: rgba(var(--v-theme-on-surface), 0.55);
}

.record--framed .record__rows > :deep(li + li) {
  border-top-style: solid;
}

.record.record--framed :deep(.record-row:hover) {
  background: rgba(var(--v-theme-on-surface), 0.04);
}

.record__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-inline: var(--record-inset);
  border-bottom: 1px solid var(--record-line);
}

.record__tabs {
  display: flex;
  gap: 24px;
  min-width: 0;
  overflow-x: auto;
}

.record__tabs button {
  position: relative;
  display: flex;
  flex: none;
  align-items: center;
  gap: 7px;
  padding: 0 0 11px;
  font-size: 12px;
  line-height: 16px;
  color: rgba(var(--v-theme-on-surface), 0.5);
  white-space: nowrap;
}

.record__tabs button span {
  min-width: 18px;
  padding: 1px 5px;
  border-radius: 4px;
  background: rgba(var(--v-theme-on-surface), 0.05);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
  text-align: center;
}

.record__tabs button:hover {
  color: rgb(var(--v-theme-on-surface));
}

.record__tabs button[aria-pressed="true"] {
  color: rgb(var(--v-theme-on-surface));
  font-weight: 500;
}

/* Sits on the toolbar's own rule rather than above it, so the tab and the table below
   read as one surface. */
.record__tabs button[aria-pressed="true"]::after {
  content: "";
  position: absolute;
  right: 0;
  bottom: -1px;
  left: 0;
  height: 2px;
  background: rgba(var(--v-theme-on-surface), 0.7);
}

.record__toolbar-end {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
  padding-bottom: 9px;
}

.record__head {
  display: grid;
  grid-template-columns: var(--record-columns);
  align-items: center;
  gap: 0 20px;
  padding: 10px var(--record-inset);
  border-bottom: 1px solid var(--record-line);
  font-size: 11px;
  line-height: 16px;
  color: rgba(var(--v-theme-on-surface), 0.45);
}

.record__head-cell--end {
  text-align: right;
}

.record__rows {
  margin: 0;
  padding: 0;
  list-style: none;
}

.record__rows > :deep(li + li) {
  border-top: 1px dashed var(--record-line);
}

.record :deep(.record-row) {
  /* Stated because a `summary` is not box-sized by Vuetify's reset, and without it the
     min-height lands on the content box and the row draws two dozen pixels too tall. */
  box-sizing: border-box;
  display: grid;
  grid-template-columns: var(--record-columns);
  align-items: center;
  gap: 0 20px;
  min-height: 56px;
  padding: 12px var(--record-inset);
  font-size: 12px;
  transition: background-color 150ms;
}

.record :deep(.record-row:hover) {
  background: rgba(var(--v-theme-on-surface), 0.025);
}

.record :deep(.record-cell) {
  min-width: 0;
}

.record :deep(.record-cell--end) {
  justify-self: end;
  text-align: right;
}

/* Stacked. The head goes, every cell names itself, and the one control on the row keeps
   the top line beside the thing it acts on. */
@media (max-width: 900px) {
  .record__head {
    display: none;
  }

  .record :deep(.record-row) {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 12px 16px;
    padding-block: 14px;
  }

  .record :deep(.record-cell) {
    grid-column: 1 / -1;
  }

  .record :deep(.record-cell--primary) {
    grid-column: 1;
    grid-row: 1;
  }

  .record :deep(.record-cell--action) {
    grid-column: 2;
    grid-row: 1;
    justify-self: end;
  }

  /* A cell short enough to sit beside its neighbour, so a stacked row is four lines and
     not seven. Marked by the caller, which is the only thing that knows. */
  .record :deep(.record-cell--half) {
    grid-column: auto;
  }

  .record :deep(.record-cell--end) {
    justify-self: start;
    text-align: left;
  }

  .record :deep(.record-cell[data-label])::before {
    content: attr(data-label);
    display: block;
    margin-bottom: 4px;
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--v-theme-on-surface), 0.45);
  }
}
</style>
