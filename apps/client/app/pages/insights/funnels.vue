<script setup lang="ts">
// How far players get through a sequence of steps: a saved funnel, or one being built, as
// the players who did each step in order within the funnel's window of their first step
// (project.md §31). This page is which funnel is open and how it is defined — the steps
// are edited in place, and saving keeps the edit; what the funnel comes to is
// FunnelReport's, which follows the draft as it changes.
//
// Funnels are saved in this browser until the server keeps them.
const { project } = useWorkspace()

const RANGES: SegmentedOption[] = [
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' }
]

const range = useQueryChoice<FunnelRange>('range', ['7d', '30d', '90d'], '30d')

// --- Which funnel -------------------------------------------------------------------------

const { funnels, save, create, remove } = useSavedFunnels()

/** A funnel not yet saved, of either kind, opens under one of these. */
const BLANKS: Record<string, () => FunnelDefinition> = {
  'new': () => ({
    id: 'new',
    kind: 'steps',
    name: 'Untitled funnel',
    window: '1d',
    steps: [{ event: 'session_start', where: [] }, { event: 'level_complete', where: [] }]
  }),
  'new-progression': () => ({
    id: 'new-progression',
    kind: 'progression',
    name: 'Untitled progression',
    window: '30d',
    event: 'level_complete',
    property: 'level',
    from: 1,
    to: 30,
    where: []
  })
}

/** The open funnel, in the URL: a saved one, or a new one of either kind. */
const selected = useQueryChoice(
  'funnel',
  () => [...Object.keys(BLANKS), ...funnels.value.map(funnel => funnel.id)],
  () => funnels.value[0]!.id
)

const saved = computed(() => funnels.value.find(funnel => funnel.id === selected.value) ?? null)

const opened = () => copyFunnel(saved.value ?? (BLANKS[selected.value] ?? BLANKS.new!)())

/** The funnel on screen: the saved one, as edited so far. */
const draft = ref<FunnelDefinition>(opened())

watch(selected, () => {
  draft.value = opened()
})

/** Whether two funnels ask the same question: everything but their id and name. */
const same = (a: FunnelDefinition, b: FunnelDefinition) => {
  const { id: _a, name: _an, ...left } = a
  const { id: _b, name: _bn, ...right } = b
  return JSON.stringify(left) === JSON.stringify(right)
}

const dirty = computed(() => !saved.value || !same(draft.value, saved.value))

/** A funnel not yet saved has no name of its own, so saving it asks for one. */
const naming = ref(false)

function onSave() {
  if (saved.value) save(draft.value)
  else naming.value = true
}

function onSaveAs(name: string) {
  const id = create(draft.value, name)
  selected.value = id
}

function onRename(name: string) {
  if (!saved.value) return
  save({ ...saved.value, name })
  draft.value = { ...draft.value, name }
}

function onRemove() {
  if (!saved.value) return
  remove(saved.value.id)
  selected.value = funnels.value[0]!.id
}

function onDiscard() {
  draft.value = opened()
}

const WINDOWS: SegmentedOption[] = [
  { value: 'session', label: 'Session', title: 'Within the session of the first step' },
  { value: '1h', label: '1h', title: 'Within 1 hour of the first step' },
  { value: '1d', label: '1d', title: 'Within 1 day of the first step' },
  { value: '7d', label: '7d', title: 'Within 7 days of the first step' },
  { value: '30d', label: '30d', title: 'Within 30 days of the first step' }
]

const conversionWindow = computed<string>({
  get: () => draft.value.window,
  set: value => void (draft.value = { ...draft.value, window: value as FunnelWindow })
})

const steps = computed<FunnelStep[]>({
  get: () => (draft.value.kind === 'steps' ? draft.value.steps : []),
  set: (value) => {
    if (draft.value.kind === 'steps') draft.value = { ...draft.value, steps: value }
  }
})

const progression = computed<ProgressionFunnel | null>({
  get: () => (draft.value.kind === 'progression' ? draft.value : null),
  set: (value) => {
    if (value) draft.value = value
  }
})

const windowWords = computed(() => windowPhrase(draft.value.window))
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Funnels">
      <template #description>
        How far <span class="text-ink">{{ project.name }}</span>'s players get through a
        sequence of steps or a run of levels, and where they stop.
      </template>
      <template #actions>
        <SegmentedControl v-model="range" :options="RANGES" label="Range" />
      </template>
    </PageHeader>

    <SectionCard
      v-if="draft.kind === 'steps'"
      :description="`Players who did each step in order, other events in between allowed, ${windowWords} of step 1.`"
      :padded="false"
      :title="draft.name"
    >
      <template #title>
        <FunnelPicker
          v-model="selected"
          :dirty="dirty"
          :funnels="funnels"
          :name="draft.name"
          :unsaved="!saved"
          @remove="onRemove"
          @rename="onRename"
          @save-as="onSaveAs"
        />
      </template>
      <template #actions>
        <span class="text-xs text-ink/55">Converted within</span>
        <SegmentedControl v-model="conversionWindow" :options="WINDOWS" label="Conversion window" />
      </template>
      <FunnelSteps v-model="steps" :catalogue="EVENT_CATALOGUE" />
    </SectionCard>

    <SectionCard
      v-if="progression"
      :description="`Players who did ${progression.event} at each ${progression.property.replaceAll('_', ' ')} in order, ${windowWords} of the first.`"
      :padded="false"
      :title="draft.name"
    >
      <template #title>
        <FunnelPicker
          v-model="selected"
          :dirty="dirty"
          :funnels="funnels"
          :name="draft.name"
          :unsaved="!saved"
          @remove="onRemove"
          @rename="onRename"
          @save-as="onSaveAs"
        />
      </template>
      <template #actions>
        <span class="text-xs text-ink/55">Reached within</span>
        <SegmentedControl v-model="conversionWindow" :options="WINDOWS" label="Window" />
      </template>
      <ProgressionEditor v-model="progression" :catalogue="EVENT_CATALOGUE" />
    </SectionCard>

    <FunnelReport v-if="draft.kind === 'steps'" :definition="draft" :range="range" />
    <ProgressionReport v-if="progression" :definition="progression" :range="range" />

    <ActionBar :open="dirty" label="Unsaved funnel">
      <template #status>
        {{ saved ? `Unsaved changes to ${draft.name}` : `${draft.name} is not saved yet` }}
      </template>
      <v-btn
        class="text-xs font-medium"
        color="neutral"
        size="small"
        variant="text"
        @click="onDiscard"
      >
        {{ saved ? 'Discard' : 'Start over' }}
      </v-btn>
      <v-btn
        class="text-xs font-medium"
        color="primary"
        size="small"
        variant="flat"
        @click="onSave"
      >
        Save
      </v-btn>
    </ActionBar>

    <NameDialog
      v-model="naming"
      description="Saved in this browser, with its steps and window."
      title="Save funnel"
      @confirm="onSaveAs"
    />
  </div>
</template>
