// Report Builder: a report is a graph read left to right. Sources hand rows to steps, steps
// shape them (filter, join, group, calculate, sort), and outputs say what becomes of the
// result: a chart, a table, or a file delivered somewhere on a schedule. The last is what
// Exports was; an export is now any report that ends in a file.
//
// Everything here is pure: what each kind of node is and what it is set to. The graph is run
// by the API (…/reports/preview for what each node gives, …/reports/{id}/runs for a run),
// over the project's own rows; this file only describes it.

// --- Columns and rows -------------------------------------------------------------------------

export type ColumnType = 'string' | 'number' | 'money' | 'percent' | 'time' | 'country' | 'platform'

export interface ReportColumn {
  key: string
  label: string
  type: ColumnType
}

export type Cell = string | number | null
export type ReportRow = Record<string, Cell>

export const isNumeric = (type: ColumnType) => type === 'number' || type === 'money' || type === 'percent'

export const NUMERIC_TYPES: readonly ColumnType[] = ['number', 'money', 'percent']

/** A column's type as a glyph, beside its name wherever a column is chosen. */
export const COLUMN_TYPE_ICONS: Record<ColumnType, string> = {
  string: 'material-symbols:text-fields',
  number: 'material-symbols:tag',
  money: 'material-symbols:attach-money',
  percent: 'material-symbols:percent',
  time: 'material-symbols:schedule',
  country: 'material-symbols:public',
  platform: 'material-symbols:devices'
}

// --- Sources ----------------------------------------------------------------------------------

export type SourceKey = 'events' | 'players' | 'purchases' | 'sessions'

export const SOURCES: Record<SourceKey, { label: string, note: string, icon: string, columns: ReportColumn[] }> = {
  events: {
    label: 'Events',
    note: 'Every event the game sent, one row each.',
    icon: 'material-symbols:bolt-outline',
    columns: [
      { key: 'event', label: 'Event', type: 'string' },
      { key: 'player_id', label: 'Player', type: 'string' },
      { key: 'country', label: 'Country', type: 'country' },
      { key: 'platform', label: 'Platform', type: 'platform' },
      { key: 'at', label: 'Time', type: 'time' },
      { key: 'level', label: 'Level', type: 'number' }
    ]
  },
  players: {
    label: 'Players',
    note: 'One row a player, with what they have done so far.',
    icon: 'material-symbols:person-outline',
    columns: [
      { key: 'player_id', label: 'Player', type: 'string' },
      { key: 'country', label: 'Country', type: 'country' },
      { key: 'platform', label: 'Platform', type: 'platform' },
      { key: 'first_seen', label: 'First seen', type: 'time' },
      { key: 'sessions', label: 'Sessions', type: 'number' },
      { key: 'spend', label: 'Spend', type: 'money' },
      { key: 'payer', label: 'Payer', type: 'string' }
    ]
  },
  purchases: {
    label: 'Purchases',
    note: 'Every purchase checked with its store.',
    icon: 'material-symbols:shopping-cart-outline',
    columns: [
      { key: 'purchase_id', label: 'Purchase', type: 'string' },
      { key: 'player_id', label: 'Player', type: 'string' },
      { key: 'product', label: 'Product', type: 'string' },
      { key: 'price', label: 'Price', type: 'money' },
      { key: 'country', label: 'Country', type: 'country' },
      { key: 'store', label: 'Store', type: 'string' },
      { key: 'status', label: 'Status', type: 'string' },
      { key: 'at', label: 'Time', type: 'time' }
    ]
  },
  sessions: {
    label: 'Sessions',
    note: 'Every session, from its first event to its last.',
    icon: 'material-symbols:timer-outline',
    columns: [
      { key: 'session_id', label: 'Session', type: 'string' },
      { key: 'player_id', label: 'Player', type: 'string' },
      { key: 'country', label: 'Country', type: 'country' },
      { key: 'platform', label: 'Platform', type: 'platform' },
      { key: 'at', label: 'Started', type: 'time' },
      { key: 'minutes', label: 'Minutes', type: 'number' }
    ]
  }
}

export const SOURCE_KEYS = Object.keys(SOURCES) as SourceKey[]

export const RANGES = [
  { value: '7', label: 'Last 7 days' },
  { value: '30', label: 'Last 30 days' },
  { value: '90', label: 'Last 90 days' }
] as const

// --- Nodes ------------------------------------------------------------------------------------

export type NodeKind = 'start' | 'finish' | 'schedule' | 'source' | 'filter' | 'join' | 'group' | 'formula' | 'sort' | 'chart' | 'table' | 'export' | 'notify'
export type NodeFamily = 'frame' | 'trigger' | 'source' | 'step' | 'output'

/** A Notify step's rule for when it sends: every run, when rows came out, or when a figure crosses a line. */
export type NotifyWhen = 'always' | 'rows' | 'threshold'
export interface NotifyThreshold { column: string, op: 'gt' | 'lt', value: string }

/** What a notification carries besides its words. */
export type NotifyInclude = 'summary' | 'rows' | 'file'

export type FilterOp = 'is' | 'is_not' | 'gt' | 'lt' | 'contains' | 'within'
export interface FilterCondition { column: string, op: FilterOp, value: string }

export type MetricFn = 'count' | 'count_distinct' | 'sum' | 'avg' | 'min' | 'max'
export interface GroupMetric { fn: MetricFn, column: string, as: string }

export interface NodeConfig {
  source?: SourceKey
  range?: string
  /** Events only: one event, or every event when empty. */
  event?: string
  match?: 'all' | 'any'
  conditions?: FilterCondition[]
  mode?: 'inner' | 'left'
  by?: string[]
  bucket?: 'day' | 'week' | 'month'
  metrics?: GroupMetric[]
  name?: string
  left?: string
  op?: '+' | '-' | '*' | '/'
  right?: string
  format?: 'number' | 'money' | 'percent'
  column?: string
  order?: 'asc' | 'desc'
  limit?: number | null
  chart?: 'line' | 'bar' | 'area'
  x?: string
  y?: string[]
  file?: 'json' | 'csv' | 'parquet'
  destination?: 'download' | 'email' | 's3' | 'bigquery' | 'gcs'
  target?: string
  /** Before schedules were nodes, an export carried one of these; read once, to migrate it. */
  schedule?: 'once' | 'daily' | 'weekly' | 'monthly'
  /** A Schedule's cron line, its timezone, and whether it is paused. */
  cron?: string
  timezone?: string
  paused?: boolean
  /** A Notify's channels, when it sends, what it says and carries. */
  channels?: string[]
  when?: NotifyWhen
  threshold?: NotifyThreshold
  subject?: string
  body?: string
  include?: NotifyInclude
  severity?: Severity
}

export interface ReportNodeData {
  kind: NodeKind
  title: string
  config: NodeConfig
}

export interface KindInfo {
  label: string
  family: NodeFamily
  /** What it does, in a line: the palette's and the inspector's. */
  note: string
  icon: string
  /** Inputs it takes: none for a source, two for a join. */
  inputs: 0 | 1 | 2
  /** Whether anything can follow it. Outputs end a report. */
  output: boolean
  /** Whether a Schedule can drive it: Start, which takes Schedules' lines on its top edge. */
  scheduled?: boolean
}

export const KINDS: Record<NodeKind, KindInfo> = {
  start: { label: 'Start', family: 'frame', note: 'Where every run begins: when you run it, and on each Schedule joined to its top edge. Every source follows it.', icon: 'material-symbols:play-circle-outline', inputs: 0, output: false, scheduled: true },
  finish: { label: 'Finish', family: 'frame', note: 'Where every run ends, once everything the report makes is done. Every output leads to it.', icon: 'material-symbols:flag-outline', inputs: 0, output: false },
  schedule: { label: 'Schedule', family: 'trigger', note: 'When the report runs: a cron line in a timezone. Connect it to the top of Start.', icon: 'material-symbols:schedule-outline', inputs: 0, output: false },
  source: { label: 'Source', family: 'source', note: 'Where the rows come from: events, players, purchases or sessions.', icon: 'material-symbols:database-outline', inputs: 0, output: true },
  filter: { label: 'Filter', family: 'step', note: 'Keeps the rows that match.', icon: 'material-symbols:filter-alt-outline', inputs: 1, output: true },
  join: { label: 'Join', family: 'step', note: 'Puts two sources side by side, player by player.', icon: 'material-symbols:join-inner', inputs: 2, output: true },
  group: { label: 'Group', family: 'step', note: 'Sums, counts and averages, by day, country or anything else.', icon: 'material-symbols:functions', inputs: 1, output: true },
  formula: { label: 'Formula', family: 'step', note: 'A column of your own from two others: revenue per player.', icon: 'material-symbols:calculate-outline', inputs: 1, output: true },
  sort: { label: 'Sort', family: 'step', note: 'Orders the rows, and keeps the top ones.', icon: 'material-symbols:sort', inputs: 1, output: true },
  chart: { label: 'Chart', family: 'output', note: 'Draws the result as a line or bars.', icon: 'material-symbols:bar-chart', inputs: 1, output: false },
  table: { label: 'Table', family: 'output', note: 'Shows the result as a table.', icon: 'material-symbols:table-outline', inputs: 1, output: false },
  export: { label: 'Export', family: 'output', note: 'Writes the result as a file: downloaded now, or delivered on a schedule.', icon: 'material-symbols:file-export-outline', inputs: 1, output: false },
  notify: { label: 'Notify', family: 'output', note: 'Tells notification channels: Slack, email, the one on call. The last step of a report that runs on its own.', icon: 'material-symbols:notifications-outline', inputs: 1, output: false }
}

/** Start and Finish: in every report, once each, and never deleted. */
export const isFrame = (kind: NodeKind) => kind === 'start' || kind === 'finish'

/** A Notify or an Export: what a run sends somewhere. */
export const isDelivery = (kind: NodeKind) => kind === 'notify' || kind === 'export'

export const FAMILIES: { key: NodeFamily, label: string, tone: number }[] = [
  { key: 'frame', label: 'Start and finish', tone: 2 },
  { key: 'trigger', label: 'Triggers', tone: 4 },
  { key: 'source', label: 'Sources', tone: 1 },
  { key: 'step', label: 'Steps', tone: 6 },
  { key: 'output', label: 'Outputs', tone: 3 }
]

export const familyTone = (family: NodeFamily) => FAMILIES.find(entry => entry.key === family)!.tone

/** The palette's entries: a source per kind of data, then every step and output. */
export const PALETTE: { family: NodeFamily, items: { kind: NodeKind, label: string, note: string, icon: string, config?: NodeConfig }[] }[] = [
  { family: 'trigger', items: [{ kind: 'schedule', label: KINDS.schedule.label, note: KINDS.schedule.note, icon: KINDS.schedule.icon }] },
  { family: 'source', items: SOURCE_KEYS.map(key => ({ kind: 'source' as const, label: SOURCES[key].label, note: SOURCES[key].note, icon: SOURCES[key].icon, config: { source: key } })) },
  { family: 'step', items: (['filter', 'join', 'group', 'formula', 'sort'] as const).map(kind => ({ kind, label: KINDS[kind].label, note: KINDS[kind].note, icon: KINDS[kind].icon })) },
  { family: 'output', items: (['chart', 'table', 'export', 'notify'] as const).map(kind => ({ kind, label: KINDS[kind].label, note: KINDS[kind].note, icon: KINDS[kind].icon })) }
]

/** A new node's settings: enough to show something the moment it is connected. */
export function defaultConfig(kind: NodeKind, seed: NodeConfig = {}): NodeConfig {
  switch (kind) {
    case 'start':
    case 'finish': return { ...seed }
    case 'schedule': return { cron: '0 9 * * 1', timezone: 'UTC', paused: false, ...seed }
    case 'source': return { source: 'purchases', range: '30', event: '', ...seed }
    case 'filter': return { match: 'all', conditions: [], ...seed }
    case 'join': return { mode: 'inner', ...seed }
    case 'group': return { by: [], bucket: 'day', metrics: [{ fn: 'count', column: '', as: 'rows' }], ...seed }
    case 'formula': return { name: 'per_player', left: '', op: '/', right: '', format: 'number', ...seed }
    case 'sort': return { column: '', order: 'desc', limit: 10, ...seed }
    case 'chart': return { chart: 'line', x: '', y: [], ...seed }
    case 'table': return { ...seed }
    case 'export': return { file: 'json', destination: 'download', target: '', ...seed }
    case 'notify': return { channels: [], when: 'always', threshold: { column: '', op: 'lt', value: '' }, subject: '{{report}}', body: '{{rows}} rows on {{date}}.', include: 'rows', severity: 'warning', ...seed }
  }
}

export function defaultTitle(kind: NodeKind, config: NodeConfig): string {
  return kind === 'source' ? SOURCES[config.source ?? 'purchases'].label : KINDS[kind].label
}

export const FILTER_OPS: Record<FilterOp, { label: string, types: ColumnType[] | 'any' }> = {
  is: { label: 'is', types: 'any' },
  is_not: { label: 'is not', types: 'any' },
  gt: { label: 'is over', types: ['number', 'money', 'percent'] },
  lt: { label: 'is under', types: ['number', 'money', 'percent'] },
  contains: { label: 'contains', types: ['string'] },
  within: { label: 'in the last', types: ['time'] }
}

export const METRIC_FNS: Record<MetricFn, { label: string, needsColumn: boolean, numeric: boolean }> = {
  count: { label: 'Count rows', needsColumn: false, numeric: false },
  count_distinct: { label: 'Count unique', needsColumn: true, numeric: false },
  sum: { label: 'Sum', needsColumn: true, numeric: true },
  avg: { label: 'Average', needsColumn: true, numeric: true },
  min: { label: 'Lowest', needsColumn: true, numeric: true },
  max: { label: 'Highest', needsColumn: true, numeric: true }
}

export const DESTINATIONS = {
  download: { label: 'Download', note: 'A file to download from here, kept for seven days.', target: '' },
  email: { label: 'Email', note: 'Attached to an email, to the addresses you name.', target: 'finance@yourstudio.com' },
  s3: { label: 'Amazon S3', note: 'Written to a bucket of yours, through a role you grant.', target: 's3://your-bucket/playersense/' },
  gcs: { label: 'Google Cloud Storage', note: 'Written to a bucket of yours, through a service account you grant.', target: 'gs://your-bucket/playersense/' },
  bigquery: { label: 'BigQuery', note: 'Loaded into a table of yours, replaced on every run.', target: 'project.dataset.table' }
} as const

// --- Notifying ------------------------------------------------------------------------------

export const NOTIFY_WHEN: Record<NotifyWhen, { label: string, note: string }> = {
  always: { label: 'Every run', note: 'Whatever came out, even nothing.' },
  rows: { label: 'When there are rows', note: 'Quiet when the report comes out empty: nothing refunded, no country over the line.' },
  threshold: { label: 'When a figure crosses a line', note: 'An alert: the first row\'s figure against a line you set.' }
}

export const NOTIFY_INCLUDE: Record<NotifyInclude, { label: string, note: string }> = {
  summary: { label: 'Words only', note: 'The subject and the message.' },
  rows: { label: 'Top rows', note: 'The first five rows, as a small table.' },
  file: { label: 'File attached', note: 'Every row, as a CSV, where the channel takes files.' }
}

/** A Notify's rule in words: "Every run", "When revenue is under 500". */
export function notifyWhenWords(config: NodeConfig, label: (key?: string) => string = key => key ?? ''): string {
  if (config.when !== 'threshold') return NOTIFY_WHEN[config.when ?? 'always'].label
  const threshold = config.threshold
  if (!threshold?.column) return 'When a figure crosses a line'
  return `When ${label(threshold.column)} is ${threshold.op === 'gt' ? 'over' : 'under'} ${threshold.value || '?'}`
}

export interface NotifyOutcome {
  /** Whether it sends on this run, and if not, why not. */
  send: boolean
  reason: string
  message: NotificationMessage
}

/**
 * What a Notify step would do with the rows it receives, as the API's preview decided it:
 * whether its rule lets it send, why, and the message, with the rows it includes. Null until
 * the preview has come back for it.
 */
export function notifyOutcomeOf(node: GraphNode, result: NodeResult | null, report: { id: string, name: string }): NotifyOutcome | null {
  const decided = result?.notify
  if (!result || result.error || !decided) return null
  const config = node.data.config
  const include = config.include ?? 'rows'
  const shown = result.columns.slice(0, 5)
  const body = [decided.body, ...(decided.lines ?? [])].filter(Boolean).join('\n')
  return {
    send: decided.send,
    reason: decided.reason,
    message: {
      title: decided.title,
      body,
      severity: (decided.severity || config.severity || 'warning') as Severity,
      link: `/reports?report=${report.id}`,
      ...(include === 'rows' && result.rows.length ? { table: { columns: shown, rows: result.rows.slice(0, 5) } } : {}),
      ...(include === 'file' ? { attachment: { name: `${fileSlug(report.name)}.csv`, rows: result.total ?? result.rows.length } } : {})
    }
  }
}

/** The line a Schedule draws to a delivery: into its top edge. */
export const WHEN_HANDLE = 'when'

/** The handles Start's and Finish's lines use: a source's left edge, an output's right. */
export const START_HANDLE = 'start'
export const FINISH_HANDLE = 'finish'

export const SCHEDULES = {
  once: 'Once, now',
  daily: 'Every day at 06:00 UTC',
  weekly: 'Every Monday at 06:00 UTC',
  monthly: 'On the 1st of every month'
} as const

// --- Graph ------------------------------------------------------------------------------------

export interface GraphNode {
  id: string
  position: { x: number, y: number }
  data: ReportNodeData
}

export interface GraphEdge {
  id: string
  source: string
  /** An output's `finish`, for its line to Finish; absent for every other line. */
  sourceHandle?: string | null
  target: string
  /** A join's `left` or `right`; absent for every other input. */
  targetHandle?: string | null
}

/** A Notify's decision on the preview's rows (NotifyOutcomeOut). */
export interface NotifyPreview {
  send: boolean
  reason: string
  title: string
  body?: string
  severity: string
  lines?: string[]
}

/** What one node gives, from the API's preview: the first rows, and how many there are. */
export interface NodeResult {
  columns: ReportColumn[]
  /** The first fifty at most. */
  rows: ReportRow[]
  /** Every row the node gives. */
  total?: number
  /** Why it has nothing: no input, a setting that no longer fits. */
  error?: string
  notify?: NotifyPreview
}

/** How many rows a node gives: every one, not only those previewed. */
export const rowsOf = (result: NodeResult | null | undefined) => result?.total ?? result?.rows.length ?? 0

/** Whether a line is one of Start's or Finish's, drawn for the report rather than by hand. */
export const isFrameLine = (edge: { id: string }) => edge.id.startsWith('frame:')

/**
 * Start's and Finish's lines, which nobody draws: from Start into every source, and into
 * Finish from every output that something feeds. An output on its own is not part of the
 * report yet. Worked out from the nodes and the drawn lines each time, so they are never
 * saved and never out of date.
 */
export function frameLines(nodes: readonly GraphNode[], drawn: readonly GraphEdge[]): GraphEdge[] {
  const start = nodes.find(node => node.data.kind === 'start')
  const finish = nodes.find(node => node.data.kind === 'finish')
  const fed = (id: string) => drawn.some(edge => edge.target === id && edge.targetHandle !== WHEN_HANDLE && !isFrameLine(edge))
  return [
    ...(start ? nodes.filter(node => node.data.kind === 'source').map(node => ({ id: `frame:${start.id}:${node.id}`, source: start.id, target: node.id, targetHandle: START_HANDLE })) : []),
    ...(finish ? nodes.filter(node => KINDS[node.data.kind].family === 'output' && fed(node.id)).map(node => ({ id: `frame:${node.id}:${finish.id}`, source: node.id, sourceHandle: FINISH_HANDLE, target: finish.id })) : [])
  ]
}

// --- Words ------------------------------------------------------------------------------------

/** A cell as it is read: money with its sign, a time as a date, a count with separators. */
export function writeCell(value: Cell, type: ColumnType): string {
  if (value === null || value === undefined) return '–'
  if (type === 'time' && typeof value === 'number') return new Date(value).toISOString().slice(0, 16).replace('T', ' ')
  if (typeof value !== 'number') return String(value)
  if (type === 'money') return `$${value.toLocaleString('en', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
  if (type === 'percent') return `${(value * 100).toFixed(1)}%`
  return value.toLocaleString('en', { maximumFractionDigits: 2 })
}

/** What a node is set to, in a line or two, for its card on the canvas. */
export function summaryOf(data: ReportNodeData, columns: readonly ReportColumn[]): string[] {
  const { kind, config } = data
  const label = (key?: string) => columns.find(column => column.key === key)?.label ?? key ?? ''
  switch (kind) {
    case 'source': return [`${RANGES.find(entry => entry.value === config.range)?.label ?? 'Last 30 days'}${config.source === 'events' && config.event ? ` · ${config.event}` : ''}`]
    case 'filter': {
      const conditions = (config.conditions ?? []).filter(condition => condition.column)
      if (!conditions.length) return ['Every row, for now']
      return conditions.slice(0, 2).map((condition, index) => `${index ? (config.match === 'any' ? 'or ' : 'and ') : ''}${label(condition.column)} ${FILTER_OPS[condition.op].label} ${condition.value}${condition.op === 'within' ? ' days' : ''}`).concat(conditions.length > 2 ? [`and ${conditions.length - 2} more`] : [])
    }
    case 'join': return [config.mode === 'left' ? 'Every left row, matched where it can be' : 'Rows with a match on both sides', 'On Player']
    case 'group': {
      const by = (config.by ?? []).map(key => label(key)).join(', ')
      const metrics = (config.metrics ?? []).map(metric => metric.as).filter(Boolean)
      return [by ? `By ${by}` : 'Everything, as one row', metrics.length ? metrics.join(', ') : 'No figures yet']
    }
    case 'formula': return [`${config.name || 'new'} = ${label(config.left) || '?'} ${config.op === '*' ? '×' : config.op === '/' ? '÷' : config.op} ${label(config.right) || config.right || '?'}`]
    case 'sort': return [config.column ? `${label(config.column)}, ${config.order === 'asc' ? 'lowest first' : 'highest first'}` : 'As it comes', config.limit ? `Top ${config.limit}` : 'Every row']
    case 'chart': return [config.x ? `${label(config.y?.[0]) || '?'}${(config.y?.length ?? 0) > 1 ? ` and ${config.y!.length - 1} more` : ''} by ${label(config.x)}` : 'Not set up yet', config.chart === 'bar' ? 'Bars' : config.chart === 'area' ? 'Area' : 'Line']
    case 'table': return ['Every column']
    case 'export': return [`${(config.file ?? 'json').toUpperCase()} to ${DESTINATIONS[config.destination ?? 'download'].label}`]
    case 'start':
    case 'finish': return []
    case 'schedule': return [describeCron(config.cron ?? ''), config.paused ? 'Paused' : CRON_TIMEZONES.find(zone => zone.value === config.timezone)?.label ?? config.timezone ?? 'UTC']
    case 'notify': {
      const count = (config.channels ?? []).length
      return [count ? `To ${count} ${count === 1 ? 'channel' : 'channels'}` : 'No channel yet', notifyWhenWords(config, label)]
    }
  }
}

// --- Templates --------------------------------------------------------------------------------

export interface ReportTemplate {
  key: string
  name: string
  note: string
  nodes: GraphNode[]
  edges: GraphEdge[]
}

const node = (id: string, x: number, y: number, kind: NodeKind, config: NodeConfig, title?: string): GraphNode => ({
  id,
  position: { x, y },
  data: { kind, title: title ?? defaultTitle(kind, { ...defaultConfig(kind), ...config }), config: defaultConfig(kind, config) }
})
const edge = (source: string, target: string, targetHandle?: string): GraphEdge => ({ id: `e-${source}-${target}${targetHandle ? `-${targetHandle}` : ''}`, source, target, targetHandle })

export const TEMPLATES: ReportTemplate[] = [
  {
    key: 'weekly-revenue',
    name: 'Weekly revenue',
    note: 'Verified purchases a week, with payers and revenue per payer: a chart, and every Monday a CSV to finance and a note in Slack.',
    nodes: [
      node('monday', 1200, -200, 'schedule', { cron: '0 9 * * 1', timezone: 'Europe/Istanbul' }, 'Every Monday'),
      node('src', 0, 120, 'source', { source: 'purchases', range: '90' }),
      node('verified', 300, 120, 'filter', { conditions: [{ column: 'status', op: 'is', value: 'verified' }] }, 'Verified only'),
      node('week', 600, 120, 'group', { by: ['at'], bucket: 'week', metrics: [{ fn: 'sum', column: 'price', as: 'revenue' }, { fn: 'count_distinct', column: 'player_id', as: 'payers' }, { fn: 'count', column: '', as: 'purchases' }] }, 'By week'),
      node('arppu', 900, 120, 'formula', { name: 'per_payer', left: 'revenue', op: '/', right: 'payers', format: 'money' }, 'Revenue per payer'),
      node('chart', 1200, 360, 'chart', { chart: 'bar', x: 'at', y: ['revenue'] }, 'Revenue a week'),
      node('export', 1200, 0, 'export', { file: 'csv', destination: 'download' }, 'CSV for finance'),
      node('slack', 1200, 170, 'notify', { channels: [], when: 'always', subject: '{{report}}: {{sum.revenue}} over the last weeks', body: 'Revenue per payer last week: {{first.per_payer}}.', include: 'rows', severity: 'info' }, 'Note to the team')
    ],
    edges: [edge('src', 'verified'), edge('verified', 'week'), edge('week', 'arppu'), edge('arppu', 'chart'), edge('arppu', 'export'), edge('arppu', 'slack'), edge('monday', 'export', WHEN_HANDLE), edge('monday', 'slack', WHEN_HANDLE)]
  },
  {
    key: 'top-countries',
    name: 'Top countries by spend',
    note: 'Paying players by country, their spend, and the ten countries that spend most.',
    nodes: [
      node('src', 0, 120, 'source', { source: 'players', range: '90' }),
      node('payers', 300, 120, 'filter', { conditions: [{ column: 'payer', op: 'is', value: 'yes' }] }, 'Payers'),
      node('country', 600, 120, 'group', { by: ['country'], metrics: [{ fn: 'sum', column: 'spend', as: 'spend' }, { fn: 'count', column: '', as: 'players' }] }, 'By country'),
      node('top', 900, 120, 'sort', { column: 'spend', order: 'desc', limit: 10 }, 'Top ten'),
      node('chart', 1200, 20, 'chart', { chart: 'bar', x: 'country', y: ['spend'] }, 'Spend by country'),
      node('table', 1200, 240, 'table', {}, 'Top ten table')
    ],
    edges: [edge('src', 'payers'), edge('payers', 'country'), edge('country', 'top'), edge('top', 'chart'), edge('top', 'table')]
  },
  {
    key: 'new-player-purchases',
    name: 'What new players buy',
    note: 'Purchases joined to the players who made them, kept to players first seen in the last 30 days, by product.',
    nodes: [
      node('purchases', 0, 20, 'source', { source: 'purchases', range: '30' }),
      node('players', 0, 240, 'source', { source: 'players', range: '90' }),
      node('join', 300, 120, 'join', { mode: 'inner' }, 'With the player'),
      node('new', 600, 120, 'filter', { conditions: [{ column: 'first_seen', op: 'within', value: '30' }] }, 'New players'),
      node('product', 900, 120, 'group', { by: ['product'], metrics: [{ fn: 'count', column: '', as: 'purchases' }, { fn: 'sum', column: 'price', as: 'revenue' }] }, 'By product'),
      node('sort', 1200, 120, 'sort', { column: 'revenue', order: 'desc', limit: null }, 'Most revenue first'),
      node('table', 1500, 120, 'table', {}, 'Products')
    ],
    edges: [edge('purchases', 'join', 'left'), edge('players', 'join', 'right'), edge('join', 'new'), edge('new', 'product'), edge('product', 'sort'), edge('sort', 'table')]
  },
  {
    key: 'refunds',
    name: 'Refunds for finance',
    note: 'Every refunded purchase, as a file in the finance bucket every morning, and an email when there were any. What Exports used to do, as a report.',
    nodes: [
      node('daily', 600, -80, 'schedule', { cron: '0 6 * * *', timezone: 'UTC' }, 'Every morning'),
      node('src', 0, 120, 'source', { source: 'purchases', range: '7' }),
      node('refunded', 300, 120, 'filter', { conditions: [{ column: 'status', op: 'is', value: 'refunded' }] }, 'Refunded'),
      node('export', 600, 120, 'export', { file: 'csv', destination: 'download' }, 'Refunds as CSV'),
      node('email', 600, 300, 'notify', { channels: [], when: 'rows', subject: '{{rows}} refunds in the last week', body: 'Refunded purchases up to {{date}}, every one of them attached.', include: 'file', severity: 'info' }, 'Email finance')
    ],
    edges: [edge('src', 'refunded'), edge('refunded', 'export'), edge('refunded', 'email'), edge('daily', 'export', WHEN_HANDLE), edge('daily', 'email', WHEN_HANDLE)]
  },
  {
    key: 'revenue-alert',
    name: 'Revenue alert',
    note: 'Yesterday\'s revenue, checked every morning: under $500 and the one on call is paged and #alerts-revenue told.',
    nodes: [
      node('morning', 1200, -60, 'schedule', { cron: '0 10 * * *', timezone: 'Europe/Istanbul' }, 'Every morning at 10'),
      node('src', 0, 140, 'source', { source: 'purchases', range: '7' }),
      node('verified', 300, 140, 'filter', { conditions: [{ column: 'status', op: 'is', value: 'verified' }] }, 'Verified only'),
      node('day', 600, 140, 'group', { by: ['at'], bucket: 'day', metrics: [{ fn: 'sum', column: 'price', as: 'revenue' }, { fn: 'count', column: '', as: 'purchases' }] }, 'By day'),
      node('latest', 900, 140, 'sort', { column: 'at', order: 'desc', limit: 1 }, 'The latest day'),
      node('alert', 1200, 140, 'notify', { channels: [], when: 'threshold', threshold: { column: 'revenue', op: 'lt', value: '500' }, subject: 'Revenue on {{first.at}} is {{first.revenue}}', body: 'Under the $500 a day line, from {{first.purchases}} purchases. Check the stores and the last release.', include: 'rows', severity: 'critical' }, 'Page on call')
    ],
    edges: [edge('src', 'verified'), edge('verified', 'day'), edge('day', 'latest'), edge('latest', 'alert'), edge('morning', 'alert', WHEN_HANDLE)]
  }
]
