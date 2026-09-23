<script setup lang="ts">
import type { ActiveFilter } from '../utils/components'

// The row over a table. On the right, the controls — search, Filter, Columns — at their
// own widths. On the left, what is narrowing the rows: each active filter as a chip that
// removes itself, and Clear all; or, when nothing is, a quiet line saying so, which is
// what tells a reader the rows below are the whole set.
withDefaults(defineProps<{
  /** The filters in force, each already worded: "Category: Economy", "“lev”". */
  filters: readonly ActiveFilter[]
  /** The rule under it, for a toolbar over a table; off where it is a card of its own. */
  ruled?: boolean
}>(), {
  ruled: true
})

const emit = defineEmits<{
  remove: [key: string]
  clear: []
}>()
</script>

<template>
  <div :class="{ 'border-b border-line': ruled }" class="flex flex-wrap items-center justify-between gap-3 px-3 py-2">
    <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
      <span v-if="!filters.length" class="text-xs text-ink/45">No filters applied</span>

      <template v-else>
        <span
          v-for="filter in filters"
          :key="filter.key"
          class="
            inline-flex h-6 max-w-64 items-center gap-1 rounded-full bg-ink/5 pr-1 pl-2.5 text-xs
            text-ink
          "
        >
          <SegmentMark v-if="filter.mark" :mark="filter.mark" :size="12" />
          <span class="truncate">{{ filter.label }}</span>
          <button
            :aria-label="`Remove ${filter.label}`"
            class="
              flex size-4 flex-none items-center justify-center rounded-full text-ink/50
              hover:bg-ink/10 hover:text-ink
            "
            type="button"
            @click="emit('remove', filter.key)"
          >
            <Icon aria-hidden="true" class="text-xs" name="material-symbols:close" />
          </button>
        </span>
        <button
          class="
            ml-1 text-xs text-ink/55 transition-colors
            hover:text-ink
          "
          type="button"
          @click="emit('clear')"
        >
          Clear all
        </button>
      </template>
    </div>

    <div class="flex flex-none flex-wrap items-center gap-2">
      <slot />
    </div>
  </div>
</template>
