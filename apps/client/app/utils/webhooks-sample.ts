// Sample data for Data & commerce > Webhooks, generated in the browser. What it stands in for:
//
//   webhook_endpoints, webhook_signing_secrets, webhook_deliveries and
//   webhook_delivery_attempts (project.md §29, §49-51), which have no schema, handler or
//   worker yet. The destinations are a studio's usual five: its game server granting
//   purchases, the same on staging, an ops bot, a privacy system and one turned off.
//
// Deliveries are drawn for the last seven days from each destination's own profile, seeded by
// the destination and the hour, so a reload draws the same deliveries and the list moves on
// with the clock. Every try follows the real schedule (RETRY_AFTER_MS), so a delivery's
// status is what the schedule makes of its tries, not a label drawn at random.

const HOUR_MS = 3_600_000
const DAY_MS = 86_400_000

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

const ALPHABET = 'abcdefghijkmnpqrstuvwxyz23456789'

/** A Stripe-like id: a prefix and characters that are the same for the same seed. */
function idOf(prefix: string, random: () => number, length = 20): string {
  let out = ''
  for (let index = 0; index < length; index++) out += ALPHABET[Math.floor(random() * ALPHABET.length)]
  return `${prefix}_${out}`
}

export function newSecret(): string {
  const random = seeded(`${Date.now()}:${Math.random()}`)
  return idOf('whsec', random, 32)
}

export function newDestinationId(): string {
  return idOf('we', seeded(`${Date.now()}:${Math.random()}`), 14)
}

const ago = (ms: number) => new Date(Date.now() - ms).toISOString()

// --- The destinations -----------------------------------------------------------------------

export function seedWebhooks(): WebhookDestination[] {
  const secret = (seed: string, days: number) => ({ value: idOf('whsec', seeded(seed), 32), created_at: ago(days * DAY_MS) })
  return [
    {
      id: 'we_k7m2p9x4q8r3tz',
      name: 'Game server: grant purchases',
      description: 'Grants what was bought and keeps memberships in step. Owned by the backend team.',
      url: 'https://api.starfall.games/playersense/webhooks',
      events: ['purchase.verified', 'purchase.refunded', 'purchase.rejected', 'subscription.started', 'subscription.renewed', 'subscription.canceled', 'subscription.expired', 'subscription.billing_issue'],
      environments: ['production'],
      status: 'enabled',
      version: '2026-09-01',
      headers: [{ name: 'X-Gateway-Token', value: 'gw_live_8c1f', secret: true }],
      rate_limit: 50,
      secret: { ...secret('grant', 12), previous: { value: idOf('whsec', seeded('grant-old'), 32), expires_at: new Date(Date.now() + 19 * HOUR_MS).toISOString() } },
      created_at: ago(214 * DAY_MS),
      updated_at: ago(5 * HOUR_MS),
      updated_by: 'deniz@starfall.games',
      history: [
        { at: ago(214 * DAY_MS), actor: 'deniz@starfall.games', text: 'Created, listening to Purchases' },
        { at: ago(96 * DAY_MS), actor: 'deniz@starfall.games', text: 'Now also listening to Subscriptions' },
        { at: ago(60 * DAY_MS), actor: 'deniz@starfall.games', text: 'Payload version 2026-04-15 → 2026-09-01' },
        { at: ago(12 * DAY_MS), actor: 'ops@starfall.games', text: 'Header X-Gateway-Token added' },
        { at: ago(5 * HOUR_MS), actor: 'deniz@starfall.games', text: 'Signing secret rolled. The old one signs too until tomorrow' }
      ]
    },
    {
      id: 'we_b4n8c2w6h9v5sd',
      name: 'Staging server',
      description: 'The same handler as the game server, on staging. Hears sandbox purchases.',
      url: 'https://staging-api.starfall.games/playersense/webhooks',
      events: ['purchase.verified', 'purchase.refunded', 'purchase.rejected', 'subscription.started', 'subscription.renewed', 'subscription.canceled', 'subscription.expired', 'subscription.billing_issue'],
      environments: ['staging', 'development'],
      status: 'enabled',
      version: '2026-09-01',
      headers: [],
      rate_limit: 10,
      secret: secret('staging', 90),
      created_at: ago(180 * DAY_MS),
      updated_at: ago(20 * DAY_MS),
      updated_by: 'deniz@starfall.games',
      history: [
        { at: ago(180 * DAY_MS), actor: 'deniz@starfall.games', text: 'Created, listening to Purchases' },
        { at: ago(96 * DAY_MS), actor: 'deniz@starfall.games', text: 'Now also listening to Subscriptions' },
        { at: ago(20 * DAY_MS), actor: 'deniz@starfall.games', text: 'Rate limit 10 a second' }
      ]
    },
    {
      id: 'we_q3r9t5y2u8e6wa',
      name: 'Ops bot',
      description: 'Posts publishes, rollbacks, Live Ops windows and alerts to the team\'s channels.',
      url: 'https://ops-bot.starfall.games/hooks/playersense',
      events: ['config.published', 'config.rolled_back', 'liveops.window.started', 'liveops.window.ended', 'experiment.started', 'experiment.concluded', 'alert.triggered', 'alert.resolved'],
      environments: ['production', 'staging'],
      status: 'enabled',
      version: '2026-04-15',
      headers: [],
      rate_limit: null,
      secret: secret('opsbot', 140),
      created_at: ago(140 * DAY_MS),
      updated_at: ago(33 * DAY_MS),
      updated_by: 'lena@starfall.games',
      history: [
        { at: ago(140 * DAY_MS), actor: 'lena@starfall.games', text: 'Created, listening to Publishing and Live Ops' },
        { at: ago(33 * DAY_MS), actor: 'lena@starfall.games', text: 'Now also listening to Experiments and Alerts' }
      ]
    },
    {
      id: 'we_z8x2c5v9b3n7mf',
      name: 'Privacy requests',
      description: 'Records every erasure in the compliance system, and collects finished exports.',
      url: 'https://privacy.starfall.games/api/v2/playersense',
      events: ['player.deleted', 'export.completed', 'export.failed'],
      environments: ['production'],
      status: 'enabled',
      version: '2026-09-01',
      headers: [{ name: 'Authorization', value: 'Bearer pv_live_x81', secret: true }],
      rate_limit: 5,
      secret: secret('privacy', 75),
      created_at: ago(75 * DAY_MS),
      updated_at: ago(75 * DAY_MS),
      updated_by: 'legal-ops@starfall.games',
      history: [
        { at: ago(75 * DAY_MS), actor: 'legal-ops@starfall.games', text: 'Created, listening to Data and privacy' }
      ]
    },
    {
      id: 'we_h6j3k9l2m5n8pb',
      name: 'Rewards server v1',
      description: 'The old rewards service. Replaced by the game server in March; kept for its history.',
      url: 'https://rewards-v1.starfall.games/hook',
      events: ['purchase.verified'],
      environments: ['production'],
      status: 'disabled',
      disabled_reason: 'manual',
      version: '2025-11-01',
      headers: [],
      rate_limit: null,
      secret: secret('rewards', 300),
      created_at: ago(320 * DAY_MS),
      updated_at: ago(190 * DAY_MS),
      updated_by: 'deniz@starfall.games',
      history: [
        { at: ago(320 * DAY_MS), actor: 'deniz@starfall.games', text: 'Created, listening to purchase.verified' },
        { at: ago(190 * DAY_MS), actor: 'deniz@starfall.games', text: 'Disabled: replaced by Game server: grant purchases' }
      ]
    }
  ]
}

// --- How each destination behaves -----------------------------------------------------------

interface Profile {
  /** Deliveries an hour, on average. */
  rate: number
  /** The chance a first try fails, and that a retry does. */
  firstFail: number
  retryFail: number
  /** Median answer time, in ms. */
  latency: number
  /** From this long ago, every try fails: the server is down. */
  downFor?: number
  /** An outage that is over: every try between these two ages failed. */
  outage?: readonly [number, number]
  /** What a failure looks like: an HTTP status, or null for no answer. */
  failures: readonly (number | null)[]
  /** How much of the day's traffic each hour carries, so the chart has the game's day in it. */
  daily?: boolean
}

const PROFILES: Record<string, Profile> = {
  we_k7m2p9x4q8r3tz: { rate: 16, firstFail: 0.004, retryFail: 0.05, latency: 140, failures: [503, 502, null], daily: true },
  we_b4n8c2w6h9v5sd: { rate: 4, firstFail: 0.03, retryFail: 0.3, latency: 620, outage: [7.5 * DAY_MS, 3.2 * DAY_MS], failures: [null, null, 500, 504] },
  we_q3r9t5y2u8e6wa: { rate: 1.2, firstFail: 0.01, retryFail: 0.1, latency: 90, failures: [502, null] },
  we_z8x2c5v9b3n7mf: { rate: 1.6, firstFail: 0.02, retryFail: 0.2, latency: 380, downFor: 41 * HOUR_MS, failures: [500] },
  we_h6j3k9l2m5n8pb: { rate: 0, firstFail: 0, retryFail: 0, latency: 200, failures: [410] }
}

/** A destination made here: its server answers, quickly. */
const FRESH: Profile = { rate: 0, firstFail: 0.01, retryFail: 0.05, latency: 160, failures: [500] }

const profileOf = (id: string) => PROFILES[id] ?? FRESH

/** When a failing destination is turned off: three days after its last success. */
export function autoDisableAt(destination: WebhookDestination, now: number): number | null {
  const profile = PROFILES[destination.id]
  if (destination.status !== 'enabled' || !profile?.downFor) return null
  return now - profile.downFor + 3 * DAY_MS
}

/** Since when a destination has answered nothing but failures, or null. */
export function failingSince(destination: WebhookDestination, now: number): number | null {
  const profile = PROFILES[destination.id]
  return destination.status === 'enabled' && profile?.downFor ? now - profile.downFor : null
}

// --- What an event carries ------------------------------------------------------------------

const PRODUCTS = [
  { id: 'gems_pouch', name: 'Pouch of gems', price: 1.99 },
  { id: 'gems_chest', name: 'Chest of gems', price: 9.99 },
  { id: 'season_pass', name: 'Season pass', price: 4.99 },
  { id: 'starter_pack', name: 'Starter pack', price: 2.99 },
  { id: 'gems_vault', name: 'Vault of gems', price: 99.99 }
]
const SUBSCRIPTIONS = [
  { id: 'vip_monthly', name: 'VIP club, monthly', price: 6.99, period: 'P1M' },
  { id: 'vip_yearly', name: 'VIP club, yearly', price: 59.99, period: 'P1Y' }
]
const COUNTRIES = ['US', 'DE', 'GB', 'TR', 'BR', 'JP', 'FR', 'KR', 'CA', 'MX']
const STORES = ['app_store', 'google_play', 'steam']
const LIVEOPS = [
  { key: 'double_xp_weekend', name: 'Double XP weekend', kind: 'boost' },
  { key: 'happy_hour_gems', name: 'Happy hour gems', kind: 'sale' },
  { key: 'harvest_festival', name: 'Harvest festival', kind: 'festival' }
]

/** The body of an event of this type, as it is POSTed: the envelope and its `data`. */
export function eventDocument(type: string, options: { id: string, created: number, environment: EnvironmentKind, version: string, seed?: string }) {
  const random = seeded(options.seed ?? options.id)
  const pick = <T>(list: readonly T[]) => list[Math.floor(random() * list.length)]!
  const at = (offset = 0) => new Date(options.created + offset).toISOString()
  const player = `p_${Math.floor(random() * 90_000_000 + 10_000_000).toString(36)}`
  const country = pick(COUNTRIES)
  const store = options.environment === 'production' ? pick(STORES) : pick(STORES.slice(0, 2))
  const price = (amount: number) => (options.version === '2025-11-01' ? { price_cents: Math.round(amount * 100), currency: 'USD' } : { price: { amount, currency: 'USD', usd: amount } })

  const data = ((): Record<string, unknown> => {
    const product = pick(PRODUCTS)
    const subscription = pick(SUBSCRIPTIONS)
    const purchase = {
      id: idOf('pur', random, 16),
      player: { id: player, country },
      product: { id: product.id, name: product.name },
      ...price(product.price),
      store: { name: store, transaction_id: ({ app_store: `2000000${Math.floor(random() * 1e9)}`, google_play: `GPA.3${Math.floor(random() * 1e3)}-${Math.floor(random() * 1e4)}-${Math.floor(random() * 1e4)}`, steam: String(Math.floor(random() * 1e12)) } as Record<string, string>)[store] },
      sandbox: options.environment !== 'production',
      purchased_at: at(-Math.floor(random() * 4000))
    }
    const member = { email: pick(['mert@starfall.games', 'ada@starfall.games', 'qa-contractor@testlab.io']), role: pick(['viewer', 'editor', 'admin']) }
    switch (type) {
      case 'purchase.verified': return { purchase: { ...purchase, status: 'verified', first_purchase: random() < 0.12 } }
      case 'purchase.rejected': return { purchase: { ...purchase, status: 'rejected' }, reason: pick(['receipt_already_used', 'receipt_not_found', 'bundle_id_mismatch']) }
      case 'purchase.refunded': return { purchase: { ...purchase, status: 'refunded' }, refund: { at: at(), reason: pick(['requested_by_player', 'unauthorized_charge', 'issue_with_item']) } }
      case 'subscription.started':
      case 'subscription.renewed':
      case 'subscription.canceled':
      case 'subscription.expired':
      case 'subscription.billing_issue': {
        const body: Record<string, unknown> = {
          subscription: {
            id: idOf('sub', random, 16),
            player: { id: player, country },
            product: { id: subscription.id, name: subscription.name, period: subscription.period },
            ...price(subscription.price),
            store: { name: store },
            status: ({ 'subscription.started': 'active', 'subscription.renewed': 'active', 'subscription.canceled': 'canceled', 'subscription.expired': 'expired', 'subscription.billing_issue': 'in_grace_period' } as Record<string, string>)[type],
            trial: type === 'subscription.started' && random() < 0.4,
            current_period_end: at(subscription.period === 'P1M' ? 30 * DAY_MS : 365 * DAY_MS),
            will_renew: type === 'subscription.started' || type === 'subscription.renewed'
          }
        }
        if (type === 'subscription.billing_issue' && options.version !== '2025-11-01') body.grace_period_ends_at = at(16 * DAY_MS)
        if (type === 'subscription.expired') body.reason = pick(['canceled_by_player', 'billing_failed', 'product_unavailable'])
        return body
      }
      case 'config.published': return { environment: options.environment, version: 40 + Math.floor(random() * 30), published_by: 'lena@starfall.games', changes: { flags: 2, experiments: 1, liveops_events: 1 }, note: 'Harvest festival goes live' }
      case 'config.rolled_back': return { environment: options.environment, from_version: 58, to_version: 57, rolled_back_by: 'deniz@starfall.games', reason: 'Shop prices wrong in the new store layout' }
      case 'liveops.window.started':
      case 'liveops.window.ended': {
        const event = pick(LIVEOPS)
        return { event: { ...event }, window: { start: at(type === 'liveops.window.started' ? 0 : -48 * HOUR_MS), end: at(type === 'liveops.window.started' ? 48 * HOUR_MS : 0) }, ...(type === 'liveops.window.started' ? { payload: { multiplier: 2, currencies: ['xp'] } } : {}) }
      }
      case 'experiment.started': return { experiment: { key: 'shop_layout_v3', name: 'Shop layout, third try', variants: ['control', 'grid', 'carousel'], traffic: 0.5 } }
      case 'experiment.concluded': return { experiment: { key: 'onboarding_skip', name: 'Skippable onboarding' }, kept: 'skip_after_2', result: { metric: 'd1_retention', lift: 0.031, chance_to_beat_control: 0.97 } }
      case 'alert.triggered': return { alert: { id: idOf('alr', random, 12), name: 'Purchase failures above 3%' }, metric: 'purchase_failure_rate', value: 0.047, threshold: 0.03, window: 'PT15M' }
      case 'alert.resolved': return { alert: { id: idOf('alr', random, 12), name: 'Purchase failures above 3%' }, metric: 'purchase_failure_rate', value: 0.012, threshold: 0.03, out_for: 'PT47M' }
      case 'export.completed': return { export: { id: idOf('exp', random, 16), kind: 'events', from: at(-7 * DAY_MS), to: at() }, files: [{ name: 'events-000.parquet', bytes: 412_883_210, url: 'https://exports.playersense.io/…signed…', expires_at: at(7 * DAY_MS) }] }
      case 'export.failed': return { export: { id: idOf('exp', random, 16), kind: 'players' }, error: 'The range is over 400 GB. Split it into shorter ranges.' }
      case 'player.deleted': return { player: { id: player }, requested_at: at(-26 * HOUR_MS), completed_at: at(), requested_by: pick(['player', 'studio']) }
      case 'member.added': return { member: { ...member, invited_by: 'lena@starfall.games' } }
      case 'member.removed': return { member: { ...member, removed_by: 'lena@starfall.games' } }
      case 'api_key.created':
      case 'api_key.revoked': return { key: { id: idOf('key', random, 12), class: pick(['sdk', 'server', 'admin']), environment: options.environment, name: 'CI uploads', prefix: 'ps_live_4Hk' } }
      case 'usage.threshold_reached': return { meter: pick(['events', 'monthly_active_players']), threshold: 0.8, used: 0.81, period: { start: at(-22 * DAY_MS), end: at(8 * DAY_MS) } }
      default: return {}
    }
  })()

  return {
    id: options.id,
    type,
    api_version: options.version,
    created_at: at(),
    project: { id: 'prj_starfall_tactics', environment: options.environment },
    data
  }
}

// --- Deliveries -----------------------------------------------------------------------------

/* How busy each hour of the day is, around a mean of 1: quiet at night, busiest in the evening. */
const DAY_SHAPE = Array.from({ length: 24 }, (_, hour) => 0.55 + 0.9 * Math.max(0, Math.sin(((hour - 6) / 24) * Math.PI * 2 - 0.3) * 0.5 + 0.5) ** 1.6)
const DAY_SHAPE_MEAN = DAY_SHAPE.reduce((sum, value) => sum + value, 0) / 24

/** Which events a destination hears, weighted by how often each happens. */
function weightsFor(destination: WebhookDestination): [string, number][] {
  const often: Record<string, number> = {
    'purchase.verified': 60, 'purchase.refunded': 1.2, 'purchase.rejected': 2, 'subscription.started': 4, 'subscription.renewed': 7, 'subscription.canceled': 1.5, 'subscription.expired': 1, 'subscription.billing_issue': 0.6,
    'config.published': 3, 'config.rolled_back': 0.3, 'liveops.window.started': 4, 'liveops.window.ended': 4, 'experiment.started': 0.5, 'experiment.concluded': 0.4, 'alert.triggered': 1.2, 'alert.resolved': 1.1,
    'export.completed': 2, 'export.failed': 0.2, 'player.deleted': 9
  }
  return eventsOf(destination.events).map(type => [type, often[type] ?? 0.3] as [string, number]).filter(([, weight]) => weight > 0)
}

function pickWeighted(entries: readonly [string, number][], random: () => number): string {
  const total = entries.reduce((sum, [, weight]) => sum + weight, 0)
  let roll = random() * total
  for (const [value, weight] of entries) {
    roll -= weight
    if (roll <= 0) return value
  }
  return entries.at(-1)![0]
}

function poisson(mean: number, random: () => number): number {
  if (mean <= 0) return 0
  let count = 0
  let product = random()
  const limit = Math.exp(-mean)
  while (product > limit && count < 200) {
    count++
    product *= random()
  }
  return count
}

const FAILURE_BODIES: Record<number, string> = {
  500: '{"error":"internal","message":"deletion ledger is read-only (maintenance)"}',
  502: '<html><head><title>502 Bad Gateway</title></head><body><center><h1>502 Bad Gateway</h1></center><hr><center>nginx</center></body></html>',
  503: '{"error":"unavailable","retry_after":30}',
  504: '<html><body><h1>504 Gateway Time-out</h1>The server didn\'t respond in time.</body></html>',
  410: '{"error":"gone","message":"This endpoint was retired."}'
}

function attemptOf(at: number, ok: boolean, profile: Profile, random: () => number): DeliveryAttempt {
  const spread = Math.exp((random() - 0.5) * 1.1)
  if (ok) {
    return { at, code: random() < 0.85 ? 200 : 204, ms: Math.round(profile.latency * spread), body: random() < 0.6 ? '{"received":true}' : '' }
  }
  const code = profile.failures[Math.floor(random() * profile.failures.length)] ?? null
  if (code === null) {
    const refused = random() < 0.25
    return { at, code: null, error: refused ? 'Connection refused' : 'Timed out after 10 s', ms: refused ? Math.round(20 + random() * 40) : 10_000, body: '' }
  }
  return { at, code, ms: Math.round(profile.latency * spread * 0.6), body: FAILURE_BODIES[code] ?? '' }
}

/**
 * One delivery, created at `created`, with every try the schedule would have made by `now`.
 * A try is made when its wait is up; the delivery succeeds at the first try that does,
 * fails after the last one, and is retrying in between.
 */
function deliveryOf(destination: WebhookDestination, profile: Profile, created: number, now: number, random: () => number): WebhookDelivery {
  const type = pickWeighted(weightsFor(destination), random)
  const environment = destination.environments.length > 1 && random() < 0.3 ? destination.environments[1]! : destination.environments[0] ?? 'production'
  const eventId = idOf('evt', random, 24)
  const downFrom = profile.downFor ? now - profile.downFor : Infinity
  const outage = profile.outage ? [now - profile.outage[0], now - profile.outage[1]] as const : null
  const attempts: DeliveryAttempt[] = []
  let at = created + Math.round(200 + random() * 900)
  for (let index = 0; index < WEBHOOK_TRIES && at <= now; index++) {
    const down = at >= downFrom || (outage !== null && at >= outage[0] && at < outage[1])
    const failChance = down ? 1 : index === 0 ? profile.firstFail : profile.retryFail
    const ok = random() >= failChance
    attempts.push(attemptOf(at, ok, profile, random))
    if (ok) break
    at += Math.round(RETRY_AFTER_MS[index]! * (1 + random() * 0.1))
  }
  const last = attempts.at(-1)
  const status: DeliveryStatus = last && answerOk(last) ? 'succeeded' : attempts.length >= WEBHOOK_TRIES ? 'failed' : 'retrying'
  return {
    id: idOf('whd', random, 20),
    destinationId: destination.id,
    eventId,
    type,
    environment,
    created,
    status,
    attempts,
    ...(status === 'retrying' ? { next: at } : {})
  }
}

const cache = new Map<string, WebhookDelivery[]>()

/**
 * A destination's deliveries in the seven days to `now`, newest first. Each hour is drawn from
 * its own seed, so an hour's deliveries are the same on every read and a new hour adds its own.
 */
export function sampleDeliveries(destination: WebhookDestination, now: number): WebhookDelivery[] {
  const hourNow = Math.floor(now / HOUR_MS)
  const key = `${destination.id}:${destination.events.join(',')}:${destination.environments.join(',')}:${destination.status}:${hourNow}`
  const hit = cache.get(key)
  if (hit) return hit

  const profile = profileOf(destination.id)
  const out: WebhookDelivery[] = []
  const since = Date.parse(destination.created_at)
  /* A destination turned off hears nothing from then; one made today, nothing before today. */
  const until = destination.status === 'disabled' ? Math.min(now, Date.parse(destination.updated_at)) : now
  for (let hour = hourNow - 7 * 24; hour <= hourNow; hour++) {
    const start = hour * HOUR_MS
    if (start + HOUR_MS < since || start > until) continue
    const random = seeded(`${destination.id}:${hour}`)
    const shape = profile.daily ? DAY_SHAPE[new Date(start).getHours()]! / DAY_SHAPE_MEAN : 1
    const count = poisson(profile.rate * shape, random)
    for (let index = 0; index < count; index++) {
      const created = start + Math.floor(random() * HOUR_MS)
      if (created > until || created < since) continue
      out.push(deliveryOf(destination, profile, created, now, random))
    }
  }
  out.sort((a, b) => b.created - a.created)
  /* One entry a destination: the last hour's draw goes when the next is made. */
  for (const stale of cache.keys()) {
    if (stale.startsWith(`${destination.id}:`)) cache.delete(stale)
  }
  cache.set(key, out)
  return out
}

// --- Health ---------------------------------------------------------------------------------

export interface WebhookHealthFigures {
  deliveries: number
  succeeded: number
  retrying: number
  failed: number
  /** Deliveries that succeeded at their first try, of those tried: the destination's own record. */
  firstTry: number
  /** Of every try made in the period, the share that got a 2xx. */
  attemptSuccess: number
  p50: number
  p95: number
}

function quantile(sorted: readonly number[], q: number): number {
  if (!sorted.length) return 0
  return sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))]!
}

/** A set of deliveries' figures, over the tries made between `from` and `to`. */
export function healthOf(deliveries: readonly WebhookDelivery[], from: number, to: number): WebhookHealthFigures {
  const made = deliveries.filter(entry => entry.created >= from && entry.created < to)
  const tries = deliveries.flatMap(entry => entry.attempts).filter(attempt => attempt.at >= from && attempt.at < to)
  const answered = tries.filter(attempt => attempt.code !== null).map(attempt => attempt.ms).sort((a, b) => a - b)
  const tried = made.filter(entry => entry.attempts.length)
  return {
    deliveries: made.length,
    succeeded: made.filter(entry => entry.status === 'succeeded').length,
    retrying: made.filter(entry => entry.status === 'retrying').length,
    failed: made.filter(entry => entry.status === 'failed').length,
    firstTry: tried.length ? tried.filter(entry => answerOk(entry.attempts[0]!)).length / tried.length : 1,
    attemptSuccess: tries.length ? tries.filter(answerOk).length / tries.length : 1,
    p50: quantile(answered, 0.5),
    p95: quantile(answered, 0.95)
  }
}

/** Tries made in each hour or day of a period, as delivered and failed, for the chart. */
export function attemptSeries(deliveries: readonly WebhookDelivery[], buckets: readonly number[], size: number) {
  const ok = buckets.map(() => 0)
  const bad = buckets.map(() => 0)
  const first = buckets[0] ?? 0
  for (const entry of deliveries) {
    for (const attempt of entry.attempts) {
      const index = Math.floor((attempt.at - first) / size)
      if (index < 0 || index >= buckets.length) continue
      if (answerOk(attempt)) ok[index]!++
      else bad[index]!++
    }
  }
  return { ok, bad }
}
