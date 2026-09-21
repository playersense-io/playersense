<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'
import type { Member, MemberRole } from '~/types'

defineProps<{
  members: Member[]
}>()

// Matches the six seeded roles (apps/backend/internal/authz/roles.go).
// Assigning owner/admin isn't a permission on its own backend route either —
// see project.md §55.1 — so this select is a placeholder for that same rule.
const roles: MemberRole[] = ['owner', 'admin', 'liveops_manager', 'developer', 'growth_manager', 'billing']

const roleLabels: Record<MemberRole, string> = {
  owner: 'Owner',
  admin: 'Admin',
  liveops_manager: 'LiveOps Manager',
  developer: 'Developer',
  growth_manager: 'Growth Manager',
  billing: 'Billing'
}

function initials(name: string) {
  return name.split(' ').map(part => part[0]).join('').slice(0, 2).toUpperCase()
}

const items = [{
  label: 'Edit access',
  onSelect: () => console.log('Edit access')
}, {
  label: 'Remove member',
  color: 'error' as const,
  onSelect: () => console.log('Remove member')
}] satisfies DropdownMenuItem[]
</script>

<template>
  <ul role="list" class="divide-y divide-default">
    <li
      v-for="(member, index) in members"
      :key="index"
      class="flex items-center justify-between gap-3 py-3 px-4 sm:px-6"
    >
      <div class="flex items-center gap-3 min-w-0">
        <UAvatar :text="initials(member.name)" size="md" />

        <div class="text-sm min-w-0">
          <p class="text-highlighted font-medium truncate">
            {{ member.name }}
          </p>
          <p class="text-muted truncate">
            {{ member.username }}
          </p>
        </div>
      </div>

      <div class="flex items-center gap-3">
        <USelect
          :model-value="member.role"
          :items="roles.map(role => ({ label: roleLabels[role], value: role }))"
          color="neutral"
        />

        <UDropdownMenu :items="items" :content="{ align: 'end' }">
          <UButton
            icon="i-lucide-ellipsis-vertical"
            color="neutral"
            variant="ghost"
          />
        </UDropdownMenu>
      </div>
    </li>
  </ul>
</template>
