import type { Schema } from '@playersense/contracts'

// Cloud Logging (…/projects/{project}/logs/{environment}): the game's own logs, exceptions and
// crashes. The range, the level bands and the search live in the URL, so a view is a link somebody
// can send; the list pages by cursor and grows as it is scrolled, and a relative range can follow
// the present ("live") without moving the page under the person reading it.

export type LogHistogram = Schema<'LogHistogramOut'>
export type LogBucket = Schema<'LogBucketOut'>
export type LogRecordSummary = Schema<'LogRecordSummaryOut'>
export type LogRecord = Schema<'LogRecordOut'>
export type LogSummary = Schema<'LogSummaryOut'>
export type LogIssue = Schema<'LogIssueOut'>
export type LogIssueDetail = Schema<'LogIssueDetailOut'>
export type LogStorage = Schema<'LogStorageOut'>

export type LogLevel = 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal'

// --- Levels -------------------------------------------------------------------------------------

/**
 * Four bands rather than six levels, for the filter chips and the colour of a level in the list:
 * six hues cannot all be told apart (the palette validator fails trace against debug and warn
 * against error in dark mode), and nobody filters trace apart from debug. The search still takes
 * any single level, as `level:fatal`.
 */
export type LogBand = 'error' | 'warn' | 'info' | 'debug'

export interface LogBandInfo {
  key: LogBand
  label: string
  levels: readonly LogLevel[]
  hint: string
}

export const LOG_BANDS: readonly LogBandInfo[] = [
  { key: 'error', label: 'Error', levels: ['error', 'fatal'], hint: 'Errors and fatal records, crashes among them' },
  { key: 'warn', label: 'Warning', levels: ['warn'], hint: 'Warnings' },
  { key: 'info', label: 'Info', levels: ['info'], hint: 'Informational records' },
  { key: 'debug', label: 'Debug', levels: ['debug', 'trace'], hint: 'Debug and trace records' }
]

/**
 * Each band's colour, validated with the dataviz palette checker in both themes: every adjacent
 * pair separates for colour-blind readers and for everyone else. Debug is a slate on purpose,
 * the one band meant to recede, and it is always labelled.
 */
export const LOG_BAND_COLORS: Record<'light' | 'dark', Record<LogBand, string>> = {
  light: { error: '#c62828', warn: '#ef6c00', info: '#2979ff', debug: '#7a8699' },
  dark: { error: '#d13f4c', warn: '#c98500', info: '#448aff', debug: '#6f7b8e' }
}

export function bandOf(level: string): LogBand {
  return LOG_BANDS.find(band => band.levels.includes(level as LogLevel))?.key ?? 'info'
}

/** The band colours for the theme showing now. */
export function useLogColors() {
  const theme = useTheme()
  return computed(() => LOG_BAND_COLORS[theme.current.value.dark ? 'dark' : 'light'])
}

/** A bucket's count for a band. */
export function bandCount(bucket: Pick<LogBucket, LogLevel>, band: LogBand): number {
  return LOG_BANDS.find(entry => entry.key === band)!.levels.reduce((sum, level) => sum + bucket[level], 0)
}

// --- The range ----------------------------------------------------------------------------------

export type LogPreset = '15m' | '1h' | '6h' | '24h' | '3d' | '7d' | '14d' | '30d' | '90d'

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

export const LOG_PRESETS: readonly { value: LogPreset, label: string, hint: string, span: number }[] = [
  { value: '15m', label: 'Last 15 minutes', hint: 'The quarter hour to now, bars of ten seconds. Live follows it.', span: 15 * MINUTE },
  { value: '1h', label: 'Last hour', hint: 'The hour to now, bars of a minute.', span: HOUR },
  { value: '6h', label: 'Last 6 hours', hint: 'Six hours to now, bars of five minutes.', span: 6 * HOUR },
  { value: '24h', label: 'Last 24 hours', hint: 'A day to now, bars of fifteen minutes.', span: DAY },
  { value: '3d', label: 'Last 3 days', hint: 'Three days to now, bars of an hour.', span: 3 * DAY },
  { value: '7d', label: 'Last 7 days', hint: 'A week to now, bars of two hours.', span: 7 * DAY },
  { value: '14d', label: 'Last 14 days', hint: 'Two weeks to now, bars of six hours.', span: 14 * DAY },
  { value: '30d', label: 'Last 30 days', hint: 'A month to now, bars of twelve hours.', span: 30 * DAY },
  { value: '90d', label: 'Last 90 days', hint: 'The longest retention, bars of a day.', span: 90 * DAY }
]

/** The presets as the range picker takes them. */
export const LOG_TIME_PRESETS = LOG_PRESETS.map(entry => ({ value: entry.value, title: entry.label, hint: entry.hint, span: entry.span }))

export interface LogRange {
  from: number
  to: number
  /** Set while the range follows the present; null for a fixed stretch. */
  preset: LogPreset | null
}

/** The narrowest range a zoom goes to: a bar is a second, and ninety of them are a minute and a half. */
const MIN_SPAN = MINUTE

/**
 * The range the page reads, from the URL: `range=1h`, or `from` and `to` as ISO times. A relative
 * range is measured from `anchor`, which moves only when the page refreshes, so a range that
 * follows the present does not refetch every tick of the clock.
 */
export function useLogRange(fallback: LogPreset = '24h') {
  const route = useRoute()
  const router = useRouter()
  const anchor = ref(Date.now())

  const range = computed<LogRange>(() => {
    const from = typeof route.query.from === 'string' ? Date.parse(route.query.from) : Number.NaN
    const to = typeof route.query.to === 'string' ? Date.parse(route.query.to) : Number.NaN
    if (Number.isFinite(from) && Number.isFinite(to) && from < to) return { from, to, preset: null }
    const key = String(route.query.range ?? fallback)
    const preset = LOG_PRESETS.find(entry => entry.value === key) ?? LOG_PRESETS.find(entry => entry.value === fallback)!
    return { from: anchor.value - preset.span, to: anchor.value, preset: preset.value }
  })

  function write(query: Record<string, string | undefined>) {
    const merged = { ...route.query, ...query }
    void router.replace({ query: Object.fromEntries(Object.entries(merged).filter(([, value]) => value !== undefined)) })
  }

  function setPreset(preset: LogPreset) {
    anchor.value = Date.now()
    write({ range: preset === fallback ? undefined : preset, from: undefined, to: undefined })
  }

  function setAbsolute(from: number, to: number) {
    if (to - from < MIN_SPAN) {
      const middle = (from + to) / 2
      from = middle - MIN_SPAN / 2
      to = middle + MIN_SPAN / 2
    }
    to = Math.min(to, Date.now())
    write({ range: undefined, from: new Date(from).toISOString(), to: new Date(to).toISOString() })
  }

  /** Twice as wide around the same middle, never into the future. */
  function zoomOut() {
    const { from, to } = range.value
    const span = to - from
    const end = Math.min(to + span / 2, Date.now())
    setAbsolute(end - span * 2, end)
  }

  /** A range's own width earlier (-1) or later (1), never past now. */
  function shift(direction: -1 | 1) {
    const { from, to } = range.value
    const span = to - from
    const end = Math.min(to + direction * span, Date.now())
    setAbsolute(end - span, end)
  }

  function refresh() {
    anchor.value = Date.now()
  }

  return { range, setPreset, setAbsolute, zoomOut, shift, refresh }
}

const fullTime = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

/** What the range button says. */
export function rangeWords(range: LogRange): string {
  if (range.preset) return LOG_PRESETS.find(entry => entry.value === range.preset)!.label
  return `${fullTime.format(range.from)} to ${fullTime.format(range.to)}`
}

// --- Writing times ------------------------------------------------------------------------------

const clock = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const day = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })

/** A record's time as the list shows it: the day, the clock and the milliseconds, all fixed width. */
export function recordTime(iso: string): { day: string, clock: string, ms: string } {
  const at = new Date(iso)
  return { day: day.format(at), clock: clock.format(at), ms: String(at.getMilliseconds()).padStart(3, '0') }
}

// --- The search ---------------------------------------------------------------------------------

/** The search the API is sent: the chosen bands as a level filter, then what was typed. */
export function effectiveQuery(bands: readonly LogBand[], text: string): string {
  const levels = bands.length && bands.length < LOG_BANDS.length
    ? `level:${LOG_BANDS.filter(band => bands.includes(band.key)).flatMap(band => band.levels).join(',')}`
    : ''
  return [levels, text.trim()].filter(Boolean).join(' ')
}

/** A filter clause for a value, quoted when it has a space. */
export function clause(field: string, value: string): string {
  return /\s/.test(value) ? `${field}:"${value}"` : `${field}:${value}`
}

// --- Reads --------------------------------------------------------------------------------------

/** The environment's logs, or null before a project is chosen. */
export function useLogsPath() {
  const workspace = useWorkspaceStore()
  return computed(() => (workspace.projectPath ? `${workspace.projectPath}/logs/${workspace.environment}` : null))
}

/**
 * The records under the chart, newest first, a page at a time. Starts over whenever the range or
 * the search changes; `more` fetches the next page and appends it.
 */
export function useLogRecords(path: Ref<string | null>, range: Ref<LogRange>, query: Ref<string>) {
  const records = shallowRef<LogRecordSummary[]>([])
  const cursor = ref<string | null>(null)
  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  const done = ref(false)
  let generation = 0

  const PAGE = 200

  async function page(reset: boolean) {
    if (!path.value) return
    if (!reset && (loading.value || done.value)) return
    const mine = reset ? ++generation : generation
    loading.value = true
    error.value = null
    try {
      const out = await apiRequest<Schema<'LogRecordsOut'>>('GET', withQuery(`${path.value}/records`, {
        from: new Date(range.value.from).toISOString(),
        to: new Date(range.value.to).toISOString(),
        q: query.value,
        cursor: reset ? undefined : cursor.value ?? undefined,
        limit: PAGE
      }))
      if (mine !== generation) return
      records.value = reset ? [...out.records] : [...records.value, ...out.records]
      cursor.value = out.next_cursor ?? null
      done.value = !out.next_cursor
    } catch (caught) {
      if (mine === generation) error.value = caught as ApiError
    } finally {
      if (mine === generation) loading.value = false
    }
  }

  // The rows on screen stay until the new first page replaces them, so a live refresh swaps them
  // in place rather than flashing an empty list between.
  watch([path, () => range.value.from, () => range.value.to, query], () => {
    cursor.value = null
    done.value = false
    void page(true)
  }, { immediate: true })

  return {
    records,
    loading,
    error,
    done,
    more: () => page(false),
    reload: () => page(true)
  }
}

/** One record whole. */
export function readLogRecord(path: string, summary: Pick<LogRecordSummary, 'id' | 'at'>): Promise<LogRecord> {
  return apiRequest<LogRecord>('GET', withQuery(`${path}/records/${summary.id}`, { at: summary.at }))
}

/** How many records arrived since the newest one on the page. */
export function countNewer(path: string, since: string, query: string): Promise<number> {
  return apiRequest<Schema<'LogNewerOut'>>('GET', withQuery(`${path}/records/newer`, { since, q: query }))
    .then(out => out.count)
}

export function setLogIssueStatus(path: string, id: string, status: 'unresolved' | 'resolved' | 'ignored') {
  return apiRequest<Schema<'LogIssueStateOut'>>('PUT', `${path}/issues/${id}`, { status })
}

// --- Issues -------------------------------------------------------------------------------------

export type IssueStatus = 'unresolved' | 'regressed' | 'resolved' | 'ignored'

export const ISSUE_STATUS: Record<IssueStatus, { label: string, tone: 'success' | 'warning' | 'error' | 'neutral' }> = {
  unresolved: { label: 'Unresolved', tone: 'warning' },
  regressed: { label: 'Regressed', tone: 'error' },
  resolved: { label: 'Resolved', tone: 'success' },
  ignored: { label: 'Ignored', tone: 'neutral' }
}

export const ISSUE_KIND: Record<string, { label: string, icon: string }> = {
  crash: { label: 'Crash', icon: 'material-symbols:heart-broken-outline' },
  exception: { label: 'Exception', icon: 'material-symbols:bug-report-outline' },
  log: { label: 'Error log', icon: 'material-symbols:error-outline' }
}

// --- Storage ------------------------------------------------------------------------------------

/** A price in micros as dollars, to the cent or finer when it is below one. */
export function priceWords(micros: number): string {
  const dollars = micros / 1_000_000
  return dollars >= 1 || dollars === 0
    ? `$${dollars.toFixed(2)}`
    : `$${dollars.toFixed(dollars < 0.01 ? 4 : 2)}`
}

export const RETENTION_WORDS: Record<number, string> = {
  1: '1 day', 3: '3 days', 7: '7 days', 14: '14 days', 30: '30 days', 60: '60 days', 90: '90 days'
}
