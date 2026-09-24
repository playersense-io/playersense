<script setup lang="ts">
import type { Member, MemberRole } from '~/types'

const { data: members, pending } = await useFetch<Member[]>('/api/members', { default: () => [] })

// A role is a tag that opens the roles (MemberRoleTag), from the six seeded roles
// (apps/backend/internal/authz/roles.go). A change is kept on this page until the members
// route takes writes; assigning owner or admin is an owner's alone there (project.md §55.1).
const changed = ref<Record<string, MemberRole>>({})
const roleOf = (member: Member) => changed.value[member.username] ?? member.role

function setRole(member: Member, role: MemberRole) {
  changed.value = { ...changed.value, [member.username]: role }
}

const owners = computed(() => members.value.filter(member => roleOf(member) === 'owner').length)
const lockOf = (member: Member) => (roleOf(member) === 'owner' && owners.value === 1 ? 'The only owner. Make somebody else an owner first.' : undefined)

const COLUMNS: DataTableColumn<Member>[] = [
  { key: 'member', title: 'Member', fixed: true, value: member => member.name },
  { key: 'role', title: 'Role', width: 220, value: member => MEMBER_ROLE_ORDER.indexOf(roleOf(member)) }
]

const columns = ref(defaultColumnState(COLUMNS))

const query = ref('')

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return members.value.filter(member => !needle
    || member.name.toLowerCase().includes(needle)
    || member.username.toLowerCase().includes(needle))
})

function initials(name: string) {
  return name.split(' ').map(part => part[0]).join('').slice(0, 2).toUpperCase()
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Members">
      <template #description>
        Who can reach <span class="font-medium text-ink">this organization</span> and what each
        of them may do. Invite by email: 3 per address per hour, 30 per organization per hour,
        and it fails closed.
      </template>
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:person-add-outline"
          size="small"
          variant="text"
        >
          Invite people
        </v-btn>
      </template>
    </PageHeader>

    <v-card border flat>
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line p-3">
        <SearchField
          v-model="query"
          class="w-full sm:w-64"
          label="Search members"
          placeholder="Search name or username"
        />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </div>

      <DataTable
        v-model:column-state="columns"
        :columns="COLUMNS"
        :items="matched"
        :loading="pending"
        :paged="false"
        empty-description="No name or username on this team matches what you typed."
        empty-title="Nobody matches."
        item-value="username"
        label="Members"
      >
        <template #cell.member="{ item }">
          <span class="flex min-w-0 items-center gap-3">
            <span
              :class="roleOf(item) === 'owner' ? 'border-primary/40 bg-primary/8 text-primary' : `
                border-line bg-ink/4 text-ink/70
              `"
              aria-hidden="true"
              class="
                flex size-7 flex-none items-center justify-center rounded-full border
                text-[10px] font-medium
              "
            >
              {{ initials(item.name) }}
            </span>
            <span class="flex min-w-0 flex-col">
              <span class="truncate font-medium text-ink">{{ item.name }}</span>
              <span class="truncate font-mono text-[11px] text-ink/55">{{ item.username }}</span>
            </span>
          </span>
        </template>

        <template #cell.role="{ item }">
          <MemberRoleTag
            :label="`Role of ${item.name}`"
            :locked="lockOf(item)"
            :model-value="roleOf(item)"
            @update:model-value="setRole(item, $event)"
          />
        </template>

        <template #actions="{ item }">
          <v-btn
            :aria-label="`Actions for ${item.name}`"
            icon
            size="small"
            variant="text"
          >
            <Icon aria-hidden="true" class="text-lg" name="material-symbols:more-vert" />
            <v-menu activator="parent" location="bottom end" :offset="4">
              <v-list min-width="200" nav>
                <v-list-item prepend-icon="material-symbols:manage-accounts-outline" title="Edit access" />
                <v-list-item
                  base-color="error"
                  prepend-icon="material-symbols:person-remove-outline"
                  title="Remove member"
                />
              </v-list>
            </v-menu>
          </v-btn>
        </template>
      </DataTable>
    </v-card>
  </div>
</template>
