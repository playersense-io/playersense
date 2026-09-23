<script setup lang="ts">
// How far players get through a sequence of steps: a saved funnel, or one being built, as
// the players who did each step in order within the funnel's window of their first step
// (project.md §31). This page is which funnel is open and how it is defined — the steps
// are edited in place, and saving keeps the edit; what the funnel comes to is
// FunnelReport's, which follows the draft as it changes.
//
// Funnels are saved in this browser until the server keeps them.
const { project } = useWorkspace()

const route = useRoute()
const router = useRouter()

const RANGES: SegmentedOption[] = [
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' }
]

const range = computed<FunnelRange>({
  get: () => (['7d', '30d', '90d'].includes(route.query.range as string) ? route.query.range as FunnelRange : '30d'),
  set: value => void router.replace({ query: { ...route.query, range: value } })
})

// --- Which funnel -------------------------------------------------------------------------

const { funnels, save, create, remove } = useSavedFunnels()

const NEW = 'new'

const selected = computed<string>({
  get: () => {
    const id = route.query.funnel as string | undefined
    return id === NEW || funnels.value.some(funnel => funnel.id === id) ? id! : funnels.value[0]!.id
  },
  set: id => void router.replace({ query: { ...route.query, funnel: id } })
})

const saved = computed(() => funnels.value.find(funnel => funnel.id === selected.value) ?? null)

const blank = (): FunnelDefinition => ({
  id: NEW,
  name: 'Untitled funnel',
  window: '1d',
  steps: [{ event: 'session_start', where: [] }, { event: 'level_complete', where: [] }]
})

/** The funnel on screen: the saved one, as edited so far. */
const draft = ref<FunnelDefinition>(copyFunnel(saved.value ?? blank()))

watch(selected, () => {
  draft.value = copyFunnel(saved.value ?? blank())
})

const same = (a: FunnelDefinition, b: FunnelDefinition) => JSON.stringify([a.steps, a.window]) === JSON.stringify([b.steps, b.window])

const dirty = computed(() => !saved.value || !same(draft.value, saved.value))

function onSave() {
  save(draft.value)
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
  draft.value = copyFunnel(saved.value ?? blank())
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
  get: () => draft.value.steps,
  set: value => void (draft.value = { ...draft.value, steps: value })
})

const windowWords = computed(() => windowPhrase(draft.value.window))
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Funnels">
      <template #description>
        How far <span class="text-ink">{{ project.name }}</span>'s players get through a
        sequence of steps, and where they stop.
      </template>
      <template #actions>
        <FunnelPicker
          v-model="selected"
          :dirty="dirty"
          :funnels="funnels"
          :name="draft.name"
          :unsaved="!saved"
          @discard="onDiscard"
          @remove="onRemove"
          @rename="onRename"
          @save="onSave"
          @save-as="onSaveAs"
        />
        <SegmentedControl v-model="range" :options="RANGES" label="Range" />
      </template>
    </PageHeader>

    <SectionCard
      :description="`Players who did each step in order, other events in between allowed, ${windowWords} of step 1.`"
      :padded="false"
      title="Steps"
    >
      <template #actions>
        <span class="text-xs text-ink/55">Converted within</span>
        <SegmentedControl v-model="conversionWindow" :options="WINDOWS" label="Conversion window" />
      </template>
      <FunnelSteps v-model="steps" :catalogue="EVENT_CATALOGUE" />
    </SectionCard>

    <FunnelReport :definition="draft" :range="range" />
  </div>
</template>
