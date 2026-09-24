// A project as Settings shows it, in the shape control-api serves (ProjectOut): its name and
// slug, the environments under it, the collection policy tier fixed at creation (§98) and the
// raw retention tier (§24). Plus the privacy requests made against its players (§61), which
// Data retention lists. Sample data until the dashboard reads the project routes.

export type PolicyTier = 'standard' | 'child_directed' | 'kids_strict'

export const POLICY_TIERS: Record<PolicyTier, { label: string, note: string, cap: number }> = {
  standard: {
    label: 'Standard',
    note: 'A persistent install ID, attribution, and retention up to the plan\'s maximum.',
    cap: 365
  },
  child_directed: {
    label: 'Child-directed',
    note: 'For COPPA and Google Play Families. A persistent ID for analytics, no acquisition data, and retention capped.',
    cap: 90
  },
  kids_strict: {
    label: 'Kids strict',
    note: 'For Apple\'s Kids Category. No persistent ID at all, so retention across days cannot be computed.',
    cap: 30
  }
}

export const RETENTION_TIERS = [7, 30, 90, 180, 365] as const
export type RetentionDays = typeof RETENTION_TIERS[number]

export type EnvironmentKind = 'development' | 'staging' | 'production'

export const ENVIRONMENT_KINDS: Record<EnvironmentKind, { label: string, tone: string }> = {
  development: { label: 'Development', tone: 'chart-4' },
  staging: { label: 'Staging', tone: 'chart-3' },
  production: { label: 'Production', tone: 'success' }
}

export interface ProjectEnvironment {
  id: string
  kind: EnvironmentKind
  created_at: string
}

/** A store or platform identifier the project's events have arrived from. */
export interface ProjectApp {
  platform: string
  identifier: string
  first_seen: string
}

export interface ProjectSettings {
  id: string
  name: string
  slug: string
  policy_tier: PolicyTier
  retention_days: RetentionDays
  /** When the tier last changed: a lowered tier purges within a day of it. */
  retention_changed_at?: string
  environments: ProjectEnvironment[]
  apps: ProjectApp[]
  created_at: string
  /** Deletion asked for: the project goes thirty days after, and can be kept until then. */
  deletion_at?: string
}

// --- Privacy requests ------------------------------------------------------------------------

export type PrivacyKind = 'erase' | 'export'
export type PrivacyStatus = 'queued' | 'running' | 'completed' | 'failed'

export const PRIVACY_KINDS: Record<PrivacyKind, { label: string, tone: string }> = {
  erase: { label: 'Erase', tone: 'error' },
  export: { label: 'Export', tone: 'chart-1' }
}

export const PRIVACY_STATUS: Record<PrivacyStatus, { label: string, tone: BadgeTone }> = {
  queued: { label: 'Queued', tone: 'neutral' },
  running: { label: 'Running', tone: 'warning' },
  completed: { label: 'Completed', tone: 'success' },
  failed: { label: 'Failed', tone: 'error' }
}

export interface PrivacyRequest {
  id: string
  kind: PrivacyKind
  player_id: string
  status: PrivacyStatus
  requested_by: string
  requested_at: string
  completed_at?: string
  /** Why it was asked for, in the requester's words: a ticket number, "player email". */
  reference?: string
}

// --- Sample ----------------------------------------------------------------------------------

const DAY = 86_400_000

export function seedProjectSettings(project: { id: string, name: string, slug: string }): ProjectSettings {
  const now = Date.now()
  const ago = (days: number) => new Date(now - days * DAY).toISOString()
  const key = project.id.replace(/^prj_/, '')
  const kids = project.id === 'prj_moonrise'
  return {
    id: project.id,
    name: project.name,
    slug: project.slug,
    policy_tier: kids ? 'child_directed' : 'standard',
    retention_days: kids ? 30 : 90,
    environments: [
      { id: `env_${key}_dev`, kind: 'development', created_at: ago(412) },
      { id: `env_${key}_stg`, kind: 'staging', created_at: ago(380) },
      { id: `env_${key}_prd`, kind: 'production', created_at: ago(412) }
    ],
    apps: [
      { platform: 'iOS', identifier: `com.acme.${key}`, first_seen: ago(362) },
      { platform: 'Android', identifier: `com.acme.${key}`, first_seen: ago(360) },
      ...(kids ? [] : [{ platform: 'Steam', identifier: '2451230', first_seen: ago(201) }])
    ],
    created_at: ago(412)
  }
}

export function seedPrivacyRequests(): PrivacyRequest[] {
  const now = Date.now()
  const ago = (hours: number) => new Date(now - hours * 3_600_000).toISOString()
  return [
    { id: 'prv_7', kind: 'erase', player_id: 'p_8f2a61c0', status: 'running', requested_by: 'Deniz Kaya', requested_at: ago(5), reference: 'Support #4821' },
    { id: 'prv_6', kind: 'export', player_id: 'p_19bd07e4', status: 'queued', requested_by: 'Deniz Kaya', requested_at: ago(2), reference: 'Support #4830' },
    { id: 'prv_5', kind: 'erase', player_id: 'p_c41e9a77', status: 'completed', requested_by: 'Mira Chen', requested_at: ago(96), completed_at: ago(70), reference: 'Deletion form' },
    { id: 'prv_4', kind: 'export', player_id: 'p_5a0d3b12', status: 'completed', requested_by: 'Mira Chen', requested_at: ago(170), completed_at: ago(169), reference: 'Support #4702' },
    { id: 'prv_3', kind: 'erase', player_id: 'p_e27c4f90', status: 'failed', requested_by: 'Deniz Kaya', requested_at: ago(260), reference: 'Support #4655' },
    { id: 'prv_2', kind: 'erase', player_id: 'p_e27c4f90', status: 'completed', requested_by: 'Deniz Kaya', requested_at: ago(240), completed_at: ago(200), reference: 'Retry of #4655' },
    { id: 'prv_1', kind: 'erase', player_id: 'p_03b8e5d1', status: 'completed', requested_by: 'Mira Chen', requested_at: ago(620), completed_at: ago(590), reference: 'Deletion form' }
  ]
}
