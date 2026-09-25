import type { Schema } from '@playersense/contracts'

/**
 * The test run Report Builder is watching: every node's progress, built up from the run's
 * events (utils/report-run.ts) as they arrive, so the canvas can draw where the run is.
 *
 * `start` asks the API for a run (POST …/reports/{id}/runs) and follows its stream; the
 * server runs it, reaches the channels and keeps it in the report's runs. `stop` asks the
 * server to stop it, and the stream says where it stopped.
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
  status: 'starting' | ReportRun['status']
  plan: string[]
  nodes: Record<string, RunNode>
  deliveries: RunDelivery[]
  startedAt: number
  finishedAt: number | null
  /** Why the run could not be started or followed. */
  problem?: string
}

export interface RunRequest {
  report: { id: string, name: string }
  focus: string | null
}

/** `finished` is told when a run ends, so the report's runs can be read again. */
export function useReportRun(finished?: (report: string) => void) {
  const workspace = useWorkspaceStore()
  const base = useRuntimeConfig().public.apiBase.replace(/\/$/, '')

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
        finished?.(current.report)
        break
    }
  }

  async function start(request: RunRequest) {
    unfollowNow()
    const report = request.report.id
    run.value = { id: '', report, focus: request.focus, status: 'starting', plan: [], nodes: {}, deliveries: [], startedAt: Date.now(), finishedAt: null }
    try {
      const out = await apiRequest<Schema<'StartedRunOut'>>('POST', `${workspace.projectPath}/reports/${report}/runs`, { dry: false, focus: request.focus ?? '' })
      const current = run.value
      if (!current || current.report !== report || current.id) return
      current.id = out.run.id
      current.startedAt = Date.parse(out.run.started_at)
      unfollow = followRun(base + out.stream_url, apply, () => {
        const lost = run.value
        if (lost?.id === out.run.id && !lost.finishedAt) {
          lost.problem = 'Lost the run\'s progress. It goes on on the server, and its outcome is in the report\'s runs.'
          lost.finishedAt = Date.now()
          unfollow = null
          finished?.(report)
        }
      })
    } catch (caught) {
      if (run.value?.report !== report) return
      run.value = { ...run.value, status: 'failed', finishedAt: Date.now(), problem: caught instanceof Error ? caught.message : String(caught) }
    }
  }

  function unfollowNow() {
    unfollow?.()
    unfollow = null
  }

  /** Asks the server to stop the run. What it has already sent stays sent; the stream says where it stopped. */
  async function stop() {
    const current = run.value
    if (!current?.id || current.finishedAt) return
    try {
      await apiRequest('POST', `${workspace.projectPath}/reports/${current.report}/runs/${current.id}/cancel`)
    } catch (caught) {
      current.problem = caught instanceof Error ? caught.message : String(caught)
    }
  }

  /** Stops following the run, and forgets it. The run itself goes on, on the server. */
  function clear() {
    unfollowNow()
    run.value = null
  }

  onScopeDispose(unfollowNow)

  const running = computed(() => Boolean(run.value && !run.value.finishedAt))

  return { run, running, start, stop, clear }
}
