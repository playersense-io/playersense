// A project as Settings shows it (GET …/projects/{project}): its name and slug, the environments
// under it, the collection policy tier fixed at creation (§98) and the raw retention tier (§24).
// Plus the privacy requests made against its players (§61), which Data retention lists.

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
}

/** A platform and SDK the project's events have arrived from (GET …/apps). */
export interface ProjectApp {
  platform: string
  sdk: string
  sdk_version: string
  /** The newest app version seen from it. */
  app_version: string
  events: number
  first_seen: string
  last_seen: string
}

export interface ProjectSettings {
  id: string
  name: string
  slug: string
  policy_tier: PolicyTier
  retention_days: RetentionDays
  /** The longest the tier may be: the lower of the plan's and the collection policy's. */
  retention_cap: number
  /** When the tier last changed: a lowered tier purges within a day of it. */
  retention_changed_at?: string
  /** A lowered tier deletes what is too old each day until then. */
  retention_purge_until?: string
  environments: ProjectEnvironment[]
  apps: ProjectApp[]
  created_at: string
  /** Deletion asked for: the project goes thirty days after, and can be kept until then. */
  deletion_at?: string
  deletion_requested_by?: string
}

// --- Privacy requests ------------------------------------------------------------------------

export type PrivacyKind = 'erase' | 'export'
export type PrivacyStatus = 'received' | 'tombstoned' | 'purging' | 'running' | 'completed' | 'failed' | 'expired'

export const PRIVACY_KINDS: Record<PrivacyKind, { label: string, tone: string }> = {
  erase: { label: 'Erase', tone: 'error' },
  export: { label: 'Export', tone: 'chart-1' }
}

export const PRIVACY_STATUS: Record<PrivacyStatus, { label: string, tone: BadgeTone }> = {
  received: { label: 'Queued', tone: 'neutral' },
  tombstoned: { label: 'Blocked', tone: 'warning' },
  purging: { label: 'Erasing', tone: 'warning' },
  running: { label: 'Running', tone: 'warning' },
  completed: { label: 'Completed', tone: 'success' },
  failed: { label: 'Failed', tone: 'error' },
  expired: { label: 'Expired', tone: 'neutral' }
}

export interface PrivacyRequest {
  id: string
  kind: PrivacyKind
  player_id: string
  status: PrivacyStatus
  requested_by: string
  requested_at: string
  completed_at?: string
  /** An export's bundle is kept until then. */
  expires_at?: string
  failure_reason?: string
  /** Where a finished export's bundle downloads from. */
  download_path?: string
  /** Why it was asked for, in the requester's words: a ticket number, "player email". */
  reference?: string
}
