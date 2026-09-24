<script setup lang="ts">
// A column of the rows flowing into a node, chosen from a menu: each with its type, its name
// and its key, and a search at the top for a wide join. Drawn as the outlined field beside
// it, at the same 36px, the way a flag rule's key picker is (AttributePicker), so a filter's
// condition reads as one row of fields.
const props = withDefaults(defineProps<{
  columns: readonly ReportColumn[]
  /** What the choice is, for a screen reader: "Condition 2 column". */
  label: string
  placeholder?: string
  /** Only columns of these types are offered: numbers for a sum. */
  types?: readonly ColumnType[]
}>(), {
  placeholder: 'Choose a column',
  types: undefined
})

const key = defineModel<string>({ required: true })

const open = ref(false)
const search = ref<string | null>('')

const offered = computed(() => props.columns.filter(column => !props.types || props.types.includes(column.type)))
const chosen = computed(() => props.columns.find(column => column.key === key.value) ?? null)

const shown = computed(() => {
  const needle = (search.value ?? '').trim().toLowerCase()
  return offered.value.filter(column => !needle || column.key.includes(needle) || column.label.toLowerCase().includes(needle))
})

function choose(next: string) {
  key.value = next
  open.value = false
}

function focusSearch() {
  document.querySelector<HTMLInputElement>('.v-overlay--active .ps-column-picker__search input')?.focus()
}
</script>

<template>
  <v-menu
    v-model="open"
    :close-on-content-click="false"
    :offset="4"
    location="bottom start"
    @after-enter="focusSearch"
    @update:model-value="search = ''"
  >
    <template #activator="{ props: activator }">
      <button
        v-bind="activator"
        :aria-label="`${label}: ${chosen?.label ?? 'none chosen'}`"
        :class="[open ? 'border-primary' : 'border-ink/25 hover:border-ink/45 dark:border-ink/10 dark:hover:border-ink/22', chosen ? 'text-ink' : 'text-ink/40']"
        :title="chosen ? `${chosen.label} (${chosen.key})` : undefined"
        class="inline-flex h-(--ps-toolbar-height) w-full min-w-0 items-center gap-2 rounded-(--ps-radius) border bg-transparent px-3 text-[13px] transition-colors duration-150 focus-visible:border-primary focus-visible:outline-none"
        type="button"
      >
        <template v-if="chosen">
          <Icon :name="COLUMN_TYPE_ICONS[chosen.type]" aria-hidden="true" class="flex-none text-sm text-ink/45" />
          <span class="min-w-0 truncate">{{ chosen.label }}</span>
        </template>
        <span v-else-if="key" class="min-w-0 truncate text-error">{{ key }}, gone</span>
        <span v-else class="flex-1 truncate text-left">{{ placeholder }}</span>
        <Icon aria-hidden="true" class="ml-auto flex-none text-base text-ink/40" name="material-symbols:unfold-more" />
      </button>
    </template>

    <v-card class="w-72 max-w-[calc(100vw-2rem)]" flat>
      <div v-if="offered.length > 6" class="border-b border-line p-2">
        <v-text-field
          v-model="search"
          aria-label="Find a column"
          class="ps-column-picker__search"
          clearable
          density="comfortable"
          hide-details
          placeholder="Find a column"
          prepend-inner-icon="material-symbols:search"
          variant="outlined"
        />
      </div>
      <div class="max-h-80 overflow-y-auto p-1">
        <button
          v-for="column in shown"
          :key="column.key"
          :class="column.key === key ? 'bg-ink/6' : 'hover:bg-ink/4'"
          class="flex w-full items-center gap-2 rounded-(--ps-radius-x-small) px-2 py-1.5 text-left transition-colors duration-150"
          type="button"
          @click="choose(column.key)"
        >
          <Icon :name="COLUMN_TYPE_ICONS[column.type]" aria-hidden="true" class="flex-none text-sm text-ink/45" />
          <span class="min-w-0 flex-1 truncate text-xs text-ink">{{ column.label }}</span>
          <span class="flex-none font-mono text-[10px] text-ink/40">{{ column.key }}</span>
        </button>
        <p v-if="!shown.length" class="m-0 px-2 py-3 text-xs text-ink/55">
          {{ offered.length ? 'No column matches.' : types ? 'No column of the right kind flows in here.' : 'Connect rows into it first.' }}
        </p>
      </div>
    </v-card>
  </v-menu>
</template>
