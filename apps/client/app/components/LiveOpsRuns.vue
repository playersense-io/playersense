<script setup lang="ts">
// Every window a recurring event ran, as a bar chart of what each did: a column a window, a bar
// from zero to its effect on the metric the event is meant to move, the figure above it and the
// dates under it. Nothing else: how sure each figure is lives in the table below the chart. The
// window being read below is the highlighted column, and the next one is an empty column, so
// the chart ends where the calendar picks up. Beside it, the three numbers people ask of a series: on average, at best,
// and lately, and whether it is wearing off.
const props = defineProps<{
  runs: readonly { run: Occurrence, impact: EventImpact }[]
  goal: LiveOpsMetricKey
  /** One window a week is too little to read for a daily event: a column is then a week of them. */
  rhythm: boolean
  next: Occurrence | null
}>()

const selected = defineModel<number | null>({ required: true })

const PLOT = 136

const comparisons = computed(() => props.runs.map(entry => entry.impact.metrics[props.goal].comparison))

/* The scale: zero, and the largest effect either way. */
const top = computed(() => Math.max(0.02, ...comparisons.value.map(item => item.lift)) * 1.05)
const bottom = computed(() => Math.min(0, ...comparisons.value.map(item => item.lift)) * 1.05)
const y = (value: number) => ((top.value - value) / (top.value - bottom.value)) * PLOT
const zero = computed(() => y(0))

const TONES = { better: 'bg-success', worse: 'bg-error', unclear: 'bg-ink/35' } as const
const TEXT = { better: 'text-success', worse: 'text-error', unclear: 'text-ink/60' } as const

const label = (run: Occurrence) => (props.rhythm ? `Week of ${windowShort({ start: run.start, end: run.start + 60_000 })}` : windowShort(run))

// --- The summary --------------------------------------------------------------------------

const ended = computed(() => props.runs.filter(entry => !entry.impact.live))

const average = computed(() => {
  const list = ended.value.length ? ended.value : props.runs
  return list.reduce((sum, entry) => sum + entry.impact.metrics[props.goal].comparison.lift, 0) / Math.max(list.length, 1)
})

const best = computed(() => [...props.runs].sort((a, b) => b.impact.metrics[props.goal].comparison.lift - a.impact.metrics[props.goal].comparison.lift)[0] ?? null)
const latest = computed(() => props.runs.at(-1) ?? null)

/* Its latest windows against its first ones: under four-fifths of it is wearing off. */
const wearing = computed(() => {
  if (ended.value.length < 4) return null
  const half = Math.floor(ended.value.length / 2)
  const mean = (list: typeof ended.value) => list.reduce((sum, entry) => sum + entry.impact.metrics[props.goal].comparison.lift, 0) / Math.max(list.length, 1)
  const early = mean(ended.value.slice(0, half))
  const late = mean(ended.value.slice(half))
  return early > 0 && late < early * 0.8 ? { early, late } : null
})

const worked = computed(() => ended.value.filter(entry => outcomeOf(entry.impact.metrics[props.goal].comparison) === 'better').length)
</script>

<template>
  <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_15rem]">
    <!-- The chart. -->
    <div class="min-w-0">
      <div class="relative">
        <!-- Zero, the line the bars stand on. -->
        <span :style="{ top: `calc(1.5rem + ${zero}px)` }" aria-hidden="true" class="pointer-events-none absolute inset-x-0 h-px bg-ink/25" />

        <div class="relative flex" role="radiogroup" :aria-label="`${LIVEOPS_METRICS[goal].label} each time it ran`">
          <button
            v-for="entry in runs"
            :key="entry.run.start"
            :aria-checked="entry.run.start === selected"
            :aria-label="`${label(entry.run)}: ${writeOpsLift(goal, entry.impact.metrics[goal].comparison)}`"
            :class="entry.run.start === selected ? 'bg-primary/8' : 'hover:bg-ink/3'"
            class="group flex min-w-0 flex-1 cursor-pointer flex-col items-center rounded-(--ps-radius) px-1 pb-2 transition-colors duration-150 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary"
            role="radio"
            type="button"
            @click="selected = entry.run.start"
          >
            <!-- The figure, over the plot. -->
            <span :class="TEXT[outcomeOf(entry.impact.metrics[goal].comparison)]" class="flex h-6 items-end pb-1 font-mono text-[11px] leading-none font-medium">
              {{ writeOpsLift(goal, entry.impact.metrics[goal].comparison) }}
            </span>

            <span :style="{ height: `${PLOT}px` }" class="relative w-full">
              <!-- The bar, from zero to the effect. -->
              <span
                :class="[TONES[outcomeOf(entry.impact.metrics[goal].comparison)], entry.run.start === selected ? 'opacity-100' : 'opacity-70 group-hover:opacity-90']"
                :style="{
                  top: `${Math.min(zero, y(entry.impact.metrics[goal].comparison.lift))}px`,
                  height: `${Math.max(2, Math.abs(zero - y(entry.impact.metrics[goal].comparison.lift)))}px`
                }"
                class="absolute left-1/2 w-[min(60%,2.25rem)] -translate-x-1/2 rounded-[3px] transition-opacity duration-150"
              />
            </span>

            <span :class="entry.run.start === selected ? 'text-ink' : 'text-ink/55'" class="mt-2 flex max-w-full items-center gap-1 text-[11px] leading-4">
              <span v-if="entry.impact.live" aria-label="On now" class="size-1.5 flex-none rounded-full bg-success" />
              <span class="truncate">{{ label(entry.run) }}</span>
            </span>
          </button>

          <!-- The next window: nothing to read yet, so an empty column that says when. -->
          <div v-if="next" class="flex min-w-0 flex-1 flex-col items-center px-1 pb-2">
            <span class="flex h-6 items-end pb-1 font-mono text-[11px] leading-none text-ink/35">next</span>
            <span :style="{ height: `${PLOT}px` }" class="relative w-full">
              <span
                :style="{ top: `${Math.max(0, zero - 36)}px`, height: '36px' }"
                class="absolute left-1/2 w-[min(60%,2.25rem)] -translate-x-1/2 rounded-[3px] border border-dashed border-ink/25"
              />
            </span>
            <span class="mt-2 max-w-full truncate text-[11px] leading-4 text-ink/40">{{ windowShort(next) }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- What the series says. -->
    <dl class="m-0 flex flex-col gap-4 border-t border-line pt-4 lg:border-t-0 lg:border-l lg:pt-0 lg:pl-6">
      <div class="flex flex-col gap-0.5">
        <dt class="text-[11px] text-ink/50">
          On average
        </dt>
        <dd class="m-0 flex items-baseline gap-2">
          <span :class="average > 0 ? 'text-success' : 'text-ink'" class="font-mono text-xl leading-7">{{ `${average >= 0 ? '+' : ''}${(average * 100).toFixed(1)}%` }}</span>
          <span class="text-[11px] text-ink/50">{{ worked }} of {{ ended.length }} clearly worked</span>
        </dd>
      </div>
      <div v-if="best" class="flex flex-col gap-0.5">
        <dt class="text-[11px] text-ink/50">
          At best
        </dt>
        <dd class="m-0 flex items-baseline gap-2">
          <span class="font-mono text-sm text-ink">{{ writeOpsLift(goal, best.impact.metrics[goal].comparison) }}</span>
          <button class="cursor-pointer text-[11px] text-ink/55 underline decoration-ink/20 underline-offset-2 hover:text-ink" type="button" @click="selected = best.run.start">
            {{ label(best.run) }}
          </button>
        </dd>
      </div>
      <div v-if="latest" class="flex flex-col gap-0.5">
        <dt class="text-[11px] text-ink/50">
          {{ latest.impact.live ? 'On now' : 'Latest' }}
        </dt>
        <dd class="m-0 flex items-baseline gap-2">
          <span class="font-mono text-sm text-ink">{{ writeOpsLift(goal, latest.impact.metrics[goal].comparison) }}</span>
          <button class="cursor-pointer text-[11px] text-ink/55 underline decoration-ink/20 underline-offset-2 hover:text-ink" type="button" @click="selected = latest.run.start">
            {{ label(latest.run) }}
          </button>
        </dd>
      </div>
      <p v-if="wearing" class="m-0 flex items-start gap-2 rounded-(--ps-radius) bg-warning/8 px-3 py-2.5 text-[11px] leading-4 text-ink/75 ring-1 ring-warning/20 ring-inset">
        <Icon aria-hidden="true" class="mt-px flex-none text-sm text-warning" name="material-symbols:trending-down" />
        <span>Wearing off: {{ (wearing.early * 100).toFixed(0) }}% in its first windows, {{ (wearing.late * 100).toFixed(0) }}% lately. Run it less often, or change what it gives.</span>
      </p>
    </dl>
  </div>
</template>
