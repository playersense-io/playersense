// Notification rules (ADR 0051): which events reach which channels. Three layers, kept apart:
//
//   channels  where: an email list, a Slack channel, the one on call (utils/channels.ts)
//   events    what can happen: the project's (the webhook catalog) and the organization's own
//   rules     the routing: these events, from these environments, to these channels, this often
//
// A project's rule hears that project's events and notifies its channels. The organization's
// rules hear its account's events, a failed top-up or a suspension, and may notify any
// project's channels. The owners, admins and billing members are emailed about those whatever
// the rules say, at most once a day each: a rule adds Slack or PagerDuty, it cannot take the
// email away.

export type RuleScope = 'project' | 'organization'

export interface NotificationKind {
  type: string
  group: string
  title: string
  summary: string
  scope: RuleScope
  environments: boolean
  emitted: boolean
  severity: Severity
  owners: boolean
}

export interface NotificationRule {
  id: string
  scope: RuleScope
  name: string
  enabled: boolean
  events: string[]
  environments: EnvironmentKind[]
  channels: string[]
  cooldownMinutes: number
  /** At most this many messages an hour; 0 is no limit. */
  maxPerHour: number
  mutedUntil: number | null
  lastFiredAt: number | null
  createdBy: string
}

export interface NotificationRuleDraft {
  name: string
  enabled: boolean
  events: string[]
  environments: EnvironmentKind[]
  channels: string[]
  cooldownMinutes: number
  maxPerHour: number
  mutedUntil: number | null
}

/** A channel a rule can point at: the project's own, or, for the organization, any project's. */
export interface RuleChannel {
  id: string
  type: ChannelType
  name: string
  enabled: boolean
  /** The project it is set up in, for the organization's rules. */
  project?: string
}

/** The organization's events, in the picker's terms. Project events use the webhook groups. */
export const BILLING_GROUP: WebhookGroup = {
  key: 'billing' as WebhookGroupKey,
  label: 'Billing and account',
  note: 'A card that did not charge, a balance below zero, events refused and let back in.',
  icon: 'material-symbols:account-balance-wallet-outline'
}

/** A kind in the shape the event picker draws. */
export function pickerEvent(kind: NotificationKind): WebhookEventType {
  return {
    type: kind.type,
    group: kind.group as WebhookGroupKey,
    summary: kind.summary,
    holds: '',
    environments: kind.environments,
    emitted: kind.emitted
  }
}

export const COOLDOWNS: readonly { value: string, label: string, note: string }[] = [
  { value: '0', label: 'Every time', note: 'One notification for every event.' },
  { value: '15', label: '15 minutes', note: 'At most once every 15 minutes for each kind of event.' },
  { value: '60', label: '1 hour', note: 'At most once an hour for each kind of event.' },
  { value: '360', label: '6 hours', note: 'At most every 6 hours for each kind of event.' },
  { value: '1440', label: '1 day', note: 'At most once a day for each kind of event.' }
]

export const HOURLY_CAPS: readonly { value: string, label: string }[] = [
  { value: '5', label: '5' },
  { value: '20', label: '20' },
  { value: '100', label: '100' },
  { value: '0', label: 'No limit' }
]

export const cooldownWords = (minutes: number) =>
  COOLDOWNS.find(entry => Number(entry.value) === minutes)?.label.toLowerCase()
  ?? (minutes < 60 ? `${minutes} minutes` : minutes % 60 === 0 ? `${minutes / 60} hours` : `${minutes} minutes`)

/** "Every event", "Automatic top-up failed", "Config published and 2 more": a rule's events in a line. */
export function ruleEventWords(rule: Pick<NotificationRule, 'events'>, kinds: readonly NotificationKind[]): string {
  if (rule.events.includes(EVERY_EVENT)) return 'Every event, and new ones as they come'
  const titles = rule.events.map(type => kinds.find(kind => kind.type === type)?.title ?? type)
  if (titles.length <= 2) return titles.join(' and ')
  return `${titles.slice(0, 2).join(', ')} and ${titles.length - 2} more`
}

/** Whether a rule is quiet now, and until when. */
export const isMuted = (rule: Pick<NotificationRule, 'mutedUntil'>, now: number) =>
  rule.mutedUntil !== null && rule.mutedUntil > now

export const MUTE_FOR: readonly { label: string, ms: number }[] = [
  { label: '1 hour', ms: 60 * 60 * 1000 },
  { label: '8 hours', ms: 8 * 60 * 60 * 1000 },
  { label: '1 day', ms: 24 * 60 * 60 * 1000 },
  { label: '1 week', ms: 7 * 24 * 60 * 60 * 1000 }
]

/** What a rule's problem is before it can be saved, in a line; empty when there is none. */
export function notificationRuleProblem(draft: NotificationRuleDraft): string {
  if (!draft.name.trim()) return 'Name the rule'
  if (!draft.events.length) return 'Choose at least one event'
  if (!draft.channels.length) return 'Choose at least one channel'
  return ''
}
