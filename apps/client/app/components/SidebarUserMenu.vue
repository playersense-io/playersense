<script setup lang="ts">
defineProps<{
  rail: boolean
}>()

// Who is signed in (Firebase), and the organizations they belong to. The organization is picked
// here rather than at the top of the drawer: a member changes project many times a day and
// organization rarely, and the drawer's head is the project's.
const auth = useAuthStore()
const workspace = useWorkspaceStore()
const { organizations, organization, selectOrganization } = useWorkspace()

async function signOut() {
  await auth.signOut()
  workspace.reset()
  await navigateTo('/login')
}

const accepting = ref('')
const acceptProblem = ref('')

async function accept(slug: string) {
  accepting.value = slug
  acceptProblem.value = ''
  try {
    await workspace.acceptInvitation(slug)
  } catch (caught) {
    acceptProblem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    accepting.value = ''
  }
}

const items: MenuItem[] = [
  { title: 'Billing', icon: 'material-symbols:receipt-long-outline', to: '/settings/billing' },
  { title: 'Settings', icon: 'material-symbols:settings-outline', to: '/settings' },
  { title: 'Sign out', icon: 'material-symbols:logout', onSelect: () => void signOut() }
]
</script>

<template>
  <SidebarAccountMenu :items="items" :name="auth.displayName || '-'" :rail="rail">
    <p class="m-0 px-3 pt-1 pb-1 text-[11px] text-ink/45">
      Organization
    </p>
    <v-list-item
      v-for="candidate in organizations"
      :key="candidate.id"
      :active="candidate.id === organization.id"
      :title="candidate.name"
      @click="selectOrganization(candidate.id)"
    >
      <template #prepend>
        <Icon :aria-hidden="true" class="text-ink/60" name="material-symbols:domain" />
      </template>
      <template v-if="candidate.id === organization.id" #append>
        <Icon :aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
      </template>
    </v-list-item>

    <template v-if="workspace.invitations.length">
      <p class="m-0 px-3 pt-2 pb-1 text-[11px] text-ink/45">
        Invitations
      </p>
      <v-list-item
        v-for="invitation in workspace.invitations"
        :key="invitation.organization_slug"
        :disabled="Boolean(accepting)"
        :subtitle="`As ${invitation.role}. Click to join`"
        :title="invitation.organization"
        @click="accept(invitation.organization_slug)"
      >
        <template #prepend>
          <Icon :aria-hidden="true" class="text-primary" name="material-symbols:mail-outline" />
        </template>
        <template v-if="accepting === invitation.organization_slug" #append>
          <v-progress-circular indeterminate size="14" width="2" />
        </template>
      </v-list-item>
      <p v-if="acceptProblem" class="m-0 px-3 pb-1 text-[11px] text-error">
        {{ acceptProblem }}
      </p>
    </template>
  </SidebarAccountMenu>
</template>
