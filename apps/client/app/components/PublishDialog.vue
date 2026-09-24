<script setup lang="ts">
// Publishing the draft to one environment: which one, everything that will change there, and a
// note saying why. The whole draft goes, never one flag of it — the environment serves a single
// document — so the list is every change waiting, with the flag this was opened from marked,
// and a flag somebody else is halfway through is visible before it ships.
//
// A draft that would not compile cannot be published, and the dialog says which flag is in the
// way. Production asks for a note, because the history is read at the moment something broke.
const props = withDefaults(defineProps<{
  environment?: EnvironmentKind
  /** The flag the dialog was opened from, marked in the list. */
  focus?: string
}>(), {
  environment: 'development',
  focus: ''
})

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{ published: [environment: EnvironmentKind, version: number] }>()

const { flags, segments, latest, draftCheck, publish } = useLiveConfig()

const target = ref<EnvironmentKind>(props.environment)
const note = ref('')
const refusal = ref('')

watch(open, (value) => {
  if (!value) return
  target.value = props.environment
  note.value = ''
  refusal.value = ''
}, { immediate: true })

const check = computed(() => draftCheck())

const changes = computed(() => draftChanges(latest(target.value), flags.value, segments.value))

const options = computed<ChoiceOption[]>(() => ENVIRONMENTS.map((environment) => {
  const version = latest(environment)
  const count = draftChanges(version, flags.value, segments.value).length
  return {
    value: environment,
    label: ENVIRONMENT_LABELS[environment],
    note: `${ENVIRONMENT_WHO[environment]} · ${!version ? 'never published' : count ? `${count} ${count === 1 ? 'change' : 'changes'} behind` : 'up to date'}`
  }
}))

const chosen = computed({
  get: () => target.value as string | null,
  set: (value: string | null) => {
    if (value) target.value = value as EnvironmentKind
  }
})

const nextVersion = computed(() => (latest(target.value)?.version ?? 0) + 1)
const needsNote = computed(() => target.value === 'production' && !note.value.trim())
const ready = computed(() => check.value.compiles && changes.value.length > 0 && !needsNote.value)

const CHANGE_ICONS: Record<DraftChange['change'], string> = {
  added: 'material-symbols:add-circle-outline',
  changed: 'material-symbols:edit-outline',
  removed: 'material-symbols:remove-circle-outline'
}

function confirm() {
  if (!ready.value) return
  const problem = publish(target.value, note.value)
  if (problem) {
    refusal.value = problem
    return
  }
  emit('published', target.value, nextVersion.value)
  open.value = false
}

const noteId = useId()
</script>

<template>
  <v-dialog v-model="open" max-width="640" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          Publish your changes
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          Everything in your draft goes to the environment you choose. Its devices get it the next
          time the game opens or comes back to the foreground, and within 15 minutes otherwise.
        </p>
      </header>

      <div class="flex flex-col gap-5 overflow-y-auto p-5">
        <ChoiceList
          v-model="chosen"
          :columns="1"
          :options="options"
          label="Environment"
        />

        <InfoNote v-if="!check.compiles" icon="material-symbols:error-outline" tone="warning">
          The draft does not compile, so nothing can be published until it does: {{ check.problem }}
        </InfoNote>

        <section class="flex flex-col gap-2">
          <h3 class="m-0 flex items-center justify-between text-xs font-medium text-ink">
            What {{ ENVIRONMENT_WHO[target].toLowerCase() }} will get
            <span class="font-mono text-[11px] font-normal text-ink/45">v{{ nextVersion - 1 }} → v{{ nextVersion }}</span>
          </h3>
          <p v-if="!changes.length" class="m-0 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs text-ink/60">
            {{ ENVIRONMENT_LABELS[target] }} already serves the draft. There is nothing to publish.
          </p>
          <ul v-else class="m-0 flex max-h-64 list-none flex-col overflow-y-auto rounded-(--ps-radius) border border-line p-0">
            <li
              v-for="change in changes"
              :key="`${change.kind}:${change.key}`"
              :class="change.key === focus ? 'bg-primary/5' : ''"
              class="flex gap-2.5 border-b border-line px-3 py-2.5 last:border-b-0"
            >
              <Icon
                :class="change.change === 'removed' ? 'text-error' : change.change === 'added' ? 'text-success' : 'text-warning'"
                :name="CHANGE_ICONS[change.change]"
                aria-hidden="true"
                class="mt-0.5 flex-none text-sm"
              />
              <div class="flex min-w-0 flex-col gap-0.5">
                <span class="flex items-center gap-2 text-xs text-ink">
                  <span class="font-mono">{{ change.key }}</span>
                  <span class="text-[11px] text-ink/45">{{ change.kind }}</span>
                  <span v-if="change.key === focus" class="text-[11px] text-primary">this flag</span>
                </span>
                <span v-for="line in change.lines" :key="line" class="text-[11px] leading-4 text-ink/60">{{ line }}</span>
              </div>
            </li>
          </ul>
        </section>

        <div class="flex flex-col gap-1">
          <label :for="noteId" class="text-xs font-medium text-ink">
            Note <span class="font-normal text-ink/45">{{ target === 'production' ? 'required for production' : 'optional' }}</span>
          </label>
          <v-text-field
            :id="noteId"
            v-model="note"
            autocomplete="off"
            hide-details
            maxlength="500"
            placeholder="What this version is for: “Store carousel to 25% of phones”"
            variant="outlined"
            @keydown.enter.prevent="confirm"
          />
        </div>

        <p v-if="refusal" class="m-0 text-xs text-error">
          {{ refusal }}
        </p>
      </div>

      <footer class="flex items-center justify-end gap-2 border-t border-line px-5 py-3">
        <div class="flex items-center gap-2">
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
            Publish to {{ ENVIRONMENT_LABELS[target] }}
          </v-btn>
        </div>
      </footer>
    </v-card>
  </v-dialog>
</template>
