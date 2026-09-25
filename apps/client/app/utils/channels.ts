// Notification channels: the places the project can tell people something. A channel is one
// destination of one type (an email list, a Slack channel, a Discord webhook, an on-call
// service), set up once here and chosen by name wherever something notifies: a report's
// Notify step today, alerts next. The way Google Cloud's monitoring keeps its channels apart
// from the policies that use them, so a Slack channel renamed is changed in one place.
//
// In the shape the API serves (…/projects/{project}/channels). A destination's secret (a
// webhook URL, an integration key, a header's value) is written and never read back: the URL
// comes back masked, the key as "has one", and leaving either blank on an edit keeps it.

export type ChannelType = 'email' | 'slack' | 'discord' | 'teams' | 'pagerduty' | 'webhook'

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
  slack: { label: 'Slack', note: 'Posted to a channel through an incoming webhook you add in Slack.', icon: 'selfhst:slack', brand: true },
  discord: { label: 'Discord', note: 'Posted to a channel through a webhook you make in its settings.', icon: 'selfhst:discord', brand: true },
  teams: { label: 'Microsoft Teams', note: 'Posted to a channel through a Workflows webhook.', icon: 'selfhst:microsoft-teams', brand: true },
  pagerduty: { label: 'PagerDuty', note: 'Opens an incident on a service, so the one on call is paged.', icon: 'material-symbols:notifications-active-outline', brand: false },
  webhook: { label: 'Webhook', note: 'POSTed as JSON to a URL of yours, signed like every PlayerSense webhook.', icon: 'material-symbols:webhook', brand: false }
}

export const CHANNEL_TYPE_ORDER: readonly ChannelType[] = ['email', 'slack', 'discord', 'teams', 'pagerduty', 'webhook']

export type Severity = 'critical' | 'error' | 'warning' | 'info'

/** Where a channel sends, as it is read back: secrets masked or only said to exist. */
export interface ChannelConfig {
  addresses?: string[]
  /** Slack, Discord, Teams and webhook: masked. */
  url?: string
  /** PagerDuty. */
  severity?: Severity
  has_routing_key?: boolean
  /** Webhook: an extra header's name, for a gateway in front of it. */
  header_name?: string
}

/** Where a channel sends, as it is written. A blank secret on an edit keeps the one it has. */
export interface ChannelConfigInput {
  addresses?: string[]
  url?: string
  routing_key?: string
  severity?: Severity
  header_name?: string
  header_value?: string
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
  status: ChannelStatus
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
  return channel.status in CHANNEL_STATUS ? channel.status : 'untested'
}

/** Where a channel sends, in a line: "#live-ops in Starfall Games", "3 addresses". */
export function targetWords(channel: Pick<NotificationChannel, 'type' | 'config'>): string {
  const config = channel.config
  switch (channel.type) {
    case 'email': return (config.addresses ?? []).length === 1 ? config.addresses![0]! : `${(config.addresses ?? []).length} addresses`
    case 'pagerduty': return `${SEVERITIES[config.severity ?? 'error'].label} incidents`
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

/**
 * Why a channel cannot be saved, or null. `keeping` is an edit, where a blank secret keeps the
 * one the channel has. The API checks again, and says what it refuses.
 */
export function channelProblem(type: ChannelType, name: string, config: ChannelConfigInput, keeping = false): string | null {
  if (name.trim().length < 2) return 'A name of two characters or more'
  const url = config.url ?? ''
  if (keeping && !url && type !== 'email' && type !== 'pagerduty') return null
  switch (type) {
    case 'email': {
      const addresses = config.addresses ?? []
      if (!addresses.length) return 'At least one address'
      if (addresses.length > 20) return 'Up to 20 addresses'
      const wrong = addresses.find(address => !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(address))
      return wrong ? `"${wrong}" is not an email address` : null
    }
    case 'slack': return /^https:\/\/hooks\.slack\.com\//.test(url) ? null : 'A Slack incoming webhook URL: https://hooks.slack.com/services/…'
    case 'pagerduty':
      if (keeping && !config.routing_key) return null
      return /^[a-z0-9]{32}$/i.test(config.routing_key ?? '') ? null : 'An integration key: 32 letters and digits, from the service\'s Events API v2 integration'
    case 'discord': return /^https:\/\/([\w-]+\.)?(discord\.com|discordapp\.com)\/api\/webhooks\/\d+\/[\w-]+$/.test(url) ? null : 'A Discord webhook URL: https://discord.com/api/webhooks/…'
    case 'teams': return /^https:\/\/[\w.-]+\.(logic\.azure\.com|webhook\.office\.com)\//.test(url) ? null : 'A Teams Workflows URL: https://….logic.azure.com/…'
    case 'webhook': return urlProblem(url)
  }
}

// --- Sending ----------------------------------------------------------------------------------

/** What one send answered: a test from this page or a report's run. */
export interface ChannelSendAnswer extends ChannelTest {
  ms: number
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

// --- The log ----------------------------------------------------------------------------------

/** One notification sent to a channel (GET …/channels/deliveries). */
export interface ChannelDelivery {
  id: string
  /** Absent once the channel was deleted; its name is kept. */
  channelId?: string
  channelName: string
  at: number
  /** What sent it: a report by name, or a test from this page. */
  source: string
  title: string
  ok: boolean
  detail: string
  test: boolean
  ms: number
}
