/**
 * The test run Report Builder is watching: every node's progress, built up from the run's
 * events (utils/report-run.ts) as they arrive, so the canvas can draw where the run is.
 *
 * The run is played here (`simulateRun`) until the reports API exists; then `start` posts
 * for a run and follows it with `followRun`, and the server keeps it in the report's runs.
 * Until then this does that too, once the run has finished.
 */
export interface RunNode {
  state: RunNodeState
  rows?: number
  ms?: number
  /** Why it failed, or was skipped. */
  error?: string
  /** A delivery's answers so far: sent, failed, or kept quiet by its rule. */
  sent: number
  failed: number
  quiet: number
}

export interface LiveRun {
  id: string
  report: string
  /** One Notify only, when the run was asked for from it. */
  focus: string | null
  status: 'starting' | 'running' | 'stopped' | ReportRun['status']
  plan: string[]
  nodes: Record<string, RunNode>
  deliveries: RunDelivery[]
  startedAt: number
  finishedAt: number | null
}

export interface RunRequest {
  report: { id: string, name: string }
  nodes: readonly GraphNode[]
  edges: readonly GraphEdge[]
  results: ReadonlyMap<string, NodeResult>
  focus: string | null
}

export function useReportRun() {
  const { channelById } = useChannels()
  const { addRun } = useReports()

  const run = ref<LiveRun | null>(null)
  let unfollow: StopFollowing | null = null

  const node = (id: string) => (run.value!.nodes[id] ??= { state: 'queued', sent: 0, failed: 0, quiet: 0 })

  function apply(event: RunEvent) {
    const current = run.value
    if (!current || current.id !== event.run) return
    switch (event.type) {
      case 'run_started':
        current.status = 'running'
        current.plan = event.plan
        current.nodes = Object.fromEntries(event.plan.map(id => [id, { state: 'queued', sent: 0, failed: 0, quiet: 0 }]))
        break
      case 'node_started':
        node(event.node).state = 'running'
        break
      case 'node_finished':
        Object.assign(node(event.node), { state: 'done', rows: event.rows, ms: event.ms })
        break
      case 'node_failed':
        Object.assign(node(event.node), { state: 'failed', error: event.error })
        break
      case 'node_skipped':
        Object.assign(node(event.node), { state: 'skipped', error: event.reason })
        break
      case 'delivery': {
        const entry = node(event.delivery.node)
        if (event.delivery.quiet) entry.quiet++
        else if (event.delivery.ok) entry.sent++
        else entry.failed++
        current.deliveries.push(event.delivery)
        break
      }
      case 'run_finished':
        current.status = event.status
        current.finishedAt = event.at
        unfollow = null
        record(current)
        break
    }
  }

  /* Kept in the report's runs, as the server will keep it. */
  function record(done: LiveRun) {
    addRun(done.report, { id: done.id, at: done.startedAt, trigger: 'test', dry: false, status: runStatusOf(done.deliveries), deliveries: [...done.deliveries] })
  }

  function start(request: RunRequest) {
    stop()
    const id = `run_${Math.random().toString(36).slice(2, 10)}`
    run.value = { id, report: request.report.id, focus: request.focus, status: 'starting', plan: [], nodes: {}, deliveries: [], startedAt: Date.now(), finishedAt: null }
    unfollow = simulateRun({
      run: id,
      ...request,
      channelOf: channelById,
      // Played in the browser until test runs stream from the reports API: nothing is sent.
      send: () => ({ at: new Date().toISOString(), ok: false, detail: 'Not sent: test runs in this browser do not reach channels', ms: 0 })
    }, apply)
  }

  /** Stops the run where it is. What it has already sent stays sent, and is kept. */
  function stop() {
    if (!unfollow) return
    unfollow()
    unfollow = null
    const current = run.value
    if (!current || current.finishedAt) return
    current.status = 'stopped'
    current.finishedAt = Date.now()
    for (const entry of Object.values(current.nodes)) {
      if (entry.state === 'running' || entry.state === 'queued') entry.state = 'skipped'
    }
    if (current.deliveries.length) record(current)
  }

  /** Stops the run, if it is still going, and forgets it. */
  function clear() {
    stop()
    run.value = null
  }

  onScopeDispose(stop)

  const running = computed(() => run.value?.status === 'starting' || run.value?.status === 'running')

  return { run, running, start, stop, clear }
}
