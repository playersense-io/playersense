<script setup lang="ts">
// One node on the Report Builder canvas: what it is, what it is set to, and what comes out
// of it, as a card. Its colour is its family's (sources, steps, outputs), so a report reads
// left to right as where the rows come from, what is done to them and where they go.
//
// Inputs are on the left and the output on the right. A join takes two inputs, labelled,
// since which side is which decides the rows it keeps; an output has nothing after it.
// Time runs down the page where rows run across: a Schedule's line leaves its bottom edge
// and enters Start's top, and every Notify and Export says when that has it send.
//
// Start and Finish are drawn smaller, as the ends of the report rather than steps in it. The
// lines that join them to the sources and the outputs are the report's own: their handles
// take no line drawn by hand.
//
// During a test run a card shows where the run is with it: waiting, running (ringed, and
// breathing), done with what came out and how long it took, or failed. A card the run does
// not need steps back.
import { Handle, Position } from '@vue-flow/core'

const props = defineProps<{
  id: string
  data: ReportNodeData
  selected?: boolean
  result?: NodeResult
  /** For a Schedule, its next run; for Start and a delivery, when the report runs; for Finish, what it waits for. */
  timing?: string | null
  /** Where a test run has got to with it, while one is shown. */
  run?: RunNode | null
  /** A test run is shown that does not run it. */
  idle?: boolean
}>()

const kind = computed(() => KINDS[props.data.kind])
const framed = computed(() => isFrame(props.data.kind))
const tone = computed(() => toneColour(familyTone(kind.value.family)))
const icon = computed(() => (props.data.kind === 'source' ? SOURCES[props.data.config.source ?? 'purchases'].icon : kind.value.icon))
const kindLabel = computed(() => (props.data.kind === 'source' ? `Source · ${SOURCES[props.data.config.source ?? 'purchases'].label}` : kind.value.label))

const summary = computed(() => summaryOf(props.data, props.result?.columns ?? []))

const rows = computed(() => rowsOf(props.result))

const frame = computed(() => {
  switch (props.run?.state) {
    case 'running': return 'ps-node-running ring-2 ring-primary'
    case 'done': return props.run.failed ? 'ring-2 ring-error/70' : 'ring-1 ring-success/60'
    case 'failed': return 'ring-2 ring-error/70'
    default: return props.selected ? 'ring-2 ring-primary' : 'ring-1 ring-line hover:ring-ink/25'
  }
})

/* What a delivery answered, in a few words. */
const answered = computed(() => {
  const run = props.run
  if (!run) return ''
  if (run.quiet && !run.sent && !run.failed) return 'Quiet this run'
  if (props.data.kind === 'export') return run.sent ? 'Written' : 'Writing'
  const tried = run.sent + run.failed
  return run.failed ? `${run.failed} of ${tried} failed` : `Sent to ${run.sent}`
})

const took = (ms: number) => (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`)

/* Start's or Finish's one line: where the run is with it, or what it is waiting for. */
const frameLine = computed(() => {
  const start = props.data.kind === 'start'
  switch (props.run?.state) {
    case 'running': return start ? 'Starting…' : 'Finishing…'
    case 'done': return start ? 'Started' : 'Done'
    case 'failed':
    case 'skipped': return props.run.error ?? 'Did not finish'
    case 'queued': return start ? 'About to start' : 'Waiting for the outputs'
    default: return props.timing ?? (start ? 'Runs when you run it' : 'Nothing leads to it yet')
  }
})
</script>

<template>
  <div
    :class="[frame, idle ? 'opacity-45' : '', framed ? 'w-48 rounded-full' : 'w-60 rounded-(--ps-radius-large)']"
    class="ps-report-node relative flex flex-col bg-surface shadow-[0_1px_2px_rgba(0,0,0,0.06)] transition-[box-shadow,opacity] duration-200"
  >
    <!-- Start and Finish: a pill, with what it is and one line under it. -->
    <div v-if="framed" class="flex items-center gap-2.5 py-2 pr-4 pl-2">
      <span
        :style="{ background: `color-mix(in srgb, ${tone} 14%, transparent)`, color: tone }"
        aria-hidden="true"
        class="flex size-8 flex-none items-center justify-center rounded-full text-lg"
      >
        <Icon :name="icon" />
      </span>
      <span class="flex min-w-0 flex-1 flex-col">
        <span class="truncate text-[13px] leading-4 font-medium text-ink">{{ data.title }}</span>
        <span
          :class="run?.state === 'running' ? 'font-medium text-primary' : run?.state === 'failed' ? 'text-error' : 'text-ink/50'"
          class="truncate text-[10px] leading-4"
        >{{ frameLine }}</span>
      </span>
      <v-progress-circular
        v-if="run?.state === 'running'"
        aria-label="Running"
        color="primary"
        indeterminate
        size="14"
        width="2"
      />
      <Icon
        v-else-if="run?.state === 'done'"
        aria-label="Done"
        class="flex-none text-base text-success"
        name="material-symbols:check-circle-outline"
      />
      <Icon
        v-else-if="run?.state === 'failed'"
        aria-label="Failed"
        class="flex-none text-base text-error"
        name="material-symbols:cancel-outline"
      />
    </div>

    <template v-else>
      <header class="flex items-center gap-2.5 px-3 pt-3 pb-2">
        <span
          :style="{ background: `color-mix(in srgb, ${tone} 14%, transparent)`, color: tone }"
          aria-hidden="true"
          class="flex size-7 flex-none items-center justify-center rounded-(--ps-radius-small) text-base"
        >
          <Icon :name="icon" />
        </span>
        <span class="flex min-w-0 flex-1 flex-col">
          <span class="truncate text-[13px] leading-4 font-medium text-ink">{{ data.title }}</span>
          <span class="truncate text-[10px] leading-4 text-ink/45">{{ kindLabel }}</span>
        </span>
        <span v-if="run" class="flex size-4 flex-none items-center justify-center">
          <v-progress-circular
            v-if="run.state === 'running'"
            aria-label="Running"
            color="primary"
            indeterminate
            size="14"
            width="2"
          />
          <Icon
            v-else-if="run.state === 'done' && run.failed"
            aria-label="Partly failed"
            class="text-base text-error"
            name="material-symbols:error-outline"
          />
          <Icon
            v-else-if="run.state === 'done'"
            aria-label="Done"
            class="text-base text-success"
            name="material-symbols:check-circle-outline"
          />
          <Icon
            v-else-if="run.state === 'failed'"
            aria-label="Failed"
            class="text-base text-error"
            name="material-symbols:cancel-outline"
          />
          <Icon
            v-else-if="run.state === 'skipped'"
            aria-label="Not run"
            class="text-base text-ink/35"
            name="material-symbols:block"
          />
          <Icon
            v-else
            aria-label="Waiting"
            class="text-base text-ink/30"
            name="material-symbols:hourglass-empty"
          />
        </span>
      </header>

      <ul class="m-0 flex list-none flex-col gap-0.5 px-3 pb-2.5 pl-[3.25rem]">
        <li v-for="(line, index) in summary" :key="index" class="truncate text-[11px] leading-4 text-ink/65">
          {{ line }}
        </li>
      </ul>

      <footer class="flex items-center gap-1.5 border-t border-line/70 px-3 py-1.5 text-[10px]">
        <!-- A test run's progress, in place of what the card says otherwise. -->
        <template v-if="run?.state === 'running'">
          <span class="truncate font-medium text-primary">{{ KINDS[data.kind].family === 'source' ? 'Reading rows…' : run.sent || run.failed ? `${answered}…` : 'Running…' }}</span>
        </template>
        <template v-else-if="run?.state === 'queued'">
          <span class="truncate text-ink/45">Waiting for its inputs</span>
        </template>
        <template v-else-if="run?.state === 'failed' || run?.state === 'skipped'">
          <Icon
            aria-hidden="true"
            class="flex-none text-xs"
            :class="run.state === 'failed' ? 'text-error' : 'text-ink/40'"
            name="material-symbols:error-outline"
          />
          <span :class="run.state === 'failed' ? 'text-error' : 'text-ink/50'" class="truncate">{{ run.error ?? 'Not run' }}</span>
        </template>
        <template v-else-if="run?.state === 'done'">
          <span class="font-mono text-ink/70">{{ (run.rows ?? 0).toLocaleString('en') }}</span>
          <span class="truncate text-ink/45">{{ run.rows === 1 ? 'row' : 'rows' }}<template v-if="isDelivery(data.kind)"> · <span :class="run.failed ? 'text-error' : 'text-success'">{{ answered }}</span></template></span>
          <span class="ml-auto flex-none font-mono text-ink/45">{{ took(run.ms ?? 0) }}</span>
        </template>
        <template v-else-if="result?.error">
          <Icon aria-hidden="true" class="flex-none text-xs text-warning" name="material-symbols:error-outline" />
          <span class="truncate text-warning">{{ result.error }}</span>
        </template>
        <template v-else-if="data.kind === 'schedule'">
          <Icon aria-hidden="true" class="flex-none text-xs text-ink/45" name="material-symbols:event-upcoming-outline" />
          <span class="truncate text-ink/55">{{ timing ?? 'Not running' }}</span>
        </template>
        <template v-else-if="result">
          <span class="font-mono text-ink/70">{{ rows.toLocaleString('en') }}</span>
          <span class="text-ink/45">{{ rows === 1 ? 'row' : 'rows' }} · {{ result.columns.length }} columns</span>
        </template>
      </footer>

      <!-- When a delivery runs: on the report's Schedules, or by hand. -->
      <p v-if="isDelivery(data.kind)" class="m-0 flex items-center gap-1.5 border-t border-line/70 px-3 py-1.5 text-[10px]">
        <Icon
          :class="timing ? '' : 'text-ink/35'"
          :style="timing ? { color: toneColour(familyTone('trigger')) } : undefined"
          aria-hidden="true"
          class="flex-none text-xs"
          name="material-symbols:schedule-outline"
        />
        <span :class="timing ? 'text-ink/65' : 'text-ink/40'" class="truncate">{{ timing ?? 'Runs when you run it' }}</span>
      </p>
    </template>

    <!-- Inputs. -->
    <template v-if="kind.inputs === 2">
      <Handle
        id="left"
        :position="Position.Left"
        :style="{ top: '38%' }"
        type="target"
      />
      <Handle
        id="right"
        :position="Position.Left"
        :style="{ top: '72%' }"
        type="target"
      />
      <span class="pointer-events-none absolute top-[38%] left-2 -translate-y-1/2 text-[9px] font-medium tracking-wide text-ink/40 uppercase">L</span>
      <span class="pointer-events-none absolute top-[72%] left-2 -translate-y-1/2 text-[9px] font-medium tracking-wide text-ink/40 uppercase">R</span>
    </template>
    <Handle v-else-if="kind.inputs === 1" :position="Position.Left" type="target" />

    <!-- The output. -->
    <Handle v-if="kind.output" :position="Position.Right" type="source" />

    <!-- Start's and Finish's lines: into a source's left edge, out of an output's right. -->
    <Handle
      v-if="data.kind === 'start'"
      :connectable="false"
      :position="Position.Right"
      class="ps-frame-handle"
      type="source"
    />
    <Handle
      v-if="data.kind === 'finish'"
      :connectable="false"
      :position="Position.Left"
      class="ps-frame-handle"
      type="target"
    />
    <Handle
      v-if="data.kind === 'source'"
      :id="START_HANDLE"
      :connectable="false"
      :position="Position.Left"
      class="ps-frame-handle"
      type="target"
    />
    <Handle
      v-if="kind.family === 'output'"
      :id="FINISH_HANDLE"
      :connectable="false"
      :position="Position.Right"
      class="ps-frame-handle"
      type="source"
    />

    <!-- Time: out of a Schedule's bottom, into Start's top. -->
    <Handle
      v-if="data.kind === 'schedule'"
      :id="WHEN_HANDLE"
      :position="Position.Bottom"
      class="ps-when-handle"
      type="source"
    />
    <Handle
      v-if="kind.scheduled"
      :id="WHEN_HANDLE"
      :position="Position.Top"
      class="ps-when-handle"
      type="target"
    />
  </div>
</template>
