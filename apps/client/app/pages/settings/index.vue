<script setup lang="ts">
// Settings > General: the project itself. Its name is the one thing here that changes; the
// slug, the ids and the collection policy tier are fixed at creation (§75, §98), and are shown
// with why, so nobody hunts for the control that is not there. Deleting the project is last,
// in its own frame, and takes thirty days, during which it can be kept.
const { project, rename, scheduleDeletion, keepProject } = useProjectSettings()
const { organization } = useWorkspace()

const name = ref(project.value.name)
watch(() => project.value.id, () => (name.value = project.value.name))

const nameProblem = computed(() => (!name.value.trim() ? 'A project needs a name' : name.value.trim().length > 60 ? 'Up to 60 characters' : null))
const nameChanged = computed(() => name.value.trim() !== project.value.name)
const nameId = useId()

const saved = ref(false)

function saveName() {
  if (nameProblem.value || !nameChanged.value) return
  rename(name.value)
  saved.value = true
  setTimeout(() => (saved.value = false), 2400)
}

const facts = computed<Fact[]>(() => [
  { key: 'id', label: 'Project ID', value: project.value.id, mono: true, copy: project.value.id, info: 'What the API calls this project: /organizations/{org}/projects/{project}.' },
  { key: 'slug', label: 'Slug', value: project.value.slug, mono: true, copy: project.value.slug, info: 'Set from the name at creation and fixed, so links and exports that carry it keep working after a rename.' },
  { key: 'organization', label: 'Organization', value: organization.value.name, to: '/settings/members' },
  { key: 'created', label: 'Created', value: formatDate(project.value.created_at) }
])

// --- Environments and apps -------------------------------------------------------------------

const ENVIRONMENT_ORDER: EnvironmentKind[] = ['production', 'staging', 'development']
const environments = computed(() => [...project.value.environments].sort((a, b) => ENVIRONMENT_ORDER.indexOf(a.kind) - ENVIRONMENT_ORDER.indexOf(b.kind)))

const tier = computed(() => POLICY_TIERS[project.value.policy_tier])
const { plan } = useBilling()
const retentionCap = computed(() => Math.min(tier.value.cap, plan.value.entitlements.max_raw_retention_days ?? 30))

// --- Deleting --------------------------------------------------------------------------------

const deleting = ref(false)
const daysLeft = computed(() => (project.value.deletion_at ? Math.max(0, Math.ceil((Date.parse(project.value.deletion_at) - Date.now()) / 86_400_000)) : 0))
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="General">
      <template #description>
        <span class="font-medium text-ink">{{ project.name }}</span> itself: what it is called, where its events come from, and the rules it was created under.
      </template>
    </PageHeader>

    <p v-if="project.deletion_at" class="m-0 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-(--ps-radius-large) bg-error/6 px-4 py-3 text-xs leading-5 text-ink/80 ring-1 ring-error/20 ring-inset">
      <Icon aria-hidden="true" class="flex-none text-base text-error" name="material-symbols:delete-outline" />
      <span class="min-w-0 flex-1">
        This project is deleted on <span class="font-medium text-ink">{{ formatDate(project.deletion_at) }}</span>, in {{ daysLeft }} days. Events are still collected until then.
      </span>
      <v-btn
        class="text-xs font-medium"
        color="neutral"
        size="small"
        variant="outlined"
        @click="keepProject"
      >
        Keep the project
      </v-btn>
    </p>

    <SectionCard description="The name is shown across the dashboard and in reports. Everything else here was set when the project was made." title="Project">
      <div class="flex flex-col gap-5">
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="saveName">
          <div class="flex max-w-md min-w-60 flex-1 flex-col gap-1.5">
            <label :for="nameId" class="text-xs text-ink/60">Name</label>
            <v-text-field
              :id="nameId"
              v-model="name"
              :error="Boolean(nameProblem)"
              autocomplete="off"
              density="comfortable"
              hide-details
              maxlength="60"
              variant="outlined"
            />
          </div>
          <v-btn
            :disabled="!nameChanged || Boolean(nameProblem)"
            class="mb-0.5 text-xs font-medium"
            color="primary"
            size="small"
            type="submit"
            variant="flat"
          >
            Save
          </v-btn>
          <span v-if="saved" class="mb-2 flex items-center gap-1 text-xs text-success">
            <Icon aria-hidden="true" class="text-sm" name="material-symbols:check" />
            Saved
          </span>
          <span v-else-if="nameProblem" class="mb-2 text-xs text-error">{{ nameProblem }}</span>
        </form>

        <FactGrid :facts="facts" label="Project facts" />
      </div>
    </SectionCard>

    <SectionCard :padded="false" description="Each environment's events are kept apart from the others'. The SDK key an event is sent with decides which one it lands in." title="Environments">
      <template #actions>
        <NuxtLink class="inline-flex items-center gap-0.5 text-xs text-ink/55 no-underline transition-colors hover:text-ink" to="/settings/api-keys">
          API keys
          <Icon aria-hidden="true" class="text-sm" name="material-symbols:chevron-right" />
        </NuxtLink>
      </template>

      <ul class="m-0 flex list-none flex-col divide-y divide-line p-0">
        <li v-for="environment in environments" :key="environment.id" class="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3">
          <span class="w-32 flex-none">
            <Tag :label="ENVIRONMENT_KINDS[environment.kind].label" :tone="ENVIRONMENT_KINDS[environment.kind].tone" />
          </span>
          <span class="flex min-w-0 flex-1 items-center gap-1.5">
            <span class="truncate font-mono text-xs text-ink/80">{{ environment.id }}</span>
            <CopyButton :label="`Copy the ${ENVIRONMENT_KINDS[environment.kind].label.toLowerCase()} environment ID`" :value="environment.id" />
          </span>
          <span class="text-[11px] text-ink/50">Since {{ formatDate(environment.created_at) }}</span>
        </li>
      </ul>
    </SectionCard>

    <SectionCard :padded="false" description="The store and platform identifiers this project's events have arrived from. A new one appears with its first event." title="Apps">
      <ul class="m-0 flex list-none flex-col divide-y divide-line p-0">
        <li v-for="app in project.apps" :key="`${app.platform}:${app.identifier}`" class="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3">
          <span class="flex w-32 flex-none items-center gap-2 text-sm text-ink">
            <PlatformIcon :platform="app.platform" :size="16" />
            {{ app.platform }}
          </span>
          <span class="min-w-0 flex-1 truncate font-mono text-xs text-ink/80">{{ app.identifier }}</span>
          <span class="text-[11px] text-ink/50">First event {{ formatDate(app.first_seen) }}</span>
        </li>
      </ul>
    </SectionCard>

    <SectionCard description="What may be collected about this project's players. It is stamped on every event, so it is fixed at creation: a later change could not make earlier collection lawful." title="Collection policy">
      <template #badge>
        <span class="rounded-full border border-line px-1.5 text-[10px] leading-4 font-medium text-ink/55">Fixed</span>
      </template>

      <div class="flex flex-col gap-4">
        <ul class="m-0 grid list-none gap-2 p-0 md:grid-cols-3" aria-label="Collection policy tiers">
          <li
            v-for="(entry, key) in POLICY_TIERS"
            :key="key"
            :aria-current="key === project.policy_tier ? 'true' : undefined"
            :class="key === project.policy_tier ? 'border-primary bg-primary/4 ring-1 ring-primary ring-inset' : 'border-line opacity-60'"
            class="flex flex-col gap-1.5 rounded-(--ps-radius-large) border p-3.5"
          >
            <span class="flex items-center justify-between gap-2 text-sm font-medium text-ink">
              {{ entry.label }}
              <Icon
                v-if="key === project.policy_tier"
                aria-hidden="true"
                class="text-base text-primary"
                name="material-symbols:check-circle"
              />
            </span>
            <span class="text-xs leading-5 text-ink/65">{{ entry.note }}</span>
          </li>
        </ul>
        <p class="m-0 text-xs text-ink/60">
          Under {{ tier.label.toLowerCase() }} collection on the {{ plan.name }} plan, raw events can be kept up to {{ retentionCap }} days.
          <NuxtLink class="text-primary no-underline hover:underline" to="/settings/data-retention">Data retention</NuxtLink>
        </p>
      </div>
    </SectionCard>

    <DangerZone
      v-if="!project.deletion_at"
      action="Delete project"
      description="Thirty days after you ask, the project and everything collected for it is gone."
      title="Delete this project"
      @confirm="deleting = true"
    >
      <p class="m-0 text-xs leading-5 text-ink/70">
        Events, players, flags, experiments, reports and keys all go with it. Until the day it is deleted, it can be kept from this page.
      </p>
    </DangerZone>

    <ConfirmDialog
      v-model="deleting"
      :description="`${project.name} and everything collected for it is deleted on ${formatDate(new Date(Date.now() + 30 * 86_400_000).toISOString())}. Until then it can be kept from this page.`"
      :title="`Delete ${project.name}?`"
      action="Delete project"
      @confirm="scheduleDeletion"
    />
  </div>
</template>
