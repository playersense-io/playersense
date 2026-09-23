<script setup lang="ts">
// A new flag: what it is called, the key the game reads, what kind of value it holds, and what
// it answers before any rule is written. The key and the kind are asked for with a warning,
// because they are the two things that cannot change afterwards — the installed build calls a
// typed reader on the key, and a kind that changed would be a document it cannot read.
//
// The key follows the name until somebody edits it, and says what it breaks on as it is typed.
// A copy of an existing flag starts from that flag's kind, fallback and rules.
const props = withDefaults(defineProps<{
  /** A flag to start from, for "Duplicate". */
  from?: FlagDef | null
}>(), {
  from: null
})

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{ created: [key: string] }>()

const { flags, segments, createFlag } = useLiveConfig()

const name = ref('')
const key = ref('')
const keyEdited = ref(false)
const kind = ref<FlagKind>('bool')
const purpose = ref<FlagPurpose>('release')
const fallback = shallowRef<Json>(false)
const fallbackInvalid = ref(false)
const description = ref('')
const refusal = ref('')

const slug = (text: string) => text.trim().toLowerCase().replace(/[^a-z0-9.]+/g, '_').replace(/^[_.]+|[_.]+$/g, '').slice(0, 64)

/* Immediate, because the dialog is mounted only while it is open. */
watch(open, (value) => {
  if (!value) return
  const source = props.from
  name.value = source ? `${source.name} (copy)` : ''
  key.value = source ? `${source.key}_copy`.slice(0, 64) : ''
  keyEdited.value = Boolean(source)
  kind.value = source?.kind ?? 'bool'
  purpose.value = source?.purpose ?? 'release'
  fallback.value = source ? JSON.parse(JSON.stringify(source.fallback)) as Json : false
  description.value = source?.description ?? ''
  refusal.value = ''
}, { immediate: true })

watch(name, (value) => {
  if (!keyEdited.value) key.value = slug(value)
})

/* A kind chosen after a fallback was typed starts that kind's own blank value. */
watch(kind, (value, previous) => {
  if (value !== previous && kindProblem(value, fallback.value)) fallback.value = value === 'bool' ? false : defaultValue(value)
})

const KINDS = computed<ChoiceOption[]>(() => (Object.keys(FLAG_KIND_LABELS) as FlagKind[]).map(value => ({
  value,
  label: FLAG_KIND_LABELS[value],
  note: FLAG_KIND_NOTES[value]
})))

const PURPOSE_ITEMS = (Object.keys(FLAG_PURPOSES) as FlagPurpose[]).map(value => ({ value, title: FLAG_PURPOSES[value].label, subtitle: FLAG_PURPOSES[value].note }))

const chosenKind = computed({
  get: () => kind.value as string | null,
  set: (value: string | null) => {
    if (value && !props.from) kind.value = value as FlagKind
  }
})

const draft = computed<FlagDef>(() => ({
  key: key.value,
  name: name.value.trim(),
  description: description.value.trim(),
  kind: kind.value,
  fallback: fallback.value,
  rules: props.from ? JSON.parse(JSON.stringify(props.from.rules)) as FlagVariant[] : [],
  rollout: props.from?.rollout ?? FULL_ROLLOUT,
  salt: '',
  archived: false,
  created_at: '',
  updated_at: '',
  updated_by: '',
  purpose: purpose.value,
  tags: props.from ? [...props.from.tags] : [],
  grantsValue: props.from?.grantsValue ?? false
}))

const keyProblem = computed(() => {
  if (!key.value) return ''
  if (flags.value.some(flag => flag.key === key.value)) return 'A flag already has this key, archived flags included. Keys are never reused.'
  return flagProblems(draft.value, segments.value).find(problem => problem.where === 'key')?.message ?? ''
})

const ready = computed(() => draft.value.name !== '' && key.value !== '' && !keyProblem.value && !fallbackInvalid.value && !kindProblem(kind.value, fallback.value))

function confirm() {
  if (!ready.value) return
  const problem = createFlag(draft.value)
  if (problem) {
    refusal.value = problem
    return
  }
  emit('created', draft.value.key)
  open.value = false
}

const nameId = useId()
const keyId = useId()
const descriptionId = useId()
</script>

<template>
  <v-dialog v-model="open" max-width="600" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ from ? `Duplicate ${from.name}` : 'New flag' }}
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          It goes into the draft. Players see nothing until the draft is published to an environment.
        </p>
      </header>

      <form class="flex flex-col gap-5 overflow-y-auto p-5" @submit.prevent="confirm">
        <div class="flex flex-col gap-5">
          <div class="flex flex-col gap-1">
            <label :for="nameId" class="text-xs font-medium text-ink">Name</label>
            <v-text-field
              :id="nameId"
              v-model="name"
              autocomplete="off"
              autofocus
              hide-details
              maxlength="120"
              placeholder="Daily reward"
              variant="outlined"
            />
          </div>
          <div class="flex flex-col gap-1">
            <label :for="keyId" class="text-xs font-medium text-ink">Key <span class="font-normal text-ink/45">what the game reads · fixed once created</span></label>
            <v-text-field
              :id="keyId"
              :error="keyProblem !== ''"
              :model-value="key"
              autocomplete="off"
              class="ps-new-flag__key"
              hide-details
              maxlength="64"
              placeholder="economy.daily_reward"
              spellcheck="false"
              variant="outlined"
              @update:model-value="key = $event; keyEdited = true"
            />
            <span v-if="keyProblem" class="text-[11px] text-error">{{ keyProblem }}</span>
          </div>
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Kind <span class="font-normal text-ink/45">fixed once created</span></span>
          <ChoiceList
            v-model="chosenKind"
            :columns="2"
            :disabled="Boolean(from)"
            :options="KINDS"
            label="Kind"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Fallback</span>
          <span class="text-[11px] leading-4 text-ink/55">What every player gets until a rule says otherwise, and what a rule's rollout leaves out.</span>
          <FlagValueEditor
            v-model="fallback"
            v-model:invalid="fallbackInvalid"
            :kind="kind"
            block
            label="Fallback"
          />
        </div>

        <div class="flex flex-col gap-5">
          <div class="flex flex-col gap-1">
            <span class="text-xs font-medium text-ink">Purpose</span>
            <v-select
              v-model="purpose"
              :items="PURPOSE_ITEMS"
              v-bind="FIELD_MENU"
              aria-label="Purpose"
              hide-details
              item-props
              variant="outlined"
            />
          </div>
          <div class="flex flex-col gap-1">
            <label :for="descriptionId" class="text-xs font-medium text-ink">Description <span class="font-normal text-ink/45">optional</span></label>
            <v-text-field
              :id="descriptionId"
              v-model="description"
              autocomplete="off"
              hide-details
              maxlength="500"
              placeholder="What it changes in the game"
              variant="outlined"
            />
          </div>
        </div>

        <p v-if="refusal" class="m-0 text-xs text-error">
          {{ refusal }}
        </p>
      </form>

      <footer class="flex items-center justify-end gap-2 border-t border-line px-5 py-3">
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
          :disabled="!ready"
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="confirm"
        >
          Create flag
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>

<style scoped>
.ps-new-flag__key :deep(input) {
  font-family: var(--font-mono);
  font-size: 12px;
}
</style>
