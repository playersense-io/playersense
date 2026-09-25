import type { Schema } from '@playersense/contracts'

// The Realtime page's live window (GET …/analytics/{env}/realtime): the last 30 minutes, and the
// same 30 minutes yesterday to measure them against. Read again every ten seconds: an event takes
// up to about ten seconds to become visible, so asking more often returns the same answer.

export type LiveWindow = Schema<'RealtimeOut'>
export type LiveMinute = Schema<'RealtimeMinuteOut'>

/** Minutes on screen. */
const WINDOW_MINUTES = 30

export function useRealtime(everyMs = 10_000) {
  const workspace = useWorkspaceStore()
  const url = computed(() => workspace.analyticsPath && withQuery(`${workspace.analyticsPath}/realtime`, { minutes: WINDOW_MINUTES }))

  const read = useApiGet<LiveWindow>(url)
  /** When the figures on screen were read. */
  const updatedAt = ref(Date.now())
  watch(read.data, () => (updatedAt.value = Date.now()))

  const { pause, resume, isActive } = useIntervalFn(() => {
    if (!read.isFetching.value) void read.execute()
  }, everyMs)

  const window = computed(() => read.data.value)
  const loading = computed(() => read.isFetching.value && !read.data.value)
  const error = computed(() => read.error.value as ApiError | null)

  return { window, loading, error, updatedAt: readonly(updatedAt), live: isActive, pause, resume, reload: () => read.execute() }
}
