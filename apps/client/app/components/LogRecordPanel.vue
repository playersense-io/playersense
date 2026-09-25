<script setup lang="ts">
import type { LogRecord } from '~/composables/logs'

/**
 * One record whole: what it said, where it was thrown, what happened before, and on what. Used
 * beside the list and on an issue's page, so it reads the same wherever a record is opened.
 *
 * Every value somebody would want more of is a filter: the logger, the release, the install, a
 * player, an attribute. Clicking one hands a search clause up, and the list narrows to it.
 */
const props = withDefaults(defineProps<{
  record: LogRecord | null
  loading?: boolean
  error?: string
  /** The panel sits in a list's split view and can be closed. */
  closable?: boolean
  /** Links to the record's issue. Off on the issue's own page. */
  issueLink?: boolean
}>(), { loading: false, error: '', closable: false, issueLink: true })

const emit = defineEmits<{ close: [], filter: [clause: string] }>()

const colors = useLogColors()

const full = new Intl.DateTimeFormat('en', { weekday: 'short', year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, timeZoneName: 'short' })
const crumbClock = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

const when = computed(() => {
  if (!props.record) return ''
  const at = new Date(props.record.at)
  return `${full.format(at)}, ${String(at.getMilliseconds()).padStart(3, '0')} ms`
})

/* Where it was thrown first: most runtimes print the innermost frame first, and so does this. */
const frames = computed(() => [...(props.record?.frames ?? [])].reverse())
const showLibrary = ref(false)
watch(() => props.record?.id, () => {
  showLibrary.value = false
})

/* Runs of engine and library frames fold into one line until asked for; the game's own frames
   are the ones a developer can do something about. */
const shownFrames = computed(() => {
  if (showLibrary.value) return frames.value.map(frame => ({ frame, folded: 0 }))
  const out: { frame: (typeof frames.value)[number] | null, folded: number }[] = []
  for (const frame of frames.value) {
    if (frame.in_app || !frames.value.some(entry => entry.in_app)) out.push({ frame, folded: 0 })
    else if (out.at(-1)?.frame === null) out.at(-1)!.folded++
    else out.push({ frame: null, folded: 1 })
  }
  return out
})

const attributes = computed(() => Object.entries(props.record?.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b)))

interface Fact { label: string, value: string, filter?: string, mono?: boolean, copy?: boolean }

const facts = computed<Fact[]>(() => {
  const r = props.record
  if (!r) return []
  const out: Fact[] = []
  if (r.app_version) out.push({ label: 'Release', value: r.app_build ? `${r.app_version} (${r.app_build})` : r.app_version, filter: clause('release', r.app_version), mono: true })
  if (r.platform) out.push({ label: 'Platform', value: [r.platform, r.os_version].filter(Boolean).join(' '), filter: clause('platform', r.platform) })
  if (r.device_model) out.push({ label: 'Device', value: r.device_model, filter: clause('device', r.device_model), mono: true })
  if (r.country_code) out.push({ label: 'Country', value: r.country_code, filter: clause('country', r.country_code) })
  if (r.logger) out.push({ label: 'Source', value: r.logger, filter: clause('logger', r.logger), mono: true })
  out.push({ label: 'Install', value: r.install_id, filter: clause('install', r.install_id), mono: true, copy: true })
  if (r.player_id) out.push({ label: 'Player', value: r.player_id, filter: clause('player', r.player_id), mono: true, copy: true })
  if (r.session_id) out.push({ label: 'Session', value: r.session_id, filter: clause('session', r.session_id), mono: true, copy: true })
  if (r.sdk) out.push({ label: 'SDK', value: r.sdk, mono: true })
  out.push({ label: 'Size', value: writeFigure(r.size, { format: 'bytes' }) })
  return out
})

const LEVEL_WORDS: Record<string, string> = { trace: 'Trace', debug: 'Debug', info: 'Info', warn: 'Warning', error: 'Error', fatal: 'Fatal' }

const headline = computed(() => {
  const r = props.record
  if (!r) return ''
  if (r.kind !== 'log' && r.exception_type) return r.exception_type
  return LEVEL_WORDS[r.level] ?? r.level
})
</script>

<template>
  <div class="flex min-h-0 flex-col">
    <header class="flex flex-none items-start gap-3 border-b border-line px-4 py-3">
      <div class="flex min-w-0 flex-1 flex-col gap-1">
        <div class="flex flex-wrap items-center gap-2">
          <span
            v-if="record"
            :style="{ background: colors[bandOf(record.level)] }"
            aria-hidden="true"
            class="size-2 flex-none rounded-full"
          />
          <h2 class="m-0 truncate text-sm font-medium text-ink">
            {{ loading && !record ? 'Loading record' : headline }}
          </h2>
          <Tag v-if="record?.kind === 'crash'" label="Crash" tone="error" />
          <Tag v-else-if="record?.kind === 'exception'" :label="record.handled ? 'Handled' : 'Unhandled'" :tone="record.handled ? 'neutral' : 'warning'" />
        </div>
        <span v-if="record" class="font-mono text-[11px] text-ink/50">{{ when }}</span>
      </div>
      <NuxtLink
        v-if="record?.issue && issueLink"
        :to="`/logs/issues/${record.issue}`"
        class="flex flex-none items-center gap-1 rounded-(--ps-radius-small) px-2 py-1 text-[11px] font-medium text-primary no-underline hover:bg-primary/8"
      >
        Issue
        <Icon aria-hidden="true" name="material-symbols:arrow-outward" />
      </NuxtLink>
      <v-btn
        v-if="closable"
        aria-label="Close the record"
        density="comfortable"
        icon="material-symbols:close"
        size="small"
        variant="text"
        @click="emit('close')"
      />
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto">
      <p v-if="error" class="m-0 p-4 text-xs text-error">
        {{ error }}
      </p>
      <div v-else-if="loading && !record" class="flex flex-col gap-2 p-4">
        <span
          v-for="index in 6"
          :key="index"
          :style="{ width: `${40 + ((index * 19) % 55)}%` }"
          class="block h-3 animate-pulse rounded bg-ink/8"
        />
      </div>

      <div v-else-if="record" class="flex flex-col gap-5 p-4">
        <!-- What it said. -->
        <section v-if="record.message || record.exception_value" class="flex flex-col gap-1.5">
          <h3 class="log-panel__heading">
            {{ record.kind === 'log' ? 'Message' : 'What was thrown' }}
          </h3>
          <pre v-if="record.exception_value" class="log-panel__text">{{ record.exception_value }}</pre>
          <pre v-if="record.message && record.message !== record.exception_value" class="log-panel__text">{{ record.message }}</pre>
          <p v-if="record.culprit" class="m-0 font-mono text-[11px] text-ink/55">
            in <span class="text-ink/80">{{ record.culprit }}</span>
          </p>
        </section>

        <!-- Where it was thrown. -->
        <section v-if="frames.length" class="flex flex-col gap-1.5">
          <div class="flex items-center justify-between gap-2">
            <h3 class="log-panel__heading">
              Stack, innermost first
            </h3>
            <button
              v-if="frames.some(frame => !frame.in_app) && frames.some(frame => frame.in_app)"
              class="text-[11px] text-primary hover:underline"
              type="button"
              @click="showLibrary = !showLibrary"
            >
              {{ showLibrary ? 'Only the game\'s frames' : 'Every frame' }}
            </button>
          </div>
          <ol class="m-0 flex list-none flex-col overflow-hidden rounded-(--ps-radius-small) border border-line p-0">
            <li
              v-for="(entry, index) in shownFrames"
              :key="index"
              :class="entry.frame?.in_app ? 'bg-primary/5' : ''"
              class="flex items-baseline gap-2 border-b border-line px-2.5 py-1.5 font-mono text-[11px] last:border-b-0"
            >
              <template v-if="entry.frame">
                <span :class="entry.frame.in_app ? 'text-ink' : 'text-ink/55'" class="min-w-0 flex-1 break-all">
                  <span class="text-ink/45">{{ entry.frame.module ? `${entry.frame.module}.` : '' }}</span>{{ entry.frame.function || '?' }}
                </span>
                <span v-if="entry.frame.file" class="flex-none text-ink/45">{{ entry.frame.file }}<template v-if="entry.frame.line">:{{ entry.frame.line }}</template></span>
              </template>
              <button
                v-else
                class="text-ink/45 hover:text-primary"
                type="button"
                @click="showLibrary = true"
              >
                {{ entry.folded }} engine or library {{ entry.folded === 1 ? 'frame' : 'frames' }}
              </button>
            </li>
          </ol>
        </section>

        <!-- What happened before. -->
        <section v-if="record.breadcrumbs.length" class="flex flex-col gap-1.5">
          <h3 class="log-panel__heading">
            Before it, oldest first
          </h3>
          <ol class="m-0 flex list-none flex-col gap-0 p-0">
            <li v-for="(crumb, index) in record.breadcrumbs" :key="index" class="grid grid-cols-[64px_64px_minmax(0,1fr)] items-baseline gap-2 border-l-2 border-line py-1 pl-2.5 text-[11px]">
              <span class="font-mono text-ink/45">{{ crumbClock.format(new Date(crumb.at)) }}</span>
              <span class="truncate text-ink/55">{{ crumb.category || 'event' }}</span>
              <span :class="crumb.level === 'error' || crumb.level === 'fatal' ? 'text-error' : crumb.level === 'warn' ? 'text-warning' : 'text-ink/80'" class="truncate font-mono">{{ crumb.message }}</span>
            </li>
          </ol>
        </section>

        <!-- On what. -->
        <section class="flex flex-col gap-1.5">
          <h3 class="log-panel__heading">
            Context
          </h3>
          <dl class="m-0 grid grid-cols-[88px_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
            <template v-for="fact in facts" :key="fact.label">
              <dt class="text-ink/50">
                {{ fact.label }}
              </dt>
              <dd class="m-0 flex min-w-0 items-center gap-1">
                <button
                  v-if="fact.filter"
                  :class="fact.mono ? 'font-mono text-[11px]' : ''"
                  :title="`Show only records where ${fact.label.toLowerCase()} is ${fact.value}`"
                  class="truncate text-left text-ink/85 hover:text-primary hover:underline"
                  type="button"
                  @click="emit('filter', fact.filter)"
                >
                  {{ fact.value }}
                </button>
                <span v-else :class="fact.mono ? 'font-mono text-[11px]' : ''" class="truncate text-ink/85">{{ fact.value }}</span>
                <CopyButton v-if="fact.copy" :label="`Copy the ${fact.label.toLowerCase()} id`" :value="fact.value" />
              </dd>
            </template>
          </dl>
        </section>

        <section v-if="attributes.length" class="flex flex-col gap-1.5">
          <h3 class="log-panel__heading">
            Attributes
          </h3>
          <dl class="m-0 grid grid-cols-[minmax(0,40%)_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
            <template v-for="[key, value] in attributes" :key="key">
              <dt class="truncate font-mono text-[11px] text-ink/50">
                {{ key }}
              </dt>
              <dd class="m-0 min-w-0">
                <button
                  :title="`Show only records where ${key} is ${value}`"
                  class="max-w-full truncate text-left font-mono text-[11px] text-ink/85 hover:text-primary hover:underline"
                  type="button"
                  @click="emit('filter', clause(`attr.${key}`, value))"
                >
                  {{ value || '""' }}
                </button>
              </dd>
            </template>
          </dl>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-panel__heading {
  color: rgba(var(--v-theme-on-surface), 0.5);
  font-size: 10px;
  font-weight: 500;
  letter-spacing: 0.04em;
  margin: 0;
  text-transform: uppercase;
}

.log-panel__text {
  background: rgba(var(--v-theme-on-surface), 0.035);
  border-radius: var(--ps-radius-small);
  color: rgb(var(--v-theme-on-surface));
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.5;
  margin: 0;
  max-height: 320px;
  overflow: auto;
  padding: 8px 10px;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
