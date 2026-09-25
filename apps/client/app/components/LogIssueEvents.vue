<script setup lang="ts">
import type { LogRange, LogRecord, LogRecordSummary } from '~/composables/logs'

/**
 * An issue's events, one at a time: the newest first, and a step to the next older or newer, as a
 * person compares two occurrences of one bug to see what they have in common. Each is the record
 * whole, laid out for a page (LogRecordPanel's wide view): what was thrown, the stack and what came
 * before it in the main column, the device and attributes beside it.
 *
 * The events are the explorer's own query, `issue:<id>`, paged newest first; stepping past the
 * last one loaded fetches the next page.
 */
const props = defineProps<{
  path: string | null
  issueId: string
  range: LogRange
  /** The issue's events in the range, for "3 of 68". */
  total: number
  /** The newest, already read with the issue, so the first shows without a second request. */
  latest: LogRecord | null
}>()

const emit = defineEmits<{ filter: [clause: string] }>()

const path = computed(() => props.path)
const range = computed(() => props.range)
const query = computed(() => `issue:${props.issueId}`)
const events = useLogRecords(path, range, query)

const index = ref(0)
const read = shallowRef<Map<string, LogRecord>>(new Map())
const loading = ref(false)
const failure = ref('')

watch(() => props.latest, (latest) => {
  if (latest) read.value = new Map(read.value).set(latest.id, latest)
}, { immediate: true })
watch(query, () => {
  index.value = 0
})

const summary = computed<LogRecordSummary | undefined>(() => events.records.value[index.value])
const record = computed(() => {
  const id = summary.value?.id ?? props.latest?.id
  return id ? read.value.get(id) ?? null : null
})

watch(summary, async (entry) => {
  if (!entry || !props.path || read.value.has(entry.id)) return
  loading.value = true
  failure.value = ''
  try {
    const whole = await readLogRecord(props.path, entry)
    read.value = new Map(read.value).set(entry.id, whole)
  } catch (caught) {
    failure.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    loading.value = false
  }
}, { immediate: true })

const loaded = computed(() => events.records.value.length)
const canOlder = computed(() => index.value < loaded.value - 1 || !events.done.value)

async function older() {
  if (index.value >= loaded.value - 1 && !events.done.value) await events.more()
  if (index.value < events.records.value.length - 1) index.value++
}

const now = useNow({ interval: 30_000 })
const full = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

const shown = computed(() => record.value ?? null)
</script>

<template>
  <section class="flex flex-col overflow-hidden rounded-(--ps-radius-x-large) border border-line bg-surface">
    <header class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line px-5 py-3.5">
      <div class="flex min-w-0 flex-1 flex-col gap-0.5">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ index === 0 ? 'Latest event' : 'Event' }}
          </h2>
          <span class="font-mono text-[11px] text-ink/50">{{ writeFigure(index + 1) }} of {{ writeFigure(Math.max(total, loaded)) }}</span>
          <Tag v-if="shown?.kind === 'crash'" label="Crash" tone="error" />
          <Tag v-else-if="shown?.kind === 'exception'" :label="shown.handled ? 'Handled' : 'Unhandled'" :tone="shown.handled ? 'neutral' : 'warning'" />
        </div>
        <span v-if="shown" class="text-[11px] text-ink/55">
          <span class="font-mono">{{ full.format(new Date(shown.at)) }}</span>
          · {{ relativeWords(Date.parse(shown.at), now.getTime()) }}
          <template v-if="shown.app_version"> · release <span class="font-mono">{{ shown.app_version }}</span></template>
          <template v-if="shown.device_model"> · <span class="font-mono">{{ shown.device_model }}</span></template>
        </span>
      </div>

      <div class="flex flex-none items-center gap-1">
        <CopyButton v-if="shown" :value="shown.id" label="Copy the event id" />
        <v-btn
          :disabled="index === 0"
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="index = 0"
        >
          Newest
        </v-btn>
        <v-btn
          :disabled="index === 0"
          aria-label="Newer event"
          class="ps-toolbar-control"
          density="comfortable"
          icon="material-symbols:chevron-left"
          size="small"
          variant="outlined"
          @click="index--"
        />
        <v-btn
          :disabled="!canOlder"
          :loading="events.loading.value && index >= loaded - 1"
          aria-label="Older event"
          class="ps-toolbar-control"
          density="comfortable"
          icon="material-symbols:chevron-right"
          size="small"
          variant="outlined"
          @click="older"
        />
      </div>
    </header>

    <LogRecordPanel
      :error="failure"
      :issue-link="false"
      :loading="loading && !record"
      :record="record"
      wide
      @filter="(value) => emit('filter', value)"
    />
  </section>
</template>
