/**
 * The project's reports, each a graph of nodes and the edges between them, with the runs it
 * has had. Kept in this browser until the dashboard is wired to a reports API; seeded with the
 * templates so the builder opens on something that runs.
 */
export interface RunDelivery {
  /** The Notify or Export node it came from. */
  node: string
  title: string
  kind: 'notify' | 'export'
  channel?: string
  ok: boolean
  /** A Notify whose rule kept it quiet on this run. */
  quiet?: boolean
  detail: string
}

export interface ReportRun {
  id: string
  at: number
  trigger: 'schedule' | 'test'
  /** A test run that sent nothing, only showed what it would have. */
  dry: boolean
  status: 'succeeded' | 'partial' | 'failed' | 'quiet'
  deliveries: RunDelivery[]
}

export interface SavedReport {
  id: string
  name: string
  description: string
  nodes: GraphNode[]
  edges: GraphEdge[]
  updated_at: string
  runs?: ReportRun[]
}

export const RUN_STATUS: Record<ReportRun['status'], { label: string, tone: BadgeTone }> = {
  succeeded: { label: 'Delivered', tone: 'success' },
  partial: { label: 'Partly failed', tone: 'warning' },
  failed: { label: 'Failed', tone: 'error' },
  quiet: { label: 'Nothing to send', tone: 'neutral' }
}

let shared: Ref<SavedReport[]> | null = null

/* The runs a scheduled template has had: its schedule's last few times, delivered. */
function seedRuns(template: ReportTemplate): ReportRun[] {
  const schedule = template.nodes.find(node => node.data.kind === 'schedule')
  if (!schedule) return []
  const now = Date.now()
  const cron = schedule.data.config.cron ?? ''
  const zone = schedule.data.config.timezone ?? 'UTC'
  const times = nextRuns(cron, zone, now - 40 * 86_400_000, 60).filter(at => at < now).slice(-5)
  const deliveries = template.nodes.filter(node => isDelivery(node.data.kind))
  return times.reverse().map((at, index) => {
    const quiet = template.key === 'revenue-alert' && index % 3 !== 0
    return {
      id: `run_${template.key}_${index}`,
      at,
      trigger: 'schedule' as const,
      dry: false,
      status: quiet ? 'quiet' as const : 'succeeded' as const,
      deliveries: deliveries.flatMap((node): RunDelivery[] => (node.data.kind === 'notify'
        ? (node.data.config.channels ?? []).map(channel => ({ node: node.id, title: node.data.title, kind: 'notify' as const, channel, ok: true, quiet, detail: quiet ? 'Revenue was over the line: quiet' : 'Sent' }))
        : [{ node: node.id, title: node.data.title, kind: 'export' as const, ok: true, detail: `Written to ${node.data.config.target || DESTINATIONS[node.data.config.destination ?? 'download'].label}` }]))
    }
  })
}

const fromTemplate = (template: ReportTemplate, id: string): SavedReport => ({
  id,
  name: template.name,
  description: template.note,
  nodes: JSON.parse(JSON.stringify(template.nodes)) as GraphNode[],
  edges: JSON.parse(JSON.stringify(template.edges)) as GraphEdge[],
  updated_at: new Date().toISOString(),
  runs: id === `rep_${template.key}` ? seedRuns(template) : []
})

function seedReports(): SavedReport[] {
  return TEMPLATES.map(template => fromTemplate(template, `rep_${template.key}`))
}

/*
 * A report saved before schedules were nodes: an export that ran daily, weekly or monthly gets
 * a Schedule above it with the same timing, joined to its top edge. Read on every load and
 * kept once saved, so a report is migrated the first time it changes.
 */
const OLD_SCHEDULES = { daily: '0 6 * * *', weekly: '0 6 * * 1', monthly: '0 6 1 * *' } as const

function migrate(report: SavedReport): SavedReport {
  return frame(scheduleNodes(report))
}

function scheduleNodes(report: SavedReport): SavedReport {
  const old = report.nodes.filter(node => node.data.kind === 'export' && node.data.config.schedule && node.data.config.schedule !== 'once')
  if (!old.length) return report
  const nodes = [...report.nodes]
  const edges = [...report.edges]
  for (const exporter of old) {
    const timing = exporter.data.config.schedule as keyof typeof OLD_SCHEDULES
    const id = `schedule-${exporter.id}`
    if (!nodes.some(node => node.id === id)) {
      nodes.push({ id, position: { x: exporter.position.x, y: exporter.position.y - 190 }, data: { kind: 'schedule', title: describeCron(OLD_SCHEDULES[timing]), config: defaultConfig('schedule', { cron: OLD_SCHEDULES[timing], timezone: 'UTC' }) } })
      edges.push({ id: `e-${id}-${exporter.id}-when`, source: id, target: exporter.id, targetHandle: WHEN_HANDLE })
    }
    const { schedule: _schedule, ...config } = exporter.data.config
    nodes[nodes.indexOf(exporter)] = { ...exporter, data: { ...exporter.data, config } }
  }
  return { ...report, nodes, edges }
}

/*
 * A report from before every report had a Start and a Finish gets them: Start to the left of
 * its sources, Finish to the right of everything. A Schedule that ran a delivery runs Start
 * instead, and so the whole report, and moves above it.
 */
function frame(report: SavedReport): SavedReport {
  const has = (kind: NodeKind) => report.nodes.some(node => node.data.kind === kind)
  const stray = report.edges.filter(edge => edge.targetHandle === WHEN_HANDLE && report.nodes.find(node => node.id === edge.target)?.data.kind !== 'start')
  if (has('start') && has('finish') && !stray.length) return report

  const nodes = [...report.nodes]
  const mean = (values: number[]) => (values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : 0)
  const rows = nodes.filter(node => node.data.kind !== 'schedule')
  const sources = nodes.filter(node => node.data.kind === 'source')
  const newId = (base: string) => (nodes.some(node => node.id === base) ? `${base}-${Math.random().toString(36).slice(2, 6)}` : base)
  const make = (kind: 'start' | 'finish', x: number, y: number): GraphNode => ({ id: newId(kind), position: { x: Math.round(x), y: Math.round(y) }, data: { kind, title: KINDS[kind].label, config: defaultConfig(kind) } })
  if (!has('start')) nodes.push(make('start', (sources.length ? Math.min(...sources.map(node => node.position.x)) : 0) - 300, mean(sources.map(node => node.position.y))))
  if (!has('finish')) {
    const outputs = nodes.filter(node => KINDS[node.data.kind].family === 'output')
    nodes.push(make('finish', (rows.length ? Math.max(...rows.map(node => node.position.x)) : 0) + 300, mean(outputs.map(node => node.position.y))))
  }

  const start = nodes.find(node => node.data.kind === 'start')!
  const moved = [...new Set(stray.map(edge => edge.source))]
  const edges = report.edges.filter(edge => !stray.includes(edge))
  moved.forEach((id, index) => {
    if (!edges.some(edge => edge.source === id && edge.target === start.id)) edges.push({ id: `e-${id}-${start.id}-when`, source: id, target: start.id, targetHandle: WHEN_HANDLE })
    const at = nodes.findIndex(node => node.id === id)
    if (at >= 0) nodes[at] = { ...nodes[at]!, position: { x: start.position.x, y: start.position.y - 200 - index * 170 } }
  })
  return { ...report, nodes, edges }
}

/*
 * Sample reports added after a browser first saved its reports, each added to it once: a
 * report deleted stays deleted. Revenue alert is the first that notifies on a schedule.
 */
const LATER_SEEDS = ['revenue-alert']

function storedReports(): Ref<SavedReport[]> {
  if (!shared) {
    /* A browser seeding its reports now gets every sample at once; one that saved them before, only the later ones it lacks. */
    let saved = false
    try {
      saved = localStorage.getItem('playersense.reports.v1') !== null
    } catch {
      /* Storage refused: the samples are drawn fresh either way. */
    }
    shared = effectScope(true).run(() => useLocalStorage<SavedReport[]>('playersense.reports.v1', seedReports, { deep: false }))!
    const added = useLocalStorage<string[]>('playersense.reports.seeds', saved ? [] : [...LATER_SEEDS])
    const missing = LATER_SEEDS.filter(key => !added.value.includes(key))
    if (missing.length && Array.isArray(shared.value)) {
      shared.value = [...shared.value, ...TEMPLATES.filter(template => missing.includes(template.key)).map(template => fromTemplate(template, `rep_${template.key}`))]
      added.value = [...added.value, ...missing]
    }
  }
  return shared
}

export function useReports() {
  const stored = storedReports()

  const reports = computed<SavedReport[]>(() => {
    const value = stored.value
    const valid = Array.isArray(value) && value.every(entry => typeof entry?.id === 'string' && Array.isArray(entry.nodes) && Array.isArray(entry.edges))
    return (valid ? value : seedReports()).map(migrate)
  })

  const reportById = (id: string) => reports.value.find(entry => entry.id === id) ?? null

  const newId = () => `rep_${Math.random().toString(36).slice(2, 10)}`

  /** A report from a template, or an empty one. Returns its id. */
  function create(template?: ReportTemplate): string {
    const id = newId()
    const report = template
      ? fromTemplate(template, id)
      : { id, name: 'Untitled report', description: '', nodes: [], edges: [], updated_at: new Date().toISOString(), runs: [] }
    stored.value = [report, ...reports.value]
    return id
  }

  function save(id: string, change: Partial<Omit<SavedReport, 'id'>>) {
    stored.value = reports.value.map(entry => (entry.id === id ? { ...entry, ...change, updated_at: new Date().toISOString() } : entry))
  }

  /** A run, newest first, the last thirty kept. */
  function addRun(id: string, run: ReportRun) {
    stored.value = reports.value.map(entry => (entry.id === id ? { ...entry, runs: [run, ...(entry.runs ?? [])].slice(0, 30) } : entry))
  }

  function duplicate(id: string): string | null {
    const report = reportById(id)
    if (!report) return null
    const copy = { ...(JSON.parse(JSON.stringify(report)) as SavedReport), id: newId(), name: `${report.name} (copy)`, updated_at: new Date().toISOString(), runs: [] }
    stored.value = [copy, ...reports.value]
    return copy.id
  }

  function remove(id: string) {
    stored.value = reports.value.filter(entry => entry.id !== id)
  }

  return { reports, reportById, create, save, addRun, duplicate, remove }
}
