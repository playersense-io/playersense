// The Realtime page's live window: the last 30 minutes, advanced every few seconds, and
// the same 30 minutes yesterday to measure them against.
//
// Sample data today (app/utils/realtime-sample.ts). Once the API client exists this polls
// `metrics/query` at `interval=minute` instead — at the same cadence, since events take up
// to ten seconds to become visible and asking more often returns the same answer.
export function useRealtime(everyMs = 2_000) {
  const now = ref(Date.now())
  const minutes = shallowRef(sampleWindow(now.value))
  const yesterday = shallowRef(sampleYesterday(now.value))

  function tick() {
    const next = Date.now()

    // A new array with the filling minute copied, so everything reading it updates.
    const shifted = [...minutes.value]
    const current = shifted.length - 1
    shifted[current] = structuredClone(toRaw(shifted[current]!))
    advanceWindow(shifted, now.value, next)
    minutes.value = shifted

    // Yesterday slides on a minute at a time, so the comparison stays the same time of day.
    for (let crossed = Math.floor(next / 60_000) - Math.floor(now.value / 60_000); crossed > 0; crossed--) {
      const last = yesterday.value[yesterday.value.length - 1]!
      yesterday.value = [...yesterday.value.slice(1), sampleMinute(last.at + 60_000, true)]
    }

    now.value = next
  }

  /** Starts over from now, for another project's window. */
  function restart() {
    now.value = Date.now()
    minutes.value = sampleWindow(now.value)
    yesterday.value = sampleYesterday(now.value)
  }

  const { pause, resume, isActive } = useIntervalFn(tick, everyMs)

  return { minutes, yesterday, updatedAt: readonly(now), live: isActive, pause, resume, restart }
}
