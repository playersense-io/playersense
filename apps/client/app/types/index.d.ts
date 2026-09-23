// Role keys match the six seeded roles: apps/backend/internal/authz/roles.go
export type MemberRole = 'owner' | 'admin' | 'liveops_manager' | 'developer' | 'growth_manager' | 'billing'

export interface Member {
  name: string
  username: string
  role: MemberRole
}

// The headline fields of LiveBalanceOut (packages/contracts), in micro-dollars.
export interface Balance {
  available_micros: number
  owed_micros: number
}
