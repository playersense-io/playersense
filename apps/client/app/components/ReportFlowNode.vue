<script setup lang="ts">
// One node on the Report Builder canvas: what it is, what it is set to, and what comes out
// of it, as a card. Its colour is its family's (sources, steps, outputs), so a report reads
// left to right as where the rows come from, what is done to them and where they go.
//
// Inputs are on the left and the output on the right. A join takes two inputs, labelled,
// since which side is which decides the rows it keeps; an output has nothing after it.
// Time runs down the page where rows run across: a Schedule's line leaves its bottom edge
// and enters the top of the Notify and Export steps it runs, which say when that is.
import { Handle, Position } from '@vue-flow/core'

const props = defineProps<{
  id: string
  data: ReportNodeData
  selected?: boolean
  result?: NodeResult
  /** For a Schedule, its next run; for a delivery, the schedule that runs it, in words. */
  timing?: string | null
}>()

const kind = computed(() => KINDS[props.data.kind])
const tone = computed(() => toneColour(familyTone(kind.value.family)))
const icon = computed(() => (props.data.kind === 'source' ? SOURCES[props.data.config.source ?? 'purchases'].icon : kind.value.icon))
const kindLabel = computed(() => (props.data.kind === 'source' ? `Source · ${SOURCES[props.data.config.source ?? 'purchases'].label}` : kind.value.label))

const summary = computed(() => summaryOf(props.data, props.result?.columns ?? []))

const rows = computed(() => props.result?.rows.length ?? 0)
</script>

<template>
  <div
    :class="selected ? 'ring-2 ring-primary' : 'ring-1 ring-line hover:ring-ink/25'"
    class="ps-report-node relative flex w-60 flex-col rounded-(--ps-radius-large) bg-surface shadow-[0_1px_2px_rgba(0,0,0,0.06)] transition-shadow"
  >
    <header class="flex items-center gap-2.5 px-3 pt-3 pb-2">
      <span
        :style="{ background: `color-mix(in srgb, ${tone} 14%, transparent)`, color: tone }"
        aria-hidden="true"
        class="flex size-7 flex-none items-center justify-center rounded-(--ps-radius-small) text-base"
      >
        <Icon :name="icon" />
      </span>
      <span class="flex min-w-0 flex-col">
        <span class="truncate text-[13px] leading-4 font-medium text-ink">{{ data.title }}</span>
        <span class="truncate text-[10px] leading-4 text-ink/45">{{ kindLabel }}</span>
      </span>
    </header>

    <ul class="m-0 flex list-none flex-col gap-0.5 px-3 pb-2.5 pl-[3.25rem]">
      <li v-for="(line, index) in summary" :key="index" class="truncate text-[11px] leading-4 text-ink/65">
        {{ line }}
      </li>
    </ul>

    <footer class="flex items-center gap-1.5 border-t border-line/70 px-3 py-1.5 text-[10px]">
      <template v-if="result?.error">
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

    <!-- When a delivery runs: its schedule, or by hand. -->
    <p v-if="kind.scheduled" class="m-0 flex items-center gap-1.5 border-t border-line/70 px-3 py-1.5 text-[10px]">
      <Icon
        :class="timing ? '' : 'text-ink/35'"
        :style="timing ? { color: toneColour(familyTone('trigger')) } : undefined"
        aria-hidden="true"
        class="flex-none text-xs"
        name="material-symbols:schedule-outline"
      />
      <span :class="timing ? 'text-ink/65' : 'text-ink/40'" class="truncate">{{ timing ?? 'Runs when you run it: connect a Schedule' }}</span>
    </p>

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

    <!-- Time: out of a Schedule's bottom, into a delivery's top. -->
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
