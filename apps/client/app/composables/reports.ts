import type { Schema } from '@playersense/contracts'
import type { MaybeRefOrGetter } from 'vue'

/**
 * The project's reports (…/projects/{project}/reports), each a graph of nodes and the edges
 * between them, and the runs each has had (…/reports/{id}/runs). The builder saves as it is
 * changed: a change shows at once, and is sent a moment later, the latest winning.
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

/** A file an Export wrote, kept to download until it expires. */
export interface ReportExport {
  node: string
  file_name: string
  rows: number
  size_bytes: number
  expires_at: string
  download_path: string
}

export interface ReportRun {
  id: string
  at: number
  finishedAt: number | null
  trigger: 'schedule' | 'test'
  /** A test run that sent nothing, only showed what it would have. */
  dry: boolean
  status: 'running' | 'succeeded' | 'partial' | 'failed' | 'quiet' | 'stopped'
  deliveries: RunDelivery[]
  exports: ReportExport[]
  /** Why the whole run failed, when it did. */
  error?: string
  requestedBy?: string
}

export type ReportEnvironment = 'production' | 'staging' | 'development'

export interface SavedReport {
  id: string
  name: string
  description: string
  /** Whose rows its sources read. */
  environment: ReportEnvironment
  nodes: GraphNode[]
  edges: GraphEdge[]
  updated_at: string
  /** When a Schedule next runs it; absent when none does. */
  next_run_at?: string
  last_run?: ReportRun
}

export const RUN_STATUS: Record<ReportRun['status'], { label: string, tone: BadgeTone }> = {
  running: { label: 'Running', tone: 'warning' },
  succeeded: { label: 'Delivered', tone: 'success' },
  partial: { label: 'Partly failed', tone: 'warning' },
  failed: { label: 'Failed', tone: 'error' },
  quiet: { label: 'Nothing to send', tone: 'neutral' },
  stopped: { label: 'Stopped', tone: 'neutral' }
}

// --- From the API -----------------------------------------------------------------------------

/** A graph as the API holds it: raw JSON, which a generated type may call a string. */
function graphOf(raw: unknown): { nodes: GraphNode[], edges: GraphEdge[] } {
  const value = typeof raw === 'string' ? safeParse(raw) : raw
  const graph = (value ?? {}) as { nodes?: GraphNode[], edges?: GraphEdge[] }
  return { nodes: Array.isArray(graph.nodes) ? graph.nodes : [], edges: Array.isArray(graph.edges) ? graph.edges : [] }
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

export function runOf(out: Schema<'ReportRunOut'>): ReportRun {
  return {
    id: out.id,
    at: Date.parse(out.started_at),
    finishedAt: out.finished_at ? Date.parse(out.finished_at) : null,
    trigger: out.trigger === 'schedule' ? 'schedule' : 'test',
    dry: Boolean(out.dry),
    status: (out.status in RUN_STATUS ? out.status : 'failed') as ReportRun['status'],
    deliveries: (out.deliveries ?? []).map(entry => entry as unknown as RunDelivery),
    exports: [...(out.exports ?? [])],
    error: out.error || undefined,
    requestedBy: out.requested_by || undefined
  }
}

function reportOf(out: Schema<'ReportOut'>): SavedReport {
  const graph = graphOf(out.graph)
  return frame({
    id: out.id,
    name: out.name,
    description: out.description ?? '',
    environment: (out.environment || 'production') as ReportEnvironment,
    nodes: graph.nodes,
    edges: graph.edges,
    updated_at: out.updated_at,
    next_run_at: out.next_run_at ?? undefined,
    last_run: out.last_run ? runOf(out.last_run as Schema<'ReportRunOut'>) : undefined
  })
}

/*
 * A report with no Start or Finish gets them: Start to the left of its sources, Finish to the
 * right of everything, as the API requires one of each. A Schedule that ran a delivery runs
 * Start instead, and so the whole report, and moves above it.
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

/** What the API is sent for a report: its words, whose rows, and the graph. */
const bodyOf = (report: Pick<SavedReport, 'name' | 'description' | 'environment' | 'nodes' | 'edges'>) => ({
  name: report.name.trim(),
  description: report.description,
  environment: report.environment,
  graph: { nodes: report.nodes, edges: report.edges }
})

// --- The reports ------------------------------------------------------------------------------

type Change = Partial<Pick<SavedReport, 'name' | 'description' | 'environment' | 'nodes' | 'edges'>>

export function useReports() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.projectPath && `${workspace.projectPath}/reports`)

  const list = useApiGet<Schema<'ReportListOut'>>(path)
  const loading = computed(() => list.isFetching.value && !list.data.value)
  const error = computed(() => list.error.value as ApiError | null)

  /* Changes not yet saved, by report: shown over what the API has until it has them too. */
  const pending = ref<Record<string, Change>>({})
  const saving = ref(false)
  const saveError = ref('')

  const reports = computed<SavedReport[]>(() => (list.data.value?.reports ?? []).map((out) => {
    const report = reportOf(out)
    const change = pending.value[report.id]
    return change ? { ...report, ...change } : report
  }))

  const reportById = (id: string) => reports.value.find(entry => entry.id === id) ?? null

  async function reload() {
    await list.execute()
  }

  /* One saved report put in place of the one the list had. */
  function replace(out: Schema<'ReportOut'>) {
    const current = list.data.value
    if (!current) return
    const known = (current.reports ?? []).some(entry => entry.id === out.id)
    list.data.value = { ...current, reports: known ? current.reports.map(entry => (entry.id === out.id ? out : entry)) : [out, ...current.reports] }
  }

  /** A report from a template, or an empty one with its Start and Finish. Returns its id. */
  async function create(template?: ReportTemplate): Promise<string> {
    const draft = frame({
      id: '',
      name: template?.name ?? 'Untitled report',
      description: template?.note ?? '',
      environment: workspace.environment,
      nodes: template ? JSON.parse(JSON.stringify(template.nodes)) as GraphNode[] : [],
      edges: template ? JSON.parse(JSON.stringify(template.edges)) as GraphEdge[] : [],
      updated_at: ''
    })
    const out = await apiRequest<Schema<'ReportOut'>>('POST', path.value!, bodyOf(draft))
    replace(out)
    return out.id
  }

  /* Each report's changes are sent on their own timer, so two reports never hold each other up. */
  const timers = new Map<string, ReturnType<typeof setTimeout>>()
  const versions = new Map<string, number>()

  async function send(id: string) {
    const report = reportById(id)
    const change = pending.value[id]
    if (!report || !change) return
    if (!report.name.trim()) {
      saveError.value = 'A report needs a name before it can be saved.'
      return
    }
    const version = versions.get(id) ?? 0
    saving.value = true
    try {
      const out = await apiRequest<Schema<'ReportOut'>>('PUT', `${path.value}/${id}`, bodyOf(report))
      replace(out)
      saveError.value = ''
      /* Changed again while this was on its way: that change is still to send. */
      if ((versions.get(id) ?? 0) === version) {
        const { [id]: _, ...rest } = pending.value
        pending.value = rest
      }
    } catch (caught) {
      saveError.value = caught instanceof Error ? caught.message : String(caught)
    } finally {
      saving.value = timers.size > 0
    }
  }

  /** Changes a report: shown now, saved half a second after the last change. */
  function save(id: string, change: Change) {
    pending.value = { ...pending.value, [id]: { ...pending.value[id], ...change } }
    versions.set(id, (versions.get(id) ?? 0) + 1)
    saving.value = true
    clearTimeout(timers.get(id))
    timers.set(id, setTimeout(() => {
      timers.delete(id)
      void send(id)
    }, 500))
  }

  /** Sends a report's unsaved changes now, before something that reads what is saved. */
  async function flush(id: string) {
    const timer = timers.get(id)
    if (timer === undefined) return
    clearTimeout(timer)
    timers.delete(id)
    await send(id)
  }

  async function duplicate(id: string): Promise<string | null> {
    const report = reportById(id)
    if (!report) return null
    const out = await apiRequest<Schema<'ReportOut'>>('POST', path.value!, bodyOf({ ...report, name: `${report.name} (copy)` }))
    replace(out)
    return out.id
  }

  async function remove(id: string) {
    await apiRequest('DELETE', `${path.value}/${id}`)
    const current = list.data.value
    if (current) list.data.value = { ...current, reports: current.reports.filter(entry => entry.id !== id) }
  }

  /** What every node of a graph gives, run by the API over the report's environment, sending nothing. */
  async function preview(report: { name: string, environment: ReportEnvironment }, graph: { nodes: GraphNode[], edges: GraphEdge[] }) {
    const out = await apiRequest<Schema<'ReportPreviewOut'>>('POST', `${path.value}/preview`, { name: report.name, environment: report.environment, graph })
    const results = new Map<string, NodeResult>()
    for (const [id, node] of Object.entries(out.nodes ?? {})) {
      results.set(id, {
        columns: (node.columns ?? []).map(column => ({ key: column.key, label: column.label, type: column.type as ColumnType })),
        rows: (node.rows ?? []) as ReportRow[],
        total: node.total,
        error: node.error || undefined,
        notify: (node.notify ?? undefined) as NotifyPreview | undefined
      })
    }
    return results
  }

  onScopeDispose(() => {
    /* Leaving with changes unsent: send them now rather than lose them. */
    for (const [id, timer] of timers) {
      clearTimeout(timer)
      void send(id)
    }
    timers.clear()
  })

  return { reports, loading, error, saving, saveError, reload, reportById, create, save, flush, duplicate, remove, preview }
}

/** One report's runs, newest first, read again when asked (after a run finishes). */
export function useReportRuns(reportId: MaybeRefOrGetter<string | null | undefined>) {
  const workspace = useWorkspaceStore()
  const list = useApiGet<Schema<'ReportRunListOut'>>(() => {
    const id = toValue(reportId)
    return workspace.projectPath && id ? `${workspace.projectPath}/reports/${id}/runs` : null
  })
  const runs = computed(() => (list.data.value?.runs ?? []).map(runOf).sort((a, b) => b.at - a.at))

  async function reload() {
    await list.execute()
  }

  /** Saves a file a run's Export wrote. */
  async function download(file: ReportExport) {
    await apiDownload(file.download_path, file.file_name)
  }

  return { runs, loading: computed(() => list.isFetching.value && !list.data.value), reload, download }
}
