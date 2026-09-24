// A project's API keys (project.md §52-53): what each kind of key may do and where it goes,
// what a key looks like, and a studio's usual keys as sample data.
//
// A key is one kind in one environment, never a set of scopes: what it may do is the single
// most important thing about it. The public half, `ps_<kind>_<env>_<random>`, is kept in the
// clear and says what the key is to anybody who reads it; the private half is kept only as a
// digest, so a key is shown once, when it is made, and never again.
//
// The routes exist (POST, GET and DELETE /organizations/{org}/projects/{project}/keys); the
// page keeps its keys in this browser until the dashboard is wired to them.

export type ApiKeyKind = 'project' | 'server' | 'reporting'

export interface ApiKeyKindInfo {
  /** What the dashboard calls it: the SDK key is the `project` class. */
  label: string
  /** Where it lives, in a few words. */
  where: string
  can: string
  cannot: string
  /** Written out in full so @nuxt/icon bundles it. */
  icon: string
  /** Whether it is a secret: an SDK key ships inside the game and cannot be. */
  secret: boolean
}

export const API_KEY_KINDS: Record<ApiKeyKind, ApiKeyKindInfo> = {
  project: {
    label: 'SDK key',
    where: 'In your game build',
    can: 'Sends analytics events and fetches flags and Live Ops.',
    cannot: 'Anyone can pull it out of a shipped build, so it can do nothing else: no reads, no purchases, no changes.',
    icon: 'material-symbols:sports-esports-outline',
    secret: false
  },
  server: {
    label: 'Server key',
    where: 'On your own servers',
    can: 'Verifies purchases, manages players, starts exports.',
    cannot: 'Never ship it in a game or a web page. Anyone holding it acts as your backend.',
    icon: 'material-symbols:dns-outline',
    secret: true
  },
  reporting: {
    label: 'Reporting key',
    where: 'In your dashboards and notebooks',
    can: 'Reads analytics through the reporting API.',
    cannot: 'Writes nothing: a leaked one discloses data but cannot change it.',
    icon: 'material-symbols:monitoring',
    secret: true
  }
}

export const API_KEY_KIND_ORDER: readonly ApiKeyKind[] = ['project', 'server', 'reporting']

export interface ApiKeyDef {
  id: string
  kind: ApiKeyKind
  environment: EnvironmentKind
  name: string
  /** The public half: the lookup key, safe to show and to log. */
  public_id: string
  created_at: string
  created_by: string
  last_used_at: string | null
  revoked_at: string | null
  revoked_by?: string
  /** Requests it made in the last 24 hours, an hour at a time: a sample figure. */
  hourly: number[]
  /** The key that replaced it, when it was rolled: it keeps working until it is revoked. */
  replaced_by?: string
}

/** The environment as the key writes it: production is `live`. */
export const keyEnvironment = (environment: EnvironmentKind) => (environment === 'production' ? 'live' : environment)

/** The part of the public half that is the same for every key of a kind and environment. */
export const publicPrefix = (kind: ApiKeyKind, environment: EnvironmentKind) => `ps_${kind}_${keyEnvironment(environment)}_`

const BASE32 = 'abcdefghijklmnopqrstuvwxyz234567'
const BASE64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'

function randomText(alphabet: string, length: number, random: () => number = Math.random): string {
  let out = ''
  for (let index = 0; index < length; index++) out += alphabet[Math.floor(random() * alphabet.length)]
  return out
}

/** A new key, both halves, the way the server mints one: 48 public bits and 192 private. */
export function mintKey(kind: ApiKeyKind, environment: EnvironmentKind) {
  const publicId = `${publicPrefix(kind, environment)}${randomText(BASE32, 10)}`
  return { publicId, secret: `${publicId}:${randomText(BASE64URL, 32)}` }
}

/** How long ago a key was last used before it is worth asking whether it is still needed. */
export const STALE_AFTER_DAYS = 90

/** What revoking a key does, said for the kind and environment it is. */
export function revokeWords(key: ApiKeyDef): string {
  if (key.kind === 'project' && key.environment === 'production') {
    return 'Every installed build that carries it stops sending events and fetching config at once, and nothing brings those players back but an update with a new key. Revoke it only once a build with its replacement is out.'
  }
  if (key.kind === 'project') return `${ENVIRONMENT_LABELS[key.environment]} builds that carry it stop sending events and fetching config at once.`
  if (key.kind === 'server') return 'Anything using it is refused from the next request: purchase verification, player operations and exports stop until your servers have a new key.'
  return 'Dashboards and scripts using it are refused from the next request.'
}

/** How to use a key, in the place it goes. */
export function usageCode(kind: ApiKeyKind, secret: string): { lang: 'csharp' | 'shellscript', title: string, code: string } {
  if (kind === 'project') {
    return {
      lang: 'csharp',
      title: 'Unity: in code, or in Project Settings > PlayerSense',
      code: `using PlayerSense;

// Before the first scene, once. Or leave this out, paste the key into
// Project Settings > PlayerSense and tick Auto Initialize.
SDK.Initialize("${secret}");`
    }
  }
  if (kind === 'server') {
    return {
      lang: 'shellscript',
      title: 'From your server, in the Authorization header',
      code: `# Keep it in your secret store, never in the repository.
export PLAYERSENSE_SERVER_KEY="${secret}"

curl -X POST "$PLAYERSENSE_API/v1/purchases/verify" \\
  -H "Authorization: Bearer $PLAYERSENSE_SERVER_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"player_id":"p_123","store":"app_store","receipt":"…"}'`
    }
  }
  return {
    lang: 'shellscript',
    title: 'From a dashboard or a notebook, in the Authorization header',
    code: `export PLAYERSENSE_REPORTING_KEY="${secret}"

curl "$PLAYERSENSE_API/v1/reports/retention?from=2026-09-01&to=2026-09-24" \\
  -H "Authorization: Bearer $PLAYERSENSE_REPORTING_KEY"`
  }
}

// --- Sample keys ------------------------------------------------------------------------------

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

const MINUTE_MS = 60_000
const HOUR_MS = 3_600_000
const DAY_MS = 86_400_000

/** A day of requests an hour, around `rate`, busier in the evening. */
export function sampleHourly(seed: string, rate: number): number[] {
  const random = seeded(seed)
  const hour = new Date().getHours()
  return Array.from({ length: 24 }, (_, index) => {
    const at = (hour - 23 + index + 24) % 24
    const shape = 0.6 + 0.8 * Math.max(0, Math.sin(((at - 7) / 24) * Math.PI * 2) * 0.5 + 0.5)
    return rate ? Math.round(rate * shape * (0.85 + random() * 0.3)) : 0
  })
}

export function seedApiKeys(): ApiKeyDef[] {
  const now = Date.now()
  const ago = (ms: number) => new Date(now - ms).toISOString()
  const key = (seed: string, kind: ApiKeyKind, environment: EnvironmentKind, rest: Omit<ApiKeyDef, 'id' | 'kind' | 'environment' | 'public_id' | 'hourly'> & { rate: number }): ApiKeyDef => {
    const random = seeded(seed)
    const { rate, ...fields } = rest
    return {
      id: `key_${randomText(BASE32, 12, random)}`,
      kind,
      environment,
      public_id: `${publicPrefix(kind, environment)}${randomText(BASE32, 10, random)}`,
      hourly: sampleHourly(seed, rate),
      ...fields
    }
  }

  const releaseV2 = key('sdk-live-2', 'project', 'production', { name: 'Release builds', created_at: ago(142 * DAY_MS), created_by: 'deniz@starfall.games', last_used_at: ago(4_000), revoked_at: null, rate: 184_000 })
  return [
    releaseV2,
    key('sdk-staging', 'project', 'staging', { name: 'QA builds', created_at: ago(301 * DAY_MS), created_by: 'deniz@starfall.games', last_used_at: ago(3 * MINUTE_MS), revoked_at: null, rate: 2_400 }),
    key('sdk-dev', 'project', 'development', { name: 'Editor and team devices', created_at: ago(301 * DAY_MS), created_by: 'deniz@starfall.games', last_used_at: ago(26 * MINUTE_MS), revoked_at: null, rate: 380 }),
    key('server-live', 'server', 'production', { name: 'Game server', created_at: ago(214 * DAY_MS), created_by: 'deniz@starfall.games', last_used_at: ago(40_000), revoked_at: null, rate: 3_100 }),
    key('server-staging', 'server', 'staging', { name: 'Staging server', created_at: ago(180 * DAY_MS), created_by: 'deniz@starfall.games', last_used_at: ago(2 * HOUR_MS), revoked_at: null, rate: 60 }),
    key('server-exporter', 'server', 'production', { name: 'Nightly export job (old)', created_at: ago(260 * DAY_MS), created_by: 'lena@starfall.games', last_used_at: ago(118 * DAY_MS), revoked_at: null, rate: 0 }),
    key('reporting-looker', 'reporting', 'production', { name: 'Looker dashboards', created_at: ago(96 * DAY_MS), created_by: 'lena@starfall.games', last_used_at: ago(52 * MINUTE_MS), revoked_at: null, rate: 44 }),
    key('reporting-notebook', 'reporting', 'production', { name: 'Data team notebooks', created_at: ago(33 * DAY_MS), created_by: 'ada@starfall.games', last_used_at: null, revoked_at: null, rate: 0 }),
    { ...key('sdk-live-1', 'project', 'production', { name: 'Release builds (1.x)', created_at: ago(310 * DAY_MS), created_by: 'deniz@starfall.games', last_used_at: ago(2 * DAY_MS), revoked_at: null, rate: 90 }), replaced_by: releaseV2.id },
    key('server-leaked', 'server', 'production', { name: 'Contractor sandbox', created_at: ago(200 * DAY_MS), created_by: 'deniz@starfall.games', last_used_at: ago(71 * DAY_MS), revoked_at: ago(70 * DAY_MS), revoked_by: 'deniz@starfall.games', rate: 0 })
  ]
}
