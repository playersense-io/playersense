/**
 * The sort a detail page's small tables carry: one column at a time, three states.
 *
 * `DataTable` gets this from Vuetify, which the record lists do not use, so the cycle
 * lives here rather than being written out twice with the third state quietly dropped.
 * Null is the table's own order and not "unsorted": every list has one it arrives in, and
 * a reader who cycles a column back past descending is asking for that, not for nothing.
 */

export type SortDirection = 'asc' | 'desc'

export interface TableSort {
  key: string
  direction: SortDirection
}

/** Unsorted, ascending, descending, and back. The order Vuetify's own header cycles in. */
export function cycleSort(current: TableSort | null, key: string): TableSort | null {
  if (current === null || current.key !== key) return { key, direction: 'asc' }
  return current.direction === 'asc' ? { key, direction: 'desc' } : null
}

/** Which way a column is sorted, or null where the sort is on another column. */
export function directionOf(sort: TableSort | null, key: string): SortDirection | null {
  return sort !== null && sort.key === key ? sort.direction : null
}

/** Whether two sorts are the same, so a preset can say it is the one in force. */
export function sameSort(a: TableSort | null, b: TableSort | null): boolean {
  if (a === null || b === null) return a === b
  return a.key === b.key && a.direction === b.direction
}

/** A comparator's result, flipped where the sort is descending. */
export function applyDirection(compared: number, direction: SortDirection): number {
  return direction === 'asc' ? compared : -compared
}
