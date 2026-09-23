// The targeting language the dashboard writes, read and checked the way the server reads and
// checks it: apps/backend/internal/targeting (rule.go, eval.go, bucket.go, version.go,
// facts.go) and internal/liveconfig (kind.go, compile.go, evaluate.go). A rule refused here
// is a rule the API would answer with a 422, said before the request rather than after it
// (ADR 0034); a player previewed here gets what /v1/config would give them.
//
// Everything here is pure. Where the server's words exist they are kept — "unknown
// operator", "does not exist" — so a message on screen and one in a log say the same thing.

// --- The grammar --------------------------------------------------------------------------

export type TargetOp
  = | 'eq' | 'ne' | 'in' | 'not_in'
    | 'lt' | 'lte' | 'gt' | 'gte'
    | 'exists' | 'not_exists'
    | 'starts_with' | 'contains'
    | 'version_lt' | 'version_lte' | 'version_gt' | 'version_gte'

export type Scalar = string | number | boolean

/**
 * One node of a rule, as stored and as sent: a group (`all`, `any`, `not`), a reference to a
 * segment, or a leaf comparing one key. An empty rule, at the root only, matches everyone.
 */
export interface TargetRule {
  all?: TargetRule[]
  any?: TargetRule[]
  not?: TargetRule
  segment?: string
  key?: string
  op?: TargetOp
  value?: Scalar
  values?: Scalar[]
}

export const MAX_RULE_DEPTH = 8
export const MAX_RULE_CONDITIONS = 64
export const MAX_RULE_VALUES = 256

/** What a key holds, which decides the operators offered and how a value is typed in. */
export type AttributeType = 'string' | 'number' | 'bool' | 'version'

// --- Who says so --------------------------------------------------------------------------

/**
 * Who asserts a value, which is the one thing a rule granting value has to know about it
 * (ADR 0027, docs/sdk/context-and-targeting.md §2). The dashboard shows it beside every key.
 */
export type Authority = 'connection' | 'history' | 'sdk' | 'client'

export const AUTHORITIES: Record<Authority, { label: string, short: string, icon: string, tone: string, forgeable: boolean, note: string }> = {
  connection: {
    label: 'From the connection',
    short: 'Server',
    icon: 'material-symbols:verified-user-outline',
    tone: 'success',
    forgeable: false,
    note: 'PlayerSense resolves it from the request\'s own address. The game never sends it, so a modified build cannot change it.'
  },
  history: {
    label: 'From the player\'s history',
    short: 'History',
    icon: 'material-symbols:verified-user-outline',
    tone: 'success',
    forgeable: false,
    note: 'Computed by PlayerSense from verified purchases and sessions. Cannot be forged, and costs a lookup per request, cached for an hour.'
  },
  sdk: {
    label: 'Reported by the SDK',
    short: 'SDK',
    icon: 'material-symbols:developer-mode-outline',
    tone: 'chart-1',
    forgeable: true,
    note: 'Sent by the SDK about the build and the device. A modified build can misreport itself, but only about itself.'
  },
  client: {
    label: 'Set by the game',
    short: 'Client',
    icon: 'material-symbols:warning-outline',
    tone: 'warning',
    forgeable: true,
    note: 'Set by the game through Context.Set. A modified client can send any value: fine for showing a banner, not for granting anything of value.'
  }
}

// --- The keys a rule can read -------------------------------------------------------------

export interface AttributeDef {
  key: string
  label: string
  type: AttributeType
  authority: Authority
  /** One sentence: what it holds, in the studio's words. */
  note: string
  /** Values to offer, where the set is known: platforms, continents, countries. */
  suggestions?: readonly string[]
  /** `micros`: stored in millionths of a dollar and typed in dollars. */
  unit?: 'micros' | 'seconds' | 'days'
  /** Needs `player_id`: absent for an install that has not signed in. */
  needsPlayer?: boolean
}

export const TARGET_PLATFORMS = ['ios', 'android', 'windows', 'macos', 'linux', 'web', 'console'] as const

export const COUNTRY_SUGGESTIONS = [
  'US', 'GB', 'DE', 'FR', 'JP', 'KR', 'CN', 'BR', 'MX', 'IN', 'ID', 'TR', 'RU', 'CA', 'AU',
  'IT', 'ES', 'NL', 'PL', 'SA', 'AE', 'AR', 'CO', 'PH', 'VN', 'TH', 'MY', 'EG', 'NG', 'ZA'
] as const

const regionNames = new Intl.DisplayNames(['en'], { type: 'region' })

/** "Turkey" for "TR"; the code itself for anything the browser cannot name. */
export function countryName(code: string): string {
  try {
    return regionNames.of(code.toUpperCase()) ?? code
  } catch {
    return code
  }
}

const HISTORY_FACTS = (scope: 'install' | 'player'): AttributeDef[] => {
  const whose = scope === 'player' ? 'the player, across every install they signed in on' : 'this install'
  const needsPlayer = scope === 'player' ? { needsPlayer: true } : {}
  return [
    { key: `commerce.${scope}.lifetime_spend_micros`, label: `Lifetime spend (${scope})`, type: 'number', authority: 'history', unit: 'micros', note: `Verified purchases by ${whose}, net of refunds, in USD.`, ...needsPlayer },
    { key: `commerce.${scope}.purchases`, label: `Purchases (${scope})`, type: 'number', authority: 'history', note: `Verified purchases by ${whose}.`, ...needsPlayer },
    { key: `commerce.${scope}.days_since_first_purchase`, label: `Days since first purchase (${scope})`, type: 'number', authority: 'history', unit: 'days', note: `Absent for ${whose} if nothing was ever bought.`, ...needsPlayer },
    { key: `commerce.${scope}.days_since_last_purchase`, label: `Days since last purchase (${scope})`, type: 'number', authority: 'history', unit: 'days', note: `Absent for ${whose} if nothing was ever bought.`, ...needsPlayer },
    { key: `activity.${scope}.days_since_install`, label: `Days since install (${scope})`, type: 'number', authority: 'history', unit: 'days', note: `Whole days since ${whose} first played.`, ...needsPlayer },
    { key: `activity.${scope}.days_since_last_seen`, label: `Days since last seen (${scope})`, type: 'number', authority: 'history', unit: 'days', note: `Whole days since ${whose} last played, before this session.`, ...needsPlayer },
    { key: `activity.${scope}.days_active`, label: `Days active (${scope})`, type: 'number', authority: 'history', unit: 'days', note: `Distinct days ${whose} played.`, ...needsPlayer },
    { key: `activity.${scope}.sessions`, label: `Sessions (${scope})`, type: 'number', authority: 'history', note: `Sessions played by ${whose}.`, ...needsPlayer },
    { key: `activity.${scope}.playtime_seconds`, label: `Playtime (${scope})`, type: 'number', authority: 'history', unit: 'seconds', note: `Seconds played by ${whose}.`, ...needsPlayer }
  ]
}

/** Every key PlayerSense itself knows about, in the order the picker lists them. */
export const BUILT_IN_ATTRIBUTES: readonly AttributeDef[] = [
  { key: 'geo.country', label: 'Country', type: 'string', authority: 'connection', suggestions: COUNTRY_SUGGESTIONS, note: 'ISO 3166 code of the country the connection comes out of. With a VPN on, that is the VPN\'s.' },
  { key: 'geo.region', label: 'Region', type: 'string', authority: 'connection', note: 'The ISO 3166-2 subdivision code: "CA" for California, "34" for Istanbul.' },
  { key: 'geo.city', label: 'City', type: 'string', authority: 'connection', note: 'The city the address geolocates to, in English.' },
  { key: 'geo.continent', label: 'Continent', type: 'string', authority: 'connection', suggestions: ['AF', 'AN', 'AS', 'EU', 'NA', 'OC', 'SA'], note: 'Two-letter continent code.' },
  { key: 'geo.timezone', label: 'Time zone', type: 'string', authority: 'connection', suggestions: ['Europe/Istanbul', 'Europe/London', 'America/New_York', 'Asia/Tokyo'], note: 'The IANA zone of the address.' },
  { key: 'network.vpn', label: 'On a VPN', type: 'bool', authority: 'connection', note: 'The address belongs to a known VPN provider.' },
  { key: 'network.proxy', label: 'Through a proxy', type: 'bool', authority: 'connection', note: 'The address is a known open or anonymising proxy.' },
  { key: 'network.tor', label: 'Through Tor', type: 'bool', authority: 'connection', note: 'The address is a Tor exit node.' },
  { key: 'network.relay', label: 'Through a relay', type: 'bool', authority: 'connection', note: 'The address is a privacy relay, such as iCloud Private Relay.' },
  { key: 'network.hosting', label: 'From a data centre', type: 'bool', authority: 'connection', note: 'The address belongs to a hosting provider: a server, a bot farm or a cloud VPN.' },
  { key: 'app.version', label: 'App version', type: 'version', authority: 'sdk', note: 'The game\'s version, compared part by part: 1.10.0 is newer than 1.9.3.' },
  { key: 'app.build', label: 'App build', type: 'string', authority: 'sdk', note: 'The build number, as the game reports it.' },
  { key: 'device.platform', label: 'Platform', type: 'string', authority: 'sdk', suggestions: TARGET_PLATFORMS, note: 'The platform the game runs on.' },
  { key: 'device.os_version', label: 'OS version', type: 'string', authority: 'sdk', note: 'The operating system as the device names it: "iOS 18.2", "Android 14".' },
  { key: 'device.locale', label: 'Device language', type: 'string', authority: 'sdk', suggestions: ['en-US', 'en-GB', 'de-DE', 'fr-FR', 'es-ES', 'pt-BR', 'ja-JP', 'ko-KR', 'tr-TR', 'zh-CN'], note: 'The device\'s locale: "tr-TR". Starts with "tr" to match every Turkish locale.' },
  { key: 'sdk.name', label: 'SDK', type: 'string', authority: 'sdk', suggestions: ['unity'], note: 'Which PlayerSense SDK the game is built with.' },
  { key: 'sdk.version', label: 'SDK version', type: 'version', authority: 'sdk', note: 'The PlayerSense SDK\'s own version.' },
  ...HISTORY_FACTS('install'),
  ...HISTORY_FACTS('player')
]

/**
 * The keys the game sets itself, as this project's players have sent them: the studio's own
 * vocabulary (docs/sdk/context-and-targeting.md §1). Stands in for the list the API will
 * read from the context keys seen on events; any other `context.` key can be typed in.
 */
export const CONTEXT_ATTRIBUTES: readonly AttributeDef[] = [
  { key: 'context.progress.level', label: 'Level', type: 'number', authority: 'client', note: 'Set by the game on Player scope.' },
  { key: 'context.progress.chapter', label: 'Chapter', type: 'number', authority: 'client', note: 'Set by the game on Player scope.' },
  { key: 'context.rank', label: 'Rank', type: 'string', authority: 'client', suggestions: ['bronze', 'silver', 'gold', 'platinum', 'diamond', 'master'], note: 'Set by the game on Player scope.' },
  { key: 'context.game_mode', label: 'Game mode', type: 'string', authority: 'client', suggestions: ['campaign', 'ranked', 'casual', 'coop', 'event'], note: 'Set by the game on Session scope.' },
  { key: 'context.tutorial.completed', label: 'Tutorial completed', type: 'bool', authority: 'client', note: 'Set by the game on Player scope.' },
  { key: 'context.guild.member', label: 'In a guild', type: 'bool', authority: 'client', note: 'Set by the game on Player scope.' },
  { key: 'context.tester', label: 'Tester build', type: 'bool', authority: 'client', note: 'Set by internal builds on Player scope.' },
  { key: 'context.quality', label: 'Graphics tier', type: 'string', authority: 'client', suggestions: ['low', 'medium', 'high', 'ultra'], note: 'Set by the game on Session scope.' }
]

export const ATTRIBUTES: readonly AttributeDef[] = [...BUILT_IN_ATTRIBUTES, ...CONTEXT_ATTRIBUTES]

/** The namespaces a key may start with: facts.go's list. Anything else is refused. */
export const NAMESPACES = ['context.', 'app.', 'device.', 'sdk.', 'geo.', 'network.', 'commerce.', 'activity.'] as const

export const NAMESPACE_GROUPS: { prefix: string, label: string }[] = [
  { prefix: 'geo.', label: 'Location' },
  { prefix: 'network.', label: 'Network' },
  { prefix: 'app.', label: 'App and device' },
  { prefix: 'commerce.', label: 'Spending' },
  { prefix: 'activity.', label: 'Activity' },
  { prefix: 'context.', label: 'Game context' }
]

export function groupOfKey(key: string): string {
  if (key.startsWith('device.') || key.startsWith('sdk.')) return 'App and device'
  return NAMESPACE_GROUPS.find(group => key.startsWith(group.prefix))?.label ?? 'Other'
}

export function authorityOf(key: string): Authority {
  if (key.startsWith('geo.') || key.startsWith('network.')) return 'connection'
  if (key.startsWith('commerce.') || key.startsWith('activity.')) return 'history'
  if (key.startsWith('context.')) return 'client'
  return 'sdk'
}

/**
 * What is known about a key: the catalogue's entry, or, for a `context.` key the studio typed
 * in, one inferred from the value it is compared with — the server does not coerce, so the
 * value's own type is the key's type as far as this rule is concerned.
 */
export function attributeOf(key: string, value?: Scalar | readonly Scalar[]): AttributeDef {
  const known = ATTRIBUTES.find(attribute => attribute.key === key)
  if (known) return known
  const sample = Array.isArray(value) ? value[0] : value
  const type: AttributeType = typeof sample === 'number' ? 'number' : typeof sample === 'boolean' ? 'bool' : 'string'
  return { key, label: key.replace(/^context\./, ''), type, authority: authorityOf(key), note: 'A key of the game\'s own, set through Context.Set.' }
}

// --- Operators ----------------------------------------------------------------------------

export const OPERATORS: Record<TargetOp, { label: string, arity: 'none' | 'one' | 'many' }> = {
  eq: { label: 'is', arity: 'one' },
  ne: { label: 'is not', arity: 'one' },
  in: { label: 'is any of', arity: 'many' },
  not_in: { label: 'is none of', arity: 'many' },
  gt: { label: 'is more than', arity: 'one' },
  gte: { label: 'is at least', arity: 'one' },
  lt: { label: 'is less than', arity: 'one' },
  lte: { label: 'is at most', arity: 'one' },
  starts_with: { label: 'starts with', arity: 'one' },
  contains: { label: 'contains', arity: 'one' },
  exists: { label: 'is set', arity: 'none' },
  not_exists: { label: 'is not set', arity: 'none' },
  version_gte: { label: 'is at least', arity: 'one' },
  version_gt: { label: 'is newer than', arity: 'one' },
  version_lte: { label: 'is at most', arity: 'one' },
  version_lt: { label: 'is older than', arity: 'one' }
}

/** The operators that mean something for a type, in the order the menu offers them. */
export const OPERATORS_FOR: Record<AttributeType, readonly TargetOp[]> = {
  string: ['eq', 'ne', 'in', 'not_in', 'starts_with', 'contains', 'exists', 'not_exists'],
  number: ['gte', 'gt', 'lte', 'lt', 'eq', 'ne', 'in', 'not_in', 'exists', 'not_exists'],
  bool: ['eq', 'ne', 'exists', 'not_exists'],
  version: ['version_gte', 'version_gt', 'version_lte', 'version_lt', 'eq', 'ne', 'in', 'not_in', 'exists', 'not_exists']
}

// --- Checking a rule ----------------------------------------------------------------------

const KNOWN_OPS = new Set(Object.keys(OPERATORS))

function isGroup(rule: TargetRule): boolean {
  return rule.all !== undefined || rule.any !== undefined || rule.not !== undefined
}

function isEmpty(rule: TargetRule): boolean {
  return !isGroup(rule) && rule.segment === undefined && rule.key === undefined && rule.op === undefined
}

/**
 * Why a rule would be refused, or null. The same walk as targeting.validate: one kind of node
 * per node, a segment reference standing alone, a value or values as the operator asks, the
 * depth and size limits, and keys in a namespace the server reads.
 */
export function ruleProblem(rule: TargetRule): string | null {
  if (isEmpty(rule)) return null
  let conditions = 0
  /* `path` numbers a node by its place in each group — "2", "3.1" — and is empty at the root. */
  const walk = (node: TargetRule, depth: number, path: string): string | null => {
    const where = path ? `condition ${path}` : 'the condition'
    if (depth > MAX_RULE_DEPTH) return `${where}: nested more than ${MAX_RULE_DEPTH} deep`
    const kinds = [node.all !== undefined, node.any !== undefined, node.not !== undefined, node.segment !== undefined, node.key !== undefined || node.op !== undefined].filter(Boolean).length
    if (kinds === 0) return `${where} is empty`
    if (kinds > 1) return `${where} holds more than one of all, any, not, a segment or a comparison`
    if (node.segment !== undefined) return node.segment ? null : `${where}: choose a segment`
    const children = node.all ?? node.any
    if (children) {
      if (!children.length) return `${path ? `group ${path}` : 'the group'} is empty`
      for (const [index, child] of children.entries()) {
        const problem = walk(child, depth + 1, path ? `${path}.${index + 1}` : String(index + 1))
        if (problem) return problem
      }
      return null
    }
    if (node.not) return walk(node.not, depth + 1, path)
    conditions += 1
    if (conditions > MAX_RULE_CONDITIONS) return `more than ${MAX_RULE_CONDITIONS} conditions`
    if (!node.key) return `${where}: choose a key`
    if (!NAMESPACES.some(prefix => node.key!.startsWith(prefix) && node.key!.length > prefix.length)) {
      return `${where}: "${node.key}" is not a key PlayerSense reads; the game's own keys start with "context."`
    }
    if (!node.op || !KNOWN_OPS.has(node.op)) return `${where}: unknown operator`
    const label = attributeOf(node.key, node.values ?? node.value).label
    const arity = OPERATORS[node.op].arity
    if (arity === 'none' && (node.value !== undefined || node.values !== undefined)) return `${where}: "${OPERATORS[node.op].label}" takes no value`
    if (arity === 'many') {
      if (!node.values?.length) return `${where}: ${label} needs at least one value`
      if (node.values.length > MAX_RULE_VALUES) return `${where}: more than ${MAX_RULE_VALUES} values`
    }
    if (arity === 'one') {
      if (node.value === undefined || node.value === '') return `${where}: ${label} needs a value`
      if (node.op.startsWith('version_') && !parseVersion(String(node.value))) return `${where}: "${node.value}" is not a version`
    }
    return null
  }
  return walk(rule, 1, '')
}

/** Every segment a rule refers to, directly. */
export function segmentsIn(rule: TargetRule | undefined): string[] {
  if (!rule) return []
  if (rule.segment) return [rule.segment]
  return [...(rule.all ?? []), ...(rule.any ?? []), ...(rule.not ? [rule.not] : [])].flatMap(segmentsIn)
}

/** Every key a rule compares, directly. */
export function keysIn(rule: TargetRule | undefined): string[] {
  if (!rule) return []
  if (rule.key) return [rule.key]
  return [...(rule.all ?? []), ...(rule.any ?? []), ...(rule.not ? [rule.not] : [])].flatMap(keysIn)
}

/**
 * Whether a set of segments, each with its rule, refers only to segments in the set and never
 * back to itself: liveconfig.Segments.Check. The first problem, in the server's words.
 */
export function segmentsProblem(rules: ReadonlyMap<string, TargetRule>): string | null {
  const state = new Map<string, 'walking' | 'done'>()
  const visit = (key: string, path: string[]): string | null => {
    if (state.get(key) === 'done') return null
    if (state.get(key) === 'walking') return `segment "${key}" refers to itself through ${[...path, key].join(' → ')}`
    const rule = rules.get(key)
    if (!rule) return `segment "${key}" does not exist`
    state.set(key, 'walking')
    for (const next of segmentsIn(rule)) {
      if (!rules.has(next)) return `segment "${key}" refers to "${next}", which does not exist`
      const problem = visit(next, [...path, key])
      if (problem) return problem
    }
    state.set(key, 'done')
    return null
  }
  for (const key of rules.keys()) {
    const problem = visit(key, [])
    if (problem) return problem
  }
  return null
}

/** A rule with every segment reference replaced by the segment's own rule, as a publish does. */
export function inlineSegments(rule: TargetRule, segments: ReadonlyMap<string, TargetRule>, depth = 0): TargetRule {
  if (depth > MAX_RULE_DEPTH) return rule
  if (rule.segment) {
    const target = segments.get(rule.segment)
    return target ? (isEmpty(target) ? {} : inlineSegments(target, segments, depth + 1)) : rule
  }
  if (rule.all) return { all: rule.all.map(child => inlineSegments(child, segments, depth)) }
  if (rule.any) return { any: rule.any.map(child => inlineSegments(child, segments, depth)) }
  if (rule.not) return { not: inlineSegments(rule.not, segments, depth) }
  return rule
}

// --- Matching -----------------------------------------------------------------------------

/** "1.12.3-beta" → [1, 12, 3]: numeric parts, anything after "-" or "+" ignored. */
export function parseVersion(text: string): number[] | null {
  const core = text.trim().split(/[-+]/)[0] ?? ''
  if (!/^\d+(\.\d+)*$/.test(core)) return null
  return core.split('.').map(Number)
}

export function compareVersions(a: string, b: string): number | null {
  const left = parseVersion(a)
  const right = parseVersion(b)
  if (!left || !right) return null
  for (let index = 0; index < Math.max(left.length, right.length); index++) {
    const difference = (left[index] ?? 0) - (right[index] ?? 0)
    if (difference) return Math.sign(difference)
  }
  return 0
}

/** Equal without coercion: "10" is not 10, and true is not 1. */
const same = (a: Scalar, b: Scalar) => typeof a === typeof b && a === b

export type TargetFacts = Record<string, Scalar>

/**
 * Whether a player with these facts is in the audience, as targeting.Matches decides it. A
 * key the player does not have is false for everything but "is not set". Segment references
 * are resolved through `segments`, and one that cannot be is nobody.
 */
export function ruleMatches(rule: TargetRule, facts: TargetFacts, segments: ReadonlyMap<string, TargetRule> = new Map(), depth = 0): boolean {
  if (isEmpty(rule)) return true
  if (depth > MAX_RULE_DEPTH * 2) return false
  if (rule.segment !== undefined) {
    const target = segments.get(rule.segment)
    return target ? ruleMatches(target, facts, segments, depth + 1) : false
  }
  if (rule.all) return rule.all.every(child => ruleMatches(child, facts, segments, depth + 1))
  if (rule.any) return rule.any.some(child => ruleMatches(child, facts, segments, depth + 1))
  if (rule.not) return !ruleMatches(rule.not, facts, segments, depth + 1)

  const actual = rule.key === undefined ? undefined : facts[rule.key]
  if (rule.op === 'not_exists') return actual === undefined
  if (actual === undefined) return false
  const value = rule.value
  switch (rule.op) {
    case 'exists': return true
    case 'eq': return value !== undefined && same(actual, value)
    case 'ne': return value !== undefined && !same(actual, value)
    case 'in': return (rule.values ?? []).some(candidate => same(actual, candidate))
    case 'not_in': return !(rule.values ?? []).some(candidate => same(actual, candidate))
    case 'lt': return typeof actual === 'number' && typeof value === 'number' && actual < value
    case 'lte': return typeof actual === 'number' && typeof value === 'number' && actual <= value
    case 'gt': return typeof actual === 'number' && typeof value === 'number' && actual > value
    case 'gte': return typeof actual === 'number' && typeof value === 'number' && actual >= value
    case 'starts_with': return typeof actual === 'string' && typeof value === 'string' && actual.startsWith(value)
    case 'contains': return typeof actual === 'string' && typeof value === 'string' && actual.includes(value)
    default: {
      if (typeof actual !== 'string' || typeof value !== 'string' || !rule.op?.startsWith('version_')) return false
      const order = compareVersions(actual, value)
      if (order === null) return false
      return { version_lt: order < 0, version_lte: order <= 0, version_gt: order > 0, version_gte: order >= 0 }[rule.op as 'version_lt'] ?? false
    }
  }
}

// --- Rollout ------------------------------------------------------------------------------

/** Hundredths of a percent: 2500 is 25%. */
export const FULL_ROLLOUT = 10_000
export const BUCKETS = 10_000

/**
 * The bucket a player lands in for one flag: the first eight bytes of
 * SHA-256(project \0 flag \0 install \0 salt), big-endian, modulo 10,000 — bucket.go to the
 * bit, so a preview here names the bucket the server will. Keyed by the install rather than
 * the player, so signing in does not move anybody between arms.
 */
export async function bucketOf(project: string, flag: string, install: string, salt: string): Promise<number> {
  const bytes = new TextEncoder().encode(`${project}\u0000${flag}\u0000${install}\u0000${salt}`)
  const digest = new DataView(await crypto.subtle.digest('SHA-256', bytes))
  return Number(digest.getBigUint64(0, false) % BigInt(BUCKETS))
}

/** "25%", "0.5%", "100%". */
export function formatRollout(rollout: number): string {
  const percent = rollout / 100
  return `${Number.isInteger(percent) ? percent : percent.toFixed(percent < 1 ? 2 : 1).replace(/0$/, '')}%`
}

// --- In words -----------------------------------------------------------------------------

/** A value as a rule writes it: a string quoted, a number and a bool bare, dollars for micros. */
export function writeScalar(value: Scalar, attribute?: AttributeDef): string {
  if (attribute?.unit === 'micros' && typeof value === 'number') return `$${writeFigure(value / 1_000_000, { decimals: 2 })}`
  if (attribute?.key === 'geo.country' && typeof value === 'string') return countryName(value)
  if (typeof value === 'string') return value
  return String(value)
}

/**
 * A rule as one line of English, for a list or a summary: "Country is any of Turkey,
 * Germany and App version is at least 1.12.0". Segments are named, not inlined.
 */
export function describeRule(rule: TargetRule | undefined, segmentName: (key: string) => string = key => key): string {
  if (!rule || isEmpty(rule)) return 'Everyone'
  const walk = (node: TargetRule, nested: boolean): string => {
    if (node.segment !== undefined) return `in ${segmentName(node.segment)}`
    if (node.not) {
      const inner = node.not
      if (inner.segment !== undefined) return `not in ${segmentName(inner.segment)}`
      return `not (${walk(inner, false)})`
    }
    const children = node.all ?? node.any
    if (children) {
      const joined = children.map(child => walk(child, true)).join(node.all ? ' and ' : ' or ')
      return nested && children.length > 1 ? `(${joined})` : joined
    }
    const attribute = attributeOf(node.key ?? '', node.values ?? node.value)
    const op = node.op ? OPERATORS[node.op] : undefined
    if (!op) return attribute.label
    if (op.arity === 'none') return `${attribute.label} ${op.label}`
    if (op.arity === 'many') {
      const list = (node.values ?? []).map(value => writeScalar(value, attribute))
      const shown = list.length > 4 ? [...list.slice(0, 3), `${list.length - 3} more`] : list
      return `${attribute.label} ${op.label} ${shown.join(', ')}`
    }
    return `${attribute.label} ${op.label} ${node.value === undefined ? '…' : writeScalar(node.value, attribute)}`
  }
  return walk(rule, false)
}

/** Counts a rule's conditions and segment references, for "3 conditions". */
export function conditionCount(rule: TargetRule | undefined): number {
  if (!rule || isEmpty(rule)) return 0
  if (rule.segment !== undefined || rule.key !== undefined) return 1
  return [...(rule.all ?? []), ...(rule.any ?? []), ...(rule.not ? [rule.not] : [])].reduce((sum, child) => sum + conditionCount(child), 0)
}
