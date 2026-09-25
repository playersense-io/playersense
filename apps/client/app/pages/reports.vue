<script setup lang="ts">
// Report Builder: a workspace rather than a page, so it fills the window under the bar
// (layouts/canvas.vue). The parts on the left, the canvas in the middle, the selected
// node's settings on the right, and under the canvas what comes out of it.
//
// A report is built by connecting parts: sources to steps to outputs, between a Start and a
// Finish that every report has and none can lose. Start's lines into the sources and the
// outputs' lines into Finish are drawn for the report (utils/report-builder.ts, frameLines);
// a Schedule joins Start's top edge and runs the whole report. The API runs the graph as it is
// changed (…/reports/preview, over the report's environment, sending nothing), so the preview
// and each card's row count are the report as it stands. Reports save themselves to the API.
// A test run is the server's: it plays on the canvas as its events stream in, rows flowing
// along the lines into the node that is running, every card saying how far it has got.
import { type Connection, ConnectionMode, type Edge, type Node, useVueFlow, VueFlow } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { ControlButton, Controls } from '@vue-flow/controls'

definePageMeta({ layout: 'canvas' })

const route = useRoute()
const router = useRouter()

const { reports, loading, error, saving, saveError, reload, reportById, create, save, flush, duplicate, remove, preview } = useReports()
const { screenToFlowCoordinate, fitView, removeNodes } = useVueFlow()

// --- Which report -------------------------------------------------------------------------

const reportId = computed({
  get: () => {
    const asked = String(route.query.report ?? '')
    return reportById(asked) ? asked : reports.value[0]?.id ?? ''
  },
  set: (id: string) => {
    router.replace({ query: { ...route.query, report: id } })
  }
})
const report = computed(() => reportById(reportId.value))

const { runs, reload: reloadRuns, download } = useReportRuns(() => report.value?.id)
const { run: liveRun, running: testRunning, start: startRun, stop: stopRun, clear: clearRun } = useReportRun(() => void reloadRuns())

usePageCrumb(() => report.value?.name ?? 'Report Builder')

const REPORT_OPTIONS = computed<GhostSelectOption[]>(() => reports.value.map(entry => ({ value: entry.id, label: entry.name })))

// --- The graph ----------------------------------------------------------------------------

/** A node as the canvas holds it: Vue Flow's, with a report's data always there. */
type FlowNode = Node<ReportNodeData> & { data: ReportNodeData }

/* Cast rather than typed through `ref`, whose unwrapping of Vue Flow's node type recurses
   past what the checker will follow. */
const nodes = ref([]) as Ref<FlowNode[]>
const edges = ref([]) as Ref<Edge[]>
const selectedId = ref<string | null>(null)

/* A deep copy with no reactivity left in it. `structuredClone` refuses the proxies a node's
   settings pick up once they have been edited; the settings are JSON, so JSON copies them. */
function plain<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

/* A line on the canvas: a Schedule's into a delivery's top edge is drawn as time, dashed. */
function lineOf(id: string, source: string, target: string, targetHandle: string | null): Edge {
  return { id, source, target, targetHandle, ...(targetHandle === WHEN_HANDLE ? { class: 'ps-edge-when' } : {}) }
}

/*
 * The lines drawn by hand, with Start's and Finish's for the nodes there are now. One that is
 * already on the canvas is kept as it is, so what a test run has drawn on it stays.
 */
function withFrame(current: Edge[]): Edge[] {
  const kept = new Map(current.filter(isFrameLine).map(edge => [edge.id, edge]))
  const byHand = current.filter(edge => !isFrameLine(edge))
  return [
    ...byHand,
    ...frameLines(nodes.value, byHand).map(line => kept.get(line.id) ?? { ...line, class: 'ps-edge-frame', deletable: false, selectable: false, focusable: false })
  ]
}

/* Only what was drawn by hand is the report's: Start's and Finish's lines follow from it. */
const drawn = computed(() => edges.value.filter(edge => !isFrameLine(edge)))

/*
 * A report onto the canvas: its nodes, then its lines a tick later, once the canvas holds the
 * nodes (Vue Flow drops a line whose ends it cannot find yet, as with any new node's). Nothing
 * is saved in between, when the canvas has the nodes without their lines.
 */
let settling = false
let loads = 0

function load(id: string) {
  const entry = reportById(id)
  const mine = ++loads
  settling = true
  nodes.value = (entry?.nodes ?? []).map(node => ({ id: node.id, type: 'report', position: { ...node.position }, data: plain(node.data), ...(isFrame(node.data.kind) ? { deletable: false } : {}) }))
  edges.value = []
  selectedId.value = null
  results.value = new Map()
  clearRun()
  fitPending = true
  nextTick(() => {
    /* Another report was opened meanwhile: its own load draws its lines. */
    if (mine !== loads) return
    /* A line the canvas would not let be drawn, saved before it said so, is left behind. */
    edges.value = withFrame((entry?.edges ?? [])
      .filter(edge => fits(edge.source, edge.target, edge.targetHandle ?? null))
      .map(edge => lineOf(edge.id, edge.source, edge.target, edge.targetHandle ?? null)))
    savedShape = snapshot()
    settling = false
    void askPreview()
  })
}

/*
 * Fitted once a report's nodes are drawn and measured: before that, there is nothing to fit.
 * Once a load, since the same event comes for every node added later, and the view should
 * not jump then.
 */
let fitPending = true
function fit() {
  if (!fitPending) return
  fitPending = false
  fitView({ padding: 0.2, maxZoom: 1 })
}

/*
 * What the results depend on, as one string: the nodes' settings and the lines, never their
 * positions. Dragging a node rewrites its position every frame; this string stays the same,
 * so the report is not run again for a move.
 */
const shape = computed(() => JSON.stringify({
  nodes: nodes.value.map(node => ({ id: node.id, position: { x: 0, y: 0 }, data: node.data })),
  edges: drawn.value.map(edge => ({ id: edge.id, source: edge.source, target: edge.target, targetHandle: edge.targetHandle ?? null }))
}))

/*
 * What each node gives, from the API: asked again a moment after the report stops changing,
 * the latest answer winning. Kept while the next is on its way, so the cards do not flicker.
 */
const results = shallowRef(new Map<string, NodeResult>())
const previewing = ref(false)
const previewError = ref('')
let asked = 0

const askPreview = useDebounceFn(async () => {
  const entry = report.value
  if (!entry || settling) return
  const mine = ++asked
  previewing.value = true
  try {
    const answer = await preview({ name: entry.name, environment: entry.environment }, JSON.parse(shape.value) as { nodes: GraphNode[], edges: GraphEdge[] })
    if (mine !== asked) return
    results.value = answer
    previewError.value = ''
  } catch (caught) {
    if (mine === asked) previewError.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    if (mine === asked) previewing.value = false
  }
}, 400)

watch([shape, () => report.value?.environment], () => void askPreview())

// --- Saving -------------------------------------------------------------------------------

/*
 * What is saved, as one string. The canvas rewrites its nodes for things that are not the
 * report (their measured size, whether they are selected), so a save happens only when this
 * string has changed: a move, a setting, a line.
 */
function snapshot() {
  return JSON.stringify([
    nodes.value.map(node => [node.id, Math.round(node.position.x), Math.round(node.position.y), node.data]),
    drawn.value.map(edge => [edge.id, edge.source, edge.target, edge.targetHandle ?? null])
  ])
}
let savedShape = ''

watch(reportId, load, { immediate: true })

/* Each change goes to useReports' save, which sends the latest a moment after the last. */
watch([nodes, edges], () => {
  if (settling || !report.value) return
  const next = snapshot()
  if (next === savedShape) return
  savedShape = next
  save(report.value.id, {
    nodes: nodes.value.map(node => ({ id: node.id, position: { x: Math.round(node.position.x), y: Math.round(node.position.y) }, data: plain(node.data) })),
    edges: drawn.value.map(edge => ({ id: edge.id, source: edge.source, target: edge.target, targetHandle: edge.targetHandle ?? null }))
  })
}, { deep: true })

/* Start's and Finish's lines follow the nodes, a tick after them, like any line to a new node. */
const frameShape = computed(() => frameLines(nodes.value, drawn.value).map(line => line.id).join(' '))
watch(frameShape, () => nextTick(() => {
  edges.value = withFrame(edges.value)
}))

// --- Changing nodes -----------------------------------------------------------------------

const selected = computed(() => nodes.value.find(node => node.id === selectedId.value) ?? null)
const selectedGraph = computed<GraphNode | null>(() => (selected.value ? { id: selected.value.id, position: selected.value.position, data: selected.value.data } : null))

/* What flows into the selected node: its (left) input's result. */
const selectedInput = computed(() => {
  if (!selected.value) return null
  const into = drawn.value.filter(edge => edge.target === selected.value!.id && edge.targetHandle !== WHEN_HANDLE)
  const first = into.find(edge => edge.targetHandle !== 'right') ?? into[0]
  return first ? results.value.get(first.source) ?? null : null
})

function updateNode(id: string, patch: Partial<ReportNodeData>) {
  nodes.value = nodes.value.map(node => (node.id === id ? { ...node, data: { ...node.data, ...patch } } : node))
}

function newNodeId(kind: NodeKind) {
  return `${kind}-${Math.random().toString(36).slice(2, 8)}`
}

/*
 * Lines to a node just added, drawn once the canvas holds the node. Vue Flow checks a line's
 * ends as it arrives, and one given in the same tick as its node is dropped as pointing at
 * nothing.
 */
function connectNew(next: () => Edge[]) {
  nextTick(() => {
    edges.value = next()
  })
}

function addNode(kind: NodeKind, seed: NodeConfig, position: { x: number, y: number }, after?: string) {
  const config = defaultConfig(kind, seed)
  const node: FlowNode = { id: newNodeId(kind), type: 'report', position, data: { kind, title: defaultTitle(kind, config), config } }
  nodes.value = [...nodes.value, node]
  if (after && KINDS[kind].inputs > 0) {
    connectNew(() => [...edges.value, { id: `e-${after}-${node.id}`, source: after, target: node.id, targetHandle: KINDS[kind].inputs === 2 ? 'left' : null }])
  }
  selectedId.value = node.id
}

/* A click in the palette: after the selected node when it can take one, else in the middle. */
function addFromPalette(kind: NodeKind, seed: NodeConfig) {
  const from = selected.value
  /* A Schedule goes above Start, over any it has already, and runs the report. */
  const start = nodes.value.find(node => node.data.kind === 'start')
  if (kind === 'schedule' && start) {
    const above = edges.value.filter(edge => edge.target === start.id && edge.targetHandle === WHEN_HANDLE).length
    addNode(kind, seed, { x: start.position.x, y: start.position.y - 200 - above * 170 })
    const schedule = selectedId.value!
    connectNew(() => [...edges.value, lineOf(`e-${schedule}-${start.id}-when`, schedule, start.id, WHEN_HANDLE)])
    return
  }
  if (from && KINDS[from.data.kind].output && KINDS[kind].inputs > 0) {
    const taken = (y: number) => nodes.value.some(node => Math.abs(node.position.x - (from.position.x + 300)) < 120 && Math.abs(node.position.y - y) < 100)
    let y = from.position.y
    while (taken(y)) y += 180
    addNode(kind, seed, { x: from.position.x + 300, y }, from.id)
    return
  }
  const box = canvas.value?.getBoundingClientRect()
  const centre = box ? screenToFlowCoordinate({ x: box.left + box.width / 2 - 120, y: box.top + box.height / 2 - 50 }) : { x: 0, y: 0 }
  addNode(kind, seed, centre)
}

const canvas = ref<HTMLElement | null>(null)

function onDragOver(event: DragEvent) {
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
}

function onDrop(event: DragEvent) {
  const raw = event.dataTransfer?.getData('application/x-report-node')
  if (!raw) return
  const { kind, config } = JSON.parse(raw) as { kind: NodeKind, config: NodeConfig }
  const at = screenToFlowCoordinate({ x: event.clientX, y: event.clientY })
  addNode(kind, config, { x: at.x - 120, y: at.y - 40 })
}

function removeSelected() {
  if (!selected.value || isFrame(selected.value.data.kind)) return
  removeNodes([selected.value.id])
  selectedId.value = null
}

// --- Connecting ---------------------------------------------------------------------------

/* Whether `from` can already be reached from `to`: a line between them would loop. */
function reaches(from: string, to: string): boolean {
  const seen = new Set<string>()
  const walk = (id: string): boolean => {
    if (id === to) return true
    if (seen.has(id)) return false
    seen.add(id)
    return edges.value.filter(edge => edge.source === id).some(edge => walk(edge.target))
  }
  return walk(from)
}

/*
 * Rows go into rows, and time into time: a Schedule only into a delivery's top edge, and rows
 * only into a node that takes them. The canvas joins an output to an input and nothing else
 * (`ConnectionMode.Strict`); this says which of those make sense.
 */
function isValidConnection(connection: Connection) {
  /* Vue Flow asks about every line it is given, Start's and Finish's too: those are the report's own. */
  if ('id' in connection && isFrameLine(connection as Connection & { id: string })) return true
  if (connection.source === connection.target || reaches(connection.target, connection.source)) return false
  return fits(connection.source, connection.target, connection.targetHandle ?? null)
}

/* Whether a line from one node into another's handle joins things that go together. */
function fits(source: string, target: string, handle: string | null) {
  const from = nodes.value.find(node => node.id === source)
  const to = nodes.value.find(node => node.id === target)
  if (!from || !to) return false
  if (from.data.kind === 'schedule') return handle === WHEN_HANDLE && Boolean(KINDS[to.data.kind].scheduled)
  if (handle === WHEN_HANDLE || !KINDS[from.data.kind].output) return false
  const inputs = KINDS[to.data.kind].inputs
  return inputs === 2 ? handle === 'left' || handle === 'right' : inputs === 1
}

/* An input takes one line: a new one replaces what was there. Start takes every Schedule. */
function onConnect(connection: Connection) {
  const handle = connection.targetHandle ?? null
  const replaces = (edge: Edge) => edge.target === connection.target && (edge.targetHandle ?? null) === handle && (handle !== WHEN_HANDLE || edge.source === connection.source)
  edges.value = [
    ...edges.value.filter(edge => !replaces(edge)),
    lineOf(`e-${connection.source}-${connection.target}${handle ? `-${handle}` : ''}`, connection.source, connection.target, handle)
  ]
}

// --- Time ---------------------------------------------------------------------------------

const clock = useNow({ interval: 60_000 })

/* The Schedules that run the report, by their lines into Start's top edge. */
const startSchedules = computed(() => {
  const start = nodes.value.find(node => node.data.kind === 'start')
  return start
    ? edges.value
        .filter(edge => edge.target === start.id && edge.targetHandle === WHEN_HANDLE)
        .map(edge => nodes.value.find(node => node.id === edge.source))
        .filter((node): node is FlowNode => Boolean(node))
    : []
})

const cronWords = (node: FlowNode) => `${describeCron(node.data.config.cron ?? '')}${node.data.config.paused ? ', paused' : ''}`

/* The outputs that lead to Finish: every one something feeds. */
const finishing = computed(() => {
  const finish = nodes.value.find(node => node.data.kind === 'finish')
  const into = new Set(frameLines(nodes.value, drawn.value).filter(line => line.target === finish?.id).map(line => line.source))
  return nodes.value.filter(node => into.has(node.id))
})

/* When the report runs, in words, or null when it runs only by hand. */
const runsWhen = computed(() => {
  const [first, ...rest] = startSchedules.value
  if (!first) return null
  return rest.length ? `${cronWords(first)}, and ${rest.length} more ${rest.length === 1 ? 'schedule' : 'schedules'}` : cronWords(first)
})

/*
 * What each card says about time: a Schedule its next run; Start and every delivery when the
 * report runs; Finish what it waits for.
 */
const timings = computed(() => {
  const out = new Map<string, string | null>()
  for (const node of nodes.value) {
    if (node.data.kind === 'schedule') {
      const config = node.data.config
      const next = config.paused ? null : nextRuns(config.cron ?? '', config.timezone ?? 'UTC', clock.value.getTime(), 1)[0]
      out.set(node.id, config.paused ? 'Paused' : next ? `Next ${writeRun(next, config.timezone ?? 'UTC')}` : null)
    } else if (node.data.kind === 'start' || isDelivery(node.data.kind)) {
      out.set(node.id, runsWhen.value)
    } else if (node.data.kind === 'finish') {
      const count = finishing.value.length
      out.set(node.id, count ? `After ${count} ${count === 1 ? 'output' : 'outputs'}` : null)
    }
  }
  return out
})

/*
 * What a node lists by name in its settings: for a Schedule on Start, the deliveries it sends;
 * for Start, its Schedules; for Finish, the outputs it waits for.
 */
function drives(id: string): string[] {
  const node = nodes.value.find(entry => entry.id === id)
  if (!node) return []
  if (node.data.kind === 'schedule') {
    if (!startSchedules.value.some(entry => entry.id === id)) return []
    const sent = nodes.value.filter(entry => isDelivery(entry.data.kind)).map(entry => entry.data.title)
    return sent.length ? sent : ['The whole report']
  }
  if (node.data.kind === 'start') return startSchedules.value.map(cronWords)
  if (node.data.kind === 'finish') return finishing.value.map(entry => entry.data.title)
  return []
}

// --- Tidying ------------------------------------------------------------------------------

/*
 * Every node in a column by how many steps it is from its source, in its current order,
 * stacked by its measured height so no card ever covers another. A Schedule joins the stack
 * of the first delivery it runs, just above it, so its line goes straight down into it.
 * Each column is centred on the rows alone, so a report's main line stays level. A node with
 * no line to anything is set aside in rows under it all, as wide as the report, so what is
 * not part of it yet reads as parts put down, not as part of the flow.
 */
const TIDY_GAP = 44
const SCHEDULE_GAP = 56
const LOOSE_GAP = 96
/* A card's width on the canvas (ReportFlowNode.vue, w-60). */
const CARD_WIDTH = 240

function tidy() {
  const depth = new Map<string, number>()
  const depthOf = (id: string, trail = new Set<string>()): number => {
    if (depth.has(id)) return depth.get(id)!
    if (trail.has(id)) return 0
    trail.add(id)
    const into = edges.value.filter(edge => edge.target === id && edge.targetHandle !== WHEN_HANDLE)
    const value = into.length ? Math.max(...into.map(edge => depthOf(edge.source, trail))) + 1 : 0
    depth.set(id, value)
    return value
  }
  /* Measured on the page: the card's own height, which zoom does not change. */
  const cardOf = (node: FlowNode) => canvas.value?.querySelector<HTMLElement>(`.vue-flow__node[data-id="${CSS.escape(node.id)}"]`)
  const heightOf = (node: FlowNode) => cardOf(node)?.offsetHeight || 120
  const widthOf = (node: FlowNode) => cardOf(node)?.offsetWidth || CARD_WIDTH
  const byPlace = (a: FlowNode, b: FlowNode) => depthOf(a.id) - depthOf(b.id) || a.position.y - b.position.y

  /* The delivery each Schedule sits above: of those it runs, the first by column, then height. */
  const anchors = new Map<string, FlowNode[]>()
  for (const schedule of nodes.value.filter(node => node.data.kind === 'schedule')) {
    const runs = edges.value
      .filter(edge => edge.source === schedule.id && edge.targetHandle === WHEN_HANDLE)
      .map(edge => nodes.value.find(node => node.id === edge.target))
      .filter((node): node is FlowNode => Boolean(node))
      .sort(byPlace)
    if (runs[0]) anchors.set(runs[0].id, [...(anchors.get(runs[0].id) ?? []), schedule])
  }

  const touched = new Set(edges.value.flatMap(edge => [edge.source, edge.target]))
  const loose = nodes.value.filter(node => !touched.has(node.id) && !isFrame(node.data.kind))

  const columns = new Map<number, FlowNode[]>()
  for (const node of nodes.value) {
    if (node.data.kind === 'schedule' || node.data.kind === 'finish' || (!touched.has(node.id) && !isFrame(node.data.kind))) continue
    const column = depthOf(node.id)
    columns.set(column, [...(columns.get(column) ?? []), node])
  }
  /* Finish closes the report: a column of its own after the last. */
  const finish = nodes.value.find(node => node.data.kind === 'finish')
  if (finish) columns.set(Math.max(0, ...columns.keys()) + 1, [finish])

  const placed = new Map<string, { x: number, y: number }>()
  for (const [column, members] of columns) {
    members.sort((a, b) => a.position.y - b.position.y)
    let y = 0
    let top: number | null = null
    let bottom = 0
    for (const node of members) {
      for (const schedule of anchors.get(node.id) ?? []) {
        placed.set(schedule.id, { x: column * 300, y })
        y += heightOf(schedule) + SCHEDULE_GAP
      }
      /* Start and Finish are narrower than a card: centred in the column, under a Schedule. */
      placed.set(node.id, { x: column * 300 + (isFrame(node.data.kind) ? (CARD_WIDTH - widthOf(node)) / 2 : 0), y })
      top ??= y
      bottom = y + heightOf(node)
      y = bottom + TIDY_GAP
    }
    const shift = ((top ?? 0) + bottom) / 2
    for (const node of [...members, ...members.flatMap(entry => anchors.get(entry.id) ?? [])]) {
      const at = placed.get(node.id)!
      placed.set(node.id, { x: at.x, y: Math.round(at.y - shift) })
    }
  }
  /* The loose ones, in the order they sit on the canvas, left to right and a row at a time. */
  const laid = nodes.value.filter(node => placed.has(node.id))
  let rowY = laid.length ? Math.max(...laid.map(node => placed.get(node.id)!.y + heightOf(node))) + LOOSE_GAP : 0
  const perRow = Math.max(columns.size, 4)
  loose.sort((a, b) => a.position.y - b.position.y || a.position.x - b.position.x)
  for (let start = 0; start < loose.length; start += perRow) {
    const row = loose.slice(start, start + perRow)
    row.sort((a, b) => a.position.x - b.position.x)
    row.forEach((node, index) => placed.set(node.id, { x: index * 300, y: Math.round(rowY) }))
    rowY += Math.max(...row.map(heightOf)) + TIDY_GAP
  }
  nodes.value = nodes.value.map(node => ({ ...node, position: placed.get(node.id) ?? node.position }))
  nextTick(() => fitView({ padding: 0.25, maxZoom: 1, duration: 300 }))
}

// --- Reports ------------------------------------------------------------------------------

function openReport(id: string) {
  reportId.value = id
}

/* What went wrong with the last thing asked of the reports, shown in the bar. */
const problem = ref('')
const busy = ref(false)

async function attempt(change: () => Promise<void>) {
  busy.value = true
  problem.value = ''
  try {
    await change()
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    busy.value = false
  }
}

function newReport(template?: ReportTemplate) {
  void attempt(async () => openReport(await create(template)))
}

function duplicateReport() {
  const entry = report.value
  if (!entry) return
  void attempt(async () => {
    const id = await duplicate(entry.id)
    if (id) openReport(id)
  })
}

const deleting = ref(false)
function confirmDelete() {
  const entry = report.value
  if (!entry) return
  void attempt(async () => {
    await remove(entry.id)
    const next = reports.value[0]?.id
    if (next) openReport(next)
  })
}

// --- What it produces ---------------------------------------------------------------------

const outputs = computed(() => nodes.value.filter(node => KINDS[node.data.kind].family === 'output'))
const schedules = computed(() => nodes.value.filter(node => node.data.kind === 'schedule'))

// --- Testing ------------------------------------------------------------------------------

const testing = ref(false)
const testFocus = ref<string | null>(null)
function openTest(focus: string | null = null) {
  clearRun()
  testFocus.value = focus
  testing.value = true
}
const testGraph = computed(() => JSON.parse(shape.value) as { nodes: GraphNode[], edges: GraphEdge[] })

/* Send steps the dialog aside, so the run can be watched on the canvas. The server runs what
   is saved, so anything still on its way is saved first. */
async function sendTest() {
  const entry = report.value
  if (!entry) return
  testing.value = false
  await flush(entry.id)
  await startRun({ report: { id: entry.id, name: entry.name }, focus: testFocus.value })
}

/* The run on the canvas, once the stream has said which nodes it runs. */
const shownRun = computed(() => (liveRun.value?.plan.length ? liveRun.value : null))

/*
 * A line during a run: rows flowing into the node that is running, drawn over once they
 * have arrived, and stepping back when the run does not use it. Kept as one string, so the
 * lines are only rewritten when one of them changes.
 */
function lookOf(edge: Edge): string {
  const run = shownRun.value
  if (!run) return ''
  const from = run.nodes[edge.source]
  const to = run.nodes[edge.target]
  if (!from || !to || edge.targetHandle === WHEN_HANDLE) return 'ps-edge-idle'
  if (to.state === 'running') return 'ps-edge-flowing'
  return from.state === 'done' && to.state !== 'queued' ? 'ps-edge-done' : ''
}
const looks = computed(() => edges.value.map(edge => `${edge.id}:${lookOf(edge)}`).join(' '))
watch(looks, () => {
  edges.value = edges.value.map((edge) => {
    const look = lookOf(edge)
    const classes = [edge.targetHandle === WHEN_HANDLE ? 'ps-edge-when' : '', isFrameLine(edge) ? 'ps-edge-frame' : '', look].filter(Boolean).join(' ')
    return { ...edge, animated: look === 'ps-edge-flowing', class: classes || undefined }
  })
})
const titleOf = (id: string) => nodes.value.find(node => node.id === id)?.data.title ?? 'a step'

const runStamp = (at: number) => new Date(at).toLocaleString('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
const problems = computed(() => nodes.value.filter(node => results.value.get(node.id)?.error))

/* The preview follows the selection; with none, it shows the first output. */
const previewNode = computed(() => {
  const node = selected.value ?? outputs.value[0] ?? null
  return node ? { id: node.id, position: node.position, data: node.data } as GraphNode : null
})
const previewOpen = useLocalStorage('playersense.reports.preview', true)

const ENVIRONMENT_OPTIONS: SegmentedOption[] = [
  { value: 'production', label: 'Production' },
  { value: 'staging', label: 'Staging' },
  { value: 'development', label: 'Development' }
]

/* An Export's file from a run, while it is kept. */
const liveExports = (run: ReportRun) => run.exports.filter(file => Date.parse(file.expires_at) > Date.now())

async function downloadFile(file: ReportExport) {
  await attempt(() => download(file))
}

const ids = { name: useId(), description: useId() }
</script>

<template>
  <!-- No report to show yet: on its way, refused, or none made. -->
  <div v-if="!report" class="flex h-full items-center justify-center overflow-y-auto p-6">
    <LoadState
      :error="error"
      :loading="loading"
      class="w-full max-w-xl"
      what="the reports"
      @retry="reload"
    >
      <div class="flex max-w-md flex-col items-center gap-4 rounded-(--ps-radius-x-large) border border-line bg-surface p-6 text-center">
        <span aria-hidden="true" class="flex size-10 items-center justify-center rounded-(--ps-radius-large) bg-primary/10 text-xl text-primary">
          <Icon name="material-symbols:account-tree-outline" />
        </span>
        <span class="flex flex-col gap-1">
          <span class="text-sm font-medium text-ink">No reports yet</span>
          <span class="text-xs leading-5 text-ink/60">A report reads the project's rows, shapes them, and ends in a chart, a table, a file or a message, by hand or on a schedule.</span>
        </span>
        <span class="flex flex-wrap justify-center gap-1.5">
          <v-btn
            :loading="busy"
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="newReport()"
          >
            Blank report
          </v-btn>
          <v-btn
            v-for="template in TEMPLATES"
            :key="template.key"
            :disabled="busy"
            class="text-xs"
            color="neutral"
            size="small"
            variant="outlined"
            @click="newReport(template)"
          >
            {{ template.name }}
          </v-btn>
        </span>
        <p v-if="problem" class="m-0 text-xs text-error">
          {{ problem }}
        </p>
      </div>
    </LoadState>
  </div>

  <div v-else class="flex h-full min-h-0">
    <!-- The parts. -->
    <aside class="hidden w-64 flex-none border-r border-line bg-surface lg:block" aria-label="Parts">
      <ReportPalette @add="addFromPalette" />
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <!-- The report: which one, and what can be done with it. -->
      <header class="flex h-12 flex-none items-center gap-3 border-b border-line bg-surface px-3">
        <Icon :aria-hidden="true" class="flex-none text-base text-ink/50" name="material-symbols:account-tree-outline" />
        <GhostSelect
          :model-value="reportId"
          :options="REPORT_OPTIONS"
          label="Report"
          size="small"
          @update:model-value="openReport(String($event))"
        />
        <span :class="saveError || problem ? 'text-error' : 'text-ink/45'" class="hidden truncate text-[11px] sm:inline">
          {{ problem || saveError || (saving ? 'Saving…' : 'Saved') }}
        </span>

        <span class="ml-auto flex items-center gap-1.5">
          <span v-if="problems.length" class="mr-1 hidden items-center gap-1 text-[11px] text-warning md:flex">
            <Icon :aria-hidden="true" class="text-sm" name="material-symbols:error-outline" />
            {{ problems.length }} to fix
          </span>
          <v-btn
            :disabled="testRunning || !outputs.some(node => node.data.kind === 'notify' || node.data.kind === 'export')"
            :loading="testRunning && !liveRun?.plan.length"
            class="text-xs font-medium"
            color="neutral"
            prepend-icon="material-symbols:play-arrow-outline"
            size="small"
            variant="outlined"
            @click="openTest()"
          >
            Test run
          </v-btn>
          <v-btn
            class="text-xs"
            color="neutral"
            prepend-icon="material-symbols:auto-awesome-mosaic-outline"
            size="small"
            variant="text"
            @click="tidy"
          >
            Tidy up
          </v-btn>
          <v-menu location="bottom end">
            <template #activator="{ props: menu }">
              <v-btn
                v-bind="menu"
                class="text-xs font-medium"
                color="primary"
                prepend-icon="material-symbols:add"
                size="small"
                variant="flat"
              >
                New report
              </v-btn>
            </template>
            <v-list density="compact" max-width="340" nav>
              <v-list-item
                prepend-icon="material-symbols:draft-outline"
                title="Blank"
                subtitle="An empty canvas"
                @click="newReport()"
              />
              <v-list-subheader class="text-[11px]">From a template</v-list-subheader>
              <v-list-item
                v-for="template in TEMPLATES"
                :key="template.key"
                :subtitle="template.note"
                :title="template.name"
                lines="two"
                prepend-icon="material-symbols:account-tree-outline"
                @click="newReport(template)"
              />
            </v-list>
          </v-menu>
          <v-menu location="bottom end">
            <template #activator="{ props: menu }">
              <v-btn
                v-bind="menu"
                aria-label="More"
                color="neutral"
                icon="material-symbols:more-vert"
                size="small"
                variant="text"
              />
            </template>
            <v-list density="compact" nav>
              <v-list-item prepend-icon="material-symbols:content-copy-outline" title="Duplicate report" @click="duplicateReport" />
              <v-list-item
                base-color="error"
                prepend-icon="material-symbols:delete-outline"
                title="Delete report"
                @click="deleting = true"
              />
            </v-list>
          </v-menu>
        </span>
      </header>

      <!-- The canvas. -->
      <div
        ref="canvas"
        class="relative min-h-0 flex-1"
        @dragover="onDragOver"
        @drop="onDrop"
      >
        <VueFlow
          v-model:edges="edges"
          v-model:nodes="nodes"
          :default-edge-options="{ type: 'default' }"
          :connection-mode="ConnectionMode.Strict"
          :is-valid-connection="isValidConnection"
          :max-zoom="1.5"
          :min-zoom="0.25"
          class="ps-flow"
          @connect="onConnect"
          @nodes-initialized="fit"
          @node-click="selectedId = $event.node.id"
          @pane-click="selectedId = null"
        >
          <Background :gap="18" :size="1.2" />
          <Controls :show-interactive="false" position="bottom-left">
            <ControlButton title="Tidy up" @click="tidy">
              <Icon :aria-hidden="true" name="material-symbols:auto-awesome-mosaic-outline" />
            </ControlButton>
          </Controls>

          <template #node-report="nodeProps">
            <ReportFlowNode
              :id="nodeProps.id"
              :data="nodeProps.data"
              :idle="Boolean(shownRun) && !shownRun!.nodes[nodeProps.id]"
              :result="results.get(nodeProps.id)"
              :run="shownRun?.nodes[nodeProps.id] ?? null"
              :selected="nodeProps.selected"
              :timing="timings.get(nodeProps.id) ?? null"
            />
          </template>
        </VueFlow>

        <!-- A test run, as it plays. -->
        <div v-if="liveRun" class="pointer-events-none absolute inset-x-0 top-3 z-10 flex justify-center">
          <ReportRunBar
            :run="liveRun"
            :title-of="titleOf"
            class="pointer-events-auto"
            @dismiss="clearRun"
            @results="testing = true"
            @stop="stopRun"
          />
        </div>

        <!-- An empty report: where to begin. -->
        <div v-if="nodes.every(node => isFrame(node.data.kind))" class="pointer-events-none absolute inset-0 flex items-center justify-center p-6">
          <div class="pointer-events-auto flex max-w-md flex-col items-center gap-4 rounded-(--ps-radius-x-large) border border-line bg-surface p-6 text-center shadow-[0_1px_2px_rgba(0,0,0,0.06)]">
            <span aria-hidden="true" class="flex size-10 items-center justify-center rounded-(--ps-radius-large) bg-primary/10 text-xl text-primary">
              <Icon name="material-symbols:account-tree-outline" />
            </span>
            <span class="flex flex-col gap-1">
              <span class="text-sm font-medium text-ink">Start with where the rows come from</span>
              <span class="text-xs leading-5 text-ink/60">Pick a source: it follows Start. Then add steps after it and end with a chart, a table or a file, which lead to Finish. Or drag parts from the left.</span>
            </span>
            <span class="flex flex-wrap justify-center gap-1.5">
              <v-btn
                v-for="key in SOURCE_KEYS"
                :key="key"
                :prepend-icon="SOURCES[key].icon"
                class="text-xs"
                color="neutral"
                size="small"
                variant="outlined"
                @click="addNode('source', { source: key }, { x: 0, y: 0 })"
              >
                {{ SOURCES[key].label }}
              </v-btn>
            </span>
          </div>
        </div>
      </div>

      <!-- What comes out of the selected node. -->
      <section :class="previewOpen ? 'h-64' : 'h-10'" class="flex flex-none flex-col border-t border-line bg-surface transition-[height] duration-200" aria-label="Preview">
        <header class="flex h-10 flex-none items-center gap-3 border-b border-line px-3">
          <button
            class="flex cursor-pointer items-center gap-1.5 text-xs font-medium text-ink"
            type="button"
            :aria-expanded="previewOpen"
            @click="previewOpen = !previewOpen"
          >
            <Icon
              :class="previewOpen ? '' : 'rotate-180'"
              :aria-hidden="true"
              class="text-base text-ink/50 transition-transform"
              name="material-symbols:keyboard-arrow-down"
            />
            Preview
          </button>
          <template v-if="previewNode">
            <span class="truncate text-xs text-ink/60">{{ previewNode.data.title }}</span>
            <span v-if="results.get(previewNode.id) && !results.get(previewNode.id)!.error" class="font-mono text-[11px] text-ink/45">
              {{ rowsOf(results.get(previewNode.id)).toLocaleString('en') }} rows
            </span>
            <span v-if="previewError" class="ml-auto truncate text-[11px] text-error">{{ previewError }}</span>
            <span v-else class="ml-auto hidden items-center gap-1.5 text-[11px] text-ink/40 sm:flex">
              <v-progress-circular
                v-if="previewing"
                indeterminate
                size="10"
                width="1.5"
              />
              The first {{ Math.min(50, rowsOf(results.get(previewNode.id))) || 50 }}, from {{ report.environment }}. Nothing is sent.
            </span>
          </template>
          <span v-else class="text-xs text-ink/45">Select a node to see what comes out of it.</span>
        </header>
        <div v-if="previewOpen && previewNode" class="min-h-0 flex-1">
          <ReportPreview :node="previewNode" :result="results.get(previewNode.id) ?? null" />
        </div>
      </section>
    </div>

    <!-- The selected node, or the report. -->
    <aside class="hidden w-[22rem] flex-none border-l border-line bg-surface md:block" aria-label="Settings">
      <ReportInspector
        v-if="selectedGraph"
        :key="selectedGraph.id"
        :input="selectedInput"
        :node="selectedGraph"
        :result="results.get(selectedGraph.id) ?? null"
        :drives="drives(selectedGraph.id)"
        :environment="report?.environment"
        :timing="timings.get(selectedGraph.id) ?? null"
        @remove="removeSelected"
        @test="openTest(selectedGraph.data.kind === 'notify' ? selectedGraph.id : null)"
        @update="updateNode(selectedGraph.id, $event)"
      />

      <div v-else-if="report" class="flex h-full flex-col">
        <!-- The report's own settings, in the node inspector's shape and fields. -->
        <header class="flex items-start gap-3 border-b border-line p-4">
          <span aria-hidden="true" class="flex size-8 flex-none items-center justify-center rounded-(--ps-radius) bg-primary/10 text-base text-primary">
            <Icon name="material-symbols:account-tree-outline" />
          </span>
          <span class="flex min-w-0 flex-1 flex-col gap-0.5">
            <span class="text-xs font-medium text-ink">Report</span>
            <span class="text-[11px] leading-4 text-ink/55">Its own settings. Select a node on the canvas to set that up instead.</span>
          </span>
        </header>
        <div class="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4">
          <div class="flex flex-col gap-1.5">
            <label :for="ids.name" class="text-xs text-ink/60">Name</label>
            <v-text-field
              :id="ids.name"
              :model-value="report.name"
              density="comfortable"
              hide-details
              maxlength="80"
              placeholder="Weekly revenue"
              variant="outlined"
              @update:model-value="save(report.id, { name: String($event ?? '') })"
            />
          </div>
          <div class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">Reads the rows of</span>
            <SegmentedControl
              :model-value="report.environment"
              :options="ENVIRONMENT_OPTIONS"
              label="Environment"
              stretch
              @update:model-value="save(report.id, { environment: $event as ReportEnvironment })"
            />
          </div>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.description" class="text-xs text-ink/60">Description <span class="text-ink/40">for the team</span></label>
            <v-textarea
              :id="ids.description"
              :model-value="report.description"
              auto-grow
              hide-details
              maxlength="500"
              placeholder="What it answers, and who reads it"
              rows="3"
              variant="outlined"
              @update:model-value="save(report.id, { description: String($event ?? '') })"
            />
          </div>

          <div class="flex flex-col gap-2 border-t border-line pt-4">
            <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
              Produces <span class="font-mono font-normal">{{ outputs.length }}</span>
            </p>
            <ul v-if="outputs.length" class="m-0 flex list-none flex-col gap-1.5 p-0">
              <li v-for="node in outputs" :key="node.id">
                <button class="flex w-full cursor-pointer items-start gap-2.5 rounded-(--ps-radius) border border-line px-2.5 py-2 text-left transition-colors duration-150 hover:bg-ink/3" type="button" @click="selectedId = node.id">
                  <Icon :name="KINDS[node.data.kind].icon" :aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/55" />
                  <span class="flex min-w-0 flex-col">
                    <span class="truncate text-xs text-ink">{{ node.data.title }}</span>
                    <span class="truncate text-[11px] text-ink/50">{{ summaryOf(node.data, results.get(node.id)?.columns ?? []).join(' · ') }}</span>
                  </span>
                </button>
              </li>
            </ul>
            <p v-else class="m-0 flex items-center gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs text-ink/70">
              <Icon :aria-hidden="true" class="text-base text-ink/45" name="material-symbols:bar-chart" />
              Nothing yet. End the report with a chart, a table or an export.
            </p>
          </div>

          <!-- When it runs. -->
          <div class="flex flex-col gap-2 border-t border-line pt-4">
            <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
              Schedule
            </p>
            <ul v-if="schedules.length" class="m-0 flex list-none flex-col gap-1.5 p-0">
              <li v-for="node in schedules" :key="node.id">
                <button class="flex w-full cursor-pointer items-start gap-2.5 rounded-(--ps-radius) border border-line px-2.5 py-2 text-left transition-colors duration-150 hover:bg-ink/3" type="button" @click="selectedId = node.id">
                  <Icon
                    :style="{ color: toneColour(familyTone('trigger')) }"
                    :aria-hidden="true"
                    class="mt-0.5 flex-none text-base"
                    name="material-symbols:schedule-outline"
                  />
                  <span class="flex min-w-0 flex-col">
                    <span class="truncate text-xs text-ink">{{ describeCron(node.data.config.cron ?? '') }}</span>
                    <span class="truncate text-[11px] text-ink/50">{{ timings.get(node.id) ?? 'Not set' }} · {{ drives(node.id).length ? 'runs the report' : 'not joined to Start' }}</span>
                  </span>
                </button>
              </li>
            </ul>
            <p v-else class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
              <Icon :aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:schedule-outline" />
              Runs only when you run it. Add a Schedule from the parts: it joins Start's top edge and runs the report on its own.
            </p>
          </div>

          <!-- What it has done. -->
          <div class="flex flex-col gap-2 border-t border-line pt-4">
            <p class="flex items-center justify-between gap-2 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
              <span>Runs <span class="font-mono font-normal">{{ runs.length }}</span></span>
            </p>
            <ol v-if="runs.length" class="m-0 flex list-none flex-col p-0">
              <li v-for="run in runs.slice(0, 8)" :key="run.id" class="flex flex-col gap-1 border-t border-line/60 py-2 first:border-t-0">
                <span class="flex items-center gap-2">
                  <span class="flex min-w-0 flex-1 flex-col">
                    <span class="text-xs text-ink">{{ runStamp(run.at) }}</span>
                    <span class="truncate text-[11px] text-ink/50">
                      {{ run.trigger === 'test' ? `Test run${run.requestedBy ? ` by ${run.requestedBy}` : ''}` : 'On schedule' }} · {{ run.error ? run.error : run.status === 'quiet' ? 'its rule kept it quiet' : `${run.deliveries.filter(entry => !entry.quiet && entry.ok).length} of ${run.deliveries.filter(entry => !entry.quiet).length} sent` }}{{ run.deliveries.some(entry => !entry.ok) ? ` · ${run.deliveries.find(entry => !entry.ok)!.detail}` : '' }}
                    </span>
                  </span>
                  <StatusBadge :label="RUN_STATUS[run.status].label" :tone="RUN_STATUS[run.status].tone" />
                </span>
                <button
                  v-for="file in liveExports(run)"
                  :key="file.node"
                  class="flex cursor-pointer items-center gap-1.5 text-left text-[11px] text-primary hover:underline"
                  type="button"
                  @click="downloadFile(file)"
                >
                  <Icon :aria-hidden="true" class="flex-none text-sm" name="material-symbols:download" />
                  <span class="truncate">{{ file.file_name }}, {{ file.rows.toLocaleString('en') }} rows</span>
                </button>
              </li>
            </ol>
            <p v-else class="m-0 text-xs text-ink/55">
              No runs yet. Test run sends it once now; a Schedule sends it from then on.
            </p>
            <p v-if="report.next_run_at" class="m-0 text-[11px] text-ink/50">
              Next on its schedule {{ runStamp(Date.parse(report.next_run_at)) }}.
            </p>
          </div>

          <div v-if="problems.length" class="flex flex-col gap-2 border-t border-line pt-4">
            <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
              To fix <span class="font-mono font-normal">{{ problems.length }}</span>
            </p>
            <ul class="m-0 flex list-none flex-col gap-1 p-0">
              <li v-for="node in problems" :key="node.id">
                <button class="flex w-full cursor-pointer items-start gap-2 text-left text-[11px] leading-4 text-ink/75 hover:text-ink" type="button" @click="selectedId = node.id">
                  <Icon :aria-hidden="true" class="mt-px flex-none text-sm text-warning" name="material-symbols:error-outline" />
                  <span><span class="font-medium">{{ node.data.title }}:</span> {{ results.get(node.id)?.error }}</span>
                </button>
              </li>
            </ul>
          </div>

          <div class="mt-auto flex flex-col gap-1 border-t border-line pt-4 text-[11px] leading-4 text-ink/50">
            <span><kbd class="rounded border border-line px-1 font-mono text-[10px]">Backspace</kbd> deletes what is selected.</span>
            <span>Drag from a node's right edge to another's left edge to connect them, and from a Schedule's bottom edge to Start's top. Start's and Finish's lines draw themselves.</span>
          </div>
        </div>
      </div>
    </aside>

    <ReportTestRun
      v-if="testing && report"
      v-model="testing"
      :edges="testGraph.edges"
      :focus="testFocus"
      :nodes="testGraph.nodes"
      :report="{ id: report.id, name: report.name }"
      :results="results"
      :environment="report.environment"
      :run="liveRun"
      @send="sendTest"
    />

    <ConfirmDialog
      v-if="deleting && report"
      v-model="deleting"
      :description="`${report.name} is deleted with every node in it. Scheduled exports in it stop. This cannot be undone.`"
      :title="`Delete ${report.name}?`"
      action="Delete report"
      tone="error"
      @confirm="confirmDelete"
    />
  </div>
</template>
