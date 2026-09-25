<script setup lang="ts">
import type { Schema } from '@playersense/contracts'

// A new project: a game. Its name, the short name its URLs use, and how its players' data may
// be handled, which is fixed once it is made (project.md §98). Used by the New project dialog
// and by Getting started, which asks for the first one.
const props = withDefaults(defineProps<{
  /** The submit button's words. */
  action?: string
  /** Offers a Cancel beside it, for a dialog. */
  cancellable?: boolean
  /** The fields' look: filled in a dialog, underlined on a plain page. */
  variant?: 'filled' | 'underlined'
}>(), { action: 'Create project', cancellable: false, variant: 'filled' })

const emit = defineEmits<{ created: [slug: string], cancel: [] }>()

const workspace = useWorkspaceStore()

const name = ref('')
const slug = ref('')
const slugTouched = ref(false)
const tier = ref<'standard' | 'child_directed' | 'kids_strict'>('standard')
const saving = ref(false)
const problem = ref('')

const TIERS = [
  { value: 'standard', title: 'Standard', note: 'Players are adults, or the game is not directed at children.' },
  { value: 'child_directed', title: 'Directed at children', note: 'COPPA and Google Play Families: no advertising identifiers, cohorts kept coarse.' },
  { value: 'kids_strict', title: 'Kids, strict', note: 'No persistent identifier at all: every batch is its own install.' }
] as const

// The short name follows the name until somebody edits it.
watch(name, (value) => {
  if (slugTouched.value) return
  slug.value = value.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64)
})

const ready = computed(() => name.value.trim().length >= 2 && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug.value))

/** Empties the form, for a dialog opened again. */
function reset() {
  name.value = ''
  slug.value = ''
  slugTouched.value = false
  tier.value = 'standard'
  problem.value = ''
}

async function create() {
  if (!ready.value || saving.value || !workspace.orgPath) return
  saving.value = true
  problem.value = ''
  try {
    const made = await apiRequest<Schema<'ProjectOut'>>('POST', `${workspace.orgPath}/projects`,
      { name: name.value.trim(), slug: slug.value, policy_tier: tier.value })
    await workspace.loadProjects()
    workspace.selectProject(made.slug)
    emit('created', made.slug)
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

defineExpose({ reset })

const ids = { name: useId(), slug: useId() }
const fieldVariant = computed(() => props.variant)
</script>

<template>
  <form class="flex flex-col gap-5" @submit.prevent="create">
    <div class="flex flex-col gap-1">
      <label :for="ids.name" class="text-xs font-medium text-ink">Name</label>
      <v-text-field
        :id="ids.name"
        v-model="name"
        :variant="fieldVariant"
        autocomplete="off"
        autofocus
        hide-details
        maxlength="120"
        placeholder="Starfall Legends"
      />
    </div>
    <div class="flex flex-col gap-1">
      <label :for="ids.slug" class="text-xs font-medium text-ink">Short name</label>
      <v-text-field
        :id="ids.slug"
        v-model="slug"
        :variant="fieldVariant"
        autocomplete="off"
        hide-details
        maxlength="64"
        @update:model-value="slugTouched = true"
      />
      <p class="m-0 text-[11px] leading-4 text-ink/50">
        In the dashboard's addresses and the API's paths. Fixed once the project is made.
      </p>
    </div>

    <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
      <legend class="mb-1 text-xs font-medium text-ink">
        Players' data
      </legend>
      <label
        v-for="entry in TIERS"
        :key="entry.value"
        :class="tier === entry.value ? 'bg-primary/6 ring-1 ring-primary ring-inset' : 'bg-ink/3 hover:bg-ink/5'"
        class="flex cursor-pointer gap-3 rounded-(--ps-radius-small) p-3 transition-colors duration-150"
      >
        <input
          v-model="tier"
          :value="entry.value"
          class="mt-0.5 accent-(--v-theme-primary)"
          name="tier"
          type="radio"
        >
        <span class="flex flex-col gap-0.5">
          <span class="text-[13px] text-ink">{{ entry.title }}</span>
          <span class="text-xs leading-5 text-ink/60">{{ entry.note }}</span>
        </span>
      </label>
      <p class="m-0 text-[11px] leading-4 text-ink/50">
        This cannot be changed later: it decides what the SDK stores from the first event.
      </p>
    </fieldset>

    <p v-if="problem" class="m-0 text-xs leading-5 text-error">
      {{ problem }}
    </p>

    <footer :class="cancellable ? 'justify-end' : ''" class="flex gap-2">
      <v-btn v-if="cancellable" variant="text" @click="emit('cancel')">
        Cancel
      </v-btn>
      <v-btn
        :block="!cancellable"
        :disabled="!ready"
        :loading="saving"
        class="ps-project-submit"
        color="primary"
        type="submit"
        variant="flat"
      >
        {{ action }}
      </v-btn>
    </footer>
  </form>
</template>

<style scoped>
/* Disabled until the form is complete: a lighter fill and muted words, as on the sign-in page,
   rather than Vuetify's darker grey. From the theme's ink, so a dark page gets the same. */
.ps-project-submit.v-btn--disabled {
  background: rgba(var(--v-theme-on-surface), 0.025);
  color: rgba(var(--v-theme-on-surface), 0.32);
  opacity: 1;
}

.ps-project-submit.v-btn--disabled :deep(.v-btn__overlay) {
  opacity: 0;
}
</style>
