<script setup lang="ts">
/**
 * The search box in the row over a table.
 *
 * One component because there were sixteen, each a `v-text-field` with the same six
 * attributes and its own copy of the same three scoped rules — and copies drift. Seven of
 * them pinned `--v-input-control-height: 32px` on `.v-field`, which was right when the
 * toolbar was 32px tall and has been wrong since it went to 36: the variable has to reach
 * the input root to mean a height, so on `.v-field` it left the control box at the
 * density's 36px and shrank only the field inside it, and the value sat low against a
 * border it no longer filled. That is the bug this component exists to stop having again.
 *
 * The height is `comfortable` density and nothing else. theme.css maps that onto
 * `--ps-toolbar-height`, which is also what `.ps-toolbar-control` gives the Filter
 * and Columns buttons beside it, so the row lines up by construction rather than by three
 * files agreeing on a number.
 *
 * What is stated here is the toolbar's type and the small corner, which are the row's
 * rather than the ladder's: a field in a form takes the panel's 13px and
 * `--ps-radius`, and this one steps down to sit beside `size="small"` buttons.
 *
 * Width is the caller's. It is the one thing that genuinely differs — a name-or-email
 * search wants more room than a country filter — and `class` falls through to the field.
 */
const model = defineModel<string>({ required: true })

/*
 * The clear button hands back `null`, and a caller holding a needle it is about to
 * `.trim()` wants the empty string. Absorbed here rather than in each screen: two of them
 * carried a `search` computed whose whole body was this coercion, and the rest were one
 * press of the cross away from finding out they needed one.
 */
const text = computed<string | null>({
  get: () => model.value,
  set: (value) => {
    model.value = value ?? ''
  }
})

withDefaults(
  defineProps<{
    /** What is being searched, for the label a pointer never sees: "Search users". */
    label: string
    /** The prompt in the empty field, which says which columns it looks at. */
    placeholder?: string
  }>(),
  { placeholder: 'Search' }
)
</script>

<template>
  <v-text-field
    v-model="text"
    :aria-label="label"
    :placeholder="placeholder"
    class="search-field"
    clearable
    density="comfortable"
    hide-details
    prepend-inner-icon="material-symbols:search"
    variant="outlined"
  />
</template>

<style scoped>
/*
 * The toolbar's type and corner. No height: see the note above — it comes from the
 * density, and stating it here is what broke seven of these.
 */
.search-field :deep(.v-field) {
  border-radius: var(--ps-radius-small);
  font-size: 0.75rem;
}

.search-field :deep(.v-field__input),
.search-field :deep(.v-field__input::placeholder) {
  font-size: 0.75rem;
}

/* The affix glyphs, which Vuetify draws at its own 24px and which next to 12px type read
   as a pair of buttons rather than as the field's own marks. */
.search-field :deep(.v-field__prepend-inner > .v-icon),
.search-field :deep(.v-field__clearable > .v-icon) {
  font-size: 1rem;
}
</style>
