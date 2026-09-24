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

const sortBy = ref<DataTableSort[]>([{ key: 'role', order: 'asc' }])

// --- Narrowing: a search, and roles under Filter, as every list here does ---------------------

const query = ref('')
const filters = ref<Record<string, string[]>>({ role: [] })

const FILTER_GROUPS: FilterGroup[] = [
  { key: 'role', label: 'Role', options: MEMBER_ROLE_ORDER.map(role => ({ value: role, label: MEMBER_ROLES[role].label })) }
]

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.role ?? []).map(role => ({ key: `role:${role}`, label: `Role: ${MEMBER_ROLES[role as MemberRole].label}` })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') query.value = ''
  else filters.value = { role: (filters.value.role ?? []).filter(role => `role:${role}` !== key) }
}

function clearFilters() {
  query.value = ''
  filters.value = { role: [] }
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const roles = filters.value.role ?? []
  return members.value.filter(member => (!roles.length || roles.includes(roleOf(member)))
    && (!needle || member.name.toLowerCase().includes(needle) || member.username.toLowerCase().includes(needle)))
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

    <SectionCard :padded="false" description="Everyone in the organisation, and the role that decides what each may do. Click a role to change it." title="Members">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search members"
          placeholder="Name or username"
        />
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="matched"
        :loading="pending"
        empty-description="No member matches these filters."
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
    </SectionCard>
  </div>
</template>
