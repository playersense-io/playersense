// Report Builder: a report is a graph read left to right. Sources hand rows to steps, steps
// shape them (filter, join, group, calculate, sort), and outputs say what becomes of the
// result: a chart, a table, or a file delivered somewhere on a schedule. The last is what
// Exports was; an export is now any report that ends in a file.
//
// Everything here is pure: what each kind of node is, what it is set to, and `runReport`,
// which runs a graph over sample rows so every node can show what passes through it. The
// rows stand in for the query service (project.md §31-33), which will run the same graph
// as one query; the shape of a report does not change when it does.

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
 * What a Notify step does with a run's result: whether its rule lets it send, and the
 * message it sends, with the variables filled in from the rows it received.
 */
export function evaluateNotify(node: GraphNode, result: NodeResult, report: { id: string, name: string }, at: number): NotifyOutcome {
  const config = node.data.config
  const context = { report: report.name, columns: result.columns, rows: result.rows, at }
  let send = true
  let reason = 'Sends on every run.'
  if (config.when === 'rows') {
    send = result.rows.length > 0
    reason = send ? `${result.rows.length.toLocaleString('en')} rows came out, so it sends.` : 'No rows came out, so it stays quiet.'
  }
  if (config.when === 'threshold' && config.threshold) {
    const column = result.columns.find(entry => entry.key === config.threshold!.column)
    const value = Number(result.rows[0]?.[config.threshold.column])
    const line = Number(config.threshold.value)
    const crossed = Number.isFinite(value) && (config.threshold.op === 'gt' ? value > line : value < line)
    send = crossed
    const written = column ? writeCell(Number.isFinite(value) ? value : null, column.type) : '–'
    const name = column?.label ?? config.threshold.column
    reason = `${name.charAt(0).toUpperCase()}${name.slice(1)} is ${written}, ${crossed ? '' : 'not '}${config.threshold.op === 'gt' ? 'over' : 'under'} ${config.threshold.value}: ${crossed ? 'it sends' : 'it stays quiet'}.`
  }
  const include = config.include ?? 'rows'
  const shown = result.columns.slice(0, 5)
  return {
    send,
    reason,
    message: {
      title: fillMessage(config.subject || '{{report}}', context),
      body: fillMessage(config.body ?? '', context),
      severity: config.severity ?? 'warning',
      link: `https://app.playersense.io/reports?report=${report.id}`,
      ...(include === 'rows' && result.rows.length ? { table: { columns: shown, rows: result.rows.slice(0, 5) } } : {}),
      ...(include === 'file' ? { attachment: { name: `${fileSlug(report.name)}.csv`, rows: result.rows.length } } : {})
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

export interface NodeResult {
  columns: ReportColumn[]
  rows: ReportRow[]
  /** Why it has nothing: no input, a setting that no longer fits. */
  error?: string
}

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

/** Every node's result, running each once from the sources forward. */
export function runReport(nodes: readonly GraphNode[], edges: readonly GraphEdge[], now: number): Map<string, NodeResult> {
  const results = new Map<string, NodeResult>()
  const visiting = new Set<string>()
  const byId = new Map(nodes.map(node => [node.id, node]))

  /* A Schedule's line into a delivery's top edge is when it runs, not rows: never an input. */
  const inputOf = (id: string, handle?: string) => {
    const edge = edges.find(entry => entry.target === id && entry.targetHandle !== 'when' && (handle ? entry.targetHandle === handle : true))
    return edge ? run(edge.source) : null
  }

  function run(id: string): NodeResult {
    const known = results.get(id)
    if (known) return known
    const node = byId.get(id)
    if (!node || visiting.has(id)) return { columns: [], rows: [], error: 'It loops back on itself.' }
    visiting.add(id)
    let result: NodeResult
    try {
      result = step(node, inputOf, now)
    } catch (error) {
      result = { columns: [], rows: [], error: error instanceof Error ? error.message : 'It could not run.' }
    }
    visiting.delete(id)
    results.set(id, result)
    return result
  }

  for (const node of nodes) run(node.id)
  return results
}

const fail = (error: string): NodeResult => ({ columns: [], rows: [], error })

function step(node: GraphNode, inputOf: (id: string, handle?: string) => NodeResult | null, now: number): NodeResult {
  const { kind, config } = node.data
  if (kind === 'start' || kind === 'finish') return { columns: [], rows: [] }
  if (kind === 'schedule') {
    const parsed = parseCron(config.cron ?? '')
    return typeof parsed === 'string' ? fail(parsed) : { columns: [], rows: [] }
  }
  if (kind === 'source') return sourceRows(config, now)

  if (kind === 'join') {
    const left = inputOf(node.id, 'left')
    const right = inputOf(node.id, 'right')
    if (!left || !right) return fail('Connect two inputs: left and right.')
    if (left.error || right.error) return fail('An input has a problem to fix first.')
    return joinRows(left, right, config.mode ?? 'inner')
  }

  const input = inputOf(node.id)
  if (!input) return fail(`Connect ${kind === 'chart' || kind === 'table' || kind === 'export' ? 'a result' : 'rows'} into it.`)
  if (input.error) return fail('Its input has a problem to fix first.')
  const has = (key: string) => input.columns.some(column => column.key === key)
  const columnOf = (key: string) => input.columns.find(column => column.key === key)

  switch (kind) {
    case 'filter': {
      const conditions = (config.conditions ?? []).filter(condition => condition.column)
      const missing = conditions.find(condition => !has(condition.column))
      if (missing) return fail(`${missing.column} is not a column here any more.`)
      if (!conditions.length) return input
      const test = (row: ReportRow, condition: FilterCondition) => matches(row[condition.column] ?? null, columnOf(condition.column)!.type, condition, now)
      const rows = input.rows.filter(row => (config.match === 'any' ? conditions.some(condition => test(row, condition)) : conditions.every(condition => test(row, condition))))
      return { columns: input.columns, rows }
    }
    case 'group': {
      const by = (config.by ?? []).filter(Boolean)
      const metrics = (config.metrics ?? []).filter(metric => metric.as.trim())
      const lost = [...by, ...metrics.filter(metric => METRIC_FNS[metric.fn].needsColumn).map(metric => metric.column)].find(key => !key || !has(key))
      if (!metrics.length) return fail('Add a figure to work out: a count or a sum.')
      if (lost !== undefined) return fail(lost ? `${lost} is not a column here any more.` : 'Choose the column each figure is worked out from.')
      return groupRows(input, by, config.bucket ?? 'day', metrics)
    }
    case 'formula': {
      const name = (config.name ?? '').trim().replace(/\s+/g, '_')
      if (!name) return fail('Name the new column.')
      if (!config.left || !has(config.left)) return fail('Choose the column it starts from.')
      const constant = Number(config.right)
      const byColumn = config.right ? has(config.right) : false
      if (!byColumn && !Number.isFinite(constant)) return fail('Choose a column or a number to work it out with.')
      const operate = (a: number, b: number) => (config.op === '+' ? a + b : config.op === '-' ? a - b : config.op === '*' ? a * b : b === 0 ? null : a / b)
      const rows = input.rows.map((row) => {
        const a = Number(row[config.left!])
        const b = byColumn ? Number(row[config.right!]) : constant
        const value = Number.isFinite(a) && Number.isFinite(b) ? operate(a, b) : null
        return { ...row, [name]: value === null ? null : Math.round(value * 10_000) / 10_000 }
      })
      return { columns: [...input.columns.filter(column => column.key !== name), { key: name, label: name.replace(/_/g, ' '), type: config.format ?? 'number' }], rows }
    }
    case 'sort': {
      if (config.column && !has(config.column)) return fail(`${config.column} is not a column here any more.`)
      const rows = config.column ? [...input.rows].sort((a, b) => compareCells(a[config.column!] ?? null, b[config.column!] ?? null) * (config.order === 'asc' ? 1 : -1)) : [...input.rows]
      return { columns: input.columns, rows: config.limit ? rows.slice(0, config.limit) : rows }
    }
    case 'notify': {
      if (!(config.channels ?? []).length) return { ...input, error: 'Choose a channel to send to.' }
      if (config.when === 'threshold' && (!config.threshold?.column || !has(config.threshold.column))) return { ...input, error: 'Choose the figure to watch.' }
      const line = (config.threshold?.value ?? '').trim()
      if (config.when === 'threshold' && (!line || !Number.isFinite(Number(line)))) return { ...input, error: 'Set the line the figure is watched against.' }
      return input
    }
    case 'chart': {
      if (!config.x || !has(config.x)) return { ...input, error: 'Choose what runs along the bottom.' }
      const y = (config.y ?? []).filter(key => has(key))
      if (!y.length) return { ...input, error: 'Choose a figure to draw.' }
      return input
    }
    default:
      return input
  }
}

function matches(value: Cell, type: ColumnType, condition: FilterCondition, now: number): boolean {
  const target = condition.value.trim()
  switch (condition.op) {
    case 'is': return String(value ?? '').toLowerCase() === target.toLowerCase()
    case 'is_not': return String(value ?? '').toLowerCase() !== target.toLowerCase()
    case 'gt': return Number(value) > Number(target)
    case 'lt': return Number(value) < Number(target)
    case 'contains': return String(value ?? '').toLowerCase().includes(target.toLowerCase())
    case 'within': return type === 'time' && typeof value === 'number' && value >= now - Number(target || 0) * DAY_MS
  }
}

function compareCells(a: Cell, b: Cell): number {
  if (a === null) return b === null ? 0 : -1
  if (b === null) return 1
  if (typeof a === 'number' && typeof b === 'number') return a - b
  return String(a).localeCompare(String(b))
}

function joinRows(left: NodeResult, right: NodeResult, mode: 'inner' | 'left'): NodeResult {
  if (!left.columns.some(column => column.key === 'player_id') || !right.columns.some(column => column.key === 'player_id')) {
    return fail('Both inputs need a Player column to join on.')
  }
  const extra = right.columns.filter(column => !left.columns.some(entry => entry.key === column.key))
  const index = new Map<Cell, ReportRow>()
  for (const row of right.rows) index.set(row.player_id ?? null, row)
  const rows: ReportRow[] = []
  for (const row of left.rows) {
    const match = index.get(row.player_id ?? null)
    if (!match && mode === 'inner') continue
    const joined: ReportRow = { ...row }
    for (const column of extra) joined[column.key] = match ? match[column.key] ?? null : null
    rows.push(joined)
  }
  return { columns: [...left.columns, ...extra], rows }
}

/** One figure of a group: its rows, and the numbers in the column it is worked out from. */
function figure(fn: MetricFn, members: readonly ReportRow[], values: readonly number[], column: string): number {
  const total = values.reduce((sum, entry) => sum + entry, 0)
  switch (fn) {
    case 'count':
      return members.length
    case 'count_distinct':
      return new Set(members.map(row => row[column])).size
    case 'sum':
      return total
    case 'avg':
      return values.length ? total / values.length : 0
    case 'min':
      return values.length ? Math.min(...values) : 0
    case 'max':
      return values.length ? Math.max(...values) : 0
  }
}

/** A time as the bucket it falls in, written so it sorts: "2026-09-14", "2026-09". */
function bucketOf(at: number, bucket: 'day' | 'week' | 'month'): string {
  const date = new Date(at)
  if (bucket === 'month') return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`
  if (bucket === 'week') date.setUTCDate(date.getUTCDate() - ((date.getUTCDay() + 6) % 7))
  return date.toISOString().slice(0, 10)
}

function groupRows(input: NodeResult, by: string[], bucket: 'day' | 'week' | 'month', metrics: GroupMetric[]): NodeResult {
  const typeOf = (key: string) => input.columns.find(column => column.key === key)!.type
  const groups = new Map<string, { key: ReportRow, rows: ReportRow[] }>()
  for (const row of input.rows) {
    const key: ReportRow = {}
    for (const column of by) {
      const value = row[column] ?? null
      key[column] = typeOf(column) === 'time' && typeof value === 'number' ? bucketOf(value, bucket) : value
    }
    const id = JSON.stringify(key)
    const group = groups.get(id) ?? { key, rows: [] }
    group.rows.push(row)
    groups.set(id, group)
  }

  const rows = [...groups.values()].map(({ key, rows: members }) => {
    const out: ReportRow = { ...key }
    for (const metric of metrics) {
      const values = members.map(row => Number(row[metric.column])).filter(Number.isFinite)
      out[metric.as.trim().replace(/\s+/g, '_')] = Math.round(figure(metric.fn, members, values, metric.column) * 100) / 100
    }
    return out
  })
  if (by.length) rows.sort((a, b) => compareCells(a[by[0]!] ?? null, b[by[0]!] ?? null))

  const columns: ReportColumn[] = [
    ...by.map((key) => {
      const column = input.columns.find(entry => entry.key === key)!
      return column.type === 'time' ? { key, label: `${column.label} (${bucket})`, type: 'string' as const } : column
    }),
    ...metrics.map((metric) => {
      const source = input.columns.find(entry => entry.key === metric.column)
      const type: ColumnType = METRIC_FNS[metric.fn].numeric && source?.type === 'money' ? 'money' : 'number'
      return { key: metric.as.trim().replace(/\s+/g, '_'), label: metric.as.trim(), type }
    })
  ]
  return { columns, rows }
}

// --- Sample rows ------------------------------------------------------------------------------

const DAY_MS = 86_400_000

function seeded(text: string): () => number {
  let state = 0
  for (const char of text) state = (Math.imul(state, 31) + char.charCodeAt(0)) >>> 0
  return () => {
    state = (state + 0x6D2B79F5) >>> 0
    let t = state
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

const COUNTRIES = [['US', 24], ['DE', 12], ['TR', 11], ['BR', 10], ['GB', 8], ['JP', 8], ['FR', 7], ['KR', 6], ['MX', 7], ['IN', 7]] as const
const PLATFORMS = [['iOS', 42], ['Android', 50], ['Steam', 8]] as const
const PRODUCTS = [['Pouch of gems', 1.99, 34], ['Starter pack', 2.99, 18], ['Season pass', 4.99, 16], ['Chest of gems', 9.99, 18], ['VIP club, monthly', 6.99, 9], ['Vault of gems', 99.99, 5]] as const
const EVENTS = [['session_start', 30], ['level_complete', 22], ['level_fail', 14], ['ad_watched', 12], ['shop_open', 9], ['tutorial_complete', 4], ['purchase', 3]] as const

export const EVENT_NAMES = EVENTS.map(([name]) => name)

function weighted<T>(entries: readonly (readonly [T, ...unknown[]])[], weight: (entry: readonly [T, ...unknown[]]) => number, random: () => number): readonly [T, ...unknown[]] {
  const total = entries.reduce((sum, entry) => sum + weight(entry), 0)
  let roll = random() * total
  for (const entry of entries) {
    roll -= weight(entry)
    if (roll <= 0) return entry
  }
  return entries.at(-1)!
}

/** 900 players, the same on every run: everything else is drawn from them. */
const PLAYERS = (() => {
  const random = seeded('report-players')
  return Array.from({ length: 900 }, (_, index) => {
    const country = weighted(COUNTRIES, entry => entry[1] as number, random)[0]
    const platform = weighted(PLATFORMS, entry => entry[1] as number, random)[0]
    return { id: `p_${(index * 7919 + 104729).toString(36)}`, country, platform, age: Math.floor(random() ** 1.6 * 120), payer: random() < 0.14 }
  })
})()

const sourceCache = new Map<string, ReportRow[]>()

function sourceRows(config: NodeConfig, now: number): NodeResult {
  const source = config.source ?? 'purchases'
  const days = Number(config.range ?? 30)
  const today = Math.floor(now / DAY_MS) * DAY_MS
  const key = `${source}:${days}:${config.event ?? ''}:${today}`
  let rows = sourceCache.get(key)
  if (!rows) {
    rows = drawRows(source, days, today, config.event ?? '')
    sourceCache.set(key, rows)
  }
  return { columns: SOURCES[source].columns, rows }
}

function drawRows(source: SourceKey, days: number, today: number, event: string): ReportRow[] {
  const random = seeded(`report:${source}:${days}:${today}`)
  const within = () => today + DAY_MS - Math.floor(random() * days * DAY_MS)
  const player = () => PLAYERS[Math.floor(random() * PLAYERS.length)]!
  const payer = () => {
    const pool = PLAYERS.filter(entry => entry.payer)
    return pool[Math.floor(random() * pool.length)]!
  }

  switch (source) {
    case 'players':
      return PLAYERS.filter(entry => entry.age <= days + 30).map((entry) => {
        const spend = entry.payer ? Math.round((2 + random() ** 3 * 180) * 100) / 100 : 0
        return { player_id: entry.id, country: entry.country, platform: entry.platform, first_seen: today - entry.age * DAY_MS, sessions: 1 + Math.floor(random() ** 2 * 60), spend, payer: entry.payer ? 'yes' : 'no' }
      })
    case 'purchases':
      return Array.from({ length: Math.round(days * 14) }, (_, index) => {
        const who = payer()
        const [product, price] = weighted(PRODUCTS, entry => entry[2] as number, random) as readonly [string, number, number]
        const roll = random()
        return { purchase_id: `pur_${(index * 104729 + days).toString(36)}`, player_id: who.id, product, price, country: who.country, store: who.platform === 'iOS' ? 'App Store' : who.platform === 'Android' ? 'Google Play' : 'Steam', status: roll < 0.93 ? 'verified' : roll < 0.97 ? 'refunded' : 'rejected', at: within() }
      })
    case 'sessions':
      return Array.from({ length: Math.round(days * 30) }, (_, index) => {
        const who = player()
        return { session_id: `ses_${(index * 7907 + days).toString(36)}`, player_id: who.id, country: who.country, platform: who.platform, at: within(), minutes: Math.round((2 + random() ** 1.8 * 40) * 10) / 10 }
      })
    case 'events':
      return Array.from({ length: Math.round(days * 60) }, () => {
        const who = player()
        const name = event || (weighted(EVENTS, entry => entry[1] as number, random)[0] as string)
        return { event: name, player_id: who.id, country: who.country, platform: who.platform, at: within(), level: name.startsWith('level') ? 1 + Math.floor(random() ** 1.4 * 40) : null }
      }).filter(row => !event || row.event === event)
  }
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
      node('export', 1200, 0, 'export', { file: 'csv', destination: 'email', target: 'finance@starfall.games' }, 'CSV to finance'),
      node('slack', 1200, 170, 'notify', { channels: ['ch_slack_liveops'], when: 'always', subject: '{{report}}: {{sum.revenue}} over the last weeks', body: 'Revenue per payer last week: {{first.per_payer}}.', include: 'rows', severity: 'info' }, 'Note in #live-ops')
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
      node('export', 600, 120, 'export', { file: 'parquet', destination: 's3', target: 's3://starfall-finance/refunds/' }, 'To the finance bucket'),
      node('email', 600, 300, 'notify', { channels: ['ch_email_finance'], when: 'rows', subject: '{{rows}} refunds in the last week', body: 'Refunded purchases up to {{date}}, every one of them attached.', include: 'file', severity: 'info' }, 'Email finance')
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
      node('alert', 1200, 140, 'notify', { channels: ['ch_slack_alerts', 'ch_pagerduty_oncall'], when: 'threshold', threshold: { column: 'revenue', op: 'lt', value: '500' }, subject: 'Revenue on {{first.at}} is {{first.revenue}}', body: 'Under the $500 a day line, from {{first.purchases}} purchases. Check the stores and the last release.', include: 'rows', severity: 'critical' }, 'Page on call')
    ],
    edges: [edge('src', 'verified'), edge('verified', 'day'), edge('day', 'latest'), edge('latest', 'alert'), edge('morning', 'alert', WHEN_HANDLE)]
  }
]
