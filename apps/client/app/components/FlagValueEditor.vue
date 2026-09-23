<script setup lang="ts">
// The value a flag serves, typed in as its kind asks: true or false as a switch of two, a
// string or a number in a field, a JSON document in a mono box that says where it stops
// parsing. The model only ever holds a value of the right kind — text that is not yet a number
// or not yet JSON stays in the box, marked, and `invalid` tells the page there is something in
// it that will not save.
const props = withDefaults(defineProps<{
  kind: FlagKind
  /** Names the field for a screen reader: "Rule 2 value". */
  label: string
  /** Other values this flag serves, offered for a string. */
  suggestions?: readonly string[]
  error?: boolean
  /** The container's full width, for a form where every field is; otherwise sized to the value. */
  block?: boolean
}>(), {
  suggestions: () => [],
  error: false,
  block: false
})

const value = defineModel<Json>({ required: true })
const invalid = defineModel<boolean>('invalid', { default: false })

const text = ref('')
const problem = ref('')

function show(current: Json) {
  if (props.kind === 'json') text.value = JSON.stringify(current, null, 2)
  else if (props.kind === 'string') text.value = typeof current === 'string' ? current : ''
}

/* Only a value that differs from what the box already means is written back into it, so the
   caret does not jump while a JSON document is being typed. */
watch(value, (current) => {
  if (props.kind === 'json') {
    try {
      if (sameJson(JSON.parse(text.value), current)) return
    } catch {
      // The box holds something unparsed: the model changed from outside, so show the model.
    }
  }
  show(current)
  problem.value = ''
  invalid.value = false
}, { immediate: true })

function onText(next: string) {
  text.value = next
  if (props.kind === 'string') {
    value.value = next
    return
  }
  try {
    const parsed = JSON.parse(next) as Json
    if (parsed === null) throw new Error('null is not a value: a flag has to answer')
    problem.value = ''
    invalid.value = false
    value.value = parsed
  } catch (error) {
    problem.value = error instanceof Error ? error.message : 'Not JSON'
    invalid.value = true
  }
}

function onNumber(next: number | null) {
  if (next === null || !Number.isFinite(next)) {
    problem.value = 'A number flag needs a number'
    invalid.value = true
    return
  }
  problem.value = ''
  invalid.value = false
  value.value = next
}

function format() {
  try {
    text.value = JSON.stringify(JSON.parse(text.value), null, 2)
  } catch {
    // Left as typed; the problem line already says why.
  }
}

const BOOLEANS = [
  { value: 'true', title: 'true' },
  { value: 'false', title: 'false' }
]

const bool = computed({
  get: () => String(value.value === true),
  set: (next: string) => {
    value.value = next === 'true'
  }
})

const lines = computed(() => Math.min(14, Math.max(4, text.value.split('\n').length)))
</script>

<template>
  <v-select
    v-if="kind === 'bool'"
    v-model="bool"
    :aria-label="label"
    :error="error"
    :items="BOOLEANS"
    v-bind="FIELD_MENU"
    :class="block ? 'w-full' : 'w-40'"
    class="ps-flag-value-editor"
    hide-details
    variant="outlined"
  />

  <v-combobox
    v-else-if="kind === 'string'"
    :aria-label="label"
    :error="error"
    :items="[...suggestions]"
    v-bind="FIELD_MENU"
    :model-value="text"
    class="ps-flag-value-editor"
    hide-details
    hide-no-data
    placeholder="Text"
    variant="outlined"
    @update:model-value="onText(String($event ?? ''))"
    @update:search="onText($event ?? '')"
  />

  <!-- A number, with steppers; a whole number steps by one and a fraction by a tenth.
       Emptied, it holds no value, which the page refuses to save. -->
  <div v-else-if="kind === 'number'" :class="block ? 'w-full' : ''" class="flex flex-col gap-1">
    <v-number-input
      :aria-label="label"
      :error="error || problem !== ''"
      :model-value="typeof value === 'number' ? value : null"
      :precision="null"
      :step="typeof value === 'number' && !Number.isInteger(value) ? 0.1 : 1"
      :class="block ? 'w-full' : 'w-56'"
      class="ps-flag-value-editor"
      control-variant="split"
      hide-details
      inset
      variant="outlined"
      @update:model-value="onNumber"
    />
    <span v-if="problem" class="text-[11px] text-error">{{ problem }}</span>
  </div>

  <div v-else class="flex flex-col gap-1">
    <v-textarea
      :aria-label="label"
      :error="error || problem !== ''"
      :model-value="text"
      :rows="lines"
      class="ps-flag-value-editor ps-flag-value-editor--json"
      hide-details
      no-resize
      spellcheck="false"
      variant="outlined"
      @blur="format"
      @update:model-value="onText($event)"
    />
    <span class="flex items-center gap-1.5 text-[11px]" :class="problem ? 'text-error' : 'text-ink/50'">
      <Icon :name="problem ? 'material-symbols:error-outline' : 'material-symbols:check-circle-outline'" aria-hidden="true" class="text-sm" />
      {{ problem || 'Valid JSON. PlayerSense passes it through untouched; the game parses it.' }}
    </span>
  </div>
</template>

<style scoped>
/* A value is read character by character, so it is set in the mono face at the field's own
   size; the size is theme.css's, not restated here. */
.ps-flag-value-editor :deep(input),
.ps-flag-value-editor :deep(.v-select__selection-text),
.ps-flag-value-editor--json :deep(textarea) {
  font-family: var(--font-mono);
}

.ps-flag-value-editor--json :deep(textarea) {
  line-height: 18px;
}
</style>
