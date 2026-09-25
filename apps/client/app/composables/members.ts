import type { Schema } from '@playersense/contracts'

// The organization's people (…/members), the invitations still out (…/invitations) and the
// roles either can hold (…/roles). A role is named by its key, projects by their slugs: what
// the API takes, and what a person reading the request can check.

export type MemberRecord = Schema<'MemberOut'>
export type InvitationRecord = Schema<'InvitationOut'>
export type RoleRecord = Schema<'RoleOut'>

/** Every project, or the ones named. */
export interface MemberAccess {
  all_projects: boolean
  projects: string[]
}

export function useMembers() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.orgPath)

  const list = useApiGet<Schema<'MemberListOut'>>(() => path.value && `${path.value}/members`)
  const invites = useApiGet<Schema<'InvitationListOut'>>(() => path.value && `${path.value}/invitations`)
  const roleList = useApiGet<Schema<'RoleListOut'>>(() => path.value && `${path.value}/roles`)

  const members = computed<MemberRecord[]>(() => [...(list.data.value?.members ?? [])])
  const invitations = computed<InvitationRecord[]>(() => (invites.data.value?.invitations ?? []).filter(entry => entry.status === 'pending'))
  const roles = computed<RoleRecord[]>(() => [...(roleList.data.value?.roles ?? [])])
  const limit = computed(() => ({ used: list.data.value?.members_used ?? 0, max: list.data.value?.member_limit ?? 0 }))

  const loading = computed(() => list.isFetching.value && !list.data.value)
  const error = computed(() => list.error.value as ApiError | null)

  async function reload() {
    await Promise.all([list.execute(), invites.execute()])
  }

  async function setRole(member: MemberRecord, role: string) {
    await apiRequest('PUT', `${path.value}/members/${member.id}/role`, { role })
    await list.execute()
  }

  async function setAccess(member: MemberRecord, access: MemberAccess) {
    await apiRequest('PUT', `${path.value}/members/${member.id}/access`, access)
    await list.execute()
  }

  async function remove(member: MemberRecord) {
    await apiRequest('DELETE', `${path.value}/members/${member.id}`)
    await list.execute()
  }

  /** Sends an invitation: whether a mail provider took the message, and when it lapses. */
  async function invite(email: string, role: string, access: MemberAccess) {
    const out = await apiRequest<Schema<'InvitationCreatedOut'>>('POST', `${path.value}/invitations`, { email, role, ...access })
    await invites.execute()
    return { delivered: out.delivered, expires_at: out.expires_at }
  }

  async function revokeInvitation(invitation: InvitationRecord) {
    await apiRequest('DELETE', `${path.value}/invitations/${invitation.id}`)
    await invites.execute()
  }

  return { members, invitations, roles, limit, loading, error, reload, setRole, setAccess, remove, invite, revokeInvitation }
}
