import {
  COHORT_OFFSETS,
  COHORT_PRESETS,
  type CohortColor,
  type CohortDays,
  type CohortMode,
  type CohortPreset,
  defaultCohortDays,
  reconcileCohortDays,
  shownOffsets
} from '../utils/cohort-table'

interface StoredCohortSettings {
  days: CohortDays
  mode: string
  color: CohortColor
}

/**
 * How a reader has set one cohort table up — its days, what its cells show, how they are
 * coloured — kept per table and per browser, like the rail. `id` names the table rather
 * than the page, so two tables on one page keep their own.
 *
 * Every read goes through the reconcilers, so a stored choice that names a preset, a mode
 * or an offset since taken away comes back as the default rather than as an empty table.
 * `useLocalStorage` rather than `useStorage`, which Nitro owns.
 */
export function useCohortTableSettings(
  id: string,
  options: {
    /** The ways a cell can be read, when the reader chooses; none, when the page decides. */
    modes?: readonly CohortMode[]
    presets?: readonly CohortPreset[]
    available?: readonly number[]
  }
) {
  const presets = options.presets ?? COHORT_PRESETS
  const available = options.available ?? COHORT_OFFSETS
  const modes = options.modes ?? []
  const firstMode = modes[0]?.key ?? ''

  const stored = useLocalStorage<StoredCohortSettings>(
    `playersense.cohorts.${id}`,
    { days: defaultCohortDays(presets), mode: firstMode, color: 'heat' },
    { mergeDefaults: true }
  )

  const days = computed<CohortDays>({
    get: () => reconcileCohortDays(stored.value.days, presets, available),
    set: value => void (stored.value = { ...stored.value, days: value })
  })

  const offsets = computed(() => shownOffsets(days.value, presets, available))

  const modeKey = computed<string>({
    get: () => (modes.some(mode => mode.key === stored.value.mode) ? stored.value.mode : firstMode),
    set: value => void (stored.value = { ...stored.value, mode: value })
  })

  const mode = computed(() => modes.find(entry => entry.key === modeKey.value) ?? modes[0])

  const color = computed<CohortColor>({
    get: () => (stored.value.color === 'average' ? 'average' : 'heat'),
    set: value => void (stored.value = { ...stored.value, color: value })
  })

  return { days, offsets, modeKey, mode, color, presets, available }
}
