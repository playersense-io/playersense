<script setup lang="ts">
// A report run now, the way its schedule would run it, to see that everything works before it
// is left to run on its own. Every delivery is shown with what it would do: a Notify with
// whether its rule lets it send and why, and the message as each of its channels will show it;
// an Export with the file it would write and where. Nothing leaves until Send is pressed; then
// every channel is sent to, each answers, and the run is kept in the report's history.
const props = defineProps<{
  report: { id: string, name: string }
  nodes: readonly GraphNode[]
  edges: readonly GraphEdge[]
  results: ReadonlyMap<string, NodeResult>
  /** One Notify only, from its own "Preview and send a test". */
  focus?: string | null
}>()

const open = defineModel<boolean>({ required: true })

const { channels, send } = useChannels()
const { addRun } = useReports()

const at = Date.now()

/* The schedule that runs a delivery, in words, or null for one that runs by hand. */
function timingOf(id: string): string | null {
  const edge = props.edges.find(entry => entry.target === id && entry.targetHandle === WHEN_HANDLE)
  const schedule = edge ? props.nodes.find(node => node.id === edge.source) : null
  if (!schedule) return null
  const words = `${describeCron(schedule.data.config.cron ?? '')}, ${CRON_TIMEZONES.find(zone => zone.value === schedule.data.config.timezone)?.label ?? 'UTC'}`
  return schedule.data.config.paused ? `${words}, paused` : words
}

interface Step {
  node: GraphNode
  result: NodeResult | null
  timing: string | null
  outcome: NotifyOutcome | null
  channels: NotificationChannel[]
}

const steps = computed<Step[]>(() => props.nodes
  .filter(node => (node.data.kind === 'notify' || node.data.kind === 'export') && (!props.focus || node.id === props.focus))
  .map((node) => {
    const result = props.results.get(node.id) ?? null
    return {
      node,
      result,
      timing: timingOf(node.id),
      outcome: node.data.kind === 'notify' && result && !result.error ? evaluateNotify(node, result, props.report, at) : null,
      channels: (node.data.config.channels ?? []).map(id => channels.value.find(channel => channel.id === id)).filter((channel): channel is NotificationChannel => Boolean(channel))
    }
  }))

/* The channel whose preview is showing, a Notify at a time. */
const showing = ref<Record<string, string>>({})
const shownChannel = (step: Step) => step.channels.find(channel => channel.id === showing.value[step.node.id]) ?? step.channels[0] ?? null

const fileOf = (step: Step) => `${fileSlug(props.report.name)}.${step.node.data.config.file ?? 'json'}`
const destinationOf = (step: Step) => {
  const config = step.node.data.config
  const where = DESTINATIONS[config.destination ?? 'download']
  return config.destination && config.destination !== 'download' ? `${config.target || where.target} (${where.label})` : 'a download here'
}

// --- Sending ------------------------------------------------------------------------------

const sending = ref(false)
const answers = ref<Record<string, { ok: boolean, detail: string, ms: number }>>({})
const sent = ref(false)

async function sendAll() {
  sending.value = true
  answers.value = {}
  const deliveries: RunDelivery[] = []
  for (const step of steps.value) {
    if (step.result?.error) continue
    await new Promise(resolve => setTimeout(resolve, 350))
    if (step.node.data.kind === 'export') {
      const detail = `Wrote ${fileOf(step)}, ${step.result?.rows.length.toLocaleString('en')} rows, to ${destinationOf(step)}`
      answers.value = { ...answers.value, [step.node.id]: { ok: true, detail, ms: 400 } }
      deliveries.push({ node: step.node.id, title: step.node.data.title, kind: 'export', ok: true, detail })
      continue
    }
    for (const channel of step.channels) {
      if (!step.outcome?.send) {
        deliveries.push({ node: step.node.id, title: step.node.data.title, kind: 'notify', channel: channel.id, ok: true, quiet: true, detail: step.outcome?.reason ?? 'Quiet' })
        continue
      }
      const answer = send(channel, step.outcome.message.title, `${props.report.name} (test run)`, false)
      answers.value = { ...answers.value, [`${step.node.id}:${channel.id}`]: answer }
      deliveries.push({ node: step.node.id, title: step.node.data.title, kind: 'notify', channel: channel.id, ok: answer.ok, detail: answer.detail })
    }
  }
  const tried = deliveries.filter(entry => !entry.quiet)
  const failed = tried.filter(entry => !entry.ok).length
  addRun(props.report.id, {
    id: `run_${Math.random().toString(36).slice(2, 10)}`,
    at: Date.now(),
    trigger: 'test',
    dry: false,
    status: !tried.length ? 'quiet' : !failed ? 'succeeded' : failed === tried.length ? 'failed' : 'partial',
    deliveries
  })
  sending.value = false
  sent.value = true
}

const failures = computed(() => Object.values(answers.value).filter(answer => !answer.ok).length)
const nothing = computed(() => !steps.value.length)

function stateOf(step: Step): { label: string, tone: BadgeTone } {
  if (step.result?.error) return { label: 'Fix first', tone: 'warning' }
  if (step.node.data.kind === 'export') return answers.value[step.node.id] ? { label: 'Written', tone: 'success' } : { label: 'Would write', tone: 'neutral' }
  if (!step.outcome?.send) return { label: 'Quiet this run', tone: 'neutral' }
  const mine = step.channels.map(channel => answers.value[`${step.node.id}:${channel.id}`]).filter(Boolean)
  if (!mine.length) return { label: 'Would send', tone: 'neutral' }
  return mine.every(answer => answer!.ok) ? { label: 'Sent', tone: 'success' } : { label: 'Partly failed', tone: 'error' }
}
</script>

<template>
  <v-dialog v-model="open" max-width="760" scrollable>
    <v-card border flat>
      <header class="flex items-start gap-3 border-b border-line p-5">
        <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-primary/10 text-lg text-primary">
          <Icon name="material-symbols:play-circle-outline" />
        </span>
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            Test run: {{ report.name }}
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            Runs {{ focus ? 'this step' : 'the report' }} now, on the sample rows, the way its schedule would, and shows what {{ focus ? 'it' : 'each delivery' }} would send. Nothing is sent until you press Send.
          </p>
        </div>
      </header>

      <div class="overflow-y-auto">
        <div class="flex flex-col gap-4 p-5">
          <p v-if="nothing" class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
            <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:info-outline" />
            Nothing to run: the report has no Notify or Export yet. Add one at its end.
          </p>

          <section v-for="step in steps" :key="step.node.id" class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border border-line">
            <header class="flex items-center gap-2.5 border-b border-line bg-ink/2 px-4 py-2.5">
              <Icon :name="KINDS[step.node.data.kind].icon" aria-hidden="true" class="flex-none text-base text-ink/55" />
              <span class="flex min-w-0 flex-1 flex-col">
                <span class="truncate text-xs font-medium text-ink">{{ step.node.data.title }}</span>
                <span class="truncate text-[11px] text-ink/50">{{ step.timing ?? 'Runs when you run it' }}</span>
              </span>
              <StatusBadge :label="stateOf(step).label" :tone="stateOf(step).tone" />
            </header>

            <div class="flex flex-col gap-3 p-4">
              <p v-if="step.result?.error" class="m-0 flex items-start gap-2 text-xs leading-5 text-ink/75">
                <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:error-outline" />
                {{ step.result.error }}
              </p>

              <!-- An export: the file, and where. -->
              <template v-else-if="step.node.data.kind === 'export'">
                <p class="m-0 flex items-start gap-2 text-xs leading-5 text-ink/75">
                  <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-ink/45" name="material-symbols:description-outline" />
                  <span>
                    <span class="font-mono text-ink">{{ fileOf(step) }}</span>, {{ step.result?.rows.length.toLocaleString('en') }} rows, to {{ destinationOf(step) }}.
                  </span>
                </p>
                <p v-if="answers[step.node.id]" class="m-0 flex items-center gap-2 text-[11px] text-success">
                  <Icon aria-hidden="true" class="text-sm" name="material-symbols:check-circle-outline" />
                  {{ answers[step.node.id]!.detail }}.
                </p>
              </template>

              <!-- A notify: its rule, then the message in each channel. -->
              <template v-else-if="step.outcome">
                <p class="m-0 flex items-start gap-2 text-xs leading-5 text-ink/75">
                  <Icon
                    :class="step.outcome.send ? 'text-success' : 'text-ink/45'"
                    :name="step.outcome.send ? 'material-symbols:check-circle-outline' : 'material-symbols:do-not-disturb-on-outline'"
                    aria-hidden="true"
                    class="mt-0.5 flex-none text-sm"
                  />
                  <span><span class="text-ink/50">{{ notifyWhenWords(step.node.data.config, key => step.result?.columns.find(column => column.key === key)?.label ?? key ?? '') }}.</span> {{ step.outcome.reason }}</span>
                </p>

                <template v-if="step.channels.length">
                  <div class="flex flex-wrap gap-1.5" role="tablist" aria-label="Channels">
                    <button
                      v-for="channel in step.channels"
                      :key="channel.id"
                      :aria-selected="shownChannel(step)?.id === channel.id"
                      :class="shownChannel(step)?.id === channel.id ? 'border-primary bg-primary/6' : 'border-line hover:bg-ink/3'"
                      class="flex items-center gap-2 rounded-(--ps-radius-small) border px-2 py-1.5 text-left transition-colors duration-150"
                      role="tab"
                      type="button"
                      @click="showing = { ...showing, [step.node.id]: channel.id }"
                    >
                      <ChannelMark :type="channel.type" size="small" />
                      <span class="text-xs text-ink">{{ channel.name }}</span>
                      <Icon
                        v-if="answers[`${step.node.id}:${channel.id}`]"
                        :class="answers[`${step.node.id}:${channel.id}`]!.ok ? 'text-success' : 'text-error'"
                        :name="answers[`${step.node.id}:${channel.id}`]!.ok ? 'material-symbols:check-circle-outline' : 'material-symbols:cancel-outline'"
                        aria-hidden="true"
                        class="text-sm"
                      />
                      <span v-else-if="statusOf(channel) === 'failing'" class="text-[10px] text-error">Failing</span>
                      <span v-else-if="statusOf(channel) === 'off'" class="text-[10px] text-ink/45">Off</span>
                    </button>
                  </div>

                  <template v-if="shownChannel(step)">
                    <NotificationPreview :at="at" :channel="shownChannel(step)!" :message="step.outcome.message" />
                    <p
                      v-if="answers[`${step.node.id}:${shownChannel(step)!.id}`]"
                      :class="answers[`${step.node.id}:${shownChannel(step)!.id}`]!.ok ? 'text-success' : 'text-error'"
                      class="m-0 text-[11px] leading-4"
                    >
                      {{ answers[`${step.node.id}:${shownChannel(step)!.id}`]!.ok ? 'Arrived' : 'Did not arrive' }}: {{ answers[`${step.node.id}:${shownChannel(step)!.id}`]!.detail }}.
                    </p>
                    <p v-else-if="!step.outcome.send" class="m-0 text-[11px] leading-4 text-ink/50">
                      This is what it would say. On this run its rule keeps it quiet, so Send skips it.
                    </p>
                  </template>
                </template>
              </template>
            </div>
          </section>
        </div>
      </div>

      <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-line px-5 py-3">
        <span class="text-[11px] text-ink/55">
          <template v-if="sent">
            {{ failures ? `Sent, with ${failures} ${failures === 1 ? 'channel' : 'channels'} failing. Fix ${failures === 1 ? 'it' : 'them'} under Notification channels.` : 'Sent. The run is in the report\'s history.' }}
          </template>
          <template v-else>
            Sending reaches the channels for real and is kept in the report's runs.
          </template>
        </span>
        <span class="flex items-center gap-2">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Close
          </v-btn>
          <v-btn
            :disabled="nothing || steps.every(step => step.result?.error)"
            :loading="sending"
            class="text-xs font-medium"
            color="primary"
            prepend-icon="material-symbols:send-outline"
            size="small"
            variant="flat"
            @click="sendAll"
          >
            {{ sent ? 'Send again' : 'Send' }}
          </v-btn>
        </span>
      </footer>
    </v-card>
  </v-dialog>
</template>
