/**
 * The project's flags, segments and published versions, and every write to them, from the API
 * (stores/live-config.ts). Every screen that uses it asks the store to read once, and a write
 * resolves with the API's refusal in its own words, or null. `key` and `kind` are never written
 * after a create, and nothing is deleted: DELETE archives, and a PUT brings it back.
 */
export function useLiveConfig() {
  const store = useLiveConfigStore()
  const { flags, segments, versions, audit, loading, error } = storeToRefs(store)

  void store.load()

  const flagByKey = (key: string) => flags.value.find(flag => flag.key === key) ?? null
  const segmentByKey = (key: string) => segments.value.find(entry => entry.key === key) ?? null
  const segmentName = (key: string) => segmentByKey(key)?.name ?? key

  /** The version an environment serves now, or null before its first publish. */
  const latest = (environment: EnvironmentKind): ConfigVersionDef | null => {
    const current = store.currentBy[environment]
    return store.versionsBy[environment].find(version => version.version === current) ?? null
  }

  const historyOf = (environment: EnvironmentKind) => [...store.versionsBy[environment]].sort((a, b) => b.version - a.version)

  /** The flags and segments whose rules name a segment, so archiving it can say what breaks. */
  function usesOf(key: string): { flags: FlagDef[], segments: SegmentDef[] } {
    return {
      flags: flags.value.filter(flag => !flag.archived && flag.rules.some(variant => segmentsIn(variant.audience).includes(key))),
      segments: segments.value.filter(entry => !entry.archived && entry.key !== key && segmentsIn(entry.rule).includes(key))
    }
  }

  /**
   * Whether the draft compiles, what it would carry, whether serving it needs the player's
   * history, and what is left out on purpose: the API's answer (GET …/config/{environment}/draft)
   * once it is in, and the same check made here before it is.
   */
  function draftCheck() {
    const server = store.serverCheck
    if (server) {
      return {
        compiles: server.compiles,
        problem: server.problem || null,
        flags: server.flags,
        needsPlayerFacts: server.needs_player_facts,
        excluded: (server.excluded ?? []).map(entry => ({ kind: entry.kind as 'flag' | 'segment', key: entry.key, reason: entry.reason }))
      }
    }
    const live = flags.value.filter(flag => !flag.archived)
    const liveSegments = segments.value.filter(entry => !entry.archived)
    let problem: string | null = segmentsProblem(new Map(liveSegments.map(entry => [entry.key, entry.rule])))
    for (const flag of live) {
      if (problem) break
      const first = flagProblems(flag, segments.value)[0]
      if (first) problem = `flag "${flag.key}": ${first.message}`
    }
    const document = compileDocument(0, live.map(documentOf), liveSegments)
    const excluded = [
      ...flags.value.filter(flag => flag.archived).map(flag => ({ kind: 'flag' as const, key: flag.key, reason: 'archived' })),
      ...segments.value.filter(entry => entry.archived).map(entry => ({ kind: 'segment' as const, key: entry.key, reason: 'archived' }))
    ]
    return { compiles: problem === null, problem, flags: live.length, needsPlayerFacts: document.needs_player_facts, excluded }
  }

  /** Creates a flag, or says why not; checked here first, as the API checks it. */
  async function createFlag(flag: FlagDef): Promise<string | null> {
    const problem = flagProblems(flag, segments.value)[0]
    if (problem) return problem.message
    return store.createFlag(flag)
  }

  async function saveFlag(next: FlagDef): Promise<string | null> {
    const current = flagByKey(next.key)
    if (!current) return `Flag "${next.key}" does not exist.`
    const flag = { ...next, key: current.key, kind: current.kind }
    const problem = flagProblems(flag, segments.value)[0]
    if (problem) return problem.message
    return store.saveFlag(flag)
  }

  async function createSegment(segment: SegmentDef): Promise<string | null> {
    const problem = segmentProblems(segment, segments.value)[0]
    if (problem) return problem
    return store.createSegment(segment)
  }

  async function saveSegment(segment: SegmentDef): Promise<string | null> {
    const problem = segmentProblems(segment, segments.value)[0]
    if (problem) return problem
    return store.saveSegment(segment)
  }

  return {
    flags,
    segments,
    versions,
    audit,
    loading,
    error,
    reload: () => store.load(true),
    loadVersion: store.loadVersion,
    flagByKey,
    segmentByKey,
    segmentName,
    latest,
    historyOf,
    createFlag,
    saveFlag,
    setFlagArchived: store.setFlagArchived,
    createSegment,
    saveSegment,
    setSegmentArchived: store.setSegmentArchived,
    usesOf,
    draftCheck,
    publish: store.publish,
    rollback: store.rollback,
    restoreDraft: store.restoreDraft
  }
}

/**
 * How many of last week's players each saved segment holds (GET …/analytics/{environment}/
 * segments/reach): the share, or why it cannot be estimated. A draft rule is not counted until
 * it is saved.
 */
export function useSegmentReach() {
  const workspace = useWorkspaceStore()
  const read = useApiGet<import('@playersense/contracts').Schema<'SegmentReachOut'>>(() => workspace.analyticsPath && `${workspace.analyticsPath}/segments/reach`)
  const rows = computed(() => new Map((read.data.value?.segments ?? []).map(row => [row.key, row])))
  /** The share of last week's players, or null where it cannot be estimated. */
  const shareOf = (key: string): number | null => {
    const row = rows.value.get(key)
    return row?.estimable && typeof row.share === 'number' ? row.share : null
  }
  const reasonOf = (key: string): string => {
    const row = rows.value.get(key)
    if (!row) return read.isFetching.value ? '' : 'Not counted yet'
    if (row.estimable) return ''
    return row.unestimable?.length ? `Reads ${row.unestimable.join(', ')}, which players are not counted by` : 'Cannot be estimated'
  }
  return { shareOf, reasonOf, exact: computed(() => read.data.value?.exact ?? false), reload: () => read.execute() }
}

/**
 * How often production devices read each flag over the last seven days, the seven before, and
 * by day (GET …/analytics/production/flags/exposures).
 */
export function useFlagExposures() {
  const workspace = useWorkspaceStore()
  const read = useApiGet<import('@playersense/contracts').Schema<'FlagExposureListOut'>>(() => workspace.projectPath && `${workspace.projectPath}/analytics/production/flags/exposures`)
  const byFlag = computed(() => new Map((read.data.value?.flags ?? []).map(row => [row.flag, row])))
  /** A flag's reads, or zeros when no device read it. */
  const readsOf = (key: string) => {
    const row = byFlag.value.get(key)
    return { reads: row?.exposures ?? 0, previous: row?.previous_exposures ?? 0, daily: [...(row?.daily ?? [])] }
  }
  return { readsOf, ready: computed(() => Boolean(read.data.value)) }
}
