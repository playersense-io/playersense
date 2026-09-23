<script setup lang="ts">
// The tags on one thing, edited in place: each tag a chip that removes itself, a field that adds
// one on Enter or a comma, and the tags already used elsewhere in the project one press away
// under it — so "economy" is picked rather than typed a second time as "Economy " and becomes
// a second tag. Tags are lower-case words; whatever is typed is folded into one.
const props = withDefaults(defineProps<{
  /** Names the field for a screen reader: "Tags". */
  label: string
  /** Tags used elsewhere, offered under the field. */
  suggestions?: readonly string[]
  placeholder?: string
}>(), {
  suggestions: () => [],
  placeholder: 'Add a tag'
})

const tags = defineModel<string[]>({ required: true })

const text = ref('')

const fold = (value: string) => value.trim().toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9_.-]/g, '').slice(0, 32)

function add(value: string) {
  const tag = fold(value)
  if (tag && !tags.value.includes(tag)) tags.value = [...tags.value, tag]
  text.value = ''
}

function remove(tag: string) {
  tags.value = tags.value.filter(entry => entry !== tag)
}

function onKey(event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ',') {
    event.preventDefault()
    add(text.value)
  } else if (event.key === 'Backspace' && !text.value && tags.value.length) {
    remove(tags.value[tags.value.length - 1]!)
  }
}

const offered = computed(() => {
  const needle = fold(text.value)
  return props.suggestions.filter(tag => !tags.value.includes(tag) && (!needle || tag.includes(needle))).slice(0, 12)
})

const id = useId()
</script>

<template>
  <div class="flex flex-col gap-2.5">
    <div class="flex flex-wrap items-center gap-1.5">
      <span
        v-for="tag in tags"
        :key="tag"
        class="inline-flex h-7 items-center gap-1 rounded-full bg-ink/6 pr-1 pl-3 text-xs text-ink"
      >
        {{ tag }}
        <button
          :aria-label="`Remove ${tag}`"
          class="flex size-5 items-center justify-center rounded-full text-ink/50 transition-colors hover:bg-ink/10 hover:text-ink"
          type="button"
          @click="remove(tag)"
        >
          <Icon aria-hidden="true" class="text-sm" name="material-symbols:close" />
        </button>
      </span>
      <span v-if="!tags.length" class="text-xs text-ink/45">No tags yet.</span>
    </div>

    <v-text-field
      :id="id"
      v-model="text"
      :aria-label="label"
      :placeholder="placeholder"
      autocomplete="off"
      hide-details
      prepend-inner-icon="material-symbols:sell-outline"
      variant="outlined"
      @blur="text.trim() && add(text)"
      @keydown="onKey"
    />

    <div v-if="offered.length" class="flex flex-wrap items-center gap-1.5">
      <span class="text-[11px] text-ink/45">Used on other flags:</span>
      <button
        v-for="tag in offered"
        :key="tag"
        class="inline-flex h-6 items-center gap-1 rounded-full border border-dashed border-ink/20 px-2.5 text-[11px] text-ink/65 transition-colors hover:border-primary/50 hover:text-primary"
        type="button"
        @click="add(tag)"
      >
        <Icon aria-hidden="true" class="text-xs" name="material-symbols:add" />
        {{ tag }}
      </button>
    </div>
  </div>
</template>
