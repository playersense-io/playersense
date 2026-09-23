<script setup lang="ts">
import type { FilterGroup } from '../utils/components'

// The Filter button over a table, and the menu of choices it opens: one or more groups of
// checkboxes, each narrowing the rows to the values ticked in it. Nothing ticked in a group
// means that group does not filter. The count on the button is how many values are ticked
// in all, so a filtered table says so before the menu is opened.
//
// Checkbox rows are flex rows with the control `flex-none`: `v-checkbox-btn` grows and
// carries a grid area of its own, and left alone it pushes its label to the middle.
const props = defineProps<{
  groups: readonly FilterGroup[]
}>()

/** Ticked values, by group key. */
const model = defineModel<Record<string, string[]>>({ required: true })

const count = computed(() => Object.values(model.value).reduce((sum, values) => sum + values.length, 0))

function toggle(group: string, value: string) {
  const current = model.value[group] ?? []
  model.value = {
    ...model.value,
    [group]: current.includes(value) ? current.filter(item => item !== value) : [...current, value]
  }
}

function clear() {
  model.value = Object.fromEntries(props.groups.map(group => [group.key, []]))
}
</script>

<template>
  <v-menu :close-on-content-click="false" :offset="6" location="bottom end">
    <template #activator="{ props: activator }">
      <v-btn
        v-bind="activator"
        class="ps-toolbar-control text-xs font-medium"
        color="neutral"
        prepend-icon="material-symbols:filter-list"
        size="small"
        variant="text"
      >
        Filter
        <span
          v-if="count"
          class="
            ml-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary
            px-1 text-[10px] text-on-primary
          "
        >{{ count }}</span>
      </v-btn>
    </template>

    <v-card min-width="240">
      <div class="flex max-h-96 flex-col gap-3 overflow-y-auto p-3">
        <fieldset v-for="group in groups" :key="group.key" class="m-0 flex flex-col gap-0.5 border-0 p-0">
          <legend class="mb-1 p-0 text-[11px] text-ink/50">
            {{ group.label }}
          </legend>
          <label
            v-for="option in group.options"
            :key="option.value"
            class="
              flex h-8 cursor-pointer items-center gap-2 rounded-(--ps-radius-small) px-1 text-[13px]
              text-ink
              hover:bg-ink/5
            "
          >
            <v-checkbox-btn
              :model-value="(model[group.key] ?? []).includes(option.value)"
              class="flex-none"
              color="primary"
              density="compact"
              @update:model-value="toggle(group.key, option.value)"
            />
            <span class="truncate">{{ option.label }}</span>
          </label>
        </fieldset>
      </div>
      <div class="flex justify-end border-t border-line p-2">
        <v-btn
          :disabled="!count"
          class="text-xs"
          color="neutral"
          size="small"
          variant="text"
          @click="clear"
        >
          Clear
        </v-btn>
      </div>
    </v-card>
  </v-menu>
</template>
