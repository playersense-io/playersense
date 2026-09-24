/**
 * The project's notification channels, and the log of what was sent to them. Kept in this
 * browser; see utils/channels.ts. Sending is simulated (`simulateSend`), and every send, a
 * test or a report's, is logged here so the channels page can show what went where.
 */
let sharedChannels: Ref<NotificationChannel[]> | null = null
let sharedLog: Ref<ChannelDelivery[]> | null = null

function stores() {
  const scope = effectScope(true)
  sharedChannels ??= scope.run(() => useLocalStorage<NotificationChannel[]>('playersense.channels.v1', seedChannels, { deep: false }))!
  sharedLog ??= scope.run(() => useLocalStorage<ChannelDelivery[]>('playersense.channels.log.v1', seedDeliveries, { deep: false }))!
  return { stored: sharedChannels, log: sharedLog }
}

export type ChannelDraft = Pick<NotificationChannel, 'type' | 'name' | 'config'>

export function useChannels() {
  const { stored, log } = stores()

  const channels = computed<NotificationChannel[]>(() => {
    const value = stored.value
    return Array.isArray(value) && value.every(entry => typeof entry?.id === 'string' && entry.type in CHANNEL_TYPES) ? value : seedChannels()
  })

  const deliveries = computed(() => (Array.isArray(log.value) ? [...log.value].sort((a, b) => b.at - a.at) : []))

  const channelById = (id: string) => channels.value.find(entry => entry.id === id) ?? null

  function write(id: string, change: (channel: NotificationChannel) => NotificationChannel) {
    stored.value = channels.value.map(entry => (entry.id === id ? change(entry) : entry))
  }

  /** A new channel, on, with the test sent from its dialog as its last test when there was one. */
  function create(draft: ChannelDraft, lastTest?: ChannelTest): string {
    const id = `ch_${Math.random().toString(36).slice(2, 10)}`
    stored.value = [...channels.value, { ...draft, id, enabled: true, created_at: new Date().toISOString(), created_by: CURRENT_ACTOR, ...(lastTest ? { last_test: lastTest } : {}) }]
    return id
  }

  function update(id: string, draft: ChannelDraft) {
    /* A new destination is not the one the last test reached. */
    write(id, entry => ({ ...entry, ...draft, ...(sameJson(entry.config, draft.config) ? {} : { last_test: undefined }) }))
  }

  function setEnabled(id: string, enabled: boolean) {
    write(id, entry => ({ ...entry, enabled }))
  }

  function remove(id: string) {
    stored.value = channels.value.filter(entry => entry.id !== id)
  }

  /** Sends to one channel now, logs it, and keeps the answer as its last test when it is one. */
  function send(channel: NotificationChannel, title: string, source: string, test: boolean) {
    const answer = simulateSend(channel)
    log.value = [{ id: `nd_${Math.random().toString(36).slice(2, 10)}`, channelId: channel.id, at: Date.parse(answer.at), source, title, ok: answer.ok, detail: answer.detail, test }, ...(log.value ?? [])].slice(0, 200)
    if (test) write(channel.id, entry => ({ ...entry, last_test: { at: answer.at, ok: answer.ok, detail: answer.detail } }))
    return answer
  }

  return { channels, deliveries, channelById, create, update, setEnabled, remove, send }
}
