<script setup lang="ts" generic="T extends object">
/**
 * The button and menu a reader arranges a table's columns from: a checkbox per column
 * to show or hide it, a pair of arrows to move it, and a way back to the default. The
 * arrows are the keyboard's route to what the table's head offers by drag, and the two
 * write the same `ColumnState`, so neither can put the arrangement somewhere the other
 * cannot read. A fixed column is listed so the reader can see it is there, and is the
 * one row with its controls off.
 */
import {
  type ColumnState,
  type DataTableColumn,
  defaultColumnState,
  isDefaultColumnState,
  moveColumn,
  orderColumns,
  toggleHidden
} from '../utils/data-table'

const props = defineProps<{
  columns: readonly DataTableColumn<T>[]
}>()

const state = defineModel<ColumnState>({ required: true })

const ordered = computed(() => orderColumns(props.columns, state.value))
const shown = computed(() => ordered.value.filter(c => !state.value.hidden.includes(c.key)))
const pristine = computed(() => isDefaultColumnState(props.columns, state.value))

/* Fixed columns hold the front, so the first movable one has nothing to move above. */
const movable = computed(() => ordered.value.filter(column => !column.fixed))

function canMove(column: DataTableColumn<T>, direction: -1 | 1): boolean {
  if (column.fixed) return false
  const at = movable.value.findIndex(entry => entry.key === column.key)
  const next = at + direction
  return next >= 0 && next < movable.value.length
}

function move(column: DataTableColumn<T>, direction: -1 | 1) {
  if (!canMove(column, direction)) return
  const at = movable.value.findIndex(entry => entry.key === column.key)
  // Up: land before the previous one. Down: land before the one after the next, or last.
  const before
    = direction === -1 ? (movable.value[at - 1]?.key ?? null) : (movable.value[at + 2]?.key ?? null)
  state.value = { ...state.value, order: moveColumn(state.value.order, column.key, before) }
}

function toggle(column: DataTableColumn<T>) {
  if (column.fixed) return
  state.value = toggleHidden(state.value, column.key)
}

function reset() {
  state.value = defaultColumnState(props.columns)
}
</script>

<template>
  <v-menu :close-on-content-click="false" :offset="6" location="bottom end">
    <template #activator="{ props: activator }">
      <v-btn
        v-bind="activator"
        class="ps-toolbar-control text-xs font-medium"
        color="neutral"
        prepend-icon="material-symbols:view-column-outline"
        size="small"
        variant="text"
      >
        Columns
        <span
          v-if="!pristine"
          aria-label="Arrangement changed"
          class="ml-1.5 size-1.5 rounded-full bg-primary"
          role="img"
        />
      </v-btn>
    </template>

    <v-card class="w-72" flat>
      <div class="flex items-baseline justify-between px-3 pt-2.5 pb-1.5">
        <p class="m-0 text-xs font-medium text-ink">
          Columns
        </p>
        <p class="m-0 text-[11px] text-ink/55">
          <span class="font-mono tabular-nums">{{ shown.length }}</span>
          of
          <span class="font-mono tabular-nums">{{ ordered.length }}</span>
          shown
        </p>
      </div>

      <ul class="m-0 list-none p-1">
        <li
          v-for="column in ordered"
          :key="column.key"
          class="group flex h-8 items-center gap-1 rounded-(--ps-radius-x-small) pr-1 pl-1.5 transition-colors duration-150 hover:bg-ink/4"
        >
          <v-checkbox-btn
            :aria-label="`Show ${column.title}`"
            :disabled="column.fixed"
            :model-value="!state.hidden.includes(column.key)"
            color="primary"
            density="compact"
            @update:model-value="toggle(column)"
          />
          <span
            :class="column.fixed ? 'text-ink/55' : 'text-ink'"
            class="min-w-0 flex-1 truncate text-xs"
          >
            {{ column.title }}
            <span v-if="column.fixed" class="text-[11px] text-ink/40">· always shown</span>
          </span>
          <span v-if="!column.fixed" class="flex opacity-0 transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100">
            <button
              :aria-label="`Move ${column.title} up`"
              :disabled="!canMove(column, -1)"
              class="columns-menu__arrow"
              type="button"
              @click="move(column, -1)"
            >
              <Icon :aria-hidden="true" class="text-base" name="material-symbols:keyboard-arrow-up" />
            </button>
            <button
              :aria-label="`Move ${column.title} down`"
              :disabled="!canMove(column, 1)"
              class="columns-menu__arrow"
              type="button"
              @click="move(column, 1)"
            >
              <Icon :aria-hidden="true" class="text-base" name="material-symbols:keyboard-arrow-down" />
            </button>
          </span>
        </li>
      </ul>

      <div class="flex items-center justify-between border-t border-line px-3 py-2">
        <p class="m-0 text-[11px] text-ink/50">
          Drag a heading to move it, too.
        </p>
        <button
          :disabled="pristine"
          class="text-xs font-medium text-ink/70 transition-colors duration-150 hover:text-ink disabled:text-ink/30"
          type="button"
          @click="reset"
        >
          Reset
        </button>
      </div>
    </v-card>
  </v-menu>
</template>

<style scoped>
.columns-menu__arrow {
  align-items: center;
  border-radius: var(--ps-radius-x-small);
  color: rgba(var(--v-theme-on-surface), 0.6);
  display: inline-flex;
  height: 24px;
  justify-content: center;
  width: 24px;
}

.columns-menu__arrow:hover:not(:disabled) {
  background: rgba(var(--v-theme-on-surface), 0.06);
  color: rgb(var(--v-theme-on-surface));
}

.columns-menu__arrow:disabled {
  color: rgba(var(--v-theme-on-surface), 0.2);
}

/* The menu's checkboxes at the table's size, for the same reason the table's are. */
:deep(.v-selection-control) {
  --v-selection-control-size: 28px;

  flex: none;
  min-height: 0;
}

:deep(.v-selection-control__input > .v-icon) {
  font-size: 1.125rem;
}

:deep(.v-selection-control:not(.v-selection-control--dirty) .v-selection-control__input > .v-icon) {
  color: rgba(var(--v-theme-on-surface), 0.35);
}
</style>
