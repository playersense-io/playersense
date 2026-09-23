<script setup lang="ts">
// Ending a test, and saying what it decided. Finishing asks which variant won, or that none
// did, with the one the results point to already chosen; stopping early asks why, because a
// test stopped halfway is a question somebody will ask about in three months. Either way the
// note goes on the test's history, next to the date.
const props = defineProps<{
  experiment: ExperimentDef
  mode: 'finish' | 'stop'
  /** The variant the results point to, chosen to start with. */
  suggested: string | null
}>()

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{ confirm: [decision: { winner: string | null, note: string }] }>()

const NONE = 'none'

const choice = ref<string | null>(props.suggested ?? NONE)
const note = ref('')

const options = computed<ChoiceOption[]>(() => [
  ...props.experiment.variants.map(entry => ({
    value: entry.id,
    label: entry.name,
    note: entry.control ? 'The control: keep what the game does today.' : `Serve ${writeValue(entry.value)}.`
  })),
  { value: NONE, label: 'No clear winner', note: 'Nothing moved enough to change. Keep the control.' }
])

function confirm() {
  emit('confirm', { winner: choice.value === NONE ? null : choice.value, note: note.value.trim() })
  open.value = false
}

const noteId = useId()
</script>

<template>
  <v-dialog v-model="open" max-width="520" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ mode === 'finish' ? 'Finish this experiment' : 'Stop this experiment early' }}
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          {{ mode === 'finish'
            ? 'Players keep getting their variant until the flag is changed. Shipping the winner is the next step.'
            : 'Stopped before its planned end, its result is not a verdict. Players keep their variant until the flag is changed.' }}
        </p>
      </header>

      <div class="flex flex-col gap-5 overflow-y-auto p-5">
        <div v-if="mode === 'finish'" class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Which variant won?</span>
          <ChoiceList v-model="choice" :options="options" label="Winner" />
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="noteId" class="text-xs font-medium text-ink">
            {{ mode === 'finish' ? 'What was decided' : 'Why it was stopped' }}
            <span class="font-normal text-ink/45">{{ mode === 'finish' ? 'optional' : 'recommended' }}</span>
          </label>
          <v-textarea
            :id="noteId"
            v-model="note"
            auto-grow
            hide-details
            maxlength="500"
            :placeholder="mode === 'finish' ? 'In a sentence, for whoever reads this later' : 'Revenue per player fell by a fifth in the first week'"
            rows="3"
            variant="outlined"
          />
        </div>
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
          :color="mode === 'finish' ? 'primary' : 'error'"
          class="text-xs font-medium"
          size="small"
          variant="flat"
          @click="confirm"
        >
          {{ mode === 'finish' ? 'Finish experiment' : 'Stop experiment' }}
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>
