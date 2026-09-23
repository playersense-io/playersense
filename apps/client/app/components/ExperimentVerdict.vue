<script setup lang="ts">
// The answer a test's page opens with, before any chart: a heading that says it in a few words
// ("Regional price is winning", "Too early to call"), the figures behind it in a sentence, how
// far through its planned days the test is, and any guardrail a variant made worse, said out
// loud whatever the headline is. The page's own actions (start, stop, ship) sit beside it.
const props = defineProps<{
  experiment: ExperimentDef
  verdict: Verdict
}>()

const LOOK: Record<VerdictKind, { icon: string, tone: string }> = {
  'draft': { icon: 'material-symbols:edit-note-outline', tone: 'bg-ink/6 text-ink/60' },
  'collecting': { icon: 'material-symbols:hourglass-top-outline', tone: 'bg-primary/10 text-primary' },
  'leading': { icon: 'material-symbols:trending-up', tone: 'bg-success/12 text-success' },
  'ready': { icon: 'material-symbols:trophy-outline', tone: 'bg-success/12 text-success' },
  'won': { icon: 'material-symbols:trophy-outline', tone: 'bg-success/12 text-success' },
  'control': { icon: 'material-symbols:trending-down', tone: 'bg-warning/12 text-warning' },
  'unclear': { icon: 'material-symbols:balance', tone: 'bg-ink/6 text-ink/60' },
  'no-winner': { icon: 'material-symbols:balance', tone: 'bg-ink/6 text-ink/60' },
  'stopped': { icon: 'material-symbols:pause-circle-outline', tone: 'bg-warning/12 text-warning' }
}

const days = computed(() => daysRun(props.experiment))
const progress = computed(() => Math.min(1, days.value / props.experiment.plannedDays))

/* Days after today: on day 34 of 35 there is one to go, on day 35 it is the last. */
const left = computed(() => props.experiment.plannedDays - (days.value + 1))
</script>

<template>
  <section class="flex flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div class="flex min-w-0 flex-1 basis-96 items-start gap-4">
        <span :class="LOOK[verdict.kind].tone" aria-hidden="true" class="flex size-11 flex-none items-center justify-center rounded-(--ps-radius-large)">
          <Icon :name="LOOK[verdict.kind].icon" class="text-2xl" />
        </span>
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-lg leading-tight font-semibold tracking-tight text-ink">
            {{ verdict.title }}
          </h2>
          <p class="m-0 text-sm leading-6 text-ink/65">
            {{ verdict.detail }}
          </p>
        </div>
      </div>
      <div v-if="$slots.default" class="flex flex-none flex-wrap items-center gap-2">
        <slot />
      </div>
    </div>

    <!-- How far through its plan: the part that answers "can I trust this yet?" -->
    <div v-if="experiment.status === 'running'" class="flex flex-col gap-1.5">
      <div class="flex items-center justify-between text-[11px] text-ink/55">
        <span>Day {{ Math.min(days + 1, experiment.plannedDays) }} of {{ experiment.plannedDays }}</span>
        <span v-if="days < MIN_DAYS">A result is worth reading from day {{ MIN_DAYS }}</span>
        <span v-else-if="left > 0">{{ left }} {{ left === 1 ? 'day' : 'days' }} to go</span>
        <span v-else-if="left === 0">Last day</span>
        <span v-else>Planned days are up</span>
      </div>
      <span class="relative h-1.5 overflow-hidden rounded-full bg-ink/8">
        <span :style="{ width: `${progress * 100}%` }" class="absolute inset-y-0 left-0 rounded-full bg-primary" />
        <span :style="{ left: `${(MIN_DAYS / experiment.plannedDays) * 100}%` }" aria-hidden="true" class="absolute inset-y-0 w-0.5 bg-surface" />
      </span>
    </div>

    <ul v-if="verdict.breaches.length" class="m-0 flex list-none flex-col gap-2 p-0">
      <li
        v-for="breach in verdict.breaches"
        :key="breach.variant.id + breach.metric"
        class="flex items-start gap-2.5 rounded-(--ps-radius) bg-warning/8 px-3 py-2.5 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset"
      >
        <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:warning-outline" />
        <span>
          Guardrail: {{ breach.variant.name }} lowered {{ EXPERIMENT_METRICS[breach.metric].label.toLowerCase() }}
          by {{ writeLift(breach.metric, breach.comparison).replace('-', '') }}, and it is {{ writeChance(1 - breach.comparison.chance) }} sure it is really worse.
        </span>
      </li>
    </ul>
  </section>
</template>
