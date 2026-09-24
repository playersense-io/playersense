// Feature flags as the dashboard holds them, and the rules for changing them: the FlagOut and
// SegmentOut the API returns (apps/backend/internal/schemas/liveops.go), the checks a write is
// refused by (ADR 0034), what a publish compiles into a document (internal/liveconfig), and how
// a player is answered from one (evaluate.go).
//
// A flag and a segment belong to the project, not to an environment. There is one draft;
// an environment is whichever version of it was last published there, so "what production
// serves" and "what the draft says" are two different documents, and the difference between
// them is what the Publishing screen is about.
//
// Three fields are the dashboard's own and are not stored by the API yet: `purpose`, `tags`
// and `grantsValue`. They sort and warn; nothing about what a player receives depends on them.

export type FlagKind = 'bool' | 'string' | 'number' | 'json'
export type Json = null | boolean | number | string | Json[] | { [key: string]: Json }

/** One rule of a flag: who it is for, what they get, and what share of them get it. */
export interface FlagVariant {
  audience: TargetRule
  value: Json
  /** Hundredths of a percent of the audience. */
  rollout: number
}

/**
 * Why the flag exists, which decides when it should go: a release is meant to be removed once
 * it is out, a kill switch and a remote value are meant to stay, and an experiment ends with a
 * decision.
 */
export type FlagPurpose = 'release' | 'kill_switch' | 'config' | 'experiment'

export interface FlagDef {
  key: string
  name: string
  description: string
  kind: FlagKind
  fallback: Json
  rules: FlagVariant[]
  /** Hundredths of a percent of every player; outside it, the fallback. */
  rollout: number
  salt: string
  archived: boolean
  created_at: string
  updated_at: string
  updated_by: string
  purpose: FlagPurpose
  tags: string[]
  /** Grants currency, items, discounts or paid content: rules on forgeable keys are warned. */
  grantsValue: boolean
}

export interface SegmentDef {
  key: string
  name: string
  description: string
  rule: TargetRule
  archived: boolean
  created_at: string
  updated_at: string
  updated_by: string
}

/** The part of a flag a published document carries. Its name and tags are not in it. */
export type FlagDocument = Pick<FlagDef, 'key' | 'kind' | 'fallback' | 'rules' | 'rollout' | 'salt'>

export type EnvironmentKind = 'development' | 'staging' | 'production'

export const ENVIRONMENTS: readonly EnvironmentKind[] = ['development', 'staging', 'production']

export const ENVIRONMENT_LABELS: Record<EnvironmentKind, string> = {
  development: 'Development',
  staging: 'Staging',
  production: 'Production'
}

/** For a column with room for a word: "Dev", "Staging", "Prod". */
export const ENVIRONMENT_SHORT: Record<EnvironmentKind, string> = {
  development: 'Dev',
  staging: 'Staging',
  production: 'Prod'
}

/** Who sees an environment, in the fewest words: what a founder needs to know about it. */
export const ENVIRONMENT_WHO: Record<EnvironmentKind, string> = {
  development: 'Your team\'s devices',
  staging: 'QA and testers',
  production: 'Every player'
}

export const ENVIRONMENT_NOTES: Record<EnvironmentKind, string> = {
  development: 'Builds with a development key: the team\'s own devices and the editor.',
  staging: 'Builds with a staging key: QA, and testers outside the studio.',
  production: 'Every player of the released game.'
}

/**
 * One publish: an immutable version of the draft, as it was, in one environment. Holds what
 * was authored — flags and segment rules — and the compiled document is derived from it, so
 * a version can be compared with the draft in the terms somebody edited.
 */
export interface ConfigVersionDef {
  environment: EnvironmentKind
  version: number
  published_at: string
  published_by: string
  note: string
  flags: FlagDocument[]
  segments: { key: string, rule: TargetRule }[]
}

export type AuditAction
  = | 'flag.create' | 'flag.write' | 'flag.archive' | 'flag.restore'
    | 'segment.create' | 'segment.write' | 'segment.archive' | 'segment.restore'
    | 'liveops.publish' | 'draft.restore'

/** A decision, as the audit log records it (ADR 0037): who, what, and what it changed. */
export interface ConfigAuditEntry {
  id: string
  at: string
  actor: string
  action: AuditAction
  /** The flag's or segment's key, or the environment published to. */
  target: string
  /** What changed, a line each: "Rollout 25% → 50%". */
  changes: string[]
  environment?: EnvironmentKind
  version?: number
  note?: string
}

export const FLAG_KIND_LABELS: Record<FlagKind, string> = {
  bool: 'Boolean',
  string: 'String',
  number: 'Number',
  json: 'JSON'
}

export const FLAG_KIND_TONES: Record<FlagKind, string> = {
  bool: 'chart-1',
  string: 'chart-3',
  number: 'chart-4',
  json: 'chart-5'
}

export const FLAG_KIND_NOTES: Record<FlagKind, string> = {
  bool: 'On or off: a feature, a kill switch, a banner.',
  string: 'One of several words: a layout, a variant, a minimum version.',
  number: 'A tuning value: a reward, a price tier, a timer, a drop rate.',
  json: 'A whole block of configuration the game parses: a level, a shop, a matchmaking table.'
}

export const FLAG_PURPOSES: Record<FlagPurpose, { label: string, note: string, icon: string }> = {
  release: {
    label: 'Release',
    note: 'Rolls out new work. Remove it from the code once everyone has it.',
    icon: 'material-symbols:rocket-launch-outline'
  },
  kill_switch: {
    label: 'Kill switch',
    note: 'Turns something off in an emergency without a release. Permanent.',
    icon: 'material-symbols:power-settings-new'
  },
  config: {
    label: 'Remote config',
    note: 'A value tuned without a release: economy, difficulty, timers. Permanent.',
    icon: 'material-symbols:tune'
  },
  experiment: {
    label: 'Experiment',
    note: 'Compares variants on a share of players. Ends with a decision.',
    icon: 'material-symbols:science-outline'
  }
}

export const KEY_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/

// --- Values -------------------------------------------------------------------------------

/**
 * Why a value will not do for a kind, in the words of somebody editing a flag rather than as
 * a Go type: liveconfig.Kind.Accepts. Absence is refused for every kind, JSON included — a flag
 * exists to answer without the network.
 */
export function kindProblem(kind: FlagKind, value: Json | undefined): string | null {
  const got = value === null || value === undefined
    ? 'nothing'
    : Array.isArray(value) ? 'a list' : typeof value === 'object' ? 'an object' : typeof value === 'boolean' ? 'true or false' : typeof value === 'number' ? 'a number' : 'text'
  if (value === null || value === undefined) return `needs a value, got ${got}`
  if (kind === 'bool' && typeof value !== 'boolean') return `expected true or false, got ${got}`
  if (kind === 'string' && typeof value !== 'string') return `expected text, got ${got}`
  if (kind === 'number' && (typeof value !== 'number' || !Number.isFinite(value))) return `expected a number, got ${got}`
  return null
}

/** A value short enough for a cell: `true`, `"grid"`, `50`, `{ 4 keys }`. */
export function writeValue(value: Json | undefined): string {
  if (value === undefined || value === null) return '–'
  if (typeof value === 'string') return `"${value}"`
  if (Array.isArray(value)) return `[ ${value.length} ${value.length === 1 ? 'item' : 'items'} ]`
  if (typeof value === 'object') {
    const count = Object.keys(value).length
    return `{ ${count} ${count === 1 ? 'key' : 'keys'} }`
  }
  return String(value)
}

export function sameJson(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/**
 * How one value became another, for a change line. Two objects are compared key by key, so a
 * matchmaking table that changed one number says which: "mmr_spread 120 → 150".
 */
export function writeChange(before: Json | undefined, after: Json | undefined): string {
  const isObject = (value: Json | undefined): value is { [key: string]: Json } => value !== null && typeof value === 'object' && !Array.isArray(value)
  if (isObject(before) && isObject(after)) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(key => !sameJson(before[key], after[key]))
    const shown = keys.slice(0, 3).map((key) => {
      if (!(key in before)) return `${key} added: ${writeValue(after[key])}`
      if (!(key in after)) return `${key} removed`
      return `${key} ${writeValue(before[key])} → ${writeValue(after[key])}`
    })
    return keys.length > 3 ? `${shown.join(', ')} and ${keys.length - 3} more` : shown.join(', ')
  }
  return `${writeValue(before)} → ${writeValue(after)}`
}

/**
 * "4 minutes ago", measured against the later of the clock and the stamp: a version published a
 * moment ago is stamped after the tick before it, and would read "in a few seconds".
 */
export function sinceWords(iso: string, now: Date): string {
  const stamp = new Date(iso)
  return relativeTimeWords(iso, now < stamp ? stamp : now)
}

/** A fresh value of a kind, for a rule just added. */
export function defaultValue(kind: FlagKind): Json {
  return { bool: true, string: '', number: 0, json: {} }[kind]
}

// --- Checking a write ---------------------------------------------------------------------

/** Where on the flag a problem is, so the editor can put it beside the field. */
export interface FlagProblem {
  where: 'key' | 'name' | 'description' | 'fallback' | 'rollout' | `rule:${number}`
  /** Within a rule, which part of it. */
  field?: 'audience' | 'value' | 'rollout'
  message: string
}

/**
 * Every reason the API would refuse this flag, in order. `segments` is the project's; an
 * archived one is refused as a reference, since the next publish would leave it out and the
 * rule would reach nobody.
 */
export function flagProblems(flag: FlagDef, segments: readonly SegmentDef[]): FlagProblem[] {
  const problems: FlagProblem[] = []
  if (flag.key.length < 2 || flag.key.length > 64) problems.push({ where: 'key', message: 'A key is 2 to 64 characters.' })
  else if (!KEY_PATTERN.test(flag.key)) problems.push({ where: 'key', message: 'Letters, digits, dots, dashes and underscores, starting with a letter or digit.' })
  if (!flag.name.trim()) problems.push({ where: 'name', message: 'Give the flag a name.' })
  if (flag.name.length > 120) problems.push({ where: 'name', message: 'A name is at most 120 characters.' })
  if (flag.description.length > 500) problems.push({ where: 'description', message: 'A description is at most 500 characters.' })
  const fallback = kindProblem(flag.kind, flag.fallback)
  if (fallback) problems.push({ where: 'fallback', message: `The fallback ${fallback}.` })
  if (flag.rollout < 0 || flag.rollout > FULL_ROLLOUT) problems.push({ where: 'rollout', message: 'A rollout is between 0% and 100%.' })

  const live = new Map(segments.filter(segment => !segment.archived).map(segment => [segment.key, segment]))
  const archived = new Set(segments.filter(segment => segment.archived).map(segment => segment.key))
  flag.rules.forEach((rule, index) => {
    const where = `rule:${index}` as const
    const audience = ruleProblem(rule.audience)
    if (audience) problems.push({ where, field: 'audience', message: `Rule ${index + 1}: ${audience}.` })
    for (const key of segmentsIn(rule.audience)) {
      if (archived.has(key)) problems.push({ where, field: 'audience', message: `Rule ${index + 1}: segment "${key}" is archived.` })
      else if (!live.has(key)) problems.push({ where, field: 'audience', message: `Rule ${index + 1}: segment "${key}" does not exist.` })
    }
    const value = kindProblem(flag.kind, rule.value)
    if (value) problems.push({ where, field: 'value', message: `Rule ${index + 1} ${value}.` })
    if (rule.rollout < 0 || rule.rollout > FULL_ROLLOUT) problems.push({ where, field: 'rollout', message: `Rule ${index + 1}: a rollout is between 0% and 100%.` })
  })
  return problems
}

/** A segment's problems, checked with itself in the set: a self-reference is a cycle. */
export function segmentProblems(segment: SegmentDef, segments: readonly SegmentDef[]): string[] {
  const problems: string[] = []
  if (segment.key.length < 2 || segment.key.length > 64) problems.push('A key is 2 to 64 characters.')
  else if (!KEY_PATTERN.test(segment.key)) problems.push('Letters, digits, dots, dashes and underscores, starting with a letter or digit.')
  if (!segment.name.trim()) problems.push('Give the segment a name.')
  const rule = ruleProblem(segment.rule)
  if (rule) problems.push(`${rule[0]!.toUpperCase()}${rule.slice(1)}.`)
  const set = new Map(segments.filter(other => !other.archived && other.key !== segment.key).map(other => [other.key, other.rule]))
  set.set(segment.key, segment.rule)
  const cycle = segmentsProblem(set)
  if (cycle) problems.push(`${cycle[0]!.toUpperCase()}${cycle.slice(1)}.`)
  return problems
}

// --- Documents ----------------------------------------------------------------------------

export function documentOf(flag: FlagDef): FlagDocument {
  return { key: flag.key, kind: flag.kind, fallback: flag.fallback, rules: flag.rules, rollout: flag.rollout, salt: flag.salt }
}

/** Keys a rule reads that cost the server a history lookup per request. */
export const needsHistory = (rule: TargetRule) => keysIn(rule).some(key => key.startsWith('commerce.') || key.startsWith('activity.'))

export interface CompiledDocument {
  version: number
  flags: { key: string, kind: FlagKind, fallback: Json, rules: FlagVariant[], rollout: number, salt?: string }[]
  liveops_events: []
  needs_player_facts: boolean
}

/**
 * What a publish sends to the environment: every flag, each rule with its segments inlined,
 * as liveconfig.Compile writes it. The SDK never sees this — it is what the server evaluates
 * against; a player's snapshot carries only the values it decided.
 */
export function compileDocument(version: number, flags: readonly FlagDocument[], segments: readonly { key: string, rule: TargetRule }[]): CompiledDocument {
  const rules = new Map(segments.map(segment => [segment.key, segment.rule]))
  const compiled = flags.map(flag => ({
    key: flag.key,
    kind: flag.kind,
    fallback: flag.fallback,
    rules: flag.rules.map(rule => ({ audience: inlineSegments(rule.audience, rules), value: rule.value, rollout: rule.rollout })),
    rollout: flag.rollout,
    ...(flag.salt ? { salt: flag.salt } : {})
  }))
  return {
    version,
    flags: compiled,
    liveops_events: [],
    needs_player_facts: compiled.some(flag => flag.rules.some(rule => needsHistory(rule.audience)))
  }
}

// --- What changed -------------------------------------------------------------------------

/**
 * One flag's document against another's, a line per difference, in the order an editor reads
 * a flag: its fallback, its reach, then each rule.
 */
export function flagChanges(before: FlagDocument, after: FlagDocument, segmentName: (key: string) => string = key => key): string[] {
  const lines: string[] = []
  if (!sameJson(before.fallback, after.fallback)) lines.push(`Fallback: ${writeChange(before.fallback, after.fallback)}`)
  if (before.rollout !== after.rollout) lines.push(`Flag rollout ${formatRollout(before.rollout)} → ${formatRollout(after.rollout)}`)
  if (before.salt !== after.salt) lines.push('Salt changed: every player is drawn again')
  const count = Math.max(before.rules.length, after.rules.length)
  for (let index = 0; index < count; index++) {
    const old = before.rules[index]
    const next = after.rules[index]
    const name = `Rule ${index + 1}`
    if (!old && next) {
      lines.push(`${name} added: ${describeRule(next.audience, segmentName)} → ${writeValue(next.value)}${next.rollout < FULL_ROLLOUT ? ` to ${formatRollout(next.rollout)}` : ''}`)
      continue
    }
    if (old && !next) {
      lines.push(`${name} removed: ${describeRule(old.audience, segmentName)}`)
      continue
    }
    if (!old || !next) continue
    if (!sameJson(old.audience, next.audience)) lines.push(`${name} audience: ${describeRule(next.audience, segmentName)}`)
    if (!sameJson(old.value, next.value)) lines.push(`${name} value: ${writeChange(old.value, next.value)}`)
    if (old.rollout !== next.rollout) lines.push(`${name} rollout ${formatRollout(old.rollout)} → ${formatRollout(next.rollout)}`)
  }
  return lines
}

/** The draft against a published version: what the next publish there would change. */
export interface DraftChange {
  kind: 'flag' | 'segment'
  key: string
  change: 'added' | 'changed' | 'removed'
  lines: string[]
}

export function draftChanges(version: ConfigVersionDef | null, flags: readonly FlagDef[], segments: readonly SegmentDef[]): DraftChange[] {
  const names = new Map(segments.map(segment => [segment.key, segment.name]))
  const segmentName = (key: string) => names.get(key) ?? key
  const changes: DraftChange[] = []
  const published = new Map((version?.flags ?? []).map(flag => [flag.key, flag]))
  const live = flags.filter(flag => !flag.archived)
  for (const flag of live) {
    const before = published.get(flag.key)
    const after = documentOf(flag)
    if (!before) changes.push({ kind: 'flag', key: flag.key, change: 'added', lines: [`Serves ${writeValue(flag.fallback)} by default${flag.rules.length ? `, with ${flag.rules.length} ${flag.rules.length === 1 ? 'rule' : 'rules'}` : ''}`] })
    else if (!sameJson(before, after)) changes.push({ kind: 'flag', key: flag.key, change: 'changed', lines: flagChanges(before, after, segmentName) })
  }
  for (const flag of published.values()) {
    if (!live.some(entry => entry.key === flag.key)) changes.push({ kind: 'flag', key: flag.key, change: 'removed', lines: ['Archived: the game falls back to its own default'] })
  }
  const segmentsBefore = new Map((version?.segments ?? []).map(segment => [segment.key, segment.rule]))
  for (const segment of segments.filter(entry => !entry.archived)) {
    const before = segmentsBefore.get(segment.key)
    if (!before) changes.push({ kind: 'segment', key: segment.key, change: 'added', lines: [describeRule(segment.rule, segmentName)] })
    else if (!sameJson(before, segment.rule)) changes.push({ kind: 'segment', key: segment.key, change: 'changed', lines: [`Audience: ${describeRule(segment.rule, segmentName)}`] })
  }
  for (const key of segmentsBefore.keys()) {
    if (!segments.some(segment => segment.key === key && !segment.archived)) changes.push({ kind: 'segment', key, change: 'removed', lines: ['Archived'] })
  }
  return changes
}

/** Where one flag stands in one environment. */
export type FlagEnvironmentState = 'same' | 'changed' | 'absent' | 'unpublished'

export function flagStateIn(flag: FlagDef, version: ConfigVersionDef | null): FlagEnvironmentState {
  if (!version) return 'unpublished'
  const published = version.flags.find(entry => entry.key === flag.key)
  if (!published) return flag.archived ? 'same' : 'absent'
  if (flag.archived) return 'changed'
  return sameJson(published, documentOf(flag)) ? 'same' : 'changed'
}

// --- Answering a player -------------------------------------------------------------------

/** Why a player got what they got, as a preview explains it. */
export type Resolution
  = | { kind: 'rule', rule: number, bucket: number | null }
    | { kind: 'rule-rollout', rule: number, bucket: number }
    | { kind: 'flag-rollout', bucket: number }
    | { kind: 'no-rule' }
    | { kind: 'missing-install' }

export interface Answer {
  value: Json
  resolution: Resolution
}

/**
 * What one player receives for one flag: evaluate.go's order. The flag's rollout first, then
 * the first rule whose audience matches — and a matching rule whose own rollout leaves the
 * player out answers with the fallback rather than falling through to the next rule.
 */
export async function answerFor(
  project: string,
  flag: FlagDocument,
  facts: TargetFacts,
  install: string,
  segments: ReadonlyMap<string, TargetRule>
): Promise<Answer> {
  const partial = (rollout: number) => rollout > 0 && rollout < FULL_ROLLOUT
  if (flag.rollout <= 0) return { value: flag.fallback, resolution: { kind: 'flag-rollout', bucket: 0 } }
  if (partial(flag.rollout)) {
    if (!install) return { value: flag.fallback, resolution: { kind: 'missing-install' } }
    const bucket = await bucketOf(project, flag.key, install, flag.salt)
    if (bucket >= flag.rollout) return { value: flag.fallback, resolution: { kind: 'flag-rollout', bucket } }
  }
  for (const [index, rule] of flag.rules.entries()) {
    if (!ruleMatches(rule.audience, facts, segments)) continue
    if (rule.rollout >= FULL_ROLLOUT) return { value: rule.value, resolution: { kind: 'rule', rule: index, bucket: null } }
    if (rule.rollout <= 0) return { value: flag.fallback, resolution: { kind: 'rule-rollout', rule: index, bucket: 0 } }
    if (!install) return { value: flag.fallback, resolution: { kind: 'missing-install' } }
    const bucket = await bucketOf(project, `${flag.key}#${index}`, install, flag.salt)
    return bucket < rule.rollout
      ? { value: rule.value, resolution: { kind: 'rule', rule: index, bucket } }
      : { value: flag.fallback, resolution: { kind: 'rule-rollout', rule: index, bucket } }
  }
  return { value: flag.fallback, resolution: { kind: 'no-rule' } }
}

/** A flag's rules and fallback as the values it can serve, for a summary: `true · false`. */
export function servedValues(flag: FlagDocument): Json[] {
  const values: Json[] = []
  for (const value of [...flag.rules.map(rule => rule.value), flag.fallback]) {
    if (!values.some(known => sameJson(known, value))) values.push(value)
  }
  return values
}

/** Whether some players are held back: a flag or a rule rolled out to less than everyone. */
export const isPartial = (flag: FlagDocument) =>
  flag.rollout < FULL_ROLLOUT || flag.rules.some(rule => rule.rollout > 0 && rule.rollout < FULL_ROLLOUT)

/** Uses a key the game sets itself, for a flag that grants value: the warning ADR 0027 asks for. */
export function forgeableKeys(flag: Pick<FlagDef, 'rules'>, segments: readonly SegmentDef[]): string[] {
  const rules = new Map(segments.map(segment => [segment.key, segment.rule]))
  const keys = flag.rules.flatMap(rule => keysIn(inlineSegments(rule.audience, rules)))
  return [...new Set(keys.filter(key => authorityOf(key) === 'client'))]
}
