<script setup lang="ts" generic="T extends object">
/**
 * The panel's one list surface: Vuetify's data table with the head, the cells and the
 * foot drawn by us, so every list in the panel sorts, selects, pages and arranges its
 * columns the same way. What varies per screen is the column list and the cells, which
 * come in as `columns` and `cell.<key>` slots; what does not vary lives here.
 *
 * The columns are the reader's to arrange. The head is drawn by hand in the `headers`
 * slot because a column is moved by dragging its heading, and Vuetify's own head has no
 * hook for that; the arrangement itself is a `ColumnState` the page owns and persists,
 * and the pure rules for changing it are in `utils/data-table.ts` so a drop that would
 * put a hidden column somewhere odd is tested rather than found. A fixed column is the
 * one a row is told apart by, and it can be neither moved nor hidden.
 *
 * The foot is ours because the default is a `v-select` and two icon buttons at a size
 * this panel does not use anywhere else, and `paged` takes it away for a table whose row
 * count is a constant. The rows are Vuetify's, addressed by `item.<key>`
 * slots forwarded under the shorter `cell.<key>`, and the cell default draws the value in
 * the mono face when the column asks for it: anything read digit by digit, which is every
 * date, count and id in the panel.
 *
 * The style block is unscoped for the reason MetricChart's tooltip block is: most of the
 * markup is Vuetify's, and a scoped rule reaches the root and nothing under it. Every
 * rule is under `.data-table`, the class this component puts on the root.
 */
import {
  arrangeColumns,
  type ColumnState,
  type DataTableColumn,
  type DataTableSort,
  moveColumn,
  orderOf
} from '../utils/data-table'

const props = withDefaults(
  defineProps<{
    columns: readonly DataTableColumn<T>[]
    items: readonly T[]
    /** The property a row is identified by, and what `selected` holds. */
    itemValue?: string
    selectable?: boolean
    loading?: boolean
    /** What the table lists, for a screen reader: "Users". */
    label: string
    emptyTitle?: string
    emptyDescription?: string
    pageSizes?: readonly number[]
    /**
     * Off for a table of a fixed, small set, which draws every row and no foot at all.
     * A pager under fourteen rows that will never become fifteen is furniture that says
     * "1-14 of 14" and offers to show fewer.
     */
    paged?: boolean
  }>(),
  {
    itemValue: 'id',
    selectable: false,
    loading: false,
    emptyTitle: 'Nothing to show.',
    emptyDescription: '',
    pageSizes: () => [10, 25, 50, 100],
    paged: true
  }
)

const selected = defineModel<string[]>('selected', { default: () => [] })
const sortBy = defineModel<DataTableSort[]>('sortBy', { default: () => [] })
const page = defineModel<number>('page', { default: 1 })
const itemsPerPage = defineModel<number>('itemsPerPage', { default: 10 })
const columnState = defineModel<ColumnState>('columnState', { required: true })

/* Vuetify's own "all of them", so an unpaged table is one page however many rows arrive.
   Written through rather than replaced, so a page that keeps the model still owns it. */
const perPage = computed({
  get: () => (props.paged ? itemsPerPage.value : -1),
  set: (value: number) => {
    itemsPerPage.value = value
  }
})

const slots = useSlots()

/* The selection bar is up, and the select column's heading is painted to match it. */
const showingSelection = computed(
  () => props.selectable && selected.value.length > 0 && slots.selection !== undefined
)

/* The columns in the reader's arrangement, and the same list keyed for the head. */
const arranged = computed(() => arrangeColumns(props.columns, columnState.value))
const byKey = computed(() => new Map(props.columns.map(column => [column.key, column])))

interface HeaderDef {
  key: string
  title: string
  sortable: boolean
  align: 'start' | 'end'
  nowrap: boolean
  /** Pinned while the table scrolls sideways: `true` at the start, `"end"` at the end. */
  fixed?: boolean | 'end'
  width?: number
  minWidth?: number
  value?: (item: T) => unknown
  sort?: (a: unknown, b: unknown) => number
}

/*
 * Vuetify's header list, built from the arrangement so the row cells follow the same
 * order as the head. Optional fields are spread in only when set: an explicit
 * `undefined` is not an absent property under exactOptionalPropertyTypes.
 *
 * The select column is declared here rather than left to `show-select`, which would add
 * one unpinned; declared, it pins with the fixed columns, and Vuetify sees the key and
 * adds no second. A fixed column is pinned at the start and the actions at the end, so a
 * row can be recognised and acted on however far the middle has scrolled. The actions
 * column exists only when the page supplies the slot.
 */
const headers = computed<HeaderDef[]>(() => {
  const list: HeaderDef[] = []
  if (props.selectable) {
    list.push({
      key: SELECT,
      title: '',
      sortable: false,
      align: 'start',
      nowrap: true,
      width: 44,
      fixed: true
    })
  }
  for (const column of arranged.value) {
    list.push({
      key: column.key,
      title: column.title,
      sortable: column.sortable ?? true,
      align: column.align ?? 'start',
      nowrap: true,
      ...(column.fixed ? { fixed: true } : {}),
      ...(column.value === undefined ? {} : { value: column.value }),
      ...(column.sort === undefined ? {} : { sort: column.sort }),
      ...(column.width === undefined ? {} : { width: column.width }),
      ...(column.minWidth === undefined ? {} : { minWidth: column.minWidth })
    })
  }
  if (slots.actions) {
    list.push({
      key: ACTIONS,
      title: '',
      sortable: false,
      align: 'end',
      nowrap: true,
      width: 56,
      fixed: 'end'
    })
  }
  return list
})

/**
 * What the head needs to know of a column Vuetify has laid out: where it is pinned and
 * how far in. Vuetify computes the offsets from the widths of the pinned columns before
 * it, and puts them on the internal header the slot receives; the classes and styles
 * below are the ones its own head would have written, restated because the head is ours.
 */
interface LaidOutColumn {
  key: string | null
  fixed?: boolean | 'start' | 'end'
  fixedOffset?: number
  fixedEndOffset?: number
  lastFixed?: boolean
  firstFixedEnd?: boolean
}

function fixedSide(column: LaidOutColumn): 'start' | 'end' | null {
  if (column.fixed === 'end') return 'end'
  return column.fixed ? 'start' : null
}

function fixedClass(column: LaidOutColumn) {
  const side = fixedSide(column)
  return {
    'v-data-table__th--fixed': side !== null,
    'v-data-table-column--fixed': side === 'start',
    'v-data-table-column--fixed-end': side === 'end',
    'v-data-table-column--last-fixed': column.lastFixed === true,
    'v-data-table-column--first-fixed-end': column.firstFixedEnd === true
  }
}

function fixedStyle(column: LaidOutColumn) {
  const side = fixedSide(column)
  return {
    left: side === 'start' ? sized(column.fixedOffset) : undefined,
    right: side === 'end' ? sized(column.fixedEndOffset) : undefined
  }
}

/* Vuetify wants a mutable array, and a readonly one is not assignable to it. */
const rows = computed(() => [...props.items])

const ACTIONS = 'actions'
const SELECT = 'data-table-select'

function idOf(item: T): string {
  // Items are addressed by the property `itemValue` names, which the type cannot know.
  return String((item as Record<string, unknown>)[props.itemValue])
}

function rowProps({ item }: { item: T }) {
  return { class: selected.value.includes(idOf(item)) ? 'data-table__row--selected' : '' }
}

/* A column can move unless it is fixed, the select box or the actions. */
function movable(key: string | null): key is string {
  if (key === null || key === SELECT || key === ACTIONS) return false
  return byKey.value.get(key)?.fixed !== true
}

function alignEnd(key: string | null): boolean {
  return key === ACTIONS || (key !== null && byKey.value.get(key)?.align === 'end')
}

function ariaSort(key: string | null): 'ascending' | 'descending' | 'none' {
  const order = key === null ? null : orderOf(sortBy.value, key)
  return order === 'asc' ? 'ascending' : order === 'desc' ? 'descending' : 'none'
}

function sized(value: string | number | undefined): string | undefined {
  return value === undefined ? undefined : typeof value === 'number' ? `${value}px` : value
}

/*
 * Reordering by drag. The dragged key and the drop target are kept here rather than in
 * the DataTransfer, which some browsers empty between events. The side is decided by
 * where the pointer is across the target, and the drop resolves to "before this key"
 * against the full order, hidden columns included, so a column dropped after the last
 * visible one lands after the hidden ones behind it too rather than between them.
 */
const dragging = ref<string | null>(null)
const dropAt = ref<{ key: string, side: 'before' | 'after' } | null>(null)

function startDrag(event: DragEvent, key: string | null) {
  if (!movable(key)) return
  dragging.value = key
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', key)
  }
}

function dragOver(event: DragEvent, key: string | null) {
  if (dragging.value === null || !movable(key) || key === dragging.value) {
    dropAt.value = null
    return
  }
  event.preventDefault()
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect() // a th listens
  dropAt.value = { key, side: event.clientX < rect.left + rect.width / 2 ? 'before' : 'after' }
}

function drop(event: DragEvent) {
  event.preventDefault()
  const key = dragging.value
  const target = dropAt.value
  endDrag()
  if (key === null || target === null) return

  const order = columnState.value.order
  const before
    = target.side === 'before' ? target.key : (order[order.indexOf(target.key) + 1] ?? null)
  columnState.value = { ...columnState.value, order: moveColumn(order, key, before) }
}

function endDrag() {
  dragging.value = null
  dropAt.value = null
}

function headClass(key: string | null) {
  return {
    'data-table__th--movable': movable(key),
    'data-table__th--dragging': dragging.value !== null && key === dragging.value,
    'data-table__th--drop-before': dropAt.value?.key === key && dropAt.value.side === 'before',
    'data-table__th--drop-after': dropAt.value?.key === key && dropAt.value.side === 'after',
    'text-right': alignEnd(key)
  }
}

/*
 * The rows drawn while the table loads: bones in the table's own columns, so the head,
 * the widths and the pinned columns are already where they will be and the rows arrive
 * into them. One per row the page will show, capped where a long page of bones would
 * only say "many". The line widths vary by position and are the same on every load,
 * the way MetricSkeleton's are, so the block reads as text waiting and not as a pattern.
 * The bones are still: the foot says the table is loading, in words, and `aria-busy`
 * says it to a screen reader.
 */
type Bone = 'select' | 'actions' | 'line' | 'identity' | 'pill' | 'glyphs'

const LINE_WIDTHS = [72, 54, 66, 46, 78, 58]

const skeletonRows = computed(() =>
  Math.min(Math.max(props.paged ? itemsPerPage.value : props.items.length, 3), 10)
)

function boneOf(key: string | null): Bone {
  if (key === SELECT) return 'select'
  if (key === ACTIONS) return 'actions'
  const column = key === null ? undefined : byKey.value.get(key)
  return column?.skeleton ?? (column?.fixed ? 'identity' : 'line')
}

function boneWidth(row: number, col: number): string {
  return `${LINE_WIDTHS[(row + col) % LINE_WIDTHS.length]}%`
}

/* The foot's arithmetic, from the models rather than the slot, so it needs no payload. */
const total = computed(() => props.items.length)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / itemsPerPage.value)))
const from = computed(() => (total.value === 0 ? 0 : (page.value - 1) * itemsPerPage.value + 1))
const to = computed(() => Math.min(page.value * itemsPerPage.value, total.value))

function display(value: unknown): string {
  if (value === null || value === undefined || value === '') return '–'
  return String(value)
}

/*
 * How wide the visible part of the table is, which is what the empty state is centred
 * on. Vuetify owns the scrolling element, so it is found rather than referenced, and
 * measured rather than read off the columns: what the reader sees is the card's width
 * and the columns can total more than that.
 */
const root = useTemplateRef<HTMLElement>('root')
const scroller = computed(
  () => root.value?.querySelector<HTMLElement>('.v-table__wrapper') ?? null
)
const { width: visible } = useElementSize(scroller)
const emptyStyle = computed(() => (visible.value > 0 ? { width: `${visible.value}px` } : {}))
</script>

<template>
  <div ref="root" class="relative">
    <!-- Laid over the head rather than above the table, so selecting a row moves nothing:
         a bar that pushed the rows down would put a different row under the pointer
         that had just clicked one. It starts past the select column, which keeps the
         select-all box in reach, and takes the head's height and radius so the head
         reads as having turned into the bar. -->
    <div
      v-if="showingSelection"
      class="data-table__selection absolute top-0 right-0 z-10 flex items-center gap-1 overflow-x-auto rounded-r-(--ps-radius-x-small) px-1 text-xs whitespace-nowrap"
    >
      <span class="mr-2 pl-2 font-medium text-ink">
        <span class="font-mono tabular-nums">{{ selected.length }}</span>
        selected
      </span>
      <slot name="selection" />
      <button
        class="ml-auto inline-flex h-7 items-center rounded-(--ps-radius-x-small) px-2 text-xs font-medium text-ink/70 transition-colors duration-150 hover:bg-ink/5 hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        type="button"
        @click="selected = []"
      >
        Clear
      </button>
    </div>

    <v-data-table
      v-model="selected"
      v-model:items-per-page="perPage"
      v-model:page="page"
      v-model:sort-by="sortBy"
      :aria-busy="loading"
      :aria-label="label"
      :headers="headers"
      :item-value="itemValue"
      :items="rows"
      :loading="loading"
      :mobile="false"
      :row-props="rowProps"
      :show-select="selectable"
      class="data-table"
      density="compact"
      hide-default-footer
      hover
      select-strategy="page"
    >
      <template #headers="{ columns: headerColumns, toggleSort, allSelected, someSelected, selectAll }">
        <tr>
          <th
            v-for="column in headerColumns"
            :key="column.key ?? 'blank'"
            :aria-sort="column.key === SELECT || column.key === ACTIONS ? undefined : ariaSort(column.key)"
            :class="[
              headClass(column.key),
              fixedClass(column),
              { 'data-table__th--selection': column.key === SELECT && showingSelection }
            ]"
            :draggable="movable(column.key)"
            :style="{ ...fixedStyle(column), width: sized(column.width), minWidth: sized(column.minWidth) }"
            class="data-table__th group"
            scope="col"
            @dragend="endDrag"
            @dragover="dragOver($event, column.key)"
            @dragstart="startDrag($event, column.key)"
            @drop="drop"
          >
            <v-checkbox-btn
              v-if="column.key === SELECT"
              :indeterminate="someSelected && !allSelected"
              :model-value="allSelected"
              aria-label="Select every row on this page"
              color="primary"
              density="compact"
              @update:model-value="selectAll"
            />

            <template v-else-if="column.key === ACTIONS">
              <span class="sr-only">Actions</span>
            </template>

            <button
              v-else-if="column.sortable"
              class="data-table__sort inline-flex h-6 max-w-full items-center gap-1 rounded-(--ps-radius-x-small) text-left transition-colors duration-150 hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              type="button"
              @click="toggleSort(column)"
            >
              <Icon
                v-if="movable(column.key)"
                aria-hidden="true"
                class="data-table__grip -ml-1 text-sm opacity-0 transition-opacity duration-150 group-hover:opacity-40"
                name="material-symbols:drag-indicator"
              />
              <span class="truncate">{{ column.title }}</span>
              <Icon
                :class="ariaSort(column.key) === 'none' ? 'opacity-0 group-hover:opacity-40' : 'text-ink opacity-100'"
                :name="ariaSort(column.key) === 'descending' ? 'material-symbols:arrow-downward' : 'material-symbols:arrow-upward'"
                aria-hidden="true"
                class="text-[13px] transition-opacity duration-150"
              />
            </button>

            <span v-else class="inline-flex h-6 items-center gap-1">
              <Icon
                v-if="movable(column.key)"
                aria-hidden="true"
                class="data-table__grip -ml-1 text-sm opacity-0 transition-opacity duration-150 group-hover:opacity-40"
                name="material-symbols:drag-indicator"
              />
              {{ column.title }}
            </span>
          </th>
        </tr>
      </template>

      <template v-for="column in arranged" :key="column.key" #[`item.${column.key}`]="{ item, value }">
        <slot
          :column="column"
          :item="item"
          :name="`cell.${column.key}`"
          :value="value"
        >
          <span :class="column.mono ? 'font-mono tabular-nums' : ''" class="block truncate">
            {{ display(value) }}
          </span>
        </slot>
      </template>

      <template v-if="slots.actions" #item.actions="{ item }">
        <div class="flex justify-end">
          <slot :item="item" name="actions" />
        </div>
      </template>

      <!-- Vuetify's own sign of loading is a progress bar under the head. The bones
           below are the sign here, so its row is left empty and collapses. -->
      <template #loader>
        <span />
      </template>

      <template v-if="loading" #body="{ columns: bodyColumns }">
        <tr
          v-for="row in skeletonRows"
          :key="row"
          aria-hidden="true"
          class="data-table__bones"
        >
          <td
            v-for="(column, col) in bodyColumns"
            :key="column.key ?? col"
            :class="[fixedClass(column), { 'text-right': alignEnd(column.key) }]"
            :style="fixedStyle(column)"
          >
            <span v-if="boneOf(column.key) === 'select'" class="data-table__bone data-table__bone--box" />

            <span v-else-if="boneOf(column.key) === 'identity'" class="flex items-center gap-2.5">
              <span class="data-table__bone data-table__bone--avatar" />
              <span class="flex flex-col gap-1.5">
                <span :style="{ width: `${88 + ((row * 7) % 40)}px` }" class="data-table__bone data-table__bone--line" />
                <span :style="{ width: `${120 + ((row * 11) % 48)}px` }" class="data-table__bone data-table__bone--line data-table__bone--thin" />
              </span>
            </span>

            <span v-else-if="boneOf(column.key) === 'pill'" class="data-table__bone data-table__bone--pill" />

            <span v-else-if="boneOf(column.key) === 'glyphs'" class="flex items-center gap-1">
              <span class="data-table__bone data-table__bone--glyph" />
              <span v-if="(row + col) % 2 === 0" class="data-table__bone data-table__bone--glyph" />
            </span>

            <span
              v-else-if="boneOf(column.key) === 'line'"
              :style="{ width: boneWidth(row, col) }"
              class="data-table__bone data-table__bone--line"
            />
          </td>
        </tr>
      </template>

      <!-- The empty state is centred on what the reader can see, not on the table. A
           table whose columns are wider than its card scrolls sideways, and a state
           centred on that full width sits half outside the card with a scrollbar under
           it. Sticking it to the wrapper's left edge at the wrapper's own width is the
           same trick the pinned columns use, and it needs the measurement because only
           the browser knows how wide the visible part is. -->
      <template #no-data>
        <div :style="emptyStyle" class="sticky left-0">
          <slot name="empty">
            <div class="flex flex-col items-center gap-1 px-4 py-14 text-center">
              <Icon aria-hidden="true" class="mb-1 text-2xl text-ink/25" name="material-symbols:search-off" />
              <p class="m-0 text-sm font-medium text-ink">
                {{ emptyTitle }}
              </p>
              <p v-if="emptyDescription" class="m-0 max-w-sm text-xs text-ink/55">
                {{ emptyDescription }}
              </p>
            </div>
          </slot>
        </div>
      </template>

      <!-- Always overridden, empty included: left to Vuetify the slot falls back to its
           own footer, which is the control this table replaced. -->
      <template #bottom>
        <div
          v-if="paged"
          class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-line px-3 py-2 text-xs text-ink/60"
        >
          <p :aria-live="loading ? 'polite' : undefined" :role="loading ? 'status' : undefined" class="m-0">
            <template v-if="loading">
              Loading {{ label.toLowerCase() }}…
            </template>
            <template v-else-if="total === 0">
              No rows
            </template>
            <template v-else>
              <span class="font-mono tabular-nums text-ink">{{ from }}–{{ to }}</span>
              of
              <span class="font-mono tabular-nums text-ink">{{ total }}</span>
            </template>
          </p>

          <div class="flex items-center gap-2">
            <v-menu :offset="6" location="top end">
              <template #activator="{ props: activator }">
                <button
                  v-bind="activator"
                  class="inline-flex h-7 items-center gap-1 rounded-(--ps-radius-x-small) px-2 text-xs text-ink/70 transition-colors duration-150 hover:bg-ink/5 hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
                  type="button"
                >
                  <span class="font-mono tabular-nums text-ink">{{ itemsPerPage }}</span>
                  per page
                  <Icon aria-hidden="true" class="text-sm" name="material-symbols:unfold-more" />
                </button>
              </template>
              <v-list density="compact" min-width="132" nav>
                <v-list-item
                  v-for="size in pageSizes"
                  :key="size"
                  :active="size === itemsPerPage"
                  :title="`${size} per page`"
                  @click="itemsPerPage = size"
                />
              </v-list>
            </v-menu>

            <div class="flex items-center gap-0.5">
              <button
                :disabled="loading || page <= 1"
                aria-label="Previous page"
                class="data-table__page-btn"
                type="button"
                @click="page = page - 1"
              >
                <Icon aria-hidden="true" class="text-base" name="material-symbols:chevron-left" />
              </button>
              <span class="px-1 font-mono text-xs tabular-nums text-ink/70">
                {{ page }}<span class="text-ink/35"> / </span>{{ pageCount }}
              </span>
              <button
                :disabled="loading || page >= pageCount"
                aria-label="Next page"
                class="data-table__page-btn"
                type="button"
                @click="page = page + 1"
              >
                <Icon aria-hidden="true" class="text-base" name="material-symbols:chevron-right" />
              </button>
            </div>
          </div>
        </div>
      </template>
    </v-data-table>
  </div>
</template>

<style>
/*
 * Everything below is under `.data-table`, the class this component puts on Vuetify's
 * root, so none of it reaches a table drawn anywhere else. The selectors mirror
 * Vuetify's own (`.v-table > .v-table__wrapper > table > ...`) because that is the
 * specificity its rules carry and an unlayered rule at the same specificity wins.
 */
.data-table {
  --v-table-header-height: 36px;
  --v-table-row-height: 44px;

  background: transparent;
  font-size: 0.75rem;
  line-height: 1rem;
}

/*
 * The head is a quiet band set a step under the surface, and an 8px gap parts it from
 * the first row: it is a caption over the list and not a row of it. The gap is a
 * pseudo-row on the head, which is the one way to put air inside a table without a
 * spacer row in the markup. The tint is mixed to an opaque colour rather than laid on as
 * an alpha, because a pinned heading has scrolled cells passing under it.
 *
 * The ends are square. They were rounded, and a rounded end only reads as one where
 * there is ground beside it to read against: the head runs the full width of the panel,
 * so both corners sat on the panel's own edge and the curve read as a clipped bar rather
 * than a bar with ends. Insetting the band instead would have taken the head out of line
 * with the columns under it, which is the one thing a table's head has to keep.
 */
.data-table > .v-table__wrapper > table > thead > tr > th {
  background: color-mix(in srgb, rgb(var(--v-theme-on-surface)) 3.5%, rgb(var(--v-theme-surface)));
  border-bottom: 0;
  color: rgba(var(--v-theme-on-surface), 0.6);
  font-size: 0.75rem;
  font-weight: 500;
  height: var(--v-table-header-height);
  padding: 0 12px;
  transition: none;
  user-select: none;
  white-space: nowrap;
}

/*
 * Pinned columns. Vuetify's own rules for these are layered and ours are not, so the
 * position has to be restated here or the rule above would flatten it. Vuetify also
 * draws a rule down the last pinned column's edge; the head's bar is one piece and the
 * rows part on hairlines already, so it goes.
 */
.data-table > .v-table__wrapper > table > thead > tr > th.v-data-table__th--fixed {
  position: sticky;
  z-index: 2;
}

.data-table > .v-table__wrapper > table > thead > tr > th.v-data-table-column--last-fixed,
.data-table > .v-table__wrapper > table > tbody > tr > td.v-data-table-column--last-fixed {
  border-right: 0;
}

.data-table > .v-table__wrapper > table > thead > tr > th.v-data-table-column--first-fixed-end,
.data-table > .v-table__wrapper > table > tbody > tr > td.v-data-table-column--first-fixed-end {
  border-left: 0;
}

.data-table > .v-table__wrapper > table > thead::after {
  content: "";
  display: table-row;
  height: 8px;
}

/* The row Vuetify adds under the head for its progress bar while loading. The bones are
   the sign of loading here, and the head rule above would otherwise give this row the
   head's full height and colour: a second, empty head. */
.data-table > .v-table__wrapper > table > thead > tr.v-data-table-progress {
  display: none;
}

/*
 * The selection bar: the head's height, past the select column, and the select column's
 * own heading painted the same, so the two make one bar with the select-all box at its
 * left end. Neutral rather than the accent, a step darker than the head on both themes:
 * a bar of actions is furniture, and the accent is kept for the boxes that are ticked.
 * Opaque because it covers the head.
 */
.data-table__selection {
  height: 36px;
  left: 44px;
}

.data-table__selection,
.data-table > .v-table__wrapper > table > thead > tr > th.data-table__th--selection {
  background: color-mix(in srgb, rgb(var(--v-theme-on-surface)) 9%, rgb(var(--v-theme-surface)));
}

:where(.v-theme--dark) .data-table__selection,
:where(.v-theme--dark) .data-table > .v-table__wrapper > table > thead > tr > th.data-table__th--selection {
  background: color-mix(in srgb, black 40%, rgb(var(--v-theme-surface)));
}

.data-table__th--movable {
  cursor: grab;
}

.data-table__th--dragging {
  opacity: 0.35;
}

/* The drop mark: a two-pixel rule on the edge the column will land against. */
.data-table__th--drop-before {
  box-shadow: inset 2px 0 0 rgb(var(--v-theme-primary));
}

.data-table__th--drop-after {
  box-shadow: inset -2px 0 0 rgb(var(--v-theme-primary));
}

.data-table__sort {
  color: inherit;
}

/* Rows part on hairlines and the last one ends on the foot's own rule, not a second. */
.data-table > .v-table__wrapper > table > tbody > tr > td {
  border-bottom: thin solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgb(var(--v-theme-on-surface));
  padding: 0 12px;
}

.data-table > .v-table__wrapper > table > tbody > tr:last-child > td {
  border-bottom: 0;
}

/* Vuetify's hover wash is the border colour, which under our opaque border is a grey
   that reads as disabled. Ink at three percent is a lift instead. */
.data-table.v-table--hover > .v-table__wrapper > table > tbody > tr:hover > td::after {
  background: rgba(var(--v-theme-on-surface), 0.03);
}

/* Mixed opaque for the same reason the head is: a pinned cell in a selected row has the
   rest of the row scrolling under it. */
.data-table > .v-table__wrapper > table > tbody > tr.data-table__row--selected > td {
  background: color-mix(in srgb, rgb(var(--v-theme-primary)) 6%, rgb(var(--v-theme-surface)));
}

/* Vuetify's checkbox is a 40px target around a 24px glyph, sized for a form. In a row
   that is 44px tall it is the row. */
.data-table .v-selection-control {
  --v-selection-control-size: 28px;

  flex: none;
  min-height: 0;
}

.data-table .v-selection-control__input > .v-icon {
  font-size: 1.125rem;
}

.data-table .v-selection-control:not(.v-selection-control--dirty) .v-selection-control__input > .v-icon {
  color: rgba(var(--v-theme-on-surface), 0.35);
}

/*
 * Bones, in the skeleton colour the rest of the panel uses. Each is the size of what
 * it stands for: a 16px box for the checkbox, a 28px disc for the avatar, a 20px pill
 * for a status, 24px squares for method glyphs, and rules at the height of a line of
 * the table's type. Still, like MetricSkeleton's, and for the same reason.
 */
.data-table__bones > td {
  vertical-align: middle;
}

.data-table__bone {
  background: var(--ps-skeleton-bone);
  display: inline-block;
  flex: none;
  vertical-align: middle;
}

.data-table__bone--box {
  border-radius: 4px;
  height: 16px;
  margin-inline: 6px;
  width: 16px;
}

.data-table__bone--avatar {
  border-radius: 999px;
  height: 28px;
  width: 28px;
}

.data-table__bone--line {
  border-radius: 999px;
  height: 10px;
  max-width: 100%;
}

.data-table__bone--thin {
  height: 8px;
}

.data-table__bone--pill {
  border-radius: 999px;
  height: 20px;
  width: 68px;
}

.data-table__bone--glyph {
  border-radius: var(--ps-radius-x-small);
  height: 24px;
  width: 24px;
}

/* The foot's two paging buttons, and nothing else in the panel, so they live here. */
.data-table__page-btn {
  align-items: center;
  border-radius: var(--ps-radius-x-small);
  color: rgba(var(--v-theme-on-surface), 0.7);
  display: inline-flex;
  height: 28px;
  justify-content: center;
  transition: background-color 150ms var(--v-transition-standard-easing, ease);
  width: 28px;
}

.data-table__page-btn:hover:not(:disabled) {
  background: rgba(var(--v-theme-on-surface), 0.05);
  color: rgb(var(--v-theme-on-surface));
}

.data-table__page-btn:disabled {
  color: rgba(var(--v-theme-on-surface), 0.25);
  cursor: default;
}

.data-table__page-btn:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: 2px;
}
</style>
