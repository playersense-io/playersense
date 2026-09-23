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

// One day of the analytics planner's metric rows (MetricRow in packages/contracts),
// summed over the organization's projects.
export interface OverviewDay {
  day: string
  active_players: number
  new_players: number
  returning_players: number
  sessions: number
  playtime_ms: number
  revenue_micros: number
  purchases: number
  payers: number
}

export interface OverviewProject {
  id: string
  name: string
  /** Daily average over the period. */
  active_players: number
  new_players: number
  revenue_micros: number
  previous_revenue_micros: number
  events: number
}

export interface OverviewBreakdown {
  value: string
  /** Daily average over the period. */
  active_players: number
}

export interface OverviewUsage {
  period_start: string
  /** The busiest day of the month, not a sum. */
  tracked_players: number
  events: number
  retained_bytes: number
  daily_tracked_players: number[]
  daily_events: number[]
  included: { tracked_players: number, events: number, retained_bytes: number }
  charged_micros: number
  pending_micros: number
  available_micros: number
}

export interface Overview {
  days: number
  current: OverviewDay[]
  previous: OverviewDay[]
  projects: OverviewProject[]
  by_platform: OverviewBreakdown[]
  by_country: OverviewBreakdown[]
  usage: OverviewUsage
}
