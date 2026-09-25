// A report run as it happens, told one event at a time: which node has started, what came
// out of it, what each delivery answered, and how the run ended. Report Builder draws the
// events on the canvas as they arrive, so a test run is watched rather than waited for.
//
// The API sends them as server-sent events: a run is asked for with
// `POST …/reports/{id}/runs`, which answers the run and its `stream_url`, and followed there,
// one event per change, each named by its `type` and numbered, so a dropped stream resumes
// where it stopped (the browser sends Last-Event-ID). The URL carries a token for an hour,
// since an EventSource cannot send an Authorization header.

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

/** Follows a run on the server's stream until it finishes; `onLost` if it cannot be reached. */
export function followRun(url: string, onEvent: (event: RunEvent) => void, onLost?: () => void): StopFollowing {
  const source = new EventSource(url)
  for (const name of RUN_EVENTS) {
    source.addEventListener(name, message => onEvent(JSON.parse((message as MessageEvent<string>).data) as RunEvent))
  }
  /* A browser reconnects to a stream that ends; a finished run has nothing more to say. */
  source.addEventListener('run_finished', () => source.close())
  /* Closed for good (refused, or the token lapsed): the browser has stopped retrying. */
  source.addEventListener('error', () => {
    if (source.readyState === EventSource.CLOSED) onLost?.()
  })
  return () => source.close()
}

// --- What a run writes ------------------------------------------------------------------------

/** The file an Export writes, named after its report. */
export const exportFileOf = (reportName: string, config: NodeConfig) => `${fileSlug(reportName)}.${config.file ?? 'json'}`

/** Where an Export writes, in words. */
export function exportDestinationOf(config: NodeConfig) {
  const where = DESTINATIONS[config.destination ?? 'download']
  return config.destination && config.destination !== 'download' ? `${config.target || where.target} (${where.label})` : 'a download here'
}
