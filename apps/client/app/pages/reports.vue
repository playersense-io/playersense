<script setup lang="ts">
// Report Builder: a workspace rather than a page, so it fills the window under the bar
// (layouts/canvas.vue). The parts on the left, the canvas in the middle, the selected
// node's settings on the right, and under the canvas what comes out of it.
//
// A report is built by connecting parts: sources to steps to outputs. Every node runs as it
// is changed (utils/report-builder.ts, over sample rows), so the preview and each card's
// row count are always the report as it stands. Reports save themselves in this browser.
import { type Connection, type Edge, type Node, useVueFlow, VueFlow } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { ControlButton, Controls } from '@vue-flow/controls'

definePageMeta({ layout: 'canvas' })

const route = useRoute()
const router = useRouter()

const { reports, reportById, create, save, duplicate, remove } = useReports()
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

function load(id: string) {
  const entry = reportById(id)
  nodes.value = (entry?.nodes ?? []).map(node => ({ id: node.id, type: 'report', position: { ...node.position }, data: plain(node.data) }))
  edges.value = (entry?.edges ?? []).map(edge => ({ id: edge.id, source: edge.source, target: edge.target, targetHandle: edge.targetHandle ?? null }))
  selectedId.value = null
  savedShape = snapshot()
  fitPending = true
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
  edges: edges.value.map(edge => ({ id: edge.id, source: edge.source, target: edge.target, targetHandle: edge.targetHandle ?? null }))
}))

const day = Math.floor(Date.now() / 86_400_000) * 86_400_000

const results = computed(() => {
  const graph = JSON.parse(shape.value) as { nodes: GraphNode[], edges: GraphEdge[] }
  return runReport(graph.nodes, graph.edges, day)
})

// --- Saving -------------------------------------------------------------------------------

/*
 * What is saved, as one string. The canvas rewrites its nodes for things that are not the
 * report (their measured size, whether they are selected), so a save happens only when this
 * string has changed: a move, a setting, a line.
 */
function snapshot() {
  return JSON.stringify([
    nodes.value.map(node => [node.id, Math.round(node.position.x), Math.round(node.position.y), node.data]),
    edges.value.map(edge => [edge.id, edge.source, edge.target, edge.targetHandle ?? null])
  ])
}
let savedShape = ''

watch(reportId, load, { immediate: true })

const saving = ref(false)
const lastSaved = ref<string | null>(report.value?.updated_at ?? null)

const persist = useDebounceFn(() => {
  if (!report.value) return
  save(report.value.id, {
    nodes: nodes.value.map(node => ({ id: node.id, position: { x: Math.round(node.position.x), y: Math.round(node.position.y) }, data: plain(node.data) })),
    edges: edges.value.map(edge => ({ id: edge.id, source: edge.source, target: edge.target, targetHandle: edge.targetHandle ?? null }))
  })
  saving.value = false
  lastSaved.value = new Date().toISOString()
}, 500)

watch([nodes, edges], () => {
  const next = snapshot()
  if (next === savedShape) return
  savedShape = next
  saving.value = true
  persist()
}, { deep: true })

// --- Changing nodes -----------------------------------------------------------------------

const selected = computed(() => nodes.value.find(node => node.id === selectedId.value) ?? null)
const selectedGraph = computed<GraphNode | null>(() => (selected.value ? { id: selected.value.id, position: selected.value.position, data: selected.value.data } : null))

/* What flows into the selected node: its (left) input's result. */
const selectedInput = computed(() => {
  if (!selected.value) return null
  const into = edges.value.filter(edge => edge.target === selected.value!.id)
  const first = into.find(edge => edge.targetHandle !== 'right') ?? into[0]
  return first ? results.value.get(first.source) ?? null : null
})

function updateNode(id: string, patch: Partial<ReportNodeData>) {
  nodes.value = nodes.value.map(node => (node.id === id ? { ...node, data: { ...node.data, ...patch } } : node))
}

function newNodeId(kind: NodeKind) {
  return `${kind}-${Math.random().toString(36).slice(2, 8)}`
}

function addNode(kind: NodeKind, seed: NodeConfig, position: { x: number, y: number }, after?: string) {
  const config = defaultConfig(kind, seed)
  const node: FlowNode = { id: newNodeId(kind), type: 'report', position, data: { kind, title: defaultTitle(kind, config), config } }
  nodes.value = [...nodes.value, node]
  if (after && KINDS[kind].inputs > 0) {
    edges.value = [...edges.value, { id: `e-${after}-${node.id}`, source: after, target: node.id, targetHandle: KINDS[kind].inputs === 2 ? 'left' : null }]
  }
  selectedId.value = node.id
}

/* A click in the palette: after the selected node when it can take one, else in the middle. */
function addFromPalette(kind: NodeKind, seed: NodeConfig) {
  const from = selected.value
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
  if (!selectedId.value) return
  removeNodes([selectedId.value])
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

function isValidConnection(connection: Connection) {
  return connection.source !== connection.target && !reaches(connection.target, connection.source)
}

/* An input takes one line: a new one replaces what was there. */
function onConnect(connection: Connection) {
  const handle = connection.targetHandle ?? null
  edges.value = [
    ...edges.value.filter(edge => !(edge.target === connection.target && (edge.targetHandle ?? null) === handle)),
    { id: `e-${connection.source}-${connection.target}${handle ? `-${handle}` : ''}`, source: connection.source, target: connection.target, targetHandle: handle }
  ]
}

// --- Tidying ------------------------------------------------------------------------------

/* Every node in a column by how many steps it is from its source, in its current order. */
function tidy() {
  const depth = new Map<string, number>()
  const depthOf = (id: string, trail = new Set<string>()): number => {
    if (depth.has(id)) return depth.get(id)!
    if (trail.has(id)) return 0
    trail.add(id)
    const into = edges.value.filter(edge => edge.target === id)
    const value = into.length ? Math.max(...into.map(edge => depthOf(edge.source, trail))) + 1 : 0
    depth.set(id, value)
    return value
  }
  const columns = new Map<number, FlowNode[]>()
  for (const node of nodes.value) {
    const column = depthOf(node.id)
    columns.set(column, [...(columns.get(column) ?? []), node])
  }
  const placed = new Map<string, { x: number, y: number }>()
  for (const [column, members] of columns) {
    members.sort((a, b) => a.position.y - b.position.y)
    members.forEach((node, index) => placed.set(node.id, { x: column * 300, y: (index - (members.length - 1) / 2) * 190 }))
  }
  nodes.value = nodes.value.map(node => ({ ...node, position: placed.get(node.id) ?? node.position }))
  nextTick(() => fitView({ padding: 0.25, maxZoom: 1, duration: 300 }))
}

// --- Reports ------------------------------------------------------------------------------

function openReport(id: string) {
  reportId.value = id
}

function newReport(template?: ReportTemplate) {
  openReport(create(template))
}

function duplicateReport() {
  if (!report.value) return
  const id = duplicate(report.value.id)
  if (id) openReport(id)
}

const deleting = ref(false)
function confirmDelete() {
  if (!report.value) return
  const gone = report.value.id
  remove(gone)
  const next = reports.value[0]?.id
  if (next) openReport(next)
  else newReport()
}

// --- What it produces ---------------------------------------------------------------------

const outputs = computed(() => nodes.value.filter(node => KINDS[node.data.kind].family === 'output'))
const scheduled = computed(() => outputs.value.filter(node => node.data.kind === 'export' && node.data.config.schedule && node.data.config.schedule !== 'once'))
const problems = computed(() => nodes.value.filter(node => results.value.get(node.id)?.error))

/* The preview follows the selection; with none, it shows the first output. */
const previewNode = computed(() => {
  const node = selected.value ?? outputs.value[0] ?? null
  return node ? { id: node.id, position: node.position, data: node.data } as GraphNode : null
})
const previewOpen = useLocalStorage('playersense.reports.preview', true)

const ids = { name: useId(), description: useId() }
</script>

<template>
  <div class="flex h-full min-h-0">
    <!-- The parts. -->
    <aside class="hidden w-64 flex-none border-r border-line bg-surface lg:block" aria-label="Parts">
      <ReportPalette @add="addFromPalette" />
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <!-- The report: which one, and what can be done with it. -->
      <header class="flex h-12 flex-none items-center gap-3 border-b border-line bg-surface px-3">
        <Icon aria-hidden="true" class="flex-none text-base text-ink/50" name="material-symbols:account-tree-outline" />
        <GhostSelect
          :model-value="reportId"
          :options="REPORT_OPTIONS"
          label="Report"
          size="small"
          @update:model-value="openReport(String($event))"
        />
        <span class="hidden text-[11px] text-ink/45 sm:inline">
          {{ saving ? 'Saving…' : lastSaved ? 'Saved' : '' }}
        </span>

        <span class="ml-auto flex items-center gap-1.5">
          <span v-if="problems.length" class="mr-1 hidden items-center gap-1 text-[11px] text-warning md:flex">
            <Icon aria-hidden="true" class="text-sm" name="material-symbols:error-outline" />
            {{ problems.length }} to fix
          </span>
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
              <Icon aria-hidden="true" name="material-symbols:auto-awesome-mosaic-outline" />
            </ControlButton>
          </Controls>

          <template #node-report="nodeProps">
            <ReportFlowNode
              :id="nodeProps.id"
              :data="nodeProps.data"
              :result="results.get(nodeProps.id)"
              :selected="nodeProps.selected"
            />
          </template>
        </VueFlow>

        <!-- An empty report: where to begin. -->
        <div v-if="!nodes.length" class="pointer-events-none absolute inset-0 flex items-center justify-center p-6">
          <div class="pointer-events-auto flex max-w-md flex-col items-center gap-4 rounded-(--ps-radius-x-large) border border-line bg-surface p-6 text-center shadow-[0_1px_2px_rgba(0,0,0,0.06)]">
            <span aria-hidden="true" class="flex size-10 items-center justify-center rounded-(--ps-radius-large) bg-primary/10 text-xl text-primary">
              <Icon name="material-symbols:account-tree-outline" />
            </span>
            <span class="flex flex-col gap-1">
              <span class="text-sm font-medium text-ink">Start with where the rows come from</span>
              <span class="text-xs leading-5 text-ink/60">Pick a source, then add steps after it and end with a chart, a table or a file. Or drag parts from the left.</span>
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
              aria-hidden="true"
              class="text-base text-ink/50 transition-transform"
              name="material-symbols:keyboard-arrow-down"
            />
            Preview
          </button>
          <template v-if="previewNode">
            <span class="truncate text-xs text-ink/60">{{ previewNode.data.title }}</span>
            <span v-if="results.get(previewNode.id) && !results.get(previewNode.id)!.error" class="font-mono text-[11px] text-ink/45">
              {{ results.get(previewNode.id)!.rows.length.toLocaleString('en') }} rows
            </span>
            <span class="ml-auto hidden text-[11px] text-ink/40 sm:inline">Sample rows. Reports run on your full data once they run on the server.</span>
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
        @remove="removeSelected"
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
                  <Icon :name="KINDS[node.data.kind].icon" aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/55" />
                  <span class="flex min-w-0 flex-col">
                    <span class="truncate text-xs text-ink">{{ node.data.title }}</span>
                    <span class="truncate text-[11px] text-ink/50">{{ summaryOf(node.data, results.get(node.id)?.columns ?? []).join(' · ') }}</span>
                  </span>
                </button>
              </li>
            </ul>
            <p v-else class="m-0 flex items-center gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs text-ink/70">
              <Icon aria-hidden="true" class="text-base text-ink/45" name="material-symbols:bar-chart" />
              Nothing yet. End the report with a chart, a table or an export.
            </p>
            <p v-if="scheduled.length" class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-primary/6 px-3 py-2.5 text-xs leading-5 text-ink/75">
              <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-primary" name="material-symbols:schedule-outline" />
              <span>{{ scheduled.length === 1 ? 'One export runs' : `${scheduled.length} exports run` }} on a schedule: {{ scheduled.map(node => `${node.data.title}, ${SCHEDULES[node.data.config.schedule ?? 'once'].replace(/^./, first => first.toLowerCase())}`).join('; ') }}.</span>
            </p>
          </div>

          <div v-if="problems.length" class="flex flex-col gap-2 border-t border-line pt-4">
            <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
              To fix <span class="font-mono font-normal">{{ problems.length }}</span>
            </p>
            <ul class="m-0 flex list-none flex-col gap-1 p-0">
              <li v-for="node in problems" :key="node.id">
                <button class="flex w-full cursor-pointer items-start gap-2 text-left text-[11px] leading-4 text-ink/75 hover:text-ink" type="button" @click="selectedId = node.id">
                  <Icon aria-hidden="true" class="mt-px flex-none text-sm text-warning" name="material-symbols:error-outline" />
                  <span><span class="font-medium">{{ node.data.title }}:</span> {{ results.get(node.id)?.error }}</span>
                </button>
              </li>
            </ul>
          </div>

          <div class="mt-auto flex flex-col gap-1 border-t border-line pt-4 text-[11px] leading-4 text-ink/50">
            <span><kbd class="rounded border border-line px-1 font-mono text-[10px]">Backspace</kbd> deletes what is selected.</span>
            <span>Drag from a node's right edge to another's left edge to connect them.</span>
          </div>
        </div>
      </div>
    </aside>

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
