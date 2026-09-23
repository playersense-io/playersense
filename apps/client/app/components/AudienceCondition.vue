<script setup lang="ts">
// One condition: a key, how it is compared, and what with. The operators offered are the ones
// that mean something for the key's type, and the value is typed in as that type — the server
// does not coerce, so a level compared with "10" matches nobody, and the only way to stop that
// is to never write it. Money keys are typed in dollars and stored in micros.
import type { AudienceConditionNode } from '~/utils/audience'

defineProps<{
  /** "Condition 2", for the labels a screen reader reads. */
  label: string
}>()

const node = defineModel<AudienceConditionNode>({ required: true })

const emit = defineEmits<{ remove: [] }>()

const attribute = computed(() => {
  const known = ATTRIBUTES.find(entry => entry.key === node.value.key)
  if (known) return known
  return { ...attributeOf(node.value.key, node.value.values ?? node.value.value), type: node.value.valueType ?? 'string' } satisfies AttributeDef
})

const custom = computed(() => node.value.key !== '' && !ATTRIBUTES.some(entry => entry.key === node.value.key))

const operators = computed(() => OPERATORS_FOR[attribute.value.type].map(op => ({ value: op, title: OPERATORS[op].label })))
const arity = computed(() => OPERATORS[node.value.op].arity)

/* A value typed in as text, turned into the key's own type on the way out. */
function typed(text: string): Scalar | undefined {
  const type = attribute.value.type
  const trimmed = text.trim()
  if (trimmed === '') return undefined
  if (type === 'number') {
    const parsed = Number(trimmed.replace(',', '.'))
    if (!Number.isFinite(parsed)) return undefined
    return attribute.value.unit === 'micros' ? Math.round(parsed * 1_000_000) : parsed
  }
  if (type === 'bool') return trimmed === 'true'
  return trimmed
}

function shown(value: Scalar | undefined): string {
  if (value === undefined) return ''
  if (attribute.value.unit === 'micros' && typeof value === 'number') return String(value / 1_000_000)
  return String(value)
}

/* A new key keeps the operator when the type still has it, and drops a value of the wrong type. */
function setKey(key: string) {
  const known = ATTRIBUTES.find(entry => entry.key === key)
  const type = known?.type ?? node.value.valueType ?? 'string'
  const op = OPERATORS_FOR[type].includes(node.value.op) ? node.value.op : OPERATORS_FOR[type][0]!
  node.value = { id: node.value.id, type: 'condition', negate: node.value.negate, key, op, valueType: type }
}

function setType(type: AttributeType) {
  const op = OPERATORS_FOR[type].includes(node.value.op) ? node.value.op : OPERATORS_FOR[type][0]!
  node.value = { id: node.value.id, type: 'condition', negate: node.value.negate, key: node.value.key, op, valueType: type }
}

function setOp(op: TargetOp) {
  const { value, values, ...rest } = node.value
  const nextArity = OPERATORS[op].arity
  node.value = {
    ...rest,
    op,
    ...(nextArity === 'one' && (value !== undefined || values?.length) ? { value: value ?? values![0]! } : {}),
    ...(nextArity === 'many' ? { values: values ?? (value === undefined ? [] : [value]) } : {})
  }
}

/* The text in the box is kept apart from the value, and only rewritten when the value changes
   to something the text does not already say: "2." is on its way to "2.5", not a 2 to redraw. */
const singleText = ref(shown(node.value.value))
watch(() => node.value.value, (value) => {
  if (typed(singleText.value) !== value) singleText.value = shown(value)
})

const single = computed({
  get: () => singleText.value,
  set: (text: string | null) => {
    singleText.value = text ?? ''
    const { value: _value, ...rest } = node.value
    const next = typed(text ?? '')
    node.value = next === undefined ? rest : { ...rest, value: next }
  }
})

const many = computed({
  get: () => (node.value.values ?? []).map(shown),
  set: (texts: string[]) => {
    const values = texts.flatMap(text => String(text).split(/[,\s]+/)).map(typed).filter((value): value is Scalar => value !== undefined)
    node.value = { ...node.value, values: [...new Map(values.map(value => [`${typeof value}:${value}`, value])).values()] }
  }
})

const booleanValue = computed({
  get: () => (node.value.value === undefined ? '' : String(node.value.value)),
  set: (text: string) => {
    node.value = { ...node.value, value: text === 'true' }
  }
})

const BOOLEANS = [{ value: 'true', title: 'true' }, { value: 'false', title: 'false' }]

const TYPES: { value: AttributeType, title: string }[] = [
  { value: 'string', title: 'Text' },
  { value: 'number', title: 'Number' },
  { value: 'bool', title: 'True or false' },
  { value: 'version', title: 'Version' }
]

/* Countries are offered by name and stored by code. */
const suggestions = computed(() => (attribute.value.suggestions ?? []).map(value => ({
  value,
  title: attribute.value.key === 'geo.country' ? `${value} · ${countryName(value)}` : value
})))

const placeholder = computed(() => {
  if (attribute.value.type === 'version') return '2.5.0'
  if (attribute.value.unit === 'micros') return 'Dollars'
  if (attribute.value.unit) return attribute.value.unit
  return attribute.value.type === 'number' ? '0' : 'Value'
})
</script>

<template>
  <div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
    <button
      v-if="node.negate"
      class="rounded-(--ps-radius-x-small) bg-error/10 px-1.5 text-[10px] leading-5 font-medium tracking-wide text-error uppercase"
      title="Turn this condition round again"
      type="button"
      @click="node = { ...node, negate: false }"
    >
      Not
    </button>

    <AttributePicker :label="`${label} key`" :model-value="node.key" @update:model-value="setKey" />

    <v-select
      v-if="custom"
      :aria-label="`${label} value type`"
      :items="TYPES"
      v-bind="FIELD_MENU"
      :model-value="node.valueType ?? 'string'"
      class="w-40 flex-none"
      density="comfortable"
      hide-details
      variant="outlined"
      @update:model-value="setType"
    />

    <template v-if="node.key">
      <v-select
        :aria-label="`${label} comparison`"
        :items="operators"
        v-bind="FIELD_MENU"
        :model-value="node.op"
        class="w-48 flex-none"
        density="comfortable"
        hide-details
        variant="outlined"
        @update:model-value="setOp"
      />

      <template v-if="arity === 'one'">
        <v-select
          v-if="attribute.type === 'bool'"
          v-model="booleanValue"
          :aria-label="`${label} value`"
          :items="BOOLEANS"
          v-bind="FIELD_MENU"
          class="w-32 flex-none font-mono"
          density="comfortable"
          hide-details
          placeholder="Choose"
          variant="outlined"
        />
        <v-combobox
          v-else-if="suggestions.length"
          v-model="single"
          :aria-label="`${label} value`"
          :items="suggestions"
          v-bind="FIELD_MENU"
          :placeholder="placeholder"
          :return-object="false"
          class="max-w-72 min-w-44 flex-1"
          density="comfortable"
          hide-details
          variant="outlined"
        />
        <v-text-field
          v-else
          v-model="single"
          :aria-label="`${label} value`"
          :inputmode="attribute.type === 'number' ? 'decimal' : undefined"
          :placeholder="placeholder"
          :prefix="attribute.unit === 'micros' ? '$' : undefined"
          class="max-w-56 min-w-36 flex-1"
          density="comfortable"
          hide-details
          variant="outlined"
        />
      </template>

      <v-combobox
        v-else-if="arity === 'many'"
        v-model="many"
        :aria-label="`${label} values`"
        :items="suggestions"
        v-bind="FIELD_MENU"
        :placeholder="`${placeholder}, one or more`"
        :return-object="false"
        chips
        class="ps-audience-condition__many max-w-lg min-w-56 flex-1"
        closable-chips
        density="comfortable"
        hide-details
        multiple
        variant="outlined"
      />
    </template>

    <v-menu location="bottom end">
      <template #activator="{ props: activator }">
        <v-btn
          v-bind="activator"
          :aria-label="`${label} options`"
          color="neutral"
          icon="material-symbols:more-horiz"
          size="small"
          variant="text"
        />
      </template>
      <v-list min-width="200" nav>
        <v-list-item
          :title="node.negate ? 'Remove Not' : 'Turn round with Not'"
          prepend-icon="material-symbols:swap-horiz"
          @click="node = { ...node, negate: !node.negate }"
        />
        <v-list-item
          base-color="error"
          prepend-icon="material-symbols:delete-outline"
          title="Remove condition"
          @click="emit('remove')"
        />
      </v-list>
    </v-menu>
  </div>
</template>

<style scoped>
/* A list of values wraps inside the field rather than scrolling in it: ten countries make
   the field taller, not wider. The height is still the density's; only the control is
   allowed to grow past it. */
.ps-audience-condition__many :deep(.v-field__input) {
  flex-wrap: wrap;
  gap: 4px;
  padding-block: 4px;
}

.ps-audience-condition__many :deep(.v-input__control) {
  height: auto;
  min-height: var(--v-input-control-height);
}

.ps-audience-condition__many :deep(.v-chip) {
  margin: 0;
}
</style>
