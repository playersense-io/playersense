<script setup lang="ts">
// How far players get through a sequence of steps: a saved funnel, or one being built, as
// the players who did each step in order within the funnel's window of their first step
// (project.md §31). This page is which funnel is open and how it is defined: the steps are
// edited in place, and saving keeps the edit for everyone in the project (…/funnels); what the
// funnel comes to is FunnelReport's, which asks the API again as the draft changes.
const workspace = useWorkspaceStore()

const RANGES: SegmentedOption[] = [
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' }
]

const range = useQueryChoice<FunnelRange>('range', ['7d', '30d', '90d'], '30d')

// --- The events, for the pickers ----------------------------------------------------------

const { catalogue, ensure } = useEventCatalogue()

// --- Which funnel -------------------------------------------------------------------------

const { funnels, loading, error, reload, save, create, remove } = useSavedFunnels()

/** The first two events a new funnel starts from: the busiest the project sends. */
const firstEvents = computed(() => [catalogue.value[0]?.name ?? 'session_start', catalogue.value[1]?.name ?? catalogue.value[0]?.name ?? 'session_start'])

/** A funnel not yet saved, of either kind, opens under one of these. */
const BLANKS: Record<string, () => FunnelDefinition> = {
  'new': () => ({
    id: 'new',
    kind: 'steps',
    name: 'Untitled funnel',
    window: '1d',
    steps: [{ event: firstEvents.value[0]!, where: [] }, { event: firstEvents.value[1]!, where: [] }]
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
  () => funnels.value[0]?.id ?? 'new'
)

const saved = computed(() => funnels.value.find(funnel => funnel.id === selected.value) ?? null)

const opened = () => copyFunnel(saved.value ?? (BLANKS[selected.value] ?? BLANKS.new!)())

/** The funnel on screen: the saved one, as edited so far. */
const draft = ref<FunnelDefinition>(opened())

/* A new funnel or another saved one opens afresh, and so does the list arriving. */
watch([selected, () => saved.value?.id], () => {
  draft.value = opened()
})

/* The events in use offer their properties to the condition menus. */
watch(draft, (funnel) => {
  for (const name of funnel.kind === 'steps' ? funnel.steps.map(step => step.event) : [funnel.event]) void ensure(name)
}, { immediate: true, deep: true })

/** Whether two funnels ask the same question: everything but their id and name. */
const same = (a: FunnelDefinition, b: FunnelDefinition) => {
  const { id: _a, name: _an, ...left } = a
  const { id: _b, name: _bn, ...right } = b
  return JSON.stringify(left) === JSON.stringify(right)
}

const dirty = computed(() => !saved.value || !same(draft.value, saved.value))

/** A funnel not yet saved has no name of its own, so saving it asks for one. */
const naming = ref(false)
const saving = ref(false)
const problem = ref('')

async function attempt(change: () => Promise<unknown>) {
  problem.value = ''
  saving.value = true
  try {
    await change()
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

function onSave() {
  if (saved.value) void attempt(() => save(draft.value))
  else naming.value = true
}

function onSaveAs(name: string) {
  void attempt(async () => {
    selected.value = await create(draft.value, name)
  })
}

function onRename(name: string) {
  const current = saved.value
  if (!current) return
  void attempt(async () => {
    await save({ ...current, name })
    draft.value = { ...draft.value, name }
  })
}

function onRemove() {
  const current = saved.value
  if (!current) return
  void attempt(async () => {
    await remove(current.id)
    selected.value = funnels.value[0]?.id ?? 'new'
  })
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
        How far <span class="text-ink">{{ workspace.project?.name }}</span>'s players get through a
        sequence of steps or a run of levels, and where they stop.
      </template>
      <template #actions>
        <SegmentedControl v-model="range" :options="RANGES" label="Range" />
      </template>
    </PageHeader>

    <p v-if="problem" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ problem }}
    </p>

    <LoadState
      :error="error"
      :loading="loading"
      what="the funnels"
      @retry="reload"
    >
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
        <FunnelSteps v-model="steps" :catalogue="catalogue" />
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
        <ProgressionEditor v-model="progression" :catalogue="catalogue" />
      </SectionCard>

      <FunnelReport v-if="draft.kind === 'steps'" :definition="draft" :range="range" />
      <ProgressionReport v-if="progression" :definition="progression" :range="range" />
    </LoadState>

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
        :loading="saving"
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
      description="Saved for everyone in the project, with its steps and window."
      title="Save funnel"
      @confirm="onSaveAs"
    />
  </div>
</template>
