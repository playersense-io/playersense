<script setup lang="ts">
// Getting started: where somebody lands with no project to show, a new account first of all.
// Everything in the dashboard is a project's, so one is made here before anything else opens
// (middleware/auth.global.ts sends every other page here until there is one). Somebody who was
// invited somewhere can join that organization instead, and somebody in several organizations
// chooses which one the project goes in.
definePageMeta({ layout: 'bare' })
useHead({ title: 'Getting started · PlayerSense' })

const auth = useAuthStore()
const workspace = useWorkspaceStore()

const organizationSlug = computed({
  get: () => workspace.organization?.slug ?? '',
  set: slug => void choose(slug)
})
const ORGANIZATIONS = computed(() => workspace.organizations.map(entry => ({ value: entry.slug, title: entry.name })))

/* An organization that already has projects is somewhere to go, not somewhere to start. */
async function choose(slug: string) {
  await workspace.selectOrganization(slug)
  if (workspace.projects.length) await navigateTo('/')
}

async function created() {
  await navigateTo('/', { replace: true })
}

const joining = ref('')
const problem = ref('')

async function join(slug: string) {
  joining.value = slug
  problem.value = ''
  try {
    await workspace.acceptInvitation(slug)
    if (workspace.projects.length) await navigateTo('/', { replace: true })
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    joining.value = ''
  }
}

async function signOut() {
  await auth.signOut()
  workspace.reset()
  await navigateTo('/login')
}
</script>

<template>
  <div class="flex w-full max-w-[440px] flex-col gap-8">
    <div class="flex justify-center">
      <Logo :size="28" />
    </div>

    <header class="flex flex-col gap-1.5 text-center">
      <h1 class="m-0 text-xl font-semibold tracking-tight text-ink">
        Create your first project
      </h1>
      <p class="m-0 text-sm leading-6 text-ink/60">
        A project is one game. It gets a production, a staging and a development environment, each with its own keys, and everything in the dashboard is a project's.
      </p>
    </header>

    <!-- Invitations waiting for this address: joining one may be all there is to do. -->
    <section v-if="workspace.invitations.length" class="flex flex-col gap-2">
      <h2 class="m-0 text-xs font-medium text-ink">
        You were invited
      </h2>
      <div
        v-for="invitation in workspace.invitations"
        :key="invitation.organization_slug"
        class="flex items-center gap-3 rounded-(--ps-radius-small) bg-ink/3 px-3.5 py-3"
      >
        <Icon class="flex-none text-lg text-primary" name="material-symbols:mail-outline" />
        <span class="flex min-w-0 flex-1 flex-col">
          <span class="truncate text-[13px] text-ink">{{ invitation.organization }}</span>
          <span class="truncate text-[11px] text-ink/55">As {{ invitation.role }}<template v-if="invitation.invited_by">, from {{ invitation.invited_by }}</template></span>
        </span>
        <v-btn
          :disabled="Boolean(joining) && joining !== invitation.organization_slug"
          :loading="joining === invitation.organization_slug"
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="join(invitation.organization_slug)"
        >
          Join
        </v-btn>
      </div>
      <p v-if="problem" class="m-0 text-xs text-error">
        {{ problem }}
      </p>
      <div class="flex items-center gap-3 pt-2 text-xs text-ink/45">
        <span class="h-px flex-1 bg-ink/8" />
        or make your own
        <span class="h-px flex-1 bg-ink/8" />
      </div>
    </section>

    <div v-if="ORGANIZATIONS.length > 1" class="flex flex-col gap-1">
      <label class="text-xs font-medium text-ink" for="start-organization">Organization</label>
      <v-select
        id="start-organization"
        v-model="organizationSlug"
        :items="ORGANIZATIONS"
        hide-details
        variant="underlined"
      />
    </div>

    <ProjectForm variant="underlined" @created="created" />

    <p class="m-0 text-center text-xs leading-5 text-ink/45">
      Signed in as {{ auth.email }}.
      <button class="cursor-pointer text-ink/65 underline-offset-2 hover:text-ink hover:underline" type="button" @click="signOut">
        Sign out
      </button>
    </p>
  </div>
</template>
