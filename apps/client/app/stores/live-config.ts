import { defineStore } from 'pinia'
import type { Schema } from '@playersense/contracts'

// The project's flags, segments and published versions (…/flags, …/segments,
// …/config/{environment}/versions), shared by every Growth screen: a write on one is what the
// others read. Read once a project is known, and the parts a write touched read again after it.
//
// A published version comes back as its compiled document: every flag with its segments
// written into its rules. The document of what each environment serves now is read with the
// list, so the draft can be compared with it; an older one is read when somebody opens it.
// Edits are the audit log's, narrowed to this project's flags and segments.

type FlagRecord = Schema<'FlagOut'>
type SegmentRecord = Schema<'SegmentOut'>
type VersionRecord = Schema<'ConfigVersionOut'>

/** An audience as the API holds it: absent is everybody, which the editor writes as {}. */
const ruleOf = (value: unknown): TargetRule => (value && typeof value === 'object' ? value as TargetRule : {})

function flagOf(out: FlagRecord): FlagDef {
  return {
    key: out.key,
    name: out.name,
    description: out.description ?? '',
    kind: out.kind as FlagKind,
    fallback: out.fallback as unknown as Json,
    rules: (out.rules ?? []).map(rule => ({ audience: ruleOf(rule.audience), value: rule.value as unknown as Json, rollout: rule.rollout })),
    rollout: out.rollout,
    salt: out.salt ?? '',
    archived: out.archived,
    created_at: out.created_at,
    updated_at: out.updated_at,
    updated_by: out.updated_by ?? '',
    purpose: (out.purpose || 'config') as FlagPurpose,
    tags: [...(out.tags ?? [])],
    grantsValue: out.grants_value
  }
}

function segmentOf(out: SegmentRecord): SegmentDef {
  return {
    key: out.key,
    name: out.name,
    description: out.description ?? '',
    rule: ruleOf(out.rule),
    archived: out.archived,
    created_at: out.created_at,
    updated_at: out.updated_at,
    updated_by: out.updated_by ?? ''
  }
}

function versionOf(out: VersionRecord): ConfigVersionDef {
  return {
    environment: out.environment as EnvironmentKind,
    version: out.version,
    published_at: out.published_at,
    published_by: out.published_by,
    note: out.note ?? '',
    flags: [],
    segments: [],
    rollback_of: out.rollback_of || undefined,
    flag_count: out.flags,
    liveops_events: out.liveops_events,
    needs_player_facts: out.needs_player_facts,
    loaded: false
  }
}

/** A compiled document's flags, in the shape the draft is compared in. */
function documentFlags(document: Record<string, unknown>): FlagDocument[] {
  const flags = Array.isArray(document.flags) ? document.flags as Record<string, unknown>[] : []
  return flags.map(flag => ({
    key: String(flag.key),
    kind: flag.kind as FlagKind,
    fallback: flag.fallback as Json,
    rules: (Array.isArray(flag.rules) ? flag.rules as Record<string, unknown>[] : [])
      .map(rule => ({ audience: ruleOf(rule.audience), value: rule.value as Json, rollout: Number(rule.rollout ?? FULL_ROLLOUT) })),
    rollout: Number(flag.rollout ?? FULL_ROLLOUT),
    salt: typeof flag.salt === 'string' ? flag.salt : ''
  }))
}

/** A flag as written: an empty audience left out, since absent is everybody. */
function flagBody(flag: FlagDef) {
  return {
    name: flag.name.trim(),
    description: flag.description.trim(),
    fallback: flag.fallback,
    rules: flag.rules.map(rule => ({
      ...(Object.keys(rule.audience).length ? { audience: rule.audience } : {}),
      value: rule.value,
      rollout: rule.rollout
    })),
    rollout: flag.rollout,
    salt: flag.salt,
    purpose: flag.purpose,
    tags: flag.tags,
    grants_value: flag.grantsValue
  }
}

const EDIT_ACTIONS = new Set(['flag.write', 'flag.archive', 'segment.write', 'segment.archive'])

/** What an audit event says changed, in the lines a history shows. */
function editLines(event: Schema<'AuditEventOut'>, archived: boolean): string[] {
  const meta = (event.metadata ?? {}) as Record<string, unknown>
  if (event.action.endsWith('.archive')) return ['Archived: left out of the next publish']
  if (meta.created) {
    return event.target_type === 'flag'
      ? [`Created as ${FLAG_KIND_LABELS[meta.kind as FlagKind]?.toLowerCase() ?? 'a flag'}${meta.rules ? `, with ${meta.rules} ${meta.rules === 1 ? 'rule' : 'rules'}` : ''}`]
      : ['Created']
  }
  const lines: string[] = []
  if (meta.experiment) lines.push(meta.rules_removed ? 'Rules set aside for an experiment' : 'Written by an experiment')
  if (typeof meta.rollout === 'number' && typeof meta.was_rollout === 'number' && meta.rollout !== meta.was_rollout) {
    lines.push(`Flag rollout ${formatRollout(meta.was_rollout)} → ${formatRollout(meta.rollout)}`)
  }
  if (!archived && !lines.length) lines.push('Edited')
  return lines
}

export const useLiveConfigStore = defineStore('live-config', () => {
  const workspace = useWorkspaceStore()

  const flags = shallowRef<FlagDef[]>([])
  const segments = shallowRef<SegmentDef[]>([])
  const versionsBy = shallowRef<Record<EnvironmentKind, ConfigVersionDef[]>>({ development: [], staging: [], production: [] })
  const currentBy = ref<Record<EnvironmentKind, number>>({ development: 0, staging: 0, production: 0 })
  const audit = shallowRef<ConfigAuditEntry[]>([])
  /** What the API says of the draft: GET …/config/production/draft. */
  const serverCheck = shallowRef<Schema<'DraftOut'> | null>(null)

  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  const loadedFor = ref<string | null>(null)

  const path = computed(() => workspace.projectPath)

  const versions = computed(() => ENVIRONMENTS.flatMap(environment => versionsBy.value[environment]))

  async function loadFlags() {
    const out = await apiRequest<Schema<'FlagListOut'>>('GET', `${path.value}/flags`)
    flags.value = (out.flags ?? []).map(flagOf)
  }

  async function loadSegments() {
    const out = await apiRequest<Schema<'SegmentListOut'>>('GET', `${path.value}/segments`)
    segments.value = (out.segments ?? []).map(segmentOf)
  }

  async function loadCheck() {
    try {
      serverCheck.value = await apiRequest<Schema<'DraftOut'>>('GET', `${path.value}/config/production/draft`)
    } catch {
      serverCheck.value = null
    }
  }

  /** One version's document, read once and kept. */
  async function loadVersion(environment: EnvironmentKind, version: number): Promise<ConfigVersionDef | null> {
    const list = versionsBy.value[environment]
    const known = list.find(entry => entry.version === version)
    if (known?.loaded) return known
    const out = await apiRequest<Schema<'ConfigVersionDocumentOut'>>('GET', `${path.value}/config/${environment}/versions/${version}`)
    const loaded = { ...versionOf(out), flags: documentFlags(out.document as Record<string, unknown>), loaded: true }
    versionsBy.value = {
      ...versionsBy.value,
      [environment]: known ? list.map(entry => (entry.version === version ? loaded : entry)) : [...list, loaded].sort((a, b) => b.version - a.version)
    }
    return loaded
  }

  async function loadVersions(environment: EnvironmentKind) {
    const out = await apiRequest<Schema<'ConfigVersionListOut'>>('GET', withQuery(`${path.value}/config/${environment}/versions`, { limit: 100 }))
    const kept = new Map(versionsBy.value[environment].filter(entry => entry.loaded).map(entry => [entry.version, entry]))
    versionsBy.value = { ...versionsBy.value, [environment]: (out.versions ?? []).map(entry => kept.get(entry.version) ?? versionOf(entry)) }
    currentBy.value = { ...currentBy.value, [environment]: out.current }
    if (out.current) await loadVersion(environment, out.current)
  }

  async function loadAudit() {
    if (!workspace.orgPath) return
    try {
      const out = await apiRequest<Schema<'AuditListOut'>>('GET', withQuery(`${workspace.orgPath}/audit`, { limit: 200 }))
      const slug = workspace.project?.slug
      const flagKeys = new Set(flags.value.map(flag => flag.key))
      const segmentKeys = new Set(segments.value.map(segment => segment.key))
      audit.value = (out.events ?? [])
        .filter((event) => {
          if (!EDIT_ACTIONS.has(event.action)) return false
          const project = (event.metadata as Record<string, unknown> | undefined)?.project
          // An archive does not record its project: the key has to be one of this project's.
          if (project) return project === slug
          return event.target_type === 'flag' ? flagKeys.has(event.target_id ?? '') : segmentKeys.has(event.target_id ?? '')
        })
        .map((event) => {
          const meta = (event.metadata ?? {}) as Record<string, unknown>
          const kind = event.target_type === 'flag' ? 'flag' : 'segment'
          const action = (event.action.endsWith('.archive') ? `${kind}.archive` : meta.created ? `${kind}.create` : `${kind}.write`) as AuditAction
          return { id: event.id, at: event.at, actor: event.actor, action, target: event.target_id ?? '', changes: editLines(event, false) }
        })
    } catch {
      // A member who may not read the audit log still gets every other part of the page.
      audit.value = []
    }
  }

  /** Everything, once per project unless forced. */
  async function load(force = false) {
    if (!path.value || loading.value) return
    if (!force && loadedFor.value === path.value) return
    loading.value = true
    error.value = null
    try {
      versionsBy.value = { development: [], staging: [], production: [] }
      await Promise.all([loadFlags(), loadSegments(), ...ENVIRONMENTS.map(loadVersions), loadCheck()])
      loadedFor.value = path.value
      void loadAudit()
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
    } finally {
      loading.value = false
    }
  }

  /** Runs a write and reads again what it touched; answers with the API's refusal, or null. */
  async function attempt(write: () => Promise<unknown>, after: () => Promise<unknown>): Promise<string | null> {
    try {
      await write()
    } catch (caught) {
      return caught instanceof Error ? caught.message : String(caught)
    }
    await Promise.all([after(), loadCheck()])
    void loadAudit()
    return null
  }

  // --- Flags --------------------------------------------------------------------------------

  const createFlag = (flag: FlagDef) => attempt(
    () => apiRequest('POST', `${path.value}/flags`, { key: flag.key, kind: flag.kind, ...flagBody(flag) }),
    loadFlags
  )

  /** A whole-object PUT: the key and kind are the stored ones, which cannot change. */
  const saveFlag = (flag: FlagDef) => attempt(
    () => apiRequest('PUT', `${path.value}/flags/${encodeURIComponent(flag.key)}`, flagBody(flag)),
    loadFlags
  )

  function setFlagArchived(key: string, archived: boolean) {
    const flag = flags.value.find(entry => entry.key === key)
    if (!flag || flag.archived === archived) return Promise.resolve(null)
    return attempt(
      () => archived
        ? apiRequest('DELETE', `${path.value}/flags/${encodeURIComponent(key)}`)
        : apiRequest('PUT', `${path.value}/flags/${encodeURIComponent(key)}`, { ...flagBody(flag), archived: false }),
      loadFlags
    )
  }

  // --- Segments -----------------------------------------------------------------------------

  const createSegment = (segment: SegmentDef) => attempt(
    () => apiRequest('POST', `${path.value}/segments`, { key: segment.key, name: segment.name.trim(), description: segment.description.trim(), rule: segment.rule }),
    loadSegments
  )

  const saveSegment = (segment: SegmentDef) => attempt(
    () => apiRequest('PUT', `${path.value}/segments/${encodeURIComponent(segment.key)}`, { name: segment.name.trim(), description: segment.description.trim(), rule: segment.rule }),
    loadSegments
  )

  function setSegmentArchived(key: string, archived: boolean) {
    const segment = segments.value.find(entry => entry.key === key)
    if (!segment || segment.archived === archived) return Promise.resolve(null)
    return attempt(
      () => archived
        ? apiRequest('DELETE', `${path.value}/segments/${encodeURIComponent(key)}`)
        : apiRequest('PUT', `${path.value}/segments/${encodeURIComponent(key)}`, { name: segment.name, description: segment.description, rule: segment.rule, archived: false }),
      loadSegments
    )
  }

  // --- Publishing ---------------------------------------------------------------------------

  const publish = (environment: EnvironmentKind, note: string) => attempt(
    () => apiRequest('POST', `${path.value}/config/${environment}/publish`, { note: note.trim() }),
    () => loadVersions(environment)
  )

  const rollback = (environment: EnvironmentKind, to: ConfigVersionDef, reason: string) => {
    if (!reason.trim()) return Promise.resolve('Say why, for whoever reads the history next.')
    return attempt(
      () => apiRequest('POST', `${path.value}/config/${environment}/versions/${to.version}/rollback`, { reason: reason.trim() }),
      () => loadVersions(environment)
    )
  }

  /** Sets the draft back to what a version was compiled from; says what it could not bring back. */
  async function restoreDraft(version: ConfigVersionDef): Promise<{ problem: string | null, skipped: string[] }> {
    let skipped: string[] = []
    const problem = await attempt(async () => {
      const out = await apiRequest<Schema<'DraftRestoredOut'>>('POST', `${path.value}/config/${version.environment}/versions/${version.version}/restore`)
      skipped = [...(out.flags_skipped ?? [])]
    }, () => Promise.all([loadFlags(), loadSegments()]))
    return { problem, skipped }
  }

  function reset() {
    loadedFor.value = null
    flags.value = []
    segments.value = []
    audit.value = []
  }

  /* Another project: nothing of this one's may show there. */
  watch(path, () => {
    reset()
    void load()
  })

  return {
    flags, segments, versions, versionsBy, currentBy, audit, serverCheck, loading, error,
    load, loadVersion, loadVersions, createFlag, saveFlag, setFlagArchived,
    createSegment, saveSegment, setSegmentArchived, publish, rollback, restoreDraft, reset
  }
})
