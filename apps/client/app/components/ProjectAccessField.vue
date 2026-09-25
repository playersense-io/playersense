<script setup lang="ts">
import type { MemberAccess } from '~/composables/members'

// Which projects a member reaches: every project, including ones made later, or the ones ticked.
const access = defineModel<MemberAccess>({ required: true })

const workspace = useWorkspaceStore()

function toggle(slug: string, on: boolean) {
  const projects = on ? [...new Set([...access.value.projects, slug])] : access.value.projects.filter(entry => entry !== slug)
  access.value = { ...access.value, projects }
}
</script>

<template>
  <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
    <legend class="mb-1 text-xs font-medium text-ink">
      Projects
    </legend>
    <v-switch
      :model-value="access.all_projects"
      color="primary"
      density="compact"
      hide-details
      inset
      label="Every project, including ones made later"
      @update:model-value="access = { ...access, all_projects: Boolean($event) }"
    />
    <div v-if="!access.all_projects" class="flex max-h-56 flex-col overflow-y-auto rounded-(--ps-radius-small) border border-line">
      <v-checkbox
        v-for="project in workspace.projects"
        :key="project.slug"
        :label="project.name"
        :model-value="access.projects.includes(project.slug)"
        class="px-2"
        density="compact"
        hide-details
        @update:model-value="toggle(project.slug, Boolean($event))"
      />
      <p v-if="!workspace.projects.length" class="m-0 px-3 py-2.5 text-xs text-ink/55">
        No projects yet.
      </p>
    </div>
    <p v-if="!access.all_projects && !access.projects.length" class="m-0 text-[11px] text-warning">
      Pick at least one project, or give every project.
    </p>
  </fieldset>
</template>
