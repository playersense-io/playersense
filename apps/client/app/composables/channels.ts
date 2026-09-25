import type { Schema } from '@playersense/contracts'

/**
 * The project's notification channels (…/projects/{project}/channels) and the log of what was
 * sent to them (…/channels/deliveries). A test is a real send, made by the API: to a saved
 * channel it is logged and kept as its last test; to a draft in the dialog it is only answered.
 */

export type ChannelDraft = { type: ChannelType, name: string, config: ChannelConfigInput }

function channelOf(out: Schema<'ChannelOut'>): NotificationChannel {
  const test = out.last_test as { at: string, ok: boolean, detail: string } | undefined | null
  return {
    id: out.id,
    type: out.type as ChannelType,
    name: out.name,
    enabled: out.enabled,
    status: out.status as ChannelStatus,
    config: {
      addresses: out.config.addresses ? [...out.config.addresses] : undefined,
      url: out.config.url || undefined,
      severity: (out.config.severity || undefined) as Severity | undefined,
      has_routing_key: out.config.has_routing_key || undefined,
      header_name: out.config.header_name || undefined
    },
    created_at: out.created_at,
    created_by: out.created_by ?? '',
    last_test: test ? { at: test.at, ok: test.ok, detail: test.detail } : undefined
  }
}

function deliveryOf(out: Schema<'ChannelDeliveryOut'>, index: number): ChannelDelivery {
  return {
    id: `${out.at}-${index}`,
    channelId: out.channel_id ?? undefined,
    channelName: out.channel_name,
    at: Date.parse(out.at),
    source: out.source,
    title: out.title,
    ok: out.ok,
    detail: out.detail,
    test: out.test,
    ms: out.duration_ms
  }
}

/** What a channel keeps on an edit that only turns it on or off: its visible settings. */
function keptConfig(channel: NotificationChannel): ChannelConfigInput {
  return { addresses: channel.config.addresses, severity: channel.config.severity, header_name: channel.config.header_name }
}

export function useChannels() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.projectPath && `${workspace.projectPath}/channels`)

  const list = useApiGet<Schema<'ChannelListOut'>>(path)
  const log = useApiGet<Schema<'ChannelDeliveriesOut'>>(() => path.value && withQuery(`${path.value}/deliveries`, { limit: 50 }))

  const channels = computed(() => (list.data.value?.channels ?? []).map(channelOf))
  const deliveries = computed(() => (log.data.value?.deliveries ?? []).map(deliveryOf))
  const loading = computed(() => list.isFetching.value && !list.data.value)
  const error = computed(() => list.error.value as ApiError | null)

  const channelById = (id: string) => channels.value.find(entry => entry.id === id) ?? null

  async function reload() {
    await Promise.all([list.execute(), log.execute()])
  }

  /** A new channel, on. A webhook channel's signing secret comes back this once. */
  async function create(draft: ChannelDraft) {
    const out = await apiRequest<Schema<'ChannelOut'>>('POST', path.value!, draft)
    await list.execute()
    return { id: out.id, signingSecret: out.signing_secret || undefined }
  }

  async function update(channel: NotificationChannel, draft: Omit<ChannelDraft, 'type'>, enabled?: boolean) {
    await apiRequest('PUT', `${path.value}/${channel.id}`, { ...draft, ...(enabled === undefined ? {} : { enabled }) })
    await list.execute()
  }

  async function setEnabled(channel: NotificationChannel, enabled: boolean) {
    await update(channel, { name: channel.name, config: keptConfig(channel) }, enabled)
  }

  async function remove(channel: NotificationChannel) {
    await apiRequest('DELETE', `${path.value}/${channel.id}`)
    await list.execute()
  }

  /** Sends a test to a saved channel: logged, and kept as its last test. */
  async function test(channel: NotificationChannel): Promise<ChannelSendAnswer> {
    const started = Date.now()
    const out = await apiRequest<Schema<'ChannelTestOut'>>('POST', `${path.value}/${channel.id}/test`)
    await reload()
    return { at: out.at, ok: out.ok, detail: out.detail, ms: Date.now() - started }
  }

  /** Sends a test to a destination not saved yet. */
  async function testDraft(type: ChannelType, config: ChannelConfigInput): Promise<ChannelSendAnswer> {
    const started = Date.now()
    const out = await apiRequest<Schema<'ChannelTestOut'>>('POST', `${path.value}/test`, { type, config })
    return { at: out.at, ok: out.ok, detail: out.detail, ms: Date.now() - started }
  }

  return { channels, deliveries, loading, error, reload, channelById, create, update, setEnabled, remove, test, testDraft }
}
