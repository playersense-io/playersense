// The organization's audit log, in the shape control-api serves
// (GET /organizations/{org}/audit → events): who did what, to what, from where, and when.
// Append-only; this dashboard only reads it. The actions are the backend's own names, each
// given the words a reader wants and the area it belongs to, which is what the log filters on.

export type AuditArea = 'organization' | 'members' | 'keys' | 'growth' | 'billing' | 'privacy'

export const AUDIT_AREAS: Record<AuditArea, { label: string, tone: string }> = {
  organization: { label: 'Organization', tone: 'neutral' },
  members: { label: 'Members', tone: 'chart-1' },
  keys: { label: 'Keys', tone: 'chart-4' },
  growth: { label: 'Growth', tone: 'chart-6' },
  billing: { label: 'Billing', tone: 'success' },
  privacy: { label: 'Privacy', tone: 'error' }
}

export const AUDIT_ACTIONS: Record<string, { label: string, area: AuditArea }> = {
  'organization.create': { label: 'Created the organization', area: 'organization' },
  'project.create': { label: 'Created a project', area: 'organization' },
  'key.create': { label: 'Created a key', area: 'keys' },
  'key.revoke': { label: 'Revoked a key', area: 'keys' },
  'member.invite': { label: 'Invited a member', area: 'members' },
  'member.join': { label: 'Joined', area: 'members' },
  'member.remove': { label: 'Removed a member', area: 'members' },
  'member.role': { label: 'Changed a role', area: 'members' },
  'member.access': { label: 'Changed project access', area: 'members' },
  'invitation.revoke': { label: 'Withdrew an invitation', area: 'members' },
  'segment.write': { label: 'Saved a segment', area: 'growth' },
  'segment.archive': { label: 'Archived a segment', area: 'growth' },
  'flag.write': { label: 'Saved a flag', area: 'growth' },
  'flag.archive': { label: 'Archived a flag', area: 'growth' },
  'liveops_event.write': { label: 'Saved a Live Ops event', area: 'growth' },
  'liveops_event.archive': { label: 'Archived a Live Ops event', area: 'growth' },
  'liveops.publish': { label: 'Published live config', area: 'growth' },
  'credit.grant': { label: 'Granted credit', area: 'billing' },
  'credit.charge': { label: 'Charged credit', area: 'billing' },
  'billing.auto_topup': { label: 'Changed auto top-up', area: 'billing' },
  'billing.plan': { label: 'Changed the plan', area: 'billing' },
  'billing.credit_limit': { label: 'Changed the credit limit', area: 'billing' },
  'billing.contract': { label: 'Changed the contract', area: 'billing' },
  'billing.invoice_paid': { label: 'Paid an invoice', area: 'billing' },
  'billing.credit_note': { label: 'Issued a credit note', area: 'billing' },
  'privacy.erase': { label: 'Asked to erase a player', area: 'privacy' },
  'privacy.export': { label: 'Asked to export a player', area: 'privacy' }
}

export const auditAction = (action: string) => AUDIT_ACTIONS[action] ?? { label: action, area: 'organization' as AuditArea }

export interface AuditEvent {
  id: string
  action: string
  /** Null for the system: a webhook, a scheduled job, an operator. */
  actor: string | null
  actor_email: string | null
  target_type: string
  target_id: string
  metadata: Record<string, unknown>
  ip: string | null
  at: string
}

// --- Sample ----------------------------------------------------------------------------------

const PEOPLE = [
  { name: 'Mira Chen', email: 'mira@acme.games', ip: '85.105.44.12' },
  { name: 'Deniz Kaya', email: 'deniz@acme.games', ip: '88.241.17.203' },
  { name: 'Jonas Weber', email: 'jonas@acme.games', ip: '91.64.120.8' },
  { name: 'Aylin Demir', email: 'aylin@acme.games', ip: '78.180.3.99' }
]

type Template = [action: string, person: number | null, target: string, id: string, metadata: Record<string, unknown>]

/* Newest first, one every few hours to a day or so, over about a month. */
const TEMPLATES: Template[] = [
  ['liveops.publish', 0, 'live_config', 'prd', { environment: 'production', version: 42, changes: 3 }],
  ['flag.write', 2, 'flag', 'new_shop_layout', { environment: 'production', rollout: 25, previous_rollout: 10 }],
  ['billing.invoice_paid', null, 'invoice', 'PS-2026-0012', { total_micros: 49_000_000, source: 'stripe' }],
  ['privacy.erase', 1, 'player', 'p_8f2a61c0', { reference: 'Support #4821' }],
  ['member.role', 0, 'member', 'aylin@acme.games', { from: 'developer', to: 'liveops_manager' }],
  ['key.create', 2, 'api_key', 'key_7Hq2', { class: 'server', environment: 'production', name: 'Game server' }],
  ['key.revoke', 2, 'api_key', 'key_3Lm9', { reason: 'rolled', replaced_by: 'key_7Hq2' }],
  ['segment.write', 3, 'segment', 'whales_30d', { rules: 2, estimated_players: 1840 }],
  ['liveops_event.write', 3, 'liveops_event', 'harvest_festival', { starts_at: '2026-10-03T09:00:00Z', ends_at: '2026-10-10T09:00:00Z' }],
  ['billing.auto_topup', 0, 'organization', 'org_acme', { enabled: true, threshold_micros: 100_000_000, amount_micros: 500_000_000 }],
  ['credit.charge', null, 'wallet', 'lot_3', { amount_micros: 500_000_000, reason: 'auto_topup', card: 'visa 4242' }],
  ['member.invite', 0, 'invitation', 'lee@acme.games', { role: 'developer', projects: ['prj_starfall'] }],
  ['invitation.revoke', 0, 'invitation', 'lee@acme.games', { reason: 'wrong address' }],
  ['member.invite', 0, 'invitation', 'lee.park@acme.games', { role: 'developer', projects: ['prj_starfall'] }],
  ['member.join', null, 'member', 'lee.park@acme.games', { role: 'developer' }],
  ['flag.archive', 2, 'flag', 'old_tutorial', { environments: ['production', 'staging'] }],
  ['member.access', 1, 'member', 'jonas@acme.games', { added: ['prj_moonrise'], removed: [] }],
  ['privacy.export', 1, 'player', 'p_5a0d3b12', { reference: 'Support #4702' }],
  ['segment.archive', 3, 'segment', 'beta_testers', {}],
  ['billing.plan', 0, 'organization', 'org_acme', { from: 'free', to: 'indie', interval: 'month' }],
  ['credit.grant', null, 'wallet', 'lot_4', { amount_micros: 40_000_000, kind: 'promo', reason: 'Outage credit', expires_in_days: 30 }],
  ['liveops_event.archive', 3, 'liveops_event', 'summer_sale', {}],
  ['member.remove', 0, 'member', 'sam@acme.games', { role: 'growth_manager' }],
  ['project.create', 0, 'project', 'prj_pocketrally', { name: 'Pocket Rally', policy_tier: 'standard' }],
  ['flag.write', 2, 'flag', 'new_shop_layout', { environment: 'production', rollout: 10, previous_rollout: 0 }],
  ['liveops.publish', 3, 'live_config', 'stg', { environment: 'staging', version: 41, changes: 5 }]
]

export function seedAudit(): AuditEvent[] {
  const now = Date.now()
  let offset = 0
  return TEMPLATES.map(([action, person, target, id, metadata], index) => {
    offset += (2 + ((index * 7) % 23)) * 3_600_000
    const who = person === null ? null : PEOPLE[person]!
    return {
      id: `aud_${String(TEMPLATES.length - index).padStart(4, '0')}`,
      action,
      actor: who?.name ?? null,
      actor_email: who?.email ?? null,
      target_type: target,
      target_id: id,
      metadata,
      ip: who?.ip ?? null,
      at: new Date(now - offset).toISOString()
    }
  })
}
