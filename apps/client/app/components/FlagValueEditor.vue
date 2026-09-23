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
}>(), {
  suggestions: () => [],
  error: false
})

const value = defineModel<Json>({ required: true })
const invalid = defineModel<boolean>('invalid', { default: false })

const text = ref('')
const problem = ref('')

function show(current: Json) {
  if (props.kind === 'json') text.value = JSON.stringify(current, null, 2)
  else if (props.kind === 'number') text.value = typeof current === 'number' ? String(current) : ''
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
  if (props.kind === 'number' && Number(text.value) === current && text.value.trim() !== '') return
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
  if (props.kind === 'number') {
    const parsed = Number(next.replace(',', '.'))
    if (next.trim() === '' || !Number.isFinite(parsed)) {
      problem.value = 'Not a number'
      invalid.value = true
      return
    }
    problem.value = ''
    invalid.value = false
    value.value = parsed
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

function format() {
  try {
    text.value = JSON.stringify(JSON.parse(text.value), null, 2)
  } catch {
    // Left as typed; the problem line already says why.
  }
}

const BOOLEANS: SegmentedOption[] = [
  { value: 'true', label: 'true' },
  { value: 'false', label: 'false' }
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
  <SegmentedControl
    v-if="kind === 'bool'"
    v-model="bool"
    :label="label"
    :options="BOOLEANS"
  />

  <v-combobox
    v-else-if="kind === 'string'"
    :aria-label="label"
    :error="error"
    :items="[...suggestions]"
    :model-value="text"
    class="ps-flag-value-editor font-mono"
    density="default"
    hide-details
    hide-no-data
    placeholder="Text"
    variant="outlined"
    @update:model-value="onText(String($event ?? ''))"
    @update:search="onText($event ?? '')"
  />

  <div v-else-if="kind === 'number'" class="flex flex-col gap-1">
    <v-text-field
      :aria-label="label"
      :error="error || problem !== ''"
      :model-value="text"
      class="ps-flag-value-editor w-48"
      density="default"
      hide-details
      inputmode="decimal"
      placeholder="0"
      variant="outlined"
      @update:model-value="onText($event)"
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
.ps-flag-value-editor :deep(input),
.ps-flag-value-editor--json :deep(textarea) {
  font-family: var(--font-mono);
  font-size: 12px;
}

.ps-flag-value-editor--json :deep(textarea) {
  line-height: 18px;
  padding-block: 10px;
}
</style>
