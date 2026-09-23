<script setup lang="ts">
/**
 * A column label that sorts, drawn the way `DataTable`'s head draws one so a record list
 * and the full tables sort by the same gesture.
 *
 * The arrow is only inked where the column is the sorted one, and shows faintly under the
 * pointer everywhere else: a row of labels each wearing an arrow reads as a row of
 * controls, which is what the head is not.
 */
import type { SortDirection } from '../utils/table-sort'

withDefaults(
  defineProps<{
    label: string
    /** Null where this column is not the one in force. */
    direction: SortDirection | null
    align?: 'start' | 'end'
  }>(),
  { align: 'start' }
)

defineEmits<{ toggle: [] }>()
</script>

<template>
  <button
    :class="align === 'end' ? 'justify-self-end' : ''"
    class="group/sort inline-flex h-4 max-w-full items-center gap-1 text-left text-inherit transition-colors duration-150 hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
    type="button"
    @click="$emit('toggle')"
  >
    <span class="truncate">{{ label }}</span>
    <Icon
      :class="direction === null ? 'opacity-0 group-hover/sort:opacity-40' : 'text-ink opacity-100'"
      :name="direction === 'desc' ? 'material-symbols:arrow-downward' : 'material-symbols:arrow-upward'"
      aria-hidden="true"
      class="flex-none text-[13px] transition-opacity duration-150"
    />
  </button>
</template>
