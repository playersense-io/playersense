<script setup lang="ts">
// The project the dashboard is showing, and the menu that changes it. Above the navigation
// because every screen under it is scoped to one project. The mark is an initial on the
// accent rather than a logo: a project is a tenant's game, not a brand we hold an image for.
// The organization it belongs to heads the menu; changing that is the account menu's job.
defineProps<{
  /** Folded to the mark alone, with the name in a tooltip. */
  rail: boolean
}>()

const { organization, project, selectProject } = useWorkspace()
const workspace = useWorkspaceStore()

const ENVIRONMENTS = [
  { kind: 'production', label: 'Production' },
  { kind: 'staging', label: 'Staging' },
  { kind: 'development', label: 'Development' }
] as const

const creating = ref(false)

function initial(name: string): string {
  return name.trim().charAt(0).toUpperCase()
}
</script>

<template>
  <!-- Padding and gap put the mark's centre on the same line as the glyphs in the list
       below it. Folded, the button is the rail's width and centres. -->
  <button
    :aria-label="rail ? `Project: ${project.name}` : undefined"
    :class="rail ? 'justify-center' : 'gap-2.5 px-2.5'"
    class="
      group flex h-9 w-full items-center overflow-hidden rounded-(--ps-radius-small) text-ink
      transition-[background-color,transform]
      hover:bg-ink/5
      focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary
      active:scale-[0.99]
    "
    type="button"
  >
    <span
      aria-hidden="true"
      class="
        flex size-5 flex-none items-center justify-center rounded-md bg-primary text-[10px]
        font-semibold text-on-primary
      "
    >
      {{ initial(project.name) }}
    </span>

    <template v-if="!rail">
      <span class="min-w-0 flex-1 truncate text-left text-[13px] font-medium">
        {{ project.name || 'No project yet' }}
      </span>
      <span v-if="workspace.environment !== 'production'" class="flex-none rounded bg-warning/15 px-1.5 text-[10px] font-medium text-warning">
        {{ workspace.environment }}
      </span>
      <Icon
        aria-hidden="true"
        class="
          flex-none text-base text-ink/50 transition-colors
          group-hover:text-ink/80
        "
        name="material-symbols:unfold-more"
      />
    </template>

    <v-tooltip
      v-if="rail"
      :text="project.name"
      activator="parent"
      location="end"
      open-delay="300"
    />

    <v-menu :offset="6" activator="parent" location="bottom start">
      <v-list min-width="232" nav>
        <p class="truncate px-3 pt-1 pb-2 text-xs text-ink/60">
          {{ organization.name }}
        </p>
        <v-list-item
          v-for="candidate in organization.projects"
          :key="candidate.id"
          :active="candidate.id === project.id"
          :title="candidate.name"
          @click="selectProject(candidate.slug)"
        >
          <template #prepend>
            <span
              aria-hidden="true"
              class="
                flex size-5 items-center justify-center rounded-md bg-primary text-[10px]
                font-semibold text-on-primary
              "
            >
              {{ initial(candidate.name) }}
            </span>
          </template>
          <template v-if="candidate.id === project.id" #append>
            <Icon aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
          </template>
        </v-list-item>
        <v-list-item title="New project" @click="creating = true">
          <template #prepend>
            <Icon aria-hidden="true" class="text-ink/60" name="material-symbols:add" />
          </template>
        </v-list-item>
        <v-divider class="my-1" />
        <p class="px-3 pt-1 pb-1 text-[11px] text-ink/45">
          Environment
        </p>
        <v-list-item
          v-for="entry in ENVIRONMENTS"
          :key="entry.kind"
          :active="entry.kind === workspace.environment"
          :title="entry.label"
          @click="workspace.selectEnvironment(entry.kind)"
        >
          <template v-if="entry.kind === workspace.environment" #append>
            <Icon aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
          </template>
        </v-list-item>
      </v-list>
    </v-menu>
  </button>
  <NewProjectDialog v-model="creating" />
</template>
