<script setup lang="ts">
// Which saved funnel is open, and what can be done with the one on screen: save its edits,
// throw them away, keep it as a new funnel, rename it, delete it. The edits are the page's
// draft; this only says what the reader asked for.
//
// A funnel not yet saved has no name of its own, so saving it asks for one.
const props = defineProps<{
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
  'save': []
  'save-as': [name: string]
  'rename': [name: string]
  'remove': []
  'discard': []
}>()

/** A new funnel of either kind, by the id the page opens it under. */
const KINDS = [
  { id: 'new', label: 'New step funnel', note: 'Events in an order you choose' },
  { id: 'new-progression', label: 'New progression', note: 'Level 1, level 2, … level N' }
] as const

const naming = ref<'save-as' | 'rename' | null>(null)
const dialogOpen = computed({
  get: () => naming.value !== null,
  set: (open) => {
    if (!open) naming.value = null
  }
})

function save() {
  if (props.unsaved) naming.value = 'save-as'
  else emit('save')
}

function named(name: string) {
  if (naming.value === 'rename') emit('rename', name)
  else emit('save-as', name)
}
</script>

<template>
  <div class="flex items-center gap-1">
    <v-menu :offset="6" location="bottom end">
      <template #activator="{ props: activator }">
        <v-btn
          v-bind="activator"
          append-icon="material-symbols:unfold-more"
          class="ps-toolbar-control text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:filter-alt-outline"
          size="small"
          variant="text"
        >
          <span class="max-w-40 truncate">{{ name }}</span>
          <span
            v-if="dirty"
            aria-label="Unsaved changes"
            class="ml-1.5 size-1.5 rounded-full bg-primary"
            role="img"
          />
        </v-btn>
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
            <Icon aria-hidden="true" class="mt-px text-base text-ink/60" name="material-symbols:add" />
            <span class="flex flex-col">
              <span class="text-xs text-ink">{{ kind.label }}</span>
              <span class="text-[11px] text-ink/50">{{ kind.note }}</span>
            </span>
          </button>
        </div>
      </v-card>
    </v-menu>

    <template v-if="dirty">
      <v-btn
        v-if="!unsaved"
        class="text-xs font-medium"
        color="neutral"
        size="small"
        variant="text"
        @click="emit('discard')"
      >
        Discard
      </v-btn>
      <v-btn
        class="text-xs font-medium"
        color="primary"
        size="small"
        variant="flat"
        @click="save"
      >
        Save
      </v-btn>
    </template>

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
          :disabled="unsaved || funnels.length <= 1"
          base-color="error"
          title="Delete funnel"
          @click="emit('remove')"
        />
      </v-list>
    </v-menu>

    <NameDialog
      v-model="dialogOpen"
      :confirm-label="naming === 'rename' ? 'Rename' : 'Save'"
      :current="naming === 'rename' ? name : ''"
      :description="naming === 'rename' ? '' : 'Saved in this browser, with its steps and window.'"
      :title="naming === 'rename' ? 'Rename funnel' : 'Save as a new funnel'"
      @confirm="named"
    />
  </div>
</template>
