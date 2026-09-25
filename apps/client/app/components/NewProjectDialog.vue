<script setup lang="ts">
import type { Schema } from '@playersense/contracts'

// A new project: a game. Its name, the short name its URLs use, and how its players' data may
// be handled, which is fixed once it is made (project.md §98).
const open = defineModel<boolean>({ required: true })

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

watch(open, (value) => {
  if (!value) return
  name.value = ''
  slug.value = ''
  slugTouched.value = false
  tier.value = 'standard'
  problem.value = ''
})

// The short name follows the name until somebody edits it.
watch(name, (value) => {
  if (slugTouched.value) return
  slug.value = value.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64)
})

const ready = computed(() => name.value.trim().length >= 2 && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug.value))

async function create() {
  if (!ready.value || !workspace.orgPath) return
  saving.value = true
  problem.value = ''
  try {
    const made = await apiRequest<Schema<'ProjectOut'>>('POST', `${workspace.orgPath}/projects`,
      { name: name.value.trim(), slug: slug.value, policy_tier: tier.value })
    await workspace.loadProjects()
    workspace.selectProject(made.slug)
    open.value = false
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <v-dialog v-model="open" max-width="480">
    <v-card border flat>
      <form class="flex flex-col gap-5 p-5" @submit.prevent="create">
        <header class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            New project
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            One project is one game. It gets a production, a staging and a development environment, each with its own keys.
          </p>
        </header>

        <div class="flex flex-col gap-1">
          <label class="text-xs font-medium text-ink" for="project-name">Name</label>
          <v-text-field
            id="project-name"
            v-model="name"
            autofocus
            hide-details
            maxlength="120"
            variant="filled"
          />
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-xs font-medium text-ink" for="project-slug">Short name</label>
          <v-text-field
            id="project-slug"
            v-model="slug"
            hide-details
            maxlength="64"
            variant="filled"
            @update:model-value="slugTouched = true"
          />
          <p class="m-0 text-[11px] leading-4 text-ink/50">
            In the dashboard's addresses and the API's paths. Fixed once the project is made.
          </p>
        </div>

        <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
          <legend class="mb-1 text-xs font-medium text-ink/70">
            Players' data
          </legend>
          <label
            v-for="entry in TIERS"
            :key="entry.value"
            :class="tier === entry.value ? 'border-primary bg-primary/5' : 'border-ink/12'"
            class="flex cursor-pointer gap-3 rounded-(--ps-radius-small) border p-3"
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

        <p v-if="problem" class="m-0 text-xs text-error">
          {{ problem }}
        </p>

        <footer class="flex justify-end gap-2">
          <v-btn variant="text" @click="open = false">
            Cancel
          </v-btn>
          <v-btn
            :disabled="!ready"
            :loading="saving"
            color="primary"
            type="submit"
            variant="flat"
          >
            Create project
          </v-btn>
        </footer>
      </form>
    </v-card>
  </v-dialog>
</template>
