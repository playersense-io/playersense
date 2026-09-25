<script setup lang="ts">
import type { InvitationRecord, MemberRecord } from '~/composables/members'
import type { MemberRole } from '~/types'

// Settings > Members: everyone in the organization, the role that decides what each may do and
// the projects it reaches, and the invitations still out. A role is a tag that opens the
// organization's roles (MemberRoleTag); assigning owner or admin is an owner's alone, and the
// API says so when it refuses (project.md §55.1).
const { members, invitations, roles, limit, loading, error, reload, setRole, setAccess, remove, invite, revokeInvitation } = useMembers()

const problem = ref('')

async function attempt(change: () => Promise<unknown>) {
  problem.value = ''
  try {
    await change()
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  }
}

const owners = computed(() => members.value.filter(member => member.role.key === 'owner').length)
const lockOf = (member: MemberRecord) => (member.role.key === 'owner' && owners.value === 1 ? 'The only owner. Make somebody else an owner first.' : undefined)

const nameOf = (member: MemberRecord) => member.name || member.email
const accessWords = (entry: { all_projects: boolean, projects?: readonly { name: string }[] }) => entry.all_projects
  ? 'Every project'
  : (entry.projects ?? []).map(project => project.name).join(', ') || 'No projects'

const COLUMNS: DataTableColumn<MemberRecord>[] = [
  { key: 'member', title: 'Member', fixed: true, value: member => nameOf(member).toLowerCase() },
  { key: 'role', title: 'Role', width: 200, value: member => MEMBER_ROLE_ORDER.indexOf(member.role.key as MemberRole) },
  { key: 'access', title: 'Projects', value: member => (member.all_projects ? '' : accessWords(member)) },
  { key: 'joined', title: 'Joined', width: 140, value: member => Date.parse(member.joined_at) }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'role', order: 'asc' }])

// --- Narrowing: a search, and roles under Filter, as every list here does ---------------------

const query = ref('')
const filters = ref<Record<string, string[]>>({ role: [] })

const FILTER_GROUPS = computed<FilterGroup[]>(() => [
  { key: 'role', label: 'Role', options: sortRoles(roles.value).map(role => ({ value: role.key, label: roleLook(role).label })) }
])

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.role ?? []).map(key => ({ key: `role:${key}`, label: `Role: ${roleLook(roles.value.find(role => role.key === key) ?? { key, name: key }).label}` })),
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
  const wanted = filters.value.role ?? []
  return members.value.filter(member => (!wanted.length || wanted.includes(member.role.key))
    && (!needle || nameOf(member).toLowerCase().includes(needle) || member.email.toLowerCase().includes(needle)))
})

function initials(text: string) {
  return text.split(/[\s@.]+/).filter(Boolean).map(part => part[0]).join('').slice(0, 2).toUpperCase()
}

// --- Dialogs ----------------------------------------------------------------------------------

const inviting = ref(false)
const editing = ref<MemberRecord | null>(null)
const editOpen = computed({
  get: () => editing.value !== null,
  set: (value) => {
    if (!value) editing.value = null
  }
})

const removing = ref<MemberRecord | null>(null)
const removeOpen = computed({
  get: () => removing.value !== null,
  set: (value) => {
    if (!value) removing.value = null
  }
})

const revoking = ref<InvitationRecord | null>(null)
const revokeOpen = computed({
  get: () => revoking.value !== null,
  set: (value) => {
    if (!value) revoking.value = null
  }
})

const workspace = useWorkspaceStore()

async function removeMember(member: MemberRecord) {
  await attempt(async () => {
    await remove(member)
    // Leaving: this organization is no longer the person's to show.
    if (member.you) {
      await workspace.load(true)
      await navigateTo('/')
    }
  })
}

const full = computed(() => limit.value.max > 0 && limit.value.used >= limit.value.max)
const seatWords = computed(() => (limit.value.max
  ? `${limit.value.used} of ${limit.value.max} seats used${full.value ? ': the plan allows no more until one is freed or the plan grows' : ''}.`
  : ''))
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader description="Who can reach this organization, what each of them may do, and in which projects." title="Members">
      <template #actions>
        <v-btn
          :disabled="full"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:person-add-outline"
          size="small"
          variant="flat"
          @click="inviting = true"
        >
          Invite people
        </v-btn>
      </template>
    </PageHeader>

    <p v-if="problem" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      {{ problem }}
    </p>

    <LoadState
      :error="error"
      :loading="loading"
      what="the members"
      @retry="reload"
    >
      <SectionCard :padded="false" title="Members">
        <template #description>
          Everyone in the organization, and the role that decides what each may do. Click a role to change it.
          {{ seatWords }}
        </template>

        <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
          <SearchField
            v-model="query"
            class="w-56"
            label="Search members"
            placeholder="Name or email"
          />
          <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
          <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
        </TableToolbar>

        <DataTable
          v-model:column-state="columns"
          v-model:sort-by="sortBy"
          :columns="COLUMNS"
          :items="matched"
          empty-description="No member matches these filters."
          empty-title="Nobody matches."
          item-value="id"
          label="Members"
        >
          <template #cell.member="{ item }">
            <span class="flex min-w-0 items-center gap-3">
              <span
                :class="item.role.key === 'owner' ? 'border-primary/40 bg-primary/8 text-primary' : 'border-line bg-ink/4 text-ink/70'"
                aria-hidden="true"
                class="flex size-7 flex-none items-center justify-center rounded-full border text-[10px] font-medium"
              >
                {{ initials(nameOf(item)) }}
              </span>
              <span class="flex min-w-0 flex-col">
                <span class="flex items-center gap-1.5">
                  <span class="truncate font-medium text-ink">{{ nameOf(item) }}</span>
                  <span v-if="item.you" class="rounded-full bg-ink/6 px-1.5 text-[10px] leading-4 text-ink/60">You</span>
                </span>
                <span v-if="item.name" class="truncate font-mono text-[11px] text-ink/55">{{ item.email }}</span>
              </span>
            </span>
          </template>

          <template #cell.role="{ item }">
            <MemberRoleTag
              :label="`Role of ${nameOf(item)}`"
              :locked="lockOf(item)"
              :model-value="item.role.key"
              :roles="roles"
              @update:model-value="attempt(() => setRole(item, $event))"
            />
          </template>

          <template #cell.access="{ item }">
            <span :class="item.all_projects ? 'text-ink/55' : 'text-ink/80'" class="line-clamp-1 text-xs">{{ accessWords(item) }}</span>
          </template>

          <template #cell.joined="{ item }">
            <span class="text-xs text-ink/65">{{ formatDate(item.joined_at) }}</span>
          </template>

          <template #actions="{ item }">
            <v-btn
              :aria-label="`Actions for ${nameOf(item)}`"
              icon
              size="small"
              variant="text"
            >
              <Icon :aria-hidden="true" class="text-lg" name="material-symbols:more-vert" />
              <v-menu activator="parent" location="bottom end" :offset="4">
                <v-list min-width="200" nav>
                  <v-list-item prepend-icon="material-symbols:manage-accounts-outline" title="Edit access" @click="editing = item" />
                  <v-list-item
                    :disabled="Boolean(lockOf(item))"
                    base-color="error"
                    prepend-icon="material-symbols:person-remove-outline"
                    :title="item.you ? 'Leave organization' : 'Remove member'"
                    @click="removing = item"
                  />
                </v-list>
              </v-menu>
            </v-btn>
          </template>
        </DataTable>
      </SectionCard>

      <SectionCard
        v-if="invitations.length"
        :padded="false"
        description="Sent and not yet accepted. Each is accepted by signing in with the address it was sent to."
        title="Invitations"
      >
        <ul class="m-0 flex list-none flex-col divide-y divide-line p-0">
          <li v-for="invitation in invitations" :key="invitation.id" class="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3">
            <span class="flex min-w-0 flex-1 basis-56 flex-col">
              <span class="truncate text-[13px] text-ink">{{ invitation.email }}</span>
              <span class="truncate text-[11px] text-ink/50">
                {{ roleLook(invitation.role).label }} · {{ accessWords(invitation) }}<template v-if="invitation.invited_by"> · from {{ invitation.invited_by }}</template>
              </span>
            </span>
            <span class="text-[11px] text-ink/50">Lapses {{ formatDate(invitation.expires_at) }}</span>
            <v-btn
              class="text-xs"
              color="error"
              size="small"
              variant="text"
              @click="revoking = invitation"
            >
              Revoke
            </v-btn>
          </li>
        </ul>
      </SectionCard>
    </LoadState>

    <InviteDialog v-model="inviting" :invite="invite" :roles="roles" />

    <MemberAccessDialog
      v-if="editing"
      v-model="editOpen"
      :member="editing"
      :save="setAccess"
    />

    <ConfirmDialog
      v-if="removing"
      v-model="removeOpen"
      :action="removing.you ? 'Leave' : 'Remove member'"
      :description="removing.you
        ? 'You lose access to every project in this organization at once. Somebody here has to invite you to come back.'
        : `${nameOf(removing)} loses access to every project in this organization at once. What they made stays.`"
      :title="removing.you ? 'Leave this organization?' : `Remove ${nameOf(removing)}?`"
      tone="error"
      @confirm="removeMember(removing)"
    />

    <ConfirmDialog
      v-if="revoking"
      v-model="revokeOpen"
      :description="`${revoking.email} can no longer join with it. You can invite them again later.`"
      :title="`Revoke the invitation to ${revoking.email}?`"
      action="Revoke"
      tone="error"
      @confirm="attempt(() => revokeInvitation(revoking!))"
    />
  </div>
</template>
