// Role keys match the six seeded roles: apps/backend/internal/authz/roles.go
export type MemberRole = 'owner' | 'admin' | 'liveops_manager' | 'developer' | 'growth_manager' | 'billing'

export interface Member {
  name: string
  username: string
  role: MemberRole
}
