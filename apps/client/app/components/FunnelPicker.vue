<script setup lang="ts">
// Which saved funnel is open, as the heading of the card that edits it: its name in the
// heading's own type, a menu of the others behind it, and what else can be done with it:
// keep it as a new funnel, rename it, delete it. Saving and discarding an edit are the page's action
// bar's; the dot here only says there is one. The edits are the page's draft; this only
// says what the reader asked for.
defineProps<{
  funnels: readonly FunnelDefinition[]
  /** The draft's name. */
  name: string
  /** The draft differs from what is saved, or has never been saved. */
  dirty: boolean
  /** The draft is a funnel not yet saved. */
  unsaved: boolean
}>()

const selected = defineModel<string>({ required: true })

const emit = defineEmits<{
  'save-as': [name: string]
  'rename': [name: string]
  'remove': []
}>()

/** A new funnel of either kind, by the id the page opens it under. */
const KINDS = [
  { id: 'new', label: 'New step funnel', note: 'Events in an order you choose' },
  { id: 'new-progression', label: 'New progression', note: 'Level 1, level 2, … level N' }
] as const

const naming = ref<'save-as' | 'rename' | null>(null)

/** Deleting asks first: a saved funnel is somebody's work, and there is no undo. */
const deleting = ref(false)
const dialogOpen = computed({
  get: () => naming.value !== null,
  set: (open) => {
    if (!open) naming.value = null
  }
})

function named(name: string) {
  if (naming.value === 'rename') emit('rename', name)
  else emit('save-as', name)
}
</script>

<template>
  <div class="flex min-w-0 items-center gap-1">
    <v-menu :offset="4" location="bottom start">
      <template #activator="{ props: activator }">
        <button
          v-bind="activator"
          :aria-label="`Funnel: ${name}${dirty ? ', unsaved changes' : ''}`"
          class="group -ml-2 inline-flex h-(--ps-toolbar-height) max-w-[calc(100%+0.5rem)] min-w-0 items-center gap-1.5 rounded-(--ps-radius) px-2 text-sm font-medium text-ink transition-colors duration-150 hover:bg-ink/5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
          type="button"
        >
          <span class="truncate">{{ name }}</span>
          <span v-if="dirty" aria-hidden="true" class="size-1.5 flex-none rounded-full bg-primary" />
          <Icon
            :aria-hidden="true"
            class="flex-none text-base text-ink/50 transition-colors duration-150 group-hover:text-ink/80"
            name="material-symbols:unfold-more"
          />
        </button>
      </template>

      <v-card class="w-72" flat>
        <p class="m-0 px-3 pt-2.5 pb-1.5 text-xs font-medium text-ink">
          Saved funnels
        </p>
        <div class="max-h-72 overflow-y-auto p-1">
          <button
            v-for="funnel in funnels"
            :key="funnel.id"
            :class="funnel.id === selected ? 'bg-primary/8' : 'hover:bg-ink/4'"
            class="flex w-full items-center justify-between gap-2 rounded-(--ps-radius-x-small) px-2 py-1.5 text-left transition-colors duration-150"
            type="button"
            @click="selected = funnel.id"
          >
            <span class="truncate text-xs text-ink">{{ funnel.name }}</span>
            <span class="flex-none font-mono text-[11px] text-ink/45">{{ funnel.kind === 'steps' ? `${funnel.steps.length} steps` : `${funnel.property} ${funnel.from}–${funnel.to}` }}</span>
          </button>
        </div>
        <div class="border-t border-line p-1">
          <button
            v-for="kind in KINDS"
            :key="kind.id"
            class="flex w-full items-start gap-1.5 rounded-(--ps-radius-x-small) px-2 py-1.5 text-left hover:bg-ink/4"
            type="button"
            @click="selected = kind.id"
          >
            <Icon :aria-hidden="true" class="mt-px text-base text-ink/60" name="material-symbols:add" />
            <span class="flex flex-col">
              <span class="text-xs text-ink">{{ kind.label }}</span>
              <span class="text-[11px] text-ink/50">{{ kind.note }}</span>
            </span>
          </button>
        </div>
      </v-card>
    </v-menu>

    <v-menu :offset="6" location="bottom end">
      <template #activator="{ props: activator }">
        <v-btn
          v-bind="activator"
          aria-label="More for this funnel"
          color="neutral"
          icon="material-symbols:more-horiz"
          size="small"
          variant="text"
        />
      </template>
      <v-list density="compact" min-width="200" nav>
        <v-list-item title="Save as new funnel…" @click="naming = 'save-as'" />
        <v-list-item :disabled="unsaved" title="Rename…" @click="naming = 'rename'" />
        <v-list-item
          :disabled="unsaved"
          base-color="error"
          title="Delete funnel"
          @click="deleting = true"
        />
      </v-list>
    </v-menu>

    <NameDialog
      v-model="dialogOpen"
      :confirm-label="naming === 'rename' ? 'Rename' : 'Save'"
      :current="naming === 'rename' ? name : ''"
      :description="naming === 'rename' ? '' : 'Saved for everyone in the project, with its steps and window.'"
      :title="naming === 'rename' ? 'Rename funnel' : 'Save as a new funnel'"
      @confirm="named"
    />

    <ConfirmDialog
      v-model="deleting"
      :description="`${name} and its steps go for everyone in the project. This cannot be undone.`"
      :title="`Delete ${name}?`"
      action="Delete funnel"
      @confirm="emit('remove')"
    />
  </div>
</template>
