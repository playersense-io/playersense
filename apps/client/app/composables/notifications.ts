import type { Schema } from '@playersense/contracts'

/**
 * Notification rules: the project's (…/projects/{project}/notification-rules) and the
 * organization's (…/organizations/{org}/notification-rules), the events either can hear
 * (…/notification-events), and the channels an organization's rule can point at
 * (…/notification-channels). The organization's are billing's to read and change; for anybody
 * else they answer 403, which the page reads as "not yours" rather than as a failure.
 */

function ruleOf(out: Schema<'NotificationRuleOut'>): NotificationRule {
  return {
    id: out.id,
    scope: out.scope as RuleScope,
    name: out.name,
    enabled: out.enabled,
    events: [...out.events],
    environments: [...(out.environments ?? [])] as EnvironmentKind[],
    channels: [...out.channels],
    cooldownMinutes: out.cooldown_minutes,
    mutedUntil: out.muted_until ? Date.parse(out.muted_until) : null,
    lastFiredAt: out.last_fired_at ? Date.parse(out.last_fired_at) : null,
    createdBy: out.created_by ?? ''
  }
}

function bodyOf(draft: NotificationRuleDraft) {
  return {
    name: draft.name.trim(),
    enabled: draft.enabled,
    events: draft.events,
    environments: draft.environments,
    channels: draft.channels,
    cooldown_minutes: draft.cooldownMinutes,
    muted_until: draft.mutedUntil === null ? null : new Date(draft.mutedUntil).toISOString()
  }
}

export const draftOf = (rule: NotificationRule): NotificationRuleDraft => ({
  name: rule.name,
  enabled: rule.enabled,
  events: [...rule.events],
  environments: [...rule.environments],
  channels: [...rule.channels],
  cooldownMinutes: rule.cooldownMinutes,
  mutedUntil: rule.mutedUntil
})

/** Every event a rule can hear, both scopes. */
export function useNotificationKinds() {
  const workspace = useWorkspaceStore()
  const get = useApiGet<Schema<'NotificationKindsOut'>>(() => workspace.orgPath && `${workspace.orgPath}/notification-events`)
  const kinds = computed<NotificationKind[]>(() => (get.data.value?.kinds ?? []).map(kind => ({
    ...kind,
    scope: kind.scope as RuleScope,
    severity: kind.severity as Severity
  })))
  const kindsOf = (scope: RuleScope) => kinds.value.filter(kind => kind.scope === scope)
  return { kinds, kindsOf, loading: computed(() => get.isFetching.value && !get.data.value) }
}

/** One scope's rules, with what changes them. */
export function useNotificationRules(scope: RuleScope) {
  const workspace = useWorkspaceStore()
  const path = computed(() => scope === 'project'
    ? workspace.projectPath && `${workspace.projectPath}/notification-rules`
    : workspace.orgPath && `${workspace.orgPath}/notification-rules`)

  const list = useApiGet<Schema<'NotificationRuleListOut'>>(path)
  const rules = computed(() => (list.data.value?.rules ?? []).map(ruleOf))
  const error = computed(() => list.error.value as ApiError | null)
  /** Somebody who may not see these rules at all: shown as whose they are, not as an error. */
  const forbidden = computed(() => error.value?.status === 403)
  const loading = computed(() => list.isFetching.value && !list.data.value)

  async function reload() {
    await list.execute()
  }

  async function create(draft: NotificationRuleDraft) {
    const out = await apiRequest<Schema<'NotificationRuleOut'>>('POST', path.value!, bodyOf(draft))
    await reload()
    return ruleOf(out)
  }

  async function update(rule: NotificationRule, draft: NotificationRuleDraft) {
    await apiRequest('PUT', `${path.value}/${rule.id}`, bodyOf(draft))
    await reload()
  }

  const setEnabled = (rule: NotificationRule, enabled: boolean) => update(rule, { ...draftOf(rule), enabled })
  const muteUntil = (rule: NotificationRule, until: number | null) => update(rule, { ...draftOf(rule), mutedUntil: until })

  async function remove(rule: NotificationRule) {
    await apiRequest('DELETE', `${path.value}/${rule.id}`)
    await reload()
  }

  /** A sample of the rule's first event, sent now to each of its channels. */
  async function test(rule: NotificationRule) {
    return apiRequest<Schema<'NotificationRuleTestOut'>>('POST', `${path.value}/${rule.id}/test`)
  }

  return { rules, error, forbidden, loading, reload, create, update, setEnabled, muteUntil, remove, test }
}

/** The channels a rule of this scope may notify. */
export function useRuleChannels(scope: RuleScope) {
  const workspace = useWorkspaceStore()
  if (scope === 'project') {
    const { channels, loading } = useChannels()
    return {
      channels: computed<RuleChannel[]>(() => channels.value.map(channel => ({
        id: channel.id, type: channel.type, name: channel.name, enabled: channel.enabled
      }))),
      loading
    }
  }
  const get = useApiGet<Schema<'OrgChannelListOut'>>(() => workspace.orgPath && `${workspace.orgPath}/notification-channels`)
  return {
    channels: computed<RuleChannel[]>(() => (get.data.value?.channels ?? []).map(channel => ({
      id: channel.id, type: channel.type as ChannelType, name: channel.name, enabled: channel.enabled, project: channel.project
    }))),
    loading: computed(() => get.isFetching.value && !get.data.value)
  }
}

export type ActivityRange = '24h' | '7d' | '30d'

export interface ActivityTotals {
  sent: number
  delivered: number
  failed: number
  tests: number
  p50: number
  p95: number
}

export interface ChannelActivityRow {
  channelId: string | null
  name: string
  type: ChannelType | null
  sent: number
  failed: number
  p50: number
  lastAt: number | null
  lastOk: boolean
  spark: number[]
}

const totalsOf = (out: Schema<'ActivityTotalsOut'>): ActivityTotals => ({
  sent: out.sent, delivered: out.delivered, failed: out.failed, tests: out.tests, p50: out.p50_ms, p95: out.p95_ms
})

/**
 * What was sent to the project's channels over a window, beside the window before it
 * (…/channels/activity). Read from the hourly rollup, so it costs the same whatever the volume.
 */
export function useChannelActivity(range: MaybeRefOrGetter<ActivityRange>) {
  const workspace = useWorkspaceStore()
  const get = useApiGet<Schema<'ChannelActivityOut'>>(() => workspace.projectPath
    && withQuery(`${workspace.projectPath}/channels/activity`, { range: toValue(range) }))
  const data = computed(() => {
    const out = get.data.value
    if (!out) return null
    return {
      bucket: out.bucket as 'hour' | 'day',
      buckets: out.buckets.map(b => ({ at: Date.parse(b.at), delivered: b.delivered, failed: b.failed, tests: b.tests })),
      current: totalsOf(out.current),
      previous: totalsOf(out.previous),
      channels: out.channels.map((c): ChannelActivityRow => ({
        channelId: c.channel_id ?? null,
        name: c.name,
        type: (c.type || null) as ChannelType | null,
        sent: c.sent,
        failed: c.failed,
        p50: c.p50_ms,
        lastAt: c.last_at ? Date.parse(c.last_at) : null,
        lastOk: c.last_ok,
        spark: [...c.spark]
      })),
      sources: out.sources.map(s => ({ source: s.source, ruleId: s.rule_id ?? null, sent: s.sent, failed: s.failed })),
      waiting: out.waiting,
      retrying: out.retrying
    }
  })
  return {
    data,
    loading: computed(() => get.isFetching.value && !get.data.value),
    error: computed(() => get.error.value as ApiError | null),
    reload: () => get.execute()
  }
}

export interface RuleStat {
  sent: number
  failed: number
  lastAt: number | null
  spark: number[]
}

/** What each of a scope's rules sent in the last seven days, tests aside. */
export function useRuleStats(scope: RuleScope) {
  const workspace = useWorkspaceStore()
  const get = useApiGet<Schema<'RuleStatsOut'>>(() => scope === 'project'
    ? workspace.projectPath && `${workspace.projectPath}/notification-rules/stats`
    : workspace.orgPath && `${workspace.orgPath}/notification-rules/stats`)
  const byRule = computed(() => new Map((get.data.value?.rules ?? []).map(rule => [rule.rule_id, {
    sent: rule.sent,
    failed: rule.failed,
    lastAt: rule.last_at ? Date.parse(rule.last_at) : null,
    spark: [...rule.spark]
  } satisfies RuleStat])))
  return { byRule, reload: () => get.execute() }
}
