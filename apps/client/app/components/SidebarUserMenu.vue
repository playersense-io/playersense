<script setup lang="ts">
defineProps<{
  rail: boolean
}>()

// Placeholder until Firebase sign-in is wired in (§55): the signed-in address then comes
// from the Firebase user, and "Sign out" calls it.
const name = 'hmmusicproducing@gmail.com'

// The organization is picked here rather than at the top of the drawer: a member changes
// project many times a day and organization rarely, and the drawer's head is the project's.
const { organizations, organization, selectOrganization } = useWorkspace()

const items: MenuItem[] = [
  { title: 'Billing', icon: 'material-symbols:receipt-long-outline', to: '/settings/billing' },
  { title: 'Settings', icon: 'material-symbols:settings-outline', to: '/settings' },
  { title: 'Sign out', icon: 'material-symbols:logout' }
]
</script>

<template>
  <SidebarAccountMenu :items="items" :name="name" :rail="rail">
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
        <Icon aria-hidden="true" class="text-ink/60" name="material-symbols:domain" />
      </template>
      <template v-if="candidate.id === organization.id" #append>
        <Icon aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
      </template>
    </v-list-item>
  </SidebarAccountMenu>
</template>
