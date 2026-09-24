<script setup lang="ts">
// Putting an environment back on an earlier version, for when something went out wrong: what
// goes back, in the same words a publish uses, and why, which is required, because the history
// is read at the moment something broke. It publishes that version again as the environment's
// next one; nothing is deleted and the draft is not touched, so the change that was pulled
// back is still there to fix, shown as waiting until it is published again.
const props = defineProps<{
  /** The earlier version to go back to. */
  version: ConfigVersionDef
}>()

const open = defineModel<boolean>({ required: true })

const { latest, rollback, segmentName } = useLiveConfig()

const reason = ref('')
const refusal = ref('')

const current = computed(() => latest(props.version.environment))
const label = computed(() => ENVIRONMENT_LABELS[props.version.environment])

/* What going back undoes, from what the environment serves now. */
const changes = computed(() => versionChanges(current.value, props.version, segmentName))

const CHANGE_LOOK: Record<DraftChange['change'], { icon: string, tone: string, word: string }> = {
  added: { icon: 'material-symbols:add-circle-outline', tone: 'text-success', word: 'Comes back' },
  changed: { icon: 'material-symbols:undo', tone: 'text-warning', word: 'Goes back' },
  removed: { icon: 'material-symbols:remove-circle-outline', tone: 'text-error', word: 'Taken out' }
}

function confirm() {
  const problem = rollback(props.version.environment, props.version, reason.value)
  if (problem) {
    refusal.value = problem
    return
  }
  open.value = false
}

const reasonId = useId()
</script>

<template>
  <v-dialog v-model="open" max-width="600" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          Roll back {{ label }} to v{{ version.version }}?
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          {{ label }} goes back to v{{ version.version }} for {{ ENVIRONMENT_WHO[version.environment].toLowerCase() }} within a minute,
          published again as v{{ (current?.version ?? 0) + 1 }}. Nothing is deleted, and your draft is not touched: what is rolled back stays in it,
          waiting, until it is fixed and published again.
        </p>
      </header>

      <div class="flex flex-col gap-5 overflow-y-auto p-5">
        <section class="flex flex-col gap-2">
          <h3 class="m-0 flex items-center justify-between text-xs font-medium text-ink">
            What goes back
            <span class="font-mono text-[11px] font-normal text-ink/45">v{{ current?.version }} → v{{ version.version }}</span>
          </h3>
          <p v-if="!changes.length" class="m-0 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs text-ink/60">
            v{{ version.version }} holds the same flags and segments as what {{ label }} serves now.
          </p>
          <ul v-else class="m-0 flex max-h-64 list-none flex-col overflow-y-auto rounded-(--ps-radius) border border-line p-0">
            <li v-for="change in changes" :key="`${change.kind}:${change.key}`" class="flex gap-2.5 border-b border-line px-3 py-2.5 last:border-b-0">
              <Icon
                :class="CHANGE_LOOK[change.change].tone"
                :name="CHANGE_LOOK[change.change].icon"
                aria-hidden="true"
                class="mt-0.5 flex-none text-sm"
              />
              <div class="flex min-w-0 flex-col gap-0.5">
                <span class="flex items-center gap-2 text-xs text-ink">
                  <span class="font-mono">{{ change.key }}</span>
                  <span class="text-[11px] text-ink/45">{{ CHANGE_LOOK[change.change].word }}</span>
                </span>
                <span v-for="line in change.lines" :key="line" class="text-[11px] leading-4 text-ink/60">{{ line }}</span>
              </div>
            </li>
          </ul>
        </section>

        <div class="flex flex-col gap-1">
          <label :for="reasonId" class="text-xs font-medium text-ink">Why <span class="font-normal text-ink/45">required: it goes in the history</span></label>
          <v-text-field
            :id="reasonId"
            v-model="reason"
            autocomplete="off"
            autofocus
            hide-details
            maxlength="300"
            placeholder="The store crashes on Android after the carousel went to 25%"
            variant="outlined"
            @keydown.enter.prevent="confirm"
          />
        </div>

        <p v-if="refusal" class="m-0 text-xs text-error">
          {{ refusal }}
        </p>
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
          :disabled="!reason.trim()"
          class="text-xs font-medium"
          color="warning"
          prepend-icon="material-symbols:undo"
          size="small"
          variant="flat"
          @click="confirm"
        >
          Roll back {{ label }}
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>
