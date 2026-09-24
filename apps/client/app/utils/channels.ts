// Notification channels: the places the project can tell people something. A channel is one
// destination of one type (an email list, a Slack channel, a Discord webhook, an on-call
// service), set up once here and chosen by name wherever something notifies: a report's
// Notify step today, alerts next. The way Google Cloud's monitoring keeps its channels apart
// from the policies that use them, so a Slack channel renamed is changed in one place.
//
// Sample data kept in this browser, until channels are a table and sending is the worker's.

export type ChannelType = 'email' | 'slack' | 'discord' | 'teams' | 'telegram' | 'pagerduty' | 'webhook'

export interface ChannelTypeInfo {
  label: string
  /** What it is for, in a line. */
  note: string
  /** A brand's own mark where there is one; a glyph where there is not. */
  icon: string
  /** The brand marks are in colour; the glyphs take the ink. */
  brand: boolean
}

export const CHANNEL_TYPES: Record<ChannelType, ChannelTypeInfo> = {
  email: { label: 'Email', note: 'To one address or a list. The report in the body, the rows attached.', icon: 'material-symbols:mail-outline', brand: false },
  slack: { label: 'Slack', note: 'Posted to a channel in your workspace by the PlayerSense app.', icon: 'selfhst:slack', brand: true },
  discord: { label: 'Discord', note: 'Posted to a channel through a webhook you make in its settings.', icon: 'selfhst:discord', brand: true },
  teams: { label: 'Microsoft Teams', note: 'Posted to a channel through a Workflows webhook.', icon: 'selfhst:microsoft-teams', brand: true },
  telegram: { label: 'Telegram', note: 'Sent to a group or a chat by the PlayerSense bot.', icon: 'selfhst:telegram', brand: true },
  pagerduty: { label: 'PagerDuty', note: 'Opens an incident on a service, so the one on call is paged.', icon: 'material-symbols:notifications-active-outline', brand: false },
  webhook: { label: 'Webhook', note: 'POSTed as JSON to a URL of yours, signed like every PlayerSense webhook.', icon: 'material-symbols:webhook', brand: false }
}

export const CHANNEL_TYPE_ORDER: readonly ChannelType[] = ['email', 'slack', 'discord', 'teams', 'telegram', 'pagerduty', 'webhook']

export type Severity = 'critical' | 'error' | 'warning' | 'info'

export interface ChannelConfig {
  addresses?: string[]
  workspace?: string
  channel?: string
  url?: string
  chat?: string
  routingKey?: string
  severity?: Severity
  /** A header sent with a webhook, for a gateway in front of it. */
  header?: string
}

export interface ChannelTest {
  at: string
  ok: boolean
  detail: string
}

export interface NotificationChannel {
  id: string
  type: ChannelType
  name: string
  enabled: boolean
  config: ChannelConfig
  created_at: string
  created_by: string
  last_test?: ChannelTest
}

export type ChannelStatus = 'working' | 'failing' | 'untested' | 'off'

export const CHANNEL_STATUS: Record<ChannelStatus, { label: string, tone: BadgeTone }> = {
  working: { label: 'Working', tone: 'success' },
  failing: { label: 'Failing', tone: 'error' },
  untested: { label: 'Not tested', tone: 'neutral' },
  off: { label: 'Off', tone: 'neutral' }
}

export function statusOf(channel: NotificationChannel): ChannelStatus {
  if (!channel.enabled) return 'off'
  if (!channel.last_test) return 'untested'
  return channel.last_test.ok ? 'working' : 'failing'
}

/** Where a channel sends, in a line: "#live-ops in Starfall Games", "3 addresses". */
export function targetWords(channel: Pick<NotificationChannel, 'type' | 'config'>): string {
  const config = channel.config
  switch (channel.type) {
    case 'email': return (config.addresses ?? []).length === 1 ? config.addresses![0]! : `${(config.addresses ?? []).length} addresses`
    case 'slack': return config.channel ? `${config.channel} in ${config.workspace ?? 'Slack'}` : 'No channel yet'
    case 'telegram': return config.chat ?? 'No chat yet'
    case 'pagerduty': return `${SEVERITIES[config.severity ?? 'error'].label} incidents, key …${(config.routingKey ?? '').slice(-4)}`
    default: return hostPath(config.url ?? '')
  }
}

function hostPath(url: string): string {
  try {
    const parsed = new URL(url)
    const path = parsed.pathname.split('/').filter(Boolean)
    return `${parsed.host}/${path.slice(0, 2).join('/')}${path.length > 2 ? '/…' : ''}`
  } catch {
    return url || 'No URL yet'
  }
}

export const SEVERITIES: Record<Severity, { label: string }> = {
  critical: { label: 'Critical' },
  error: { label: 'Error' },
  warning: { label: 'Warning' },
  info: { label: 'Info' }
}

/** Why a channel cannot be saved, or null. */
export function channelProblem(type: ChannelType, name: string, config: ChannelConfig): string | null {
  if (name.trim().length < 2) return 'A name of two characters or more'
  switch (type) {
    case 'email': {
      const addresses = config.addresses ?? []
      if (!addresses.length) return 'At least one address'
      const wrong = addresses.find(address => !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(address))
      return wrong ? `"${wrong}" is not an email address` : null
    }
    case 'slack': return !config.workspace ? 'Connect a Slack workspace' : !config.channel ? 'Choose a channel' : null
    case 'telegram': return !config.chat ? 'Choose the chat the bot is in' : null
    case 'pagerduty': return /^[a-z0-9]{32}$/i.test(config.routingKey ?? '') ? null : 'An integration key: 32 letters and digits, from the service\'s Events API v2 integration'
    case 'discord': return /^https:\/\/(discord\.com|discordapp\.com)\/api\/webhooks\/\d+\/[\w-]+$/.test(config.url ?? '') ? null : 'A Discord webhook URL: https://discord.com/api/webhooks/…'
    case 'teams': return /^https:\/\/[\w.-]+\.(logic\.azure\.com|office\.com|webhook\.office\.com)\//.test(config.url ?? '') ? null : 'A Teams Workflows URL: https://….logic.azure.com/…'
    case 'webhook': return urlProblem(config.url ?? '')
  }
}

/** The Slack channels and Telegram chats the connected app can see: sample. */
export const SLACK_CHANNELS = ['#live-ops', '#alerts-revenue', '#finance', '#general', '#release', '#community']
export const TELEGRAM_CHATS = ['Starfall ops', 'Starfall leadership', 'QA night shift']

// --- Sending ----------------------------------------------------------------------------------

/**
 * What one channel answers when something is sent to it now: sample, and the same for the
 * same channel. A Discord webhook that was deleted answers 404, as a real one does, so a test
 * shows what a failure looks like and the page says what to do about it.
 */
export function simulateSend(channel: NotificationChannel): ChannelTest & { ms: number } {
  const at = new Date().toISOString()
  const ms = 120 + Math.round(Math.random() * 380)
  if (!channel.enabled) return { at, ok: false, detail: 'The channel is off. Turn it on to send to it.', ms: 0 }
  if (channel.config.url?.includes('/1188')) return { at, ok: false, detail: '404 Unknown Webhook: the webhook was deleted in Discord. Make a new one and paste its URL here.', ms }
  const detail = {
    email: `Accepted for ${(channel.config.addresses ?? []).length} ${(channel.config.addresses ?? []).length === 1 ? 'address' : 'addresses'}`,
    slack: `Posted to ${channel.config.channel}`,
    discord: '204 No Content: posted',
    teams: '202 Accepted: posted',
    telegram: `Sent to ${channel.config.chat}`,
    pagerduty: '202 Accepted: incident opened, deduplicated by report',
    webhook: '200 OK'
  }[channel.type]
  return { at, ok: true, detail, ms }
}

// --- Messages ---------------------------------------------------------------------------------

export interface NotificationMessage {
  title: string
  body: string
  /** The rows included in the message itself, when any are. */
  table?: { columns: ReportColumn[], rows: ReportRow[] }
  /** A file attached, where the channel takes files. */
  attachment?: { name: string, rows: number }
  severity: Severity
  /** Where "Open the report" goes. */
  link: string
}

/** The variables a message can say, with what each stands for. */
export const MESSAGE_VARIABLES = [
  { key: 'report', note: 'The report\'s name' },
  { key: 'rows', note: 'How many rows came out' },
  { key: 'date', note: 'The day it ran' },
  { key: 'first.<column>', note: 'A column of the first row' },
  { key: 'sum.<column>', note: 'A column added up' }
] as const

/** A message's text with its variables filled in from the rows it is about. */
export function fillMessage(text: string, context: { report: string, columns: readonly ReportColumn[], rows: readonly ReportRow[], at: number }): string {
  return text.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (whole, name: string) => {
    if (name === 'report') return context.report
    if (name === 'rows') return context.rows.length.toLocaleString('en')
    if (name === 'date') return new Date(context.at).toLocaleDateString('en', { weekday: 'short', month: 'short', day: 'numeric' })
    const [scope, key] = name.split('.') as [string, string | undefined]
    const column = context.columns.find(entry => entry.key === key)
    if (!column) return whole
    if (scope === 'first') return writeCell(context.rows[0]?.[column.key] ?? null, column.type)
    if (scope === 'sum') return writeCell(context.rows.reduce((sum, row) => sum + (Number(row[column.key]) || 0), 0), column.type)
    return whole
  })
}

// --- Sample channels --------------------------------------------------------------------------

export function seedChannels(): NotificationChannel[] {
  const now = Date.now()
  const ago = (days: number) => new Date(now - days * 86_400_000).toISOString()
  const tested = (days: number, ok = true, detail = 'Posted'): ChannelTest => ({ at: ago(days), ok, detail })
  return [
    { id: 'ch_email_finance', type: 'email', name: 'Finance team', enabled: true, config: { addresses: ['finance@starfall.games', 'cfo@starfall.games'] }, created_at: ago(120), created_by: 'lena@starfall.games', last_test: tested(40, true, 'Accepted for 2 addresses') },
    { id: 'ch_slack_liveops', type: 'slack', name: 'Live ops channel', enabled: true, config: { workspace: 'Starfall Games', channel: '#live-ops' }, created_at: ago(140), created_by: 'lena@starfall.games', last_test: tested(9, true, 'Posted to #live-ops') },
    { id: 'ch_slack_alerts', type: 'slack', name: 'Revenue alerts', enabled: true, config: { workspace: 'Starfall Games', channel: '#alerts-revenue' }, created_at: ago(60), created_by: 'deniz@starfall.games', last_test: tested(2, true, 'Posted to #alerts-revenue') },
    { id: 'ch_discord_mods', type: 'discord', name: 'Community mods', enabled: true, config: { url: 'https://discord.com/api/webhooks/1188204731/xG2kP9-starfall-mods' }, created_at: ago(90), created_by: 'ada@starfall.games', last_test: tested(1, false, '404 Unknown Webhook: the webhook was deleted in Discord. Make a new one and paste its URL here.') },
    { id: 'ch_teams_leadership', type: 'teams', name: 'Leadership', enabled: true, config: { url: 'https://prod-42.westeurope.logic.azure.com/workflows/7f2c9e/triggers/manual' }, created_at: ago(30), created_by: 'lena@starfall.games' },
    { id: 'ch_pagerduty_oncall', type: 'pagerduty', name: 'Backend on call', enabled: true, config: { routingKey: 'r3f9c2a7b1d84e6f9a0c5b2e7d1f4a8c', severity: 'critical' }, created_at: ago(200), created_by: 'deniz@starfall.games', last_test: tested(14, true, '202 Accepted: incident opened, deduplicated by report') },
    { id: 'ch_webhook_warehouse', type: 'webhook', name: 'Warehouse loader', enabled: false, config: { url: 'https://ingest.starfall-data.net/hooks/reports' }, created_at: ago(70), created_by: 'ada@starfall.games', last_test: tested(20, true, '200 OK') }
  ]
}

export interface ChannelDelivery {
  id: string
  channelId: string
  at: number
  /** What sent it: a report by name, or a test from this page. */
  source: string
  title: string
  ok: boolean
  detail: string
  test: boolean
}

/** The last fortnight's notifications, for the log under the channels: sample. */
export function seedDeliveries(): ChannelDelivery[] {
  const now = Date.now()
  const HOUR = 3_600_000
  const day = (days: number, hour: number) => Math.floor((now - days * 24 * HOUR) / (24 * HOUR)) * 24 * HOUR + hour * HOUR
  return [
    { id: 'nd_1', channelId: 'ch_slack_alerts', at: day(0, 10), source: 'Revenue alert', title: 'Revenue yesterday is under $500', ok: true, detail: 'Posted to #alerts-revenue', test: false },
    { id: 'nd_2', channelId: 'ch_pagerduty_oncall', at: day(0, 10), source: 'Revenue alert', title: 'Revenue yesterday is under $500', ok: true, detail: '202 Accepted: incident opened', test: false },
    { id: 'nd_3', channelId: 'ch_discord_mods', at: day(1, 18), source: 'Test from this page', title: 'Test notification from PlayerSense', ok: false, detail: '404 Unknown Webhook', test: true },
    { id: 'nd_4', channelId: 'ch_email_finance', at: day(3, 9), source: 'Weekly revenue', title: 'Weekly revenue for the week of Sep 14', ok: true, detail: 'Accepted for 2 addresses', test: false },
    { id: 'nd_5', channelId: 'ch_slack_liveops', at: day(3, 9), source: 'Weekly revenue', title: 'Weekly revenue for the week of Sep 14', ok: true, detail: 'Posted to #live-ops', test: false },
    { id: 'nd_6', channelId: 'ch_slack_alerts', at: day(6, 10), source: 'Revenue alert', title: 'Revenue yesterday is under $500', ok: true, detail: 'Posted to #alerts-revenue', test: false },
    { id: 'nd_7', channelId: 'ch_email_finance', at: day(10, 9), source: 'Weekly revenue', title: 'Weekly revenue for the week of Sep 7', ok: true, detail: 'Accepted for 2 addresses', test: false }
  ]
}
