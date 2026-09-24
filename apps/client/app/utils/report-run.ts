// A report run as it happens, told one event at a time: which node has started, what came
// out of it, what each delivery answered, and how the run ended. Report Builder draws the
// events on the canvas as they arrive, so a test run is watched rather than waited for.
//
// The server will send these over Server-Sent Events (Muzak's `Router.SSE`): a run is asked
// for with `POST /v1/reports/{id}/runs`, which answers its id, and followed at
// `GET /v1/reports/{id}/runs/{run}/events`, one event per change, each named by its `type`
// and numbered so a dropped stream resumes where it stopped. `followRun` reads that stream.
// Until it exists, `simulateRun` plays the same events in this browser, over the sample
// rows, at the pace a server would send them.

/** Where one node of a run has got to. */
export type RunNodeState = 'queued' | 'running' | 'done' | 'failed' | 'skipped'

/** One event on a run's stream. `type` is also the event's name on the wire. */
export type RunEvent
  = | { type: 'run_started', run: string, at: number, plan: string[] }
    | { type: 'node_started', run: string, node: string, at: number }
    | { type: 'node_finished', run: string, node: string, rows: number, ms: number }
    | { type: 'node_failed', run: string, node: string, error: string }
    | { type: 'node_skipped', run: string, node: string, reason: string }
    | { type: 'delivery', run: string, delivery: RunDelivery, ms: number }
    | { type: 'run_finished', run: string, at: number, status: ReportRun['status'] }

export const RUN_EVENTS: readonly RunEvent['type'][] = ['run_started', 'node_started', 'node_finished', 'node_failed', 'node_skipped', 'delivery', 'run_finished']

/** Stops following a run. The run itself goes on, on the server. */
export type StopFollowing = () => void

/** Follows a run on the server's stream until it finishes. */
export function followRun(url: string, onEvent: (event: RunEvent) => void): StopFollowing {
  const source = new EventSource(url, { withCredentials: true })
  for (const name of RUN_EVENTS) {
    source.addEventListener(name, message => onEvent(JSON.parse((message as MessageEvent<string>).data) as RunEvent))
  }
  /* A browser reconnects to a stream that ends; a finished run has nothing more to say. */
  source.addEventListener('run_finished', () => source.close())
  return () => source.close()
}

// --- What a run runs --------------------------------------------------------------------------

const delivers = (node: GraphNode) => node.data.kind === 'notify' || node.data.kind === 'export'

/* The lines rows run along into a node: every input but a Schedule's. */
const inputsOf = (id: string, edges: readonly GraphEdge[]) => edges.filter(edge => edge.target === id && edge.targetHandle !== WHEN_HANDLE)

/**
 * The nodes a run runs, in an order that puts every node after its inputs: each delivery
 * (or only `focus`) and everything it reads from. A chart or a table nothing delivers is
 * left out, and so is a Schedule, since a test run is run by hand.
 */
export function planRun(nodes: readonly GraphNode[], edges: readonly GraphEdge[], focus: string | null): string[] {
  const needed = new Set<string>()
  const visit = (id: string) => {
    if (needed.has(id)) return
    needed.add(id)
    for (const edge of inputsOf(id, edges)) visit(edge.source)
  }
  for (const node of nodes) if (delivers(node) && (!focus || node.id === focus)) visit(node.id)

  const depth = new Map<string, number>()
  const depthOf = (id: string, trail = new Set<string>()): number => {
    if (depth.has(id)) return depth.get(id)!
    if (trail.has(id)) return 0
    trail.add(id)
    const into = inputsOf(id, edges)
    const value = into.length ? Math.max(...into.map(edge => depthOf(edge.source, trail))) + 1 : 0
    depth.set(id, value)
    return value
  }
  const at = new Map(nodes.map(node => [node.id, node.position.y]))
  return [...needed]
    .filter(id => at.has(id))
    .sort((a, b) => depthOf(a) - depthOf(b) || at.get(a)! - at.get(b)!)
}

/** The file an Export writes, named after its report. */
export const exportFileOf = (reportName: string, config: NodeConfig) => `${fileSlug(reportName)}.${config.file ?? 'json'}`

/** Where an Export writes, in words. */
export function exportDestinationOf(config: NodeConfig) {
  const where = DESTINATIONS[config.destination ?? 'download']
  return config.destination && config.destination !== 'download' ? `${config.target || where.target} (${where.label})` : 'a download here'
}

/** How a run ended, from what its deliveries answered. */
export function runStatusOf(deliveries: readonly RunDelivery[]): ReportRun['status'] {
  const tried = deliveries.filter(entry => !entry.quiet)
  const failed = tried.filter(entry => !entry.ok).length
  return !tried.length ? 'quiet' : !failed ? 'succeeded' : failed === tried.length ? 'failed' : 'partial'
}

// --- A run played here ------------------------------------------------------------------------

export interface SimulatedRun {
  run: string
  report: { id: string, name: string }
  nodes: readonly GraphNode[]
  edges: readonly GraphEdge[]
  /** What each node produces, from the sample rows: what the server would compute. */
  results: ReadonlyMap<string, NodeResult>
  focus: string | null
  channelOf: (id: string) => NotificationChannel | null
  /** Reaches a channel for real. */
  send: (channel: NotificationChannel, title: string) => { ok: boolean, detail: string, ms: number }
}

/* How long a node of each kind takes on the server, roughly: reading rows is the slow part. */
const WORK_MS: Record<NodeKind, [number, number]> = {
  schedule: [0, 0],
  source: [700, 1100],
  filter: [350, 600],
  join: [650, 950],
  group: [450, 750],
  formula: [300, 500],
  sort: [300, 500],
  chart: [250, 400],
  table: [250, 400],
  export: [600, 950],
  notify: [250, 400]
}

const between = ([low, high]: [number, number]) => Math.round(low + Math.random() * (high - low))

/**
 * Plays a run's events as the server will send them. A node starts once every input it
 * reads has finished, so two branches run side by side, and a node after one that failed
 * is skipped. Returns a stop, after which nothing more is sent.
 */
export function simulateRun(input: SimulatedRun, onEvent: (event: RunEvent) => void): StopFollowing {
  const { run, nodes, edges, results } = input
  const plan = planRun(nodes, edges, input.focus)
  const inPlan = new Set(plan)
  const nodeOf = (id: string) => nodes.find(node => node.id === id)!
  const started = new Set<string>()
  const ended = new Map<string, 'done' | 'failed' | 'skipped'>()
  const deliveries: RunDelivery[] = []
  const at = Date.now()

  const timers = new Set<ReturnType<typeof setTimeout>>()
  let stopped = false
  const later = (ms: number, then: () => void) => {
    const timer = setTimeout(() => {
      timers.delete(timer)
      if (!stopped) then()
    }, ms)
    timers.add(timer)
  }

  const end = (id: string, how: 'done' | 'failed' | 'skipped', event: RunEvent) => {
    ended.set(id, how)
    onEvent(event)
    advance()
  }

  function deliver(node: GraphNode, result: NodeResult, begun: number) {
    if (node.data.kind === 'export') {
      const detail = `Wrote ${exportFileOf(input.report.name, node.data.config)}, ${result.rows.length.toLocaleString('en')} rows, to ${exportDestinationOf(node.data.config)}`
      const delivery: RunDelivery = { node: node.id, title: node.data.title, kind: 'export', ok: true, detail }
      deliveries.push(delivery)
      onEvent({ type: 'delivery', run, delivery, ms: Date.now() - begun })
      end(node.id, 'done', { type: 'node_finished', run, node: node.id, rows: result.rows.length, ms: Date.now() - begun })
      return
    }
    /* A Notify: its rule first, then each channel in turn, each answering in its own time. */
    const outcome = evaluateNotify(node, result, input.report, at)
    const channels = (node.data.config.channels ?? []).map(input.channelOf).filter((channel): channel is NotificationChannel => Boolean(channel))
    const next = (index: number) => {
      const channel = channels[index]
      if (!channel) {
        end(node.id, 'done', { type: 'node_finished', run, node: node.id, rows: result.rows.length, ms: Date.now() - begun })
        return
      }
      if (!outcome.send) {
        const delivery: RunDelivery = { node: node.id, title: node.data.title, kind: 'notify', channel: channel.id, ok: true, quiet: true, detail: outcome.reason }
        deliveries.push(delivery)
        onEvent({ type: 'delivery', run, delivery, ms: 0 })
        next(index + 1)
        return
      }
      const answer = input.send(channel, outcome.message.title)
      later(Math.max(answer.ms, 150), () => {
        const delivery: RunDelivery = { node: node.id, title: node.data.title, kind: 'notify', channel: channel.id, ok: answer.ok, detail: answer.detail }
        deliveries.push(delivery)
        onEvent({ type: 'delivery', run, delivery, ms: answer.ms })
        next(index + 1)
      })
    }
    next(0)
  }

  function start(id: string) {
    started.add(id)
    const node = nodeOf(id)
    const begun = Date.now()
    onEvent({ type: 'node_started', run, node: id, at: begun })
    later(between(WORK_MS[node.data.kind]), () => {
      const result = results.get(id)
      if (!result || result.error) {
        end(id, 'failed', { type: 'node_failed', run, node: id, error: result?.error ?? 'It produced nothing.' })
        return
      }
      if (delivers(node)) deliver(node, result, begun)
      else end(id, 'done', { type: 'node_finished', run, node: id, rows: result.rows.length, ms: Date.now() - begun })
    })
  }

  function advance() {
    if (stopped) return
    for (const id of plan) {
      if (started.has(id)) continue
      const from = inputsOf(id, edges).map(edge => edge.source).filter(source => inPlan.has(source))
      const broken = from.find(source => ended.get(source) === 'failed' || ended.get(source) === 'skipped')
      if (broken) {
        started.add(id)
        end(id, 'skipped', { type: 'node_skipped', run, node: id, reason: `${nodeOf(broken).data.title} did not finish` })
        return
      }
      if (from.every(source => ended.get(source) === 'done')) start(id)
    }
    if (ended.size === plan.length) {
      stopped = true
      onEvent({ type: 'run_finished', run, at: Date.now(), status: runStatusOf(deliveries) })
    }
  }

  /* The stream opens with the plan, a moment after it is asked for, as a request would. */
  later(120, () => {
    onEvent({ type: 'run_started', run, at, plan })
    advance()
  })

  return () => {
    stopped = true
    for (const timer of timers) clearTimeout(timer)
    timers.clear()
  }
}
