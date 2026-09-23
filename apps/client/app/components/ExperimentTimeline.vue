<script setup lang="ts">
// Every test on one calendar, a lane each: when it ran, how it ended, and what is running now.
// It answers the question a list of rows cannot, "what did we test in the spring, and did any
// of it overlap?", at a glance.
//
// A bar is the days a test ran, coloured by how it ended: the accent while it runs, green when
// a variant won, grey when none did, amber when it was stopped. A running test's bar carries on
// as an outline to its planned end, so what is left is visible too. Today is a line.
const props = defineProps<{
  experiments: readonly ExperimentDef[]
}>()

const DAY = 86_400_000

const now = Date.now()

interface Lane {
  experiment: ExperimentDef
  start: number
  end: number
  planned: number | null
}

const lanes = computed<Lane[]>(() => props.experiments
  .filter(entry => entry.started_at)
  .map((entry) => {
    const start = Date.parse(entry.started_at!)
    const end = entry.ended_at ? Date.parse(entry.ended_at) : now
    return { experiment: entry, start, end, planned: entry.status === 'running' ? start + entry.plannedDays * DAY : null }
  })
  .sort((a, b) => b.start - a.start))

/* The span shown: the first start to the furthest planned end, a little past on either side. */
const span = computed(() => {
  const starts = lanes.value.map(lane => lane.start)
  const ends = lanes.value.map(lane => lane.planned ?? lane.end)
  const from = Math.min(...starts, now - 90 * DAY) - 7 * DAY
  const to = Math.max(...ends, now) + 10 * DAY
  return { from, to }
})

const at = (time: number) => ((time - span.value.from) / (span.value.to - span.value.from)) * 100

const months = computed(() => {
  const marks: { label: string, left: number }[] = []
  const cursor = new Date(span.value.from)
  cursor.setDate(1)
  cursor.setMonth(cursor.getMonth() + 1)
  while (cursor.getTime() < span.value.to) {
    marks.push({ label: cursor.toLocaleDateString('en', { month: 'short', ...(cursor.getMonth() === 0 ? { year: 'numeric' } : {}) }), left: at(cursor.getTime()) })
    cursor.setMonth(cursor.getMonth() + 1)
  }
  return marks
})

function barClass(experiment: ExperimentDef): string {
  if (experiment.status === 'running') return 'bg-primary'
  if (experiment.status === 'stopped') return 'bg-warning/70'
  return experiment.winner ? 'bg-success/75' : 'bg-ink/25'
}

function outcome(experiment: ExperimentDef): string {
  if (experiment.status === 'running') return 'Running'
  if (experiment.status === 'stopped') return 'Stopped early'
  const winner = experiment.variants.find(entry => entry.id === experiment.winner)
  return winner ? `${winner.name} won` : 'No clear winner'
}
</script>

<template>
  <div class="flex flex-col">
    <!-- The months, over the lanes. -->
    <div class="grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)] gap-4">
      <span />
      <div class="relative h-6 border-b border-line">
        <span
          v-for="month in months"
          :key="month.label + month.left"
          :style="{ left: `${month.left}%` }"
          class="absolute bottom-1.5 -translate-x-1/2 text-[11px] text-ink/45"
        >{{ month.label }}</span>
      </div>
    </div>

    <ol class="m-0 list-none p-0">
      <li v-for="lane in lanes" :key="lane.experiment.id" class="grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)] items-center gap-4 border-b border-line/60 last:border-b-0">
        <NuxtLink :to="`/growth/experiments/${lane.experiment.id}`" class="group flex min-w-0 flex-col py-2.5 no-underline">
          <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ lane.experiment.name }}</span>
          <span class="truncate text-[11px] text-ink/50">{{ outcome(lane.experiment) }}</span>
        </NuxtLink>

        <div class="relative h-10">
          <!-- Month rules, faint, so a bar can be read against the calendar. -->
          <span
            v-for="month in months"
            :key="month.label + month.left"
            :style="{ left: `${month.left}%` }"
            aria-hidden="true"
            class="absolute inset-y-0 w-px bg-ink/5"
          />
          <span :style="{ left: `${at(now)}%` }" aria-hidden="true" class="absolute inset-y-0 w-px bg-primary/40" />
          <!-- What is left of a running test's plan. -->
          <span
            v-if="lane.planned && lane.planned > lane.end"
            :style="{ left: `${at(lane.end)}%`, width: `${at(lane.planned) - at(lane.end)}%` }"
            aria-hidden="true"
            class="absolute top-1/2 h-3 -translate-y-1/2 rounded-r-full border border-dashed border-primary/60"
          />
          <v-tooltip location="top" :text="`${lane.experiment.name}: ${whenWords(lane.experiment)}. ${outcome(lane.experiment)}.`">
            <template #activator="{ props: tip }">
              <NuxtLink
                v-bind="tip"
                :aria-label="`${lane.experiment.name}, ${whenWords(lane.experiment)}`"
                :class="[barClass(lane.experiment), lane.planned ? 'rounded-l-full' : 'rounded-full']"
                :style="{ left: `${at(lane.start)}%`, width: `${Math.max(at(lane.end) - at(lane.start), 0.6)}%` }"
                :to="`/growth/experiments/${lane.experiment.id}`"
                class="absolute top-1/2 h-3 -translate-y-1/2 transition-opacity hover:opacity-80"
              />
            </template>
          </v-tooltip>
        </div>
      </li>
    </ol>

    <!-- Today, across every lane. -->
    <div class="pointer-events-none relative -mt-px grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)] gap-4">
      <span />
      <div class="relative h-5">
        <span :style="{ left: `${at(now)}%` }" class="absolute top-1 -translate-x-1/2 text-[10px] font-medium text-primary">Today</span>
      </div>
    </div>

    <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-ink/55">
      <span class="flex items-center gap-1.5"><span class="h-2 w-4 rounded-full bg-primary" />Running</span>
      <span class="flex items-center gap-1.5"><span class="h-2 w-4 rounded-full border border-dashed border-primary/60" />Planned</span>
      <span class="flex items-center gap-1.5"><span class="h-2 w-4 rounded-full bg-success/75" />A variant won</span>
      <span class="flex items-center gap-1.5"><span class="h-2 w-4 rounded-full bg-ink/25" />No clear winner</span>
      <span class="flex items-center gap-1.5"><span class="h-2 w-4 rounded-full bg-warning/70" />Stopped early</span>
    </div>
  </div>
</template>
