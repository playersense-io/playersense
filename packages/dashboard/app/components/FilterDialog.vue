<script setup lang="ts">
import type { FilterGroup, FilterOption } from '../utils/components'
import { matchParts } from '../utils/search'
/**
 * The Filter button, and the dialog it opens for filters too long for a menu: a country
 * among two hundred, a version among dozens. The groups down the left, each with how many
 * of its values are ticked; the chosen group's values on the right, under a search that
 * narrows by name and note and marks what it matched, each with its flag or platform mark.
 *
 * Ticks are a draft until Apply, so choosing five countries asks one question rather than
 * five, and Cancel or Escape leaves the page's filters as they were. Nothing ticked in a
 * group means that group does not filter — said in the list, so an empty group reads as
 * "every value" rather than "none".
 */
const props = defineProps<{
  groups: readonly FilterGroup[]
  /** Names the dialog: "Filter cohorts". */
  title?: string
}>()

/** Ticked values, by group key. */
const model = defineModel<Record<string, string[]>>({ required: true })

const open = ref(false)
const draft = ref<Record<string, string[]>>({})
const current = ref(props.groups[0]?.key ?? '')
const search = ref<string | null>('')

const count = computed(() => Object.values(model.value).reduce((sum, values) => sum + values.length, 0))
const draftCount = computed(() => Object.values(draft.value).reduce((sum, values) => sum + values.length, 0))

watch(open, (value) => {
  if (!value) return
  draft.value = Object.fromEntries(props.groups.map(group => [group.key, [...(model.value[group.key] ?? [])]]))
  search.value = ''
})

watch(current, () => {
  search.value = ''
})

const group = computed(() => props.groups.find(entry => entry.key === current.value) ?? props.groups[0])
const noun = computed(() => group.value?.noun ?? group.value?.label.toLowerCase() ?? 'value')
const ticked = computed(() => draft.value[group.value?.key ?? ''] ?? [])

const needle = computed(() => (search.value ?? '').trim().toLowerCase())

const shown = computed<readonly FilterOption[]>(() => {
  const options = group.value?.options ?? []
  if (!needle.value) return options
  return options.filter(option => [option.label, option.note].some(text => text?.toLowerCase().includes(needle.value)))
})

const partsOf = (text: string) => matchParts(text, needle.value)

function set(key: string, values: string[]) {
  draft.value = { ...draft.value, [key]: values }
}

function toggle(value: string) {
  const key = group.value?.key
  if (!key) return
  set(key, ticked.value.includes(value) ? ticked.value.filter(entry => entry !== value) : [...ticked.value, value])
}

function tickShown() {
  const key = group.value?.key
  if (key) set(key, [...new Set([...ticked.value, ...shown.value.map(option => option.value)])])
}

function clearGroup() {
  const key = group.value?.key
  if (key) set(key, [])
}

function clearAll() {
  draft.value = Object.fromEntries(props.groups.map(entry => [entry.key, []]))
}

function apply() {
  model.value = draft.value
  open.value = false
}

const field = useTemplateRef<{ focus: () => void }>('field')
</script>

<template>
  <v-btn
    class="ps-toolbar-control text-xs font-medium"
    color="neutral"
    prepend-icon="material-symbols:filter-list"
    size="small"
    variant="text"
    @click="open = true"
  >
    Filter
    <span
      v-if="count"
      class="ml-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] text-on-primary"
    >{{ count }}</span>
  </v-btn>

  <v-dialog v-model="open" max-width="760" @after-enter="field?.focus()">
    <v-card border flat>
      <header class="flex items-baseline justify-between gap-3 border-b border-line px-5 py-4">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ title ?? 'Filter' }}
        </h2>
        <p class="m-0 text-xs text-ink/55">
          <span class="font-mono">{{ draftCount }}</span> ticked
        </p>
      </header>

      <div class="grid min-h-0 sm:grid-cols-[13rem_1fr]">
        <nav aria-label="Filter by" class="flex flex-row gap-1 overflow-x-auto border-b border-line p-2 sm:flex-col sm:border-r sm:border-b-0">
          <button
            v-for="entry in groups"
            :key="entry.key"
            :aria-current="entry.key === group?.key || undefined"
            :class="entry.key === group?.key ? 'bg-ink/6 text-ink' : 'text-ink/70 hover:bg-ink/4 hover:text-ink'"
            class="flex h-9 flex-none items-center justify-between gap-3 rounded-(--ps-radius) px-3 text-left text-[13px] transition-colors duration-150"
            type="button"
            @click="current = entry.key"
          >
            <span class="truncate">{{ entry.label }}</span>
            <span
              v-if="(draft[entry.key] ?? []).length"
              class="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] text-on-primary"
            >{{ (draft[entry.key] ?? []).length }}</span>
            <span v-else class="text-[11px] text-ink/40">All</span>
          </button>
        </nav>

        <section v-if="group" :aria-label="group.label" class="flex min-h-0 flex-col">
          <div class="flex flex-col gap-2 p-3 pb-2">
            <v-text-field
              ref="field"
              v-model="search"
              :aria-label="`Find a ${noun}`"
              :placeholder="`Find a ${noun}`"
              autocomplete="off"
              clearable
              density="comfortable"
              hide-details
              prepend-inner-icon="material-symbols:search"
              variant="filled"
            />
            <div class="flex items-center justify-between px-1 text-[11px] text-ink/55">
              <span>
                {{ ticked.length ? `${ticked.length} of ${group.options.length} ticked` : `Every ${noun} — tick some to narrow` }}
              </span>
              <span class="flex gap-3">
                <button
                  :disabled="!shown.length"
                  class="font-medium text-ink/70 hover:text-ink disabled:text-ink/30"
                  type="button"
                  @click="tickShown"
                >
                  {{ needle ? 'Tick the shown' : 'Tick all' }}
                </button>
                <button
                  :disabled="!ticked.length"
                  class="font-medium text-ink/70 hover:text-ink disabled:text-ink/30"
                  type="button"
                  @click="clearGroup"
                >
                  Clear
                </button>
              </span>
            </div>
          </div>

          <div class="h-80 overflow-y-auto px-2 pb-2" role="group" :aria-label="`${group.label} values`">
            <label
              v-for="option in shown"
              :key="option.value"
              class="flex h-9 cursor-pointer items-center gap-2 rounded-(--ps-radius-small) px-1.5 text-[13px] text-ink hover:bg-ink/4"
            >
              <v-checkbox-btn
                :model-value="ticked.includes(option.value)"
                class="flex-none"
                color="primary"
                density="compact"
                @update:model-value="toggle(option.value)"
              />
              <SegmentMark v-if="option.mark" :mark="option.mark" />
              <span class="min-w-0 truncate">
                <template v-for="(part, at) in partsOf(option.label)" :key="at">
                  <mark v-if="part.match" class="ps-filter-dialog__match">{{ part.text }}</mark>
                  <template v-else>{{ part.text }}</template>
                </template>
              </span>
              <span v-if="option.note" class="ml-auto flex-none font-mono text-[11px] text-ink/45">
                <template v-for="(part, at) in partsOf(option.note)" :key="at">
                  <mark v-if="part.match" class="ps-filter-dialog__match">{{ part.text }}</mark>
                  <template v-else>{{ part.text }}</template>
                </template>
              </span>
            </label>
            <p v-if="!shown.length" class="m-0 py-10 text-center text-xs text-ink/55">
              No {{ noun }} matches “{{ search }}”.
            </p>
          </div>
        </section>
      </div>

      <footer class="flex items-center justify-between gap-2 border-t border-line px-5 py-3">
        <v-btn
          :disabled="!draftCount"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="clearAll"
        >
          Clear all
        </v-btn>
        <span class="flex gap-2">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Cancel
          </v-btn>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="apply"
          >
            Apply
          </v-btn>
        </span>
      </footer>
    </v-card>
  </v-dialog>
</template>

<style scoped>
/* The accent, faint, behind the letters that matched; the ink stays the text's own. */
.ps-filter-dialog__match {
  background: rgba(var(--v-theme-primary), 0.18);
  border-radius: 2px;
  color: inherit;
  padding: 0 1px;
}

/* The dialog's checkboxes at the list's size, as the Columns menu has them. */
:deep(.v-selection-control) {
  --v-selection-control-size: 28px;

  flex: none;
  min-height: 0;
}
</style>
