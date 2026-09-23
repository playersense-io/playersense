<script setup lang="ts">
import type { GhostSelectOption } from '../utils/components'
/**
 * A heading that is a choice from a long list: it reads and sits like `GhostSelect`, and
 * opens a dialog rather than a menu, because twenty options in a dropdown run off the
 * screen and have to be hunted for. The dialog leads with a search, ready to type into,
 * that narrows by name, note and group at once; the options sit in their groups, two to a
 * row, each with the note that says what it answers.
 *
 * What the search matched is marked in each label and note, so the reader sees why an
 * option is still there. The keyboard goes all the way: arrows move through what the
 * search left, Enter chooses it, Escape closes. What is chosen is marked, and the list
 * opens with it in view.
 */
const props = withDefaults(
  defineProps<{
    options: readonly GhostSelectOption[]
    /** What is chosen, for a screen reader and the dialog's title: "Metric". */
    label: string
    /** The words over the list: "Choose what each cohort is measured by." */
    description?: string
    placeholder?: string
  }>(),
  { description: '', placeholder: 'Choose' }
)

const model = defineModel<string>()
const open = ref(false)
const search = ref<string | null>('')
const highlighted = ref(0)

const current = computed(() => props.options.find(option => option.value === model.value))

const needle = computed(() => (search.value ?? '').trim().toLowerCase())

/** A text cut where the search matches it, every time it does, so the matches can be marked. */
function partsOf(text: string): { text: string, match: boolean }[] {
  if (!needle.value) return [{ text, match: false }]
  const parts: { text: string, match: boolean }[] = []
  const lower = text.toLowerCase()
  let at = 0
  for (let found = lower.indexOf(needle.value); found !== -1; found = lower.indexOf(needle.value, at)) {
    if (found > at) parts.push({ text: text.slice(at, found), match: false })
    parts.push({ text: text.slice(found, found + needle.value.length), match: true })
    at = found + needle.value.length
  }
  if (at < text.length) parts.push({ text: text.slice(at), match: false })
  return parts
}

const matches = computed(() => {
  if (!needle.value) return props.options
  return props.options.filter(option => [option.label, option.note, option.group]
    .some(text => text?.toLowerCase().includes(needle.value)))
})

/** The matches in their groups, in the order they come; an option without one has its own. */
const groups = computed(() => {
  const out: { name: string, options: { option: GhostSelectOption, index: number }[] }[] = []
  matches.value.forEach((option, index) => {
    const name = option.group ?? ''
    const last = out[out.length - 1]
    if (last && last.name === name) last.options.push({ option, index })
    else out.push({ name, options: [{ option, index }] })
  })
  return out
})

watch(search, () => {
  highlighted.value = 0
})

watch(open, (value) => {
  if (!value) return
  search.value = ''
  highlighted.value = Math.max(0, props.options.findIndex(option => option.value === model.value))
})

const listId = `${useId()}-options`
const optionId = (index: number) => `${listId}-${index}`

function choose(option: GhostSelectOption | undefined) {
  if (!option) return
  model.value = option.value
  open.value = false
}

function onKey(event: KeyboardEvent) {
  const count = matches.value.length
  if (!count) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowRight') highlighted.value = (highlighted.value + 1) % count
  else if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') highlighted.value = (highlighted.value - 1 + count) % count
  else if (event.key === 'Enter') choose(matches.value[highlighted.value])
  else return
  event.preventDefault()
  void nextTick(() => document.getElementById(optionId(highlighted.value))?.scrollIntoView({ block: 'nearest' }))
}

const field = useTemplateRef<{ focus: () => void }>('field')

function entered() {
  field.value?.focus()
  document.getElementById(optionId(highlighted.value))?.scrollIntoView({ block: 'nearest' })
}
</script>

<template>
  <button
    :aria-label="`${label}: ${current?.label ?? placeholder}`"
    aria-haspopup="dialog"
    class="group -ml-2 inline-flex h-(--ps-toolbar-height) max-w-[calc(100%+0.5rem)] items-center gap-1 rounded-(--ps-radius) px-2 text-sm font-medium text-ink transition-colors duration-150 hover:bg-ink/5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
    :class="open ? 'bg-ink/5' : ''"
    type="button"
    @click="open = true"
  >
    <span class="truncate">{{ current?.label ?? placeholder }}</span>
    <Icon
      aria-hidden="true"
      class="flex-none text-base text-ink/50 transition-colors duration-150 group-hover:text-ink/80"
      name="material-symbols:unfold-more"
    />
  </button>

  <v-dialog
    v-model="open"
    max-width="720"
    scrollable
    @after-enter="entered"
  >
    <v-card border flat>
      <header class="flex flex-col gap-3 border-b border-line p-5 pb-4">
        <div class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ label }}
          </h2>
          <p v-if="description" class="m-0 text-xs text-ink/60">
            {{ description }}
          </p>
        </div>
        <v-text-field
          ref="field"
          v-model="search"
          :aria-activedescendant="matches.length ? optionId(highlighted) : undefined"
          :aria-controls="listId"
          :aria-label="`Find a ${label.toLowerCase()}`"
          :placeholder="`Find a ${label.toLowerCase()}`"
          autocomplete="off"
          clearable
          density="comfortable"
          hide-details
          prepend-inner-icon="material-symbols:search"
          role="combobox"
          variant="filled"
          @keydown="onKey"
        />
      </header>

      <div :id="listId" class="max-h-[60vh] overflow-y-auto p-5 pt-3" role="listbox">
        <section v-for="group in groups" :key="group.name" class="pt-2">
          <h3 v-if="group.name" class="m-0 mb-1.5 text-[11px] font-medium tracking-wide text-ink/45 uppercase">
            {{ group.name }}
          </h3>
          <div class="grid gap-1 sm:grid-cols-2">
            <button
              v-for="{ option, index } in group.options"
              :id="optionId(index)"
              :key="option.value"
              :aria-selected="option.value === model"
              :class="[
                index === highlighted ? 'bg-ink/5' : '',
                option.value === model ? 'ring-1 ring-primary/40' : ''
              ]"
              class="flex items-start gap-2 rounded-(--ps-radius) px-3 py-2 text-left transition-colors duration-100"
              role="option"
              tabindex="-1"
              type="button"
              @click="choose(option)"
              @pointermove="highlighted = index"
            >
              <span class="flex min-w-0 flex-1 flex-col gap-0.5">
                <span class="text-[13px] text-ink">
                  <template v-for="(part, at) in partsOf(option.label)" :key="at">
                    <mark v-if="part.match" class="ps-ghost-picker__match">{{ part.text }}</mark>
                    <template v-else>{{ part.text }}</template>
                  </template>
                </span>
                <span v-if="option.note" class="text-[11px] text-ink/55">
                  <template v-for="(part, at) in partsOf(option.note)" :key="at">
                    <mark v-if="part.match" class="ps-ghost-picker__match">{{ part.text }}</mark>
                    <template v-else>{{ part.text }}</template>
                  </template>
                </span>
              </span>
              <Icon
                v-if="option.value === model"
                aria-hidden="true"
                class="mt-0.5 flex-none text-base text-primary"
                name="material-symbols:check"
              />
            </button>
          </div>
        </section>

        <p v-if="!matches.length" class="m-0 py-8 text-center text-xs text-ink/55">
          No {{ label.toLowerCase() }} matches “{{ search }}”.
        </p>
      </div>

      <footer class="flex items-center justify-between border-t border-line px-5 py-2.5 text-[11px] text-ink/45">
        <span><span class="font-mono">↑↓</span> to move · <span class="font-mono">Enter</span> to choose · <span class="font-mono">Esc</span> to close</span>
        <span class="font-mono">{{ matches.length }} of {{ options.length }}</span>
      </footer>
    </v-card>
  </v-dialog>
</template>

<style scoped>
/* The accent, faint, behind the letters that matched; the ink stays the text's own. */
.ps-ghost-picker__match {
  background: rgba(var(--v-theme-primary), 0.18);
  border-radius: 2px;
  color: inherit;
  padding: 0 1px;
}
</style>
