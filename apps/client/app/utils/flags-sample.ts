// Sample data for Growth > Feature flags, Segments and Publishing. What it stands in for:
//
//   The flags, segments and published versions are what GET …/flags, …/segments and
//   config_versions hold (apps/backend/internal/schemas/liveops.go, config.go); they are kept
//   in this browser until the API client is wired in, and every write goes through the same
//   checks the API would make (utils/flags.ts). There is no route yet to list a version's
//   history or read an old document back, so the history here is what that route will return.
//
//   The exposures are the `feature_exposure` events the SDK already sends once per flag, config
//   version and session (sdk/core/src/exposure.rs): flag, value, config_version and whether the
//   value came from a published document or the game's own default. Nothing queries them yet
//   (project.md §44); this draws what that query will answer.
//
// A tactics RPG's configuration: kill switches, an economy tuned remotely, a minimum version,
// a matchmaking table, a rollout, a test. Seeded by the flag's key, so a reload draws the same.

const DAY = 86_400_000
const HOUR = 3_600_000

export const SAMPLE_ACTORS = ['deniz@acme.games', 'mira@acme.games', 'hmmusicproducing@gmail.com', 'kaan@acme.games'] as const

/** Who is signed in: the address the account menu shows. */
export const CURRENT_ACTOR = 'hmmusicproducing@gmail.com'

const ago = (ms: number) => new Date(Date.now() - ms).toISOString()

function seeded(text: string): () => number {
  let state = 0
  for (const char of text) state = (Math.imul(state, 31) + char.charCodeAt(0)) >>> 0
  return () => {
    state = (state + 0x6D2B79F5) >>> 0
    let t = state
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

// --- Segments -----------------------------------------------------------------------------

const segment = (key: string, name: string, description: string, rule: TargetRule, age: number, archived = false): SegmentDef => ({
  key,
  name,
  description,
  rule,
  archived,
  created_at: ago(age * DAY),
  updated_at: ago(age * DAY * 0.4),
  updated_by: SAMPLE_ACTORS[age % SAMPLE_ACTORS.length]!
})

const SAMPLE_SEGMENTS: SegmentDef[] = [
  segment('whales', 'Whales', 'Players who have spent $100 or more, across every install they signed in on.', { key: 'commerce.player.lifetime_spend_micros', op: 'gte', value: 100_000_000 }, 120),
  segment('payers', 'Payers', 'Installs with at least one verified purchase.', { key: 'commerce.install.purchases', op: 'gt', value: 0 }, 118),
  segment('new_players', 'New players', 'Installs in their first three days.', { key: 'activity.install.days_since_install', op: 'lt', value: 3 }, 90),
  segment('lapsed_players', 'Lapsed players', 'Back after two weeks or more away.', { key: 'activity.install.days_since_last_seen', op: 'gte', value: 14 }, 64),
  segment('internal_testers', 'Internal testers', 'Studio and QA devices running a tester build.', { key: 'context.tester', op: 'eq', value: true }, 150),
  segment('emerging_markets', 'Emerging markets', 'Countries where the store\'s regional pricing applies.', { key: 'geo.country', op: 'in', values: ['BR', 'IN', 'ID', 'PH', 'VN', 'MX', 'TR', 'EG', 'NG', 'CO'] }, 45),
  segment('anonymised_traffic', 'Anonymised traffic', 'Connections through a VPN, a proxy, Tor or a data centre.', { any: [{ key: 'network.vpn', op: 'eq', value: true }, { key: 'network.proxy', op: 'eq', value: true }, { key: 'network.tor', op: 'eq', value: true }, { key: 'network.hosting', op: 'eq', value: true }] }, 30),
  segment('high_value_mobile', 'High-value mobile', 'Payers on a phone who have played for a week or more.', { all: [{ segment: 'payers' }, { key: 'device.platform', op: 'in', values: ['ios', 'android'] }, { key: 'activity.install.days_active', op: 'gte', value: 7 }] }, 20),
  segment('closed_beta_2025', 'Closed beta 2025', 'Installs from last year\'s closed beta.', { key: 'app.version', op: 'version_lt', value: '2.0.0' }, 300, true)
]

// --- Flags --------------------------------------------------------------------------------

interface FlagSeed {
  key: string
  name: string
  description: string
  kind: FlagKind
  fallback: Json
  rules?: FlagVariant[]
  rollout?: number
  purpose: FlagPurpose
  tags: string[]
  grantsValue?: boolean
  archived?: boolean
  /** Days since it was created, and since it was last edited. */
  age: number
  edited: number
}

const rule = (audience: TargetRule, value: Json, rollout = FULL_ROLLOUT): FlagVariant => ({ audience, value, rollout })

const MATCHMAKING: Json = { mmr_spread: 150, max_wait_seconds: 45, bots_after_seconds: 30, regions: ['eu', 'na'], party_size_limit: 3 }

const SEEDS: FlagSeed[] = [
  { key: 'shop.enabled', name: 'Shop', description: 'Turns the in-game shop off without a release: a store outage, a pricing mistake, a payment provider down.', kind: 'bool', fallback: true, purpose: 'kill_switch', tags: ['economy', 'store'], age: 210, edited: 34 },
  { key: 'pvp.ranked_enabled', name: 'Ranked matchmaking', description: 'Ranked queues. Off for builds older than 2.4.0, which desync in ranked turns.', kind: 'bool', fallback: true, rules: [rule({ key: 'app.version', op: 'version_lt', value: '2.4.0' }, false)], purpose: 'kill_switch', tags: ['pvp'], age: 180, edited: 12 },
  { key: 'app.min_supported_version', name: 'Minimum supported version', description: 'Builds below this ask the player to update before they can play. iOS is held one release behind while review is slower.', kind: 'string', fallback: '2.4.0', rules: [rule({ key: 'device.platform', op: 'eq', value: 'ios' }, '2.3.2')], purpose: 'config', tags: ['client'], age: 200, edited: 6 },
  { key: 'economy.daily_reward_gems', name: 'Daily reward', description: 'Gems granted by the daily login chest.', kind: 'number', fallback: 50, rules: [rule({ segment: 'new_players' }, 100), rule({ segment: 'whales' }, 80)], purpose: 'config', tags: ['economy'], grantsValue: true, age: 160, edited: 3 },
  { key: 'economy.energy_refill_seconds', name: 'Energy refill time', description: 'Seconds for one point of energy to refill.', kind: 'number', fallback: 480, rules: [rule({ segment: 'lapsed_players' }, 240)], purpose: 'config', tags: ['economy'], grantsValue: true, age: 150, edited: 21 },
  { key: 'ads.interstitial_every_n_matches', name: 'Interstitial frequency', description: 'An interstitial ad after every Nth match. 0 turns them off.', kind: 'number', fallback: 3, rules: [rule({ segment: 'payers' }, 0), rule({ key: 'activity.install.days_since_install', op: 'lt', value: 2 }, 0)], purpose: 'config', tags: ['ads', 'monetization'], age: 140, edited: 9 },
  { key: 'store.layout', name: 'Store layout', description: 'The new carousel storefront, rolling out on phones.', kind: 'string', fallback: 'grid', rules: [rule({ key: 'device.platform', op: 'in', values: ['ios', 'android'] }, 'carousel', 2500)], purpose: 'release', tags: ['store'], age: 26, edited: 1 },
  { key: 'matchmaking.config', name: 'Matchmaking tuning', description: 'MMR spread, queue timeouts and when bots fill a lobby. South America and Asia have thinner queues.', kind: 'json', fallback: MATCHMAKING, rules: [rule({ key: 'geo.continent', op: 'in', values: ['SA', 'AS'] }, { mmr_spread: 250, max_wait_seconds: 60, bots_after_seconds: 20, regions: ['sa', 'ap'], party_size_limit: 3 })], purpose: 'config', tags: ['pvp'], age: 170, edited: 2 },
  { key: 'events.halloween_banner', name: 'Halloween banner', description: 'The main menu banner for the Halloween event, past the tutorial chapters.', kind: 'bool', fallback: false, rules: [rule({ all: [{ key: 'context.progress.level', op: 'gte', value: 10 }, { key: 'app.version', op: 'version_gte', value: '2.5.0' }] }, true)], purpose: 'release', tags: ['liveops'], age: 8, edited: 1 },
  { key: 'tutorial.skip_button', name: 'Skip tutorial', description: 'Tests whether a skip button on the tutorial helps day-one retention.', kind: 'bool', fallback: false, rules: [rule({ key: 'context.tutorial.completed', op: 'ne', value: true }, true, 5000)], purpose: 'experiment', tags: ['onboarding'], age: 19, edited: 19 },
  { key: 'guilds.v2', name: 'Guilds 2.0', description: 'The rebuilt guild screens. Testers first, then a tenth of players.', kind: 'bool', fallback: false, rules: [rule({ segment: 'internal_testers' }, true), rule({}, true, 1000)], purpose: 'release', tags: ['social'], age: 12, edited: 0.2 },
  { key: 'client.log_level', name: 'Client log level', description: 'How much the client logs. Debug for testers only.', kind: 'string', fallback: 'warn', rules: [rule({ segment: 'internal_testers' }, 'debug')], purpose: 'config', tags: ['client', 'debug'], age: 190, edited: 60 },
  { key: 'battle_pass.price_tier', name: 'Battle pass price tier', description: 'Tests the regional price point for the season pass where regional pricing applies.', kind: 'string', fallback: 'standard', rules: [rule({ segment: 'emerging_markets' }, 'regional', 5000)], purpose: 'experiment', tags: ['monetization'], grantsValue: true, age: 33, edited: 33 },
  { key: 'chat.global_enabled', name: 'Global chat', description: 'The world chat channel. Off where the store listing does not allow open chat.', kind: 'bool', fallback: true, rules: [rule({ key: 'geo.country', op: 'in', values: ['CN', 'KR'] }, false)], purpose: 'kill_switch', tags: ['social'], age: 205, edited: 70 },
  { key: 'graphics.frame_cap', name: 'Frame rate cap', description: 'The frame rate the renderer caps at. Lower on low-end devices to save battery.', kind: 'number', fallback: 60, rules: [rule({ key: 'context.quality', op: 'eq', value: 'low' }, 30)], purpose: 'config', tags: ['client', 'performance'], age: 130, edited: 44 },
  { key: 'offers.starter_pack_discount', name: 'Starter pack discount', description: 'Percent off the starter pack for new players who have not bought anything.', kind: 'number', fallback: 0, rules: [rule({ all: [{ segment: 'new_players' }, { not: { segment: 'payers' } }] }, 30)], purpose: 'config', tags: ['monetization', 'store'], grantsValue: true, age: 6, edited: 0.05 },
  { key: 'ui.new_main_menu', name: 'New main menu', description: 'The redesigned main menu, out to everyone since the 2.3 update.', kind: 'bool', fallback: true, purpose: 'release', tags: ['client'], age: 95, edited: 64 },
  { key: 'inventory.legacy_grid', name: 'Legacy inventory grid', description: 'The old inventory screen, kept for a month after the redesign.', kind: 'bool', fallback: false, purpose: 'release', tags: ['client'], archived: true, age: 240, edited: 80 }
]

const flagOf = (seed: FlagSeed): FlagDef => ({
  key: seed.key,
  name: seed.name,
  description: seed.description,
  kind: seed.kind,
  fallback: seed.fallback,
  rules: seed.rules ?? [],
  rollout: seed.rollout ?? FULL_ROLLOUT,
  salt: '',
  archived: seed.archived ?? false,
  created_at: ago(seed.age * DAY),
  updated_at: ago(seed.edited * DAY),
  updated_by: SAMPLE_ACTORS[Math.round(seed.age) % SAMPLE_ACTORS.length]!,
  purpose: seed.purpose,
  tags: seed.tags,
  grantsValue: seed.grantsValue ?? false
})

// --- Published versions -------------------------------------------------------------------

type Documents = FlagDocument[]

const edit = (flags: Documents, key: string, change: (flag: FlagDocument) => FlagDocument | null): Documents =>
  flags.flatMap((flag) => {
    if (flag.key !== key) return [flag]
    const next = change(flag)
    return next ? [next] : []
  })

/**
 * Each environment's history, newest first, as the edits that separate one version from the
 * one before: production trails staging, which trails development, which trails the draft.
 */
function sampleVersions(flags: readonly FlagDef[], segments: readonly SegmentDef[]): ConfigVersionDef[] {
  const draft = flags.filter(flag => !flag.archived).map(documentOf)
  const segmentRules = segments.filter(entry => !entry.archived).map(entry => ({ key: entry.key, rule: entry.rule }))

  // Development trails the draft by one edit: the starter pack discount went from 25 to 30.
  const development = edit(draft, 'offers.starter_pack_discount', flag => ({ ...flag, rules: flag.rules.map(entry => ({ ...entry, value: 25 })) }))
  // Staging has neither the discount nor the wider store rollout.
  const staging = edit(edit(development, 'offers.starter_pack_discount', () => null), 'store.layout', flag => ({ ...flag, rules: flag.rules.map(entry => ({ ...entry, rollout: 1000 })) }))
  // Production is behind staging on the banner, the guilds rollout and the matchmaking spread.
  const production = edit(
    edit(
      edit(staging, 'events.halloween_banner', () => null),
      'guilds.v2',
      flag => ({ ...flag, rules: flag.rules.slice(0, 1) })
    ),
    'matchmaking.config',
    flag => ({ ...flag, fallback: { ...(flag.fallback as Record<string, Json>), mmr_spread: 120 } })
  )

  const version = (environment: EnvironmentKind, number: number, hoursAgo: number, by: string, note: string, documents: Documents): ConfigVersionDef => ({
    environment,
    version: number,
    published_at: ago(hoursAgo * HOUR),
    published_by: by,
    note,
    flags: documents,
    segments: segmentRules
  })

  const productionPrevious = edit(production, 'pvp.ranked_enabled', flag => ({ ...flag, rules: [] }))
  const productionOlder = edit(productionPrevious, 'store.layout', () => null)

  return [
    version('development', 57, 0.7, 'deniz@acme.games', 'Starter pack discount for new players', development),
    version('development', 56, 5, 'deniz@acme.games', 'Guilds 2.0 to a tenth of players', edit(development, 'offers.starter_pack_discount', () => null)),
    version('development', 55, 27, 'kaan@acme.games', 'Halloween banner', edit(edit(development, 'offers.starter_pack_discount', () => null), 'guilds.v2', flag => ({ ...flag, rules: flag.rules.slice(0, 1) }))),
    version('staging', 41, 5.5, 'mira@acme.games', 'QA pass for 2.5.0: banner, guilds rollout, matchmaking spread', staging),
    version('staging', 40, 50, 'mira@acme.games', 'Store carousel on phones, 10%', edit(staging, 'events.halloween_banner', () => null)),
    version('production', 38, 49, 'mira@acme.games', 'Store carousel to 10% of phones', production),
    version('production', 37, 290, 'kaan@acme.games', 'Ranked off below 2.4.0 after the desync reports', productionPrevious),
    version('production', 36, 530, 'deniz@acme.games', 'Energy refill for lapsed players', productionOlder)
  ]
}

// --- The audit trail ----------------------------------------------------------------------

function sampleAudit(flags: readonly FlagDef[], versions: readonly ConfigVersionDef[]): ConfigAuditEntry[] {
  const entries: ConfigAuditEntry[] = []
  let id = 0
  const push = (entry: Omit<ConfigAuditEntry, 'id'>) => entries.push({ id: `sample-${++id}`, ...entry })

  for (const flag of flags) {
    push({ at: flag.created_at, actor: flag.updated_by, action: 'flag.create', target: flag.key, changes: [`Created as ${FLAG_KIND_LABELS[flag.kind].toLowerCase()}, serving ${writeValue(flag.fallback)}`] })
    if (flag.updated_at !== flag.created_at) {
      const lines = flag.rules.length
        ? [`Rule ${flag.rules.length}: ${describeRule(flag.rules[flag.rules.length - 1]!.audience)} → ${writeValue(flag.rules[flag.rules.length - 1]!.value)}`]
        : [`Fallback ${writeValue(flag.fallback)}`]
      push({ at: flag.updated_at, actor: flag.updated_by, action: flag.archived ? 'flag.archive' : 'flag.write', target: flag.key, changes: flag.archived ? ['Archived'] : lines })
    }
  }
  for (const version of versions) {
    push({ at: version.published_at, actor: version.published_by, action: 'liveops.publish', target: version.environment, environment: version.environment, version: version.version, note: version.note, changes: [`${version.flags.length} flags`] })
  }
  return entries.sort((a, b) => b.at.localeCompare(a.at))
}

export interface LiveConfigState {
  flags: FlagDef[]
  segments: SegmentDef[]
  versions: ConfigVersionDef[]
  audit: ConfigAuditEntry[]
}

/** A project's flags as a new visit finds them, drawn fresh. */
export function seedLiveConfig(): LiveConfigState {
  const flags = SEEDS.map(flagOf)
  const segments = SAMPLE_SEGMENTS.map(entry => ({ ...entry }))
  const versions = sampleVersions(flags, segments)
  return { flags, segments, versions, audit: sampleAudit(flags, versions) }
}

// --- Exposures ----------------------------------------------------------------------------

/** Players a day, in production. */
const DAILY_PLAYERS = 48_000

/**
 * How many of a day's players read each flag: the ones behind a screen few open read less,
 * one on every launch path is read by everyone, and a flag the shipped build never reads is
 * read by nobody — which is the flag that is costing a rule and doing nothing.
 */
const READ_SHARE: Record<string, number> = {
  'shop.enabled': 0.98,
  'pvp.ranked_enabled': 0.41,
  'app.min_supported_version': 1,
  'economy.daily_reward_gems': 0.72,
  'economy.energy_refill_seconds': 0.88,
  'ads.interstitial_every_n_matches': 0.64,
  'store.layout': 0.36,
  'matchmaking.config': 0.52,
  'events.halloween_banner': 0,
  'tutorial.skip_button': 0.06,
  'guilds.v2': 0,
  'client.log_level': 1,
  'battle_pass.price_tier': 0.22,
  'chat.global_enabled': 0.47,
  'graphics.frame_cap': 1,
  'offers.starter_pack_discount': 0,
  'ui.new_main_menu': 0.99
}

/** One value a flag served, day by day. */
export interface ExposureLine {
  label: string
  /** The value served, or null for the game's own default: no document had reached the device. */
  value: Json | null
  source: 'rule' | 'fallback' | 'default'
  rule?: number
  daily: number[]
  players: number
}

export interface FlagExposureSample {
  days: number[]
  lines: ExposureLine[]
  exposures: number
  previousExposures: number
  players: number
  /** Minutes since the last exposure, or null for none in the range. */
  lastSeen: number | null
  /** Exposures by the config version the device held. */
  byVersion: { version: number, exposures: number }[]
  /** By app version, the share still answered by the game's own default. */
  byAppVersion: { version: string, players: number, defaultShare: number }[]
}

/**
 * A flag's exposures in production over `days` days: each value it served, from which rule,
 * and how many reads found no document at all. Shares follow the published rules — a rule for
 * a tenth of players serves about a tenth — and the audiences are sized by a draw per rule.
 */
export function sampleFlagExposure(flag: FlagDocument, days: number, productionVersion: number): FlagExposureSample {
  const random = seeded(`flags:${flag.key}:${days}:${JSON.stringify(flag.rules)}:${flag.rollout}`)
  const readShare = READ_SHARE[flag.key] ?? 0.5
  const now = Date.now()
  const dayStarts = Array.from({ length: days }, (_, index) => {
    const at = new Date(now - (days - 1 - index) * DAY)
    at.setUTCHours(0, 0, 0, 0)
    return at.getTime()
  })

  // What share of readers each outcome gets, before the noise.
  const inFlag = flag.rollout / FULL_ROLLOUT
  let remaining = 1
  const shares: { label: string, value: Json | null, source: ExposureLine['source'], rule?: number, share: number }[] = []
  flag.rules.forEach((variant, index) => {
    const audience = Object.keys(variant.audience).length === 0 ? 1 : 0.06 + random() * 0.3
    const matched = remaining * audience
    const served = matched * (variant.rollout / FULL_ROLLOUT) * inFlag
    shares.push({ label: `Rule ${index + 1}`, value: variant.value, source: 'rule', rule: index, share: served })
    remaining -= matched
  })
  const defaultShare = 0.015 + random() * 0.025
  const fallbackShare = Math.max(0, 1 - shares.reduce((sum, entry) => sum + entry.share, 0))
  shares.push({ label: 'Fallback', value: flag.fallback, source: 'fallback', share: fallbackShare })

  // Rules serving the same value as the fallback are merged into it, as the event records only the value.
  const lines: ExposureLine[] = []
  for (const entry of shares) {
    const same = lines.find(line => line.source !== 'default' && sameJson(line.value, entry.value))
    const daily = dayStarts.map((_, index) => {
      const weekly = 1 + 0.12 * Math.sin((index / 7) * Math.PI * 2)
      return Math.round(DAILY_PLAYERS * readShare * (1 - defaultShare) * entry.share * weekly * (0.9 + random() * 0.2) * 1.6)
    })
    if (same) {
      same.daily = same.daily.map((value, index) => value + daily[index]!)
      if (entry.source === 'fallback') same.label = `${same.label} + fallback`
    } else {
      lines.push({ label: entry.label, value: entry.value, source: entry.source, ...(entry.rule === undefined ? {} : { rule: entry.rule }), daily, players: 0 })
    }
  }
  lines.push({
    label: 'Game default',
    value: null,
    source: 'default',
    daily: dayStarts.map(() => Math.round(DAILY_PLAYERS * readShare * defaultShare * 1.6 * (0.8 + random() * 0.4))),
    players: 0
  })
  for (const line of lines) line.players = Math.round(line.daily.reduce((sum, value) => sum + value, 0) / 1.6 / Math.sqrt(days) * 2.2)

  const exposures = lines.reduce((sum, line) => sum + line.daily.reduce((total, value) => total + value, 0), 0)
  const versions = [productionVersion, productionVersion - 1, productionVersion - 2].filter(version => version > 0)
  const byVersion = versions.map((version, index) => ({ version, exposures: Math.round(exposures * [0.78, 0.17, 0.05][index]!) }))
  const appVersions = ['2.5.1', '2.5.0', '2.4.1', '2.4.0', '2.3.2']
  const byAppVersion = appVersions.map((version, index) => ({
    version,
    players: Math.round(DAILY_PLAYERS * readShare * [0.12, 0.3, 0.33, 0.17, 0.08][index]! * 3),
    defaultShare: defaultShare * (0.6 + index * 0.35)
  }))

  return {
    days: dayStarts,
    lines: lines.filter(line => line.daily.some(Boolean)),
    exposures,
    previousExposures: Math.round(exposures * (0.85 + random() * 0.3)),
    players: Math.round(DAILY_PLAYERS * readShare * Math.min(days, 7) * 0.9),
    lastSeen: readShare ? Math.round(random() * 4) : null,
    byVersion: exposures ? byVersion : [],
    byAppVersion: exposures ? byAppVersion : []
  }
}

/** Reads in the last seven days and the seven before, for the list. */
export function sampleWeeklyReads(key: string): { reads: number, previous: number, daily: number[] } {
  const random = seeded(`reads:${key}`)
  const share = READ_SHARE[key] ?? 0.5
  const daily = Array.from({ length: 14 }, (_, index) => Math.round(DAILY_PLAYERS * share * 1.6 * (1 + 0.1 * Math.sin(index)) * (0.9 + random() * 0.2)))
  const sum = (values: number[]) => values.reduce((total, value) => total + value, 0)
  return { reads: sum(daily.slice(7)), previous: sum(daily.slice(0, 7)), daily: daily.slice(7) }
}

/**
 * The share of last week's players a segment holds, from a draw per condition: what the reach
 * estimate on a segment will read from the players table.
 */
export function sampleReach(rule: TargetRule): number {
  const count = Math.max(1, conditionCount(rule))
  if (!Object.keys(rule).length) return 1
  const random = seeded(`reach:${JSON.stringify(rule)}`)
  return Math.max(0.002, Math.min(0.9, (0.04 + random() * 0.35) / Math.sqrt(count)))
}
