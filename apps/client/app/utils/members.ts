// The organisation's roles as the dashboard shows them: their names, what each may do in a
// line, and a colour each so a list of members reads at a glance. The keys and what they allow
// are the role templates' (apps/backend/internal/authz/roles.go); the lines say the same in
// words, most powerful first.
import type { MemberRole } from '~/types'

export const MEMBER_ROLES: Record<MemberRole, { label: string, note: string, tone: string }> = {
  owner: { label: 'Owner', note: 'Everything, including who is a member and what each may do.', tone: 'chart-6' },
  admin: { label: 'Admin', note: 'Everything except deleting the organisation.', tone: 'chart-1' },
  liveops_manager: { label: 'LiveOps Manager', note: 'Schedules and publishes Live Ops. Reads no analytics.', tone: 'chart-4' },
  developer: { label: 'Developer', note: 'Keys, the SDK and drafts of config. Cannot publish.', tone: 'chart-3' },
  growth_manager: { label: 'Growth Manager', note: 'Reads the numbers and what is scheduled. Changes neither.', tone: 'chart-5' },
  billing: { label: 'Billing', note: 'The bill and nothing else.', tone: 'neutral' }
}

export const MEMBER_ROLE_ORDER: readonly MemberRole[] = ['owner', 'admin', 'liveops_manager', 'developer', 'growth_manager', 'billing']
