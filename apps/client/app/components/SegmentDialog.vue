<script setup lang="ts">
// A segment, created or edited: an audience with a name, which flags refer to rather than copy.
// Editing one changes every rule that names it at the next publish, so the dialog lists those
// flags before the save rather than after. A segment is checked with itself in the set, so one
// that would refer back to itself through another is refused at the write that closes the loop.
const props = withDefaults(defineProps<{
  /** The segment to edit; absent, a new one. */
  segment?: SegmentDef | null
}>(), {
  segment: null
})

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{ saved: [key: string] }>()

const { segments, createSegment, saveSegment, usesOf } = useLiveConfig()

const key = ref('')
const keyEdited = ref(false)
const name = ref('')
const description = ref('')
const rule = ref<TargetRule>({})
const refusal = ref('')

watch(open, (value) => {
  if (!value) return
  key.value = props.segment?.key ?? ''
  keyEdited.value = Boolean(props.segment)
  name.value = props.segment?.name ?? ''
  description.value = props.segment?.description ?? ''
  rule.value = props.segment ? JSON.parse(JSON.stringify(props.segment.rule)) as TargetRule : {}
  refusal.value = ''
}, { immediate: true })

watch(name, (value) => {
  if (!keyEdited.value) key.value = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '').slice(0, 64)
})

const candidate = computed<SegmentDef>(() => ({
  key: key.value,
  name: name.value.trim(),
  description: description.value.trim(),
  rule: rule.value,
  archived: false,
  created_at: '',
  updated_at: '',
  updated_by: ''
}))

const problems = computed(() => {
  const found = segmentProblems(candidate.value, segments.value)
  if (!props.segment && segments.value.some(entry => entry.key === key.value)) found.unshift('A segment already has this key, archived ones included.')
  return found
})

const uses = computed(() => (props.segment ? usesOf(props.segment.key) : { flags: [], segments: [] }))
const { shareOf } = useSegmentReach()
/* Counted for the segment as saved: an edit is counted once it is saved. */
const reach = computed(() => (props.segment ? shareOf(props.segment.key) : null))

const ready = computed(() => name.value.trim() !== '' && key.value !== '' && !problems.value.length)

const saving = ref(false)

async function confirm() {
  if (!ready.value || saving.value) return
  saving.value = true
  const problem = props.segment ? await saveSegment(candidate.value) : await createSegment(candidate.value)
  saving.value = false
  if (problem) {
    refusal.value = problem
    return
  }
  emit('saved', candidate.value.key)
  open.value = false
}

const nameId = useId()
const keyId = useId()
const descriptionId = useId()
</script>

<template>
  <v-dialog v-model="open" max-width="860" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ segment ? `Edit ${segment.name}` : 'New segment' }}
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          An audience with a name. Flags refer to it, so changing it here changes every rule that names it at the next publish.
        </p>
      </header>

      <div class="flex flex-col gap-5 overflow-y-auto p-5">
        <div class="grid gap-4 sm:grid-cols-2">
          <div class="flex flex-col gap-1">
            <label :for="nameId" class="text-xs font-medium text-ink">Name</label>
            <v-text-field
              :id="nameId"
              v-model="name"
              autocomplete="off"
              hide-details
              maxlength="120"
              placeholder="Lapsed payers"
              variant="outlined"
            />
          </div>
          <div class="flex flex-col gap-1">
            <label :for="keyId" class="text-xs font-medium text-ink">Key <span class="font-normal text-ink/45">what rules refer to · fixed once created</span></label>
            <v-text-field
              :id="keyId"
              :disabled="Boolean(segment)"
              :model-value="key"
              autocomplete="off"
              class="ps-segment-dialog__key"
              hide-details
              maxlength="64"
              placeholder="lapsed_payers"
              spellcheck="false"
              variant="outlined"
              @update:model-value="key = $event; keyEdited = true"
            />
          </div>
        </div>

        <div class="flex flex-col gap-1">
          <label :for="descriptionId" class="text-xs font-medium text-ink">Description <span class="font-normal text-ink/45">optional</span></label>
          <v-text-field
            :id="descriptionId"
            v-model="description"
            autocomplete="off"
            hide-details
            maxlength="500"
            placeholder="Who they are, in a sentence"
            variant="outlined"
          />
        </div>

        <div class="flex flex-col gap-2">
          <div class="flex items-baseline justify-between gap-2">
            <span class="text-xs font-medium text-ink">Players who match</span>
            <span v-if="reach !== null" class="text-[11px] text-ink/50">as saved, about <span class="font-mono text-ink/70">{{ formatShare(reach) }}</span> of last week's players</span>
            <span v-else class="text-[11px] text-ink/50">Counted once it is saved</span>
          </div>
          <div class="rounded-(--ps-radius-large) border border-line p-4">
            <AudienceEditor v-model="rule" :exclude="segment?.key ?? key" :segments="segments" />
          </div>
        </div>

        <InfoNote v-if="uses.flags.length || uses.segments.length" icon="material-symbols:link">
          Used by
          <template v-for="(flag, index) in uses.flags" :key="flag.key">
            <NuxtLink :to="`/growth/flags/${encodeURIComponent(flag.key)}`" class="font-medium text-ink underline decoration-ink/30 underline-offset-2">{{ flag.name }}</NuxtLink>{{ index < uses.flags.length - 1 || uses.segments.length ? ', ' : '' }}
          </template>
          <template v-for="(entry, index) in uses.segments" :key="entry.key">
            segment {{ entry.name }}{{ index < uses.segments.length - 1 ? ', ' : '' }}
          </template>.
          A change here reaches all of them at the next publish.
        </InfoNote>

        <ul v-if="(problems.length && name.trim()) || refusal" class="m-0 flex list-none flex-col gap-1 p-0">
          <li v-for="problem in refusal ? [refusal] : problems" :key="problem" class="flex items-start gap-1.5 text-xs text-error">
            <Icon :aria-hidden="true" class="mt-0.5 flex-none text-sm" name="material-symbols:error-outline" />
            {{ problem }}
          </li>
        </ul>
      </div>

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
          :loading="saving"
          :disabled="!ready"
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="confirm"
        >
          {{ segment ? 'Save segment' : 'Create segment' }}
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>

<style scoped>
.ps-segment-dialog__key :deep(input) {
  font-family: var(--font-mono);
  font-size: 12px;
}
</style>
