<script setup lang="ts">
// A test run as it plays on the canvas, over the top of it: what is running now and how far
// the run has got, with a stop; once it has ended, how it went, what was sent, and the way
// back to every channel's answer.
const props = defineProps<{
  run: LiveRun
  /** A node's title, by its id. */
  titleOf: (id: string) => string
}>()

const emit = defineEmits<{ stop: [], results: [], dismiss: [] }>()

const live = computed(() => props.run.status === 'starting' || props.run.status === 'running')
const entries = computed(() => props.run.plan.map(id => props.run.nodes[id]).filter((entry): entry is RunNode => Boolean(entry)))
const ended = computed(() => entries.value.filter(entry => entry.state !== 'queued' && entry.state !== 'running').length)
const broken = computed(() => entries.value.filter(entry => entry.state === 'failed').length)

const running = computed(() => props.run.plan.filter(id => props.run.nodes[id]?.state === 'running').map(props.titleOf))

const tried = computed(() => props.run.deliveries.filter(entry => !entry.quiet))
const sent = computed(() => tried.value.filter(entry => entry.ok).length)

const LOOK: Record<Exclude<LiveRun['status'], 'starting' | 'running'>, { icon: string, colour: string, label: string }> = {
  succeeded: { icon: 'material-symbols:check-circle-outline', colour: 'text-success', label: RUN_STATUS.succeeded.label },
  partial: { icon: 'material-symbols:error-outline', colour: 'text-warning', label: RUN_STATUS.partial.label },
  failed: { icon: 'material-symbols:cancel-outline', colour: 'text-error', label: RUN_STATUS.failed.label },
  quiet: { icon: 'material-symbols:do-not-disturb-on-outline', colour: 'text-ink/50', label: RUN_STATUS.quiet.label },
  stopped: { icon: 'material-symbols:stop-circle-outline', colour: 'text-ink/50', label: 'Stopped' }
}
const look = computed(() => (live.value ? null : LOOK[props.run.status as keyof typeof LOOK]))

const headline = computed(() => {
  if (props.run.status === 'starting') return 'Starting the test run'
  if (live.value) {
    const [first, ...rest] = running.value
    return first ? `Running ${first}${rest.length ? ` and ${rest.length} more` : ''}` : 'Running'
  }
  return `Test run: ${look.value!.label}`
})

const seconds = computed(() => (((props.run.finishedAt ?? Date.now()) - props.run.startedAt) / 1000).toFixed(1))

const detail = computed(() => {
  const plan = props.run.plan.length
  if (live.value) return plan ? `${ended.value} of ${plan} steps done` : 'Asking for a run'
  const parts = [
    tried.value.length ? `${sent.value} of ${tried.value.length} sent` : props.run.deliveries.length ? 'its rule kept it quiet' : 'nothing sent',
    ...(broken.value ? [`${broken.value} ${broken.value === 1 ? 'step' : 'steps'} failed`] : []),
    ...(props.run.status === 'stopped' ? [`${ended.value} of ${plan} steps run`] : []),
    `${seconds.value} s`
  ]
  return parts.join(' · ')
})

const progress = computed(() => (props.run.plan.length ? (ended.value / props.run.plan.length) * 100 : 0))
</script>

<template>
  <div
    aria-live="polite"
    class="relative flex max-w-[calc(100%-2rem)] items-center gap-3 overflow-hidden rounded-(--ps-radius-large) border border-line bg-surface py-2 pr-2 pl-3 shadow-[0_4px_16px_rgba(0,0,0,0.08)]"
    role="status"
  >
    <span class="flex size-5 flex-none items-center justify-center">
      <v-progress-circular
        v-if="live"
        color="primary"
        indeterminate
        size="16"
        width="2"
      />
      <Icon
        v-else
        :class="look!.colour"
        :name="look!.icon"
        aria-hidden="true"
        class="text-lg"
      />
    </span>

    <span class="flex min-w-0 flex-col">
      <span class="truncate text-xs font-medium text-ink">{{ headline }}</span>
      <span class="truncate text-[11px] text-ink/55">{{ detail }}</span>
    </span>

    <span class="ml-2 flex flex-none items-center gap-1">
      <v-btn
        v-if="live"
        class="text-xs"
        color="neutral"
        prepend-icon="material-symbols:stop-outline"
        size="small"
        variant="text"
        @click="emit('stop')"
      >
        Stop
      </v-btn>
      <template v-else>
        <v-btn
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="outlined"
          @click="emit('results')"
        >
          What was sent
        </v-btn>
        <v-btn
          aria-label="Close the test run"
          color="neutral"
          icon="material-symbols:close"
          size="small"
          variant="text"
          @click="emit('dismiss')"
        />
      </template>
    </span>

    <!-- How far it has got. -->
    <span
      v-if="live"
      :style="{ width: `${progress}%` }"
      aria-hidden="true"
      class="absolute bottom-0 left-0 h-0.5 bg-primary transition-[width] duration-300"
    />
  </div>
</template>
