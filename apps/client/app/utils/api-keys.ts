// A project's API keys (project.md §52-53): what each kind of key may do and where it goes,
// and what a key looks like.
//
// A key is one kind in one environment, never a set of scopes: what it may do is the single
// most important thing about it. The public half, `ps_<kind>_<env>_<random>`, is kept in the
// clear and says what the key is to anybody who reads it; the private half is kept only as a
// digest, so a key is shown once, when it is made, and never again.

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
  /** Requests it made in the last 24 hours, an hour at a time, oldest first. */
  hourly: number[]
  /** When it was rolled: a new key of the same kind and environment took its place. */
  rolled_at?: string | null
  /** The key that took its place. */
  replaced_by?: string
  /** When a rolled key stops working: it works on until then, so the new one can be deployed. */
  expires_at?: string | null
}

/** Whether a key still opens anything: not revoked, and not past a roll's grace. */
export const keyWorks = (key: ApiKeyDef, now = Date.now()) => !key.revoked_at
  && !(key.rolled_at && (!key.expires_at || Date.parse(key.expires_at) <= now))

/** The environment as the key writes it: production is `live`. */
export const keyEnvironment = (environment: EnvironmentKind) => (environment === 'production' ? 'live' : environment)

/** The part of the public half that is the same for every key of a kind and environment. */
export const publicPrefix = (kind: ApiKeyKind, environment: EnvironmentKind) => `ps_${kind}_${keyEnvironment(environment)}_`

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
