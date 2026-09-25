// The organization's audit log, in the shape control-api serves
// (GET /organizations/{org}/audit → events): who did what, to what, from where, and when.
// Append-only; this dashboard only reads it. The actions are the backend's own names, each
// given the words a reader wants and the area it belongs to, which is what the log filters on.

export type AuditArea = 'organization' | 'members' | 'keys' | 'growth' | 'analytics' | 'integrations' | 'billing' | 'privacy'

export const AUDIT_AREAS: Record<AuditArea, { label: string, tone: string }> = {
  organization: { label: 'Organization', tone: 'neutral' },
  members: { label: 'Members', tone: 'chart-1' },
  keys: { label: 'Keys', tone: 'chart-4' },
  growth: { label: 'Growth', tone: 'chart-6' },
  analytics: { label: 'Analytics', tone: 'chart-2' },
  integrations: { label: 'Integrations', tone: 'chart-3' },
  billing: { label: 'Billing', tone: 'success' },
  privacy: { label: 'Privacy', tone: 'error' }
}

export const AUDIT_ACTIONS: Record<string, { label: string, area: AuditArea }> = {
  'organization.create': { label: 'Created the organization', area: 'organization' },
  'org.revenue_settings': { label: 'Changed revenue settings', area: 'organization' },
  'project.create': { label: 'Created a project', area: 'organization' },
  'project.update': { label: 'Renamed a project', area: 'organization' },
  'project.retention': { label: 'Changed data retention', area: 'organization' },
  'project.delete': { label: 'Scheduled a project for deletion', area: 'organization' },
  'project.keep': { label: 'Kept a project from deletion', area: 'organization' },
  'key.create': { label: 'Created a key', area: 'keys' },
  'key.revoke': { label: 'Revoked a key', area: 'keys' },
  'key.roll': { label: 'Rolled a key', area: 'keys' },
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
  'liveops.rollback': { label: 'Rolled back live config', area: 'growth' },
  'liveops.restore_draft': { label: 'Restored a draft from a version', area: 'growth' },
  'experiment.write': { label: 'Saved an experiment', area: 'growth' },
  'experiment.delete': { label: 'Deleted an experiment', area: 'growth' },
  'experiment.start': { label: 'Started an experiment', area: 'growth' },
  'experiment.traffic': { label: 'Changed experiment traffic', area: 'growth' },
  'experiment.stop': { label: 'Stopped an experiment', area: 'growth' },
  'experiment.conclude': { label: 'Concluded an experiment', area: 'growth' },
  'experiment.ship': { label: 'Shipped an experiment variant', area: 'growth' },
  'funnel.write': { label: 'Saved a funnel', area: 'analytics' },
  'funnel.delete': { label: 'Deleted a funnel', area: 'analytics' },
  'event.define': { label: 'Described an event', area: 'analytics' },
  'report.write': { label: 'Saved a report', area: 'analytics' },
  'report.delete': { label: 'Deleted a report', area: 'analytics' },
  'channel.write': { label: 'Saved a notification channel', area: 'integrations' },
  'channel.delete': { label: 'Deleted a notification channel', area: 'integrations' },
  'store.connect': { label: 'Connected a store', area: 'integrations' },
  'store.disconnect': { label: 'Disconnected a store', area: 'integrations' },
  'store.rotate_url': { label: 'Rotated a store notification URL', area: 'integrations' },
  'webhook.write': { label: 'Saved a webhook', area: 'integrations' },
  'webhook.delete': { label: 'Deleted a webhook', area: 'integrations' },
  'webhook.secret_roll': { label: 'Rolled a webhook secret', area: 'integrations' },
  'webhook.secret_read': { label: 'Revealed a webhook secret', area: 'integrations' },
  'credit.grant': { label: 'Granted credit', area: 'billing' },
  'credit.charge': { label: 'Charged credit', area: 'billing' },
  'billing.auto_topup': { label: 'Changed auto top-up', area: 'billing' },
  'billing.plan': { label: 'Changed the plan', area: 'billing' },
  'billing.credit_limit': { label: 'Changed the credit limit', area: 'billing' },
  'billing.entitlements': { label: 'Changed entitlements', area: 'billing' },
  'billing.contract': { label: 'Changed the contract', area: 'billing' },
  'billing.invoice_paid': { label: 'Paid an invoice', area: 'billing' },
  'billing.credit_note': { label: 'Issued a credit note', area: 'billing' },
  'billing.card_default': { label: 'Changed the default card', area: 'billing' },
  'billing.card_remove': { label: 'Removed a card', area: 'billing' },
  'privacy.erase': { label: 'Asked to erase a player', area: 'privacy' },
  'privacy.export': { label: 'Asked to export a player', area: 'privacy' }
}

export const auditAction = (action: string) => AUDIT_ACTIONS[action] ?? { label: action, area: 'organization' as AuditArea }

export interface AuditEvent {
  id: string
  action: string
  /** Who did it, rendered: a person's name, a key's label, or PlayerSense staff. */
  actor: string
  /** Present only when a signed-in person did it. */
  actor_email?: string
  target_type: string
  target_id: string
  metadata: Record<string, unknown>
  ip?: string
  at: string
}
