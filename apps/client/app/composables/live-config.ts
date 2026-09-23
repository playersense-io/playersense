/**
 * The project's flags, segments and published versions, and every write to them. Kept in this
 * browser until the API client is wired in, and seeded with a game's worth of configuration.
 *
 * Each write is checked as the API checks it (ADR 0034) and refused with the same sentence, so
 * the screens handle a refusal now the way they will handle a 422 then. A write that succeeds
 * is recorded in the audit trail with what it changed. `key` and `kind` are never written after
 * a create, and nothing is deleted: archiving keeps the row, as DELETE does.
 */
let shared: Ref<LiveConfigState> | null = null

/*
 * One ref for the whole app, so a write on one screen is the state every other screen reads. It
 * lives in a detached scope: made inside the first screen's setup, its watcher — the one that
 * writes to storage — would stop when that screen unmounted, and every write after it would be
 * lost on the next reload.
 */
function storedState(): Ref<LiveConfigState> {
  shared ??= effectScope(true).run(() => useLocalStorage<LiveConfigState>('playersense.liveconfig', seedLiveConfig, { deep: false }))!
  return shared
}

export function useLiveConfig() {
  const stored = storedState()

  /* A stored state from an older shape is drawn again rather than half-read. */
  const state = computed<LiveConfigState>(() => {
    const value = stored.value as Partial<LiveConfigState> | null
    const valid = value && Array.isArray(value.flags) && Array.isArray(value.segments) && Array.isArray(value.versions) && Array.isArray(value.audit)
      && value.flags.every(flag => typeof flag?.key === 'string' && Array.isArray(flag.rules) && Array.isArray(flag.tags))
    return valid ? value as LiveConfigState : seedLiveConfig()
  })

  const flags = computed(() => state.value.flags)
  const segments = computed(() => state.value.segments)
  const versions = computed(() => state.value.versions)
  const audit = computed(() => state.value.audit)

  const flagByKey = (key: string) => flags.value.find(flag => flag.key === key) ?? null
  const segmentByKey = (key: string) => segments.value.find(entry => entry.key === key) ?? null
  const segmentName = (key: string) => segmentByKey(key)?.name ?? key

  /** The version an environment serves now, or null before its first publish. */
  const latest = (environment: EnvironmentKind): ConfigVersionDef | null =>
    versions.value.filter(version => version.environment === environment).sort((a, b) => b.version - a.version)[0] ?? null

  const historyOf = (environment: EnvironmentKind) =>
    versions.value.filter(version => version.environment === environment).sort((a, b) => b.version - a.version)

  function write(next: Partial<LiveConfigState>, entry?: Omit<ConfigAuditEntry, 'id' | 'at' | 'actor'>) {
    const audited = entry
      ? [{ ...entry, id: `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`, at: new Date().toISOString(), actor: CURRENT_ACTOR }, ...state.value.audit]
      : state.value.audit
    stored.value = { ...state.value, ...next, audit: audited }
  }

  const stamp = () => ({ updated_at: new Date().toISOString(), updated_by: CURRENT_ACTOR })

  // --- Flags --------------------------------------------------------------------------------

  /** Creates a flag, or says why not. The key must be new, archived flags included. */
  function createFlag(flag: FlagDef): string | null {
    if (flags.value.some(existing => existing.key === flag.key)) return `A flag called "${flag.key}" already exists${flagByKey(flag.key)?.archived ? ', archived' : ''}. Keys are never reused.`
    const problem = flagProblems(flag, segments.value)[0]
    if (problem) return problem.message
    const now = new Date().toISOString()
    write(
      { flags: [...flags.value, { ...flag, created_at: now, ...stamp() }] },
      { action: 'flag.create', target: flag.key, changes: [`Created as ${FLAG_KIND_LABELS[flag.kind].toLowerCase()}, serving ${writeValue(flag.fallback)}`, ...flag.rules.map((variant, index) => `Rule ${index + 1}: ${describeRule(variant.audience, segmentName)} → ${writeValue(variant.value)}`)] }
    )
    return null
  }

  /**
   * Writes a flag over the one with its key: a whole-object PUT. Its key and kind are the stored
   * ones whatever arrives, since neither can change.
   */
  function saveFlag(next: FlagDef): string | null {
    const current = flagByKey(next.key)
    if (!current) return `Flag "${next.key}" does not exist.`
    const flag = { ...next, key: current.key, kind: current.kind }
    const problem = flagProblems(flag, segments.value)[0]
    if (problem) return problem.message
    const lines = flagChanges(documentOf(current), documentOf(flag), segmentName)
    if (current.name !== flag.name) lines.unshift(`Renamed "${current.name}" → "${flag.name}"`)
    if (current.description !== flag.description) lines.push('Description edited')
    if (current.purpose !== flag.purpose) lines.push(`Purpose ${FLAG_PURPOSES[current.purpose].label} → ${FLAG_PURPOSES[flag.purpose].label}`)
    if (!sameJson(current.tags, flag.tags)) lines.push(`Tags: ${flag.tags.join(', ') || 'none'}`)
    if (current.grantsValue !== flag.grantsValue) lines.push(flag.grantsValue ? 'Marked as granting value' : 'No longer marked as granting value')
    if (!lines.length) return null
    write(
      { flags: flags.value.map(entry => (entry.key === flag.key ? { ...flag, ...stamp() } : entry)) },
      { action: 'flag.write', target: flag.key, changes: lines }
    )
    return null
  }

  function setFlagArchived(key: string, archived: boolean) {
    const current = flagByKey(key)
    if (!current || current.archived === archived) return
    write(
      { flags: flags.value.map(entry => (entry.key === key ? { ...entry, archived, ...stamp() } : entry)) },
      { action: archived ? 'flag.archive' : 'flag.restore', target: key, changes: [archived ? 'Archived: left out of the next publish' : 'Restored: back in the next publish'] }
    )
  }

  // --- Segments -----------------------------------------------------------------------------

  function createSegment(segment: SegmentDef): string | null {
    if (segments.value.some(existing => existing.key === segment.key)) return `A segment called "${segment.key}" already exists. Keys are never reused.`
    const problem = segmentProblems(segment, segments.value)[0]
    if (problem) return problem
    const now = new Date().toISOString()
    write(
      { segments: [...segments.value, { ...segment, archived: false, created_at: now, ...stamp() }] },
      { action: 'segment.create', target: segment.key, changes: [describeRule(segment.rule, segmentName)] }
    )
    return null
  }

  function saveSegment(next: SegmentDef): string | null {
    const current = segmentByKey(next.key)
    if (!current) return `Segment "${next.key}" does not exist.`
    const segment = { ...next, key: current.key }
    const problem = segmentProblems(segment, segments.value)[0]
    if (problem) return problem
    const lines: string[] = []
    if (current.name !== segment.name) lines.push(`Renamed "${current.name}" → "${segment.name}"`)
    if (current.description !== segment.description) lines.push('Description edited')
    if (!sameJson(current.rule, segment.rule)) lines.push(`Audience: ${describeRule(segment.rule, segmentName)}`)
    if (!lines.length) return null
    write(
      { segments: segments.value.map(entry => (entry.key === segment.key ? { ...segment, ...stamp() } : entry)) },
      { action: 'segment.write', target: segment.key, changes: lines }
    )
    return null
  }

  function setSegmentArchived(key: string, archived: boolean) {
    const current = segmentByKey(key)
    if (!current || current.archived === archived) return
    write(
      { segments: segments.value.map(entry => (entry.key === key ? { ...entry, archived, ...stamp() } : entry)) },
      { action: archived ? 'segment.archive' : 'segment.restore', target: key, changes: [archived ? 'Archived' : 'Restored'] }
    )
  }

  /** The flags and segments whose rules name a segment, so archiving it can say what breaks. */
  function usesOf(key: string): { flags: FlagDef[], segments: SegmentDef[] } {
    return {
      flags: flags.value.filter(flag => !flag.archived && flag.rules.some(variant => segmentsIn(variant.audience).includes(key))),
      segments: segments.value.filter(entry => !entry.archived && entry.key !== key && segmentsIn(entry.rule).includes(key))
    }
  }

  // --- The draft and its publishing ---------------------------------------------------------

  /**
   * What GET …/config/{environment}/draft reports: whether the draft compiles, what it would
   * carry, whether serving it needs the player's history, and what is left out on purpose.
   */
  function draftCheck() {
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

  /** Publishes the draft to an environment as its next version. Refused if it does not compile. */
  function publish(environment: EnvironmentKind, note: string): string | null {
    const check = draftCheck()
    if (!check.compiles) return check.problem
    const previous = latest(environment)
    const changes = draftChanges(previous, flags.value, segments.value)
    const version: ConfigVersionDef = {
      environment,
      version: (previous?.version ?? 0) + 1,
      published_at: new Date().toISOString(),
      published_by: CURRENT_ACTOR,
      note: note.trim(),
      flags: flags.value.filter(flag => !flag.archived).map(documentOf).map(flag => JSON.parse(JSON.stringify(flag)) as FlagDocument),
      segments: segments.value.filter(entry => !entry.archived).map(entry => ({ key: entry.key, rule: JSON.parse(JSON.stringify(entry.rule)) as TargetRule }))
    }
    write(
      { versions: [...versions.value, version] },
      {
        action: 'liveops.publish',
        target: environment,
        environment,
        version: version.version,
        note: version.note,
        changes: changes.length ? changes.map(change => `${change.change === 'added' ? 'Added' : change.change === 'removed' ? 'Removed' : 'Changed'} ${change.kind} ${change.key}`) : ['No changes: the same document again']
      }
    )
    return null
  }

  /**
   * Brings a published version back into the draft: each flag's rules, fallback and reach as they
   * were, flags that were not in it archived, flags that were restored. It is a set of ordinary
   * writes, which is all a rollback needs — the draft is then published like any other.
   */
  function restoreDraft(version: ConfigVersionDef) {
    const published = new Map(version.flags.map(flag => [flag.key, flag]))
    const nextFlags = flags.value.map((flag) => {
      const then = published.get(flag.key)
      if (!then) return flag.archived ? flag : { ...flag, archived: true, ...stamp() }
      return { ...flag, ...JSON.parse(JSON.stringify(then)) as FlagDocument, kind: flag.kind, archived: false, ...stamp() }
    })
    const rules = new Map(version.segments.map(entry => [entry.key, entry.rule]))
    const nextSegments = segments.value.map((entry) => {
      const then = rules.get(entry.key)
      if (!then) return entry
      return sameJson(then, entry.rule) && !entry.archived ? entry : { ...entry, rule: then, archived: false, ...stamp() }
    })
    write(
      { flags: nextFlags, segments: nextSegments },
      { action: 'draft.restore', target: version.environment, environment: version.environment, version: version.version, changes: [`Draft set to ${ENVIRONMENT_LABELS[version.environment]} v${version.version}`] }
    )
  }

  /** Starts the sample over. */
  function reset() {
    stored.value = seedLiveConfig()
  }

  return {
    flags,
    segments,
    versions,
    audit,
    flagByKey,
    segmentByKey,
    segmentName,
    latest,
    historyOf,
    createFlag,
    saveFlag,
    setFlagArchived,
    createSegment,
    saveSegment,
    setSegmentArchived,
    usesOf,
    draftCheck,
    publish,
    restoreDraft,
    reset
  }
}
