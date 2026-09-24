// Outgoing webhooks (project.md §49-51): what a destination is, which events one can listen
// to, what a delivery and its attempts are, and the words each of them is shown in.
//
// A destination is one HTTPS URL of the studio's own, the events it listens to and the
// environments it hears them from. Every event it listens to is POSTed to it as signed JSON,
// at least once: an answer outside 2xx, or none in ten seconds, is tried again on a schedule
// that gives up after three days. A destination that has answered nothing but failures for
// three days is turned off, and its owners are told.
//
// Raw analytics events are not webhook events (§49): a destination hears what the business
// did, a verified purchase or a published config, never every tap a player made.

// --- Events ---------------------------------------------------------------------------------

export type WebhookGroupKey = 'purchases' | 'subscriptions' | 'publishing' | 'liveops' | 'experiments' | 'alerts' | 'data' | 'project'

export interface WebhookGroup {
  key: WebhookGroupKey
  label: string
  /** What the group is for, in a line: who usually listens to it. */
  note: string
  /** Written out in full so @nuxt/icon bundles it. */
  icon: string
}

export const WEBHOOK_GROUPS: readonly WebhookGroup[] = [
  { key: 'purchases', label: 'Purchases', note: 'Grant what was bought, on your own server, once the store has confirmed it.', icon: 'material-symbols:shopping-cart-outline' },
  { key: 'subscriptions', label: 'Subscriptions', note: 'Keep a player\'s membership in step with the store: renewals, cancellations, lapses.', icon: 'material-symbols:autorenew' },
  { key: 'publishing', label: 'Publishing', note: 'Know when a config reaches an environment, or is rolled back.', icon: 'material-symbols:rocket-launch-outline' },
  { key: 'liveops', label: 'Live Ops', note: 'Start and stop things of your own the moment an event\'s window opens or closes.', icon: 'material-symbols:event-outline' },
  { key: 'experiments', label: 'Experiments', note: 'Hear when a test starts and when it has an answer.', icon: 'material-symbols:science-outline' },
  { key: 'alerts', label: 'Alerts', note: 'Page somebody when a metric crosses a line you set, and again when it recovers.', icon: 'material-symbols:notifications-outline' },
  { key: 'data', label: 'Data and privacy', note: 'Collect an export when it is ready; record a player\'s deletion in your own systems.', icon: 'material-symbols:database-outline' },
  { key: 'project', label: 'Project', note: 'Who joined, who left, which keys were made and revoked: for your own audit trail.', icon: 'material-symbols:shield-person-outline' }
]

export const WEBHOOK_GROUP: Record<WebhookGroupKey, WebhookGroup> = Object.fromEntries(WEBHOOK_GROUPS.map(group => [group.key, group])) as Record<WebhookGroupKey, WebhookGroup>

export interface WebhookEventType {
  type: string
  group: WebhookGroupKey
  /** What happened, as a sentence a founder reads. */
  summary: string
  /** What `data` holds, in a few words. */
  holds: string
  /** Sent from test builds too: sandbox purchases, a staging publish. */
  environments: boolean
}

export const WEBHOOK_EVENTS: readonly WebhookEventType[] = [
  { type: 'purchase.verified', group: 'purchases', summary: 'A purchase was confirmed with the store. Grant the item on this one.', holds: 'The purchase: player, product, price, store transaction', environments: true },
  { type: 'purchase.rejected', group: 'purchases', summary: 'A receipt did not check out with the store. Nothing should be granted.', holds: 'The purchase and why the store refused it', environments: true },
  { type: 'purchase.refunded', group: 'purchases', summary: 'The store refunded a purchase. Take back what it granted, if you do.', holds: 'The purchase, when and why it was refunded', environments: true },
  { type: 'subscription.started', group: 'subscriptions', summary: 'A player subscribed, trial included.', holds: 'The subscription: player, product, period, renews at', environments: true },
  { type: 'subscription.renewed', group: 'subscriptions', summary: 'A subscription renewed for another period.', holds: 'The subscription and its new period', environments: true },
  { type: 'subscription.canceled', group: 'subscriptions', summary: 'A player turned renewal off. It stays on until its period ends.', holds: 'The subscription and when it will end', environments: true },
  { type: 'subscription.expired', group: 'subscriptions', summary: 'A subscription ended and did not renew. Take away what it gave.', holds: 'The subscription and why it ended', environments: true },
  { type: 'subscription.billing_issue', group: 'subscriptions', summary: 'The store could not charge for a renewal and is retrying.', holds: 'The subscription and the store\'s grace period', environments: true },
  { type: 'config.published', group: 'publishing', summary: 'A config was published to an environment: flags, experiments and Live Ops.', holds: 'The environment, the version, who published and what changed', environments: true },
  { type: 'config.rolled_back', group: 'publishing', summary: 'An environment was rolled back to an earlier version.', holds: 'The environment, both versions, who and why', environments: true },
  { type: 'liveops.window.started', group: 'liveops', summary: 'A Live Ops event came on: one of its windows opened.', holds: 'The event, its window and its payload', environments: true },
  { type: 'liveops.window.ended', group: 'liveops', summary: 'A Live Ops event\'s window closed.', holds: 'The event and the window that closed', environments: true },
  { type: 'experiment.started', group: 'experiments', summary: 'An experiment started taking players.', holds: 'The experiment, its variants and its traffic', environments: false },
  { type: 'experiment.concluded', group: 'experiments', summary: 'An experiment ended with a winner, or with none.', holds: 'The experiment, the variant kept and its result', environments: false },
  { type: 'alert.triggered', group: 'alerts', summary: 'A metric crossed a line an alert watches.', holds: 'The alert, the metric, its value and the line', environments: false },
  { type: 'alert.resolved', group: 'alerts', summary: 'A metric came back inside its line.', holds: 'The alert and how long it was out', environments: false },
  { type: 'export.completed', group: 'data', summary: 'An export finished. Its files are ready to download for seven days.', holds: 'The export, its files and their signed links', environments: false },
  { type: 'export.failed', group: 'data', summary: 'An export could not be finished.', holds: 'The export and what went wrong', environments: false },
  { type: 'player.deleted', group: 'data', summary: 'A player\'s data was erased, on their request or yours.', holds: 'The player id and when the erasure finished', environments: true },
  { type: 'member.added', group: 'project', summary: 'Somebody joined the project.', holds: 'The member, their role and who invited them', environments: false },
  { type: 'member.removed', group: 'project', summary: 'Somebody left the project or was removed from it.', holds: 'The member and who removed them', environments: false },
  { type: 'api_key.created', group: 'project', summary: 'A key was made.', holds: 'The key\'s class, environment and name. Never the key.', environments: false },
  { type: 'api_key.revoked', group: 'project', summary: 'A key was revoked. Anything still using it is refused from now.', holds: 'The key\'s class, environment and name', environments: false },
  { type: 'usage.threshold_reached', group: 'project', summary: 'The project reached a share of its plan: 80% and 100% of events or players.', holds: 'The meter, the threshold and the period', environments: false }
]

export const WEBHOOK_EVENT: Record<string, WebhookEventType> = Object.fromEntries(WEBHOOK_EVENTS.map(entry => [entry.type, entry]))

/** A destination listening to this hears every event, those added later included. */
export const EVERY_EVENT = '*'

/** The event types a destination's list stands for, `*` read as all of them. */
export function eventsOf(list: readonly string[]): string[] {
  return list.includes(EVERY_EVENT) ? WEBHOOK_EVENTS.map(entry => entry.type) : WEBHOOK_EVENTS.filter(entry => list.includes(entry.type)).map(entry => entry.type)
}

/** "Every event", "Purchases and 3 more", "purchase.verified": a destination's list in a line. */
export function listeningWords(list: readonly string[]): string {
  if (list.includes(EVERY_EVENT)) return 'Every event, and new ones as they come'
  const types = eventsOf(list)
  if (!types.length) return 'No events'
  if (types.length === 1) return types[0]!
  const whole = WEBHOOK_GROUPS.filter(group => WEBHOOK_EVENTS.filter(entry => entry.group === group.key).every(entry => types.includes(entry.type)))
  const loose = types.filter(type => !whole.some(group => group.key === WEBHOOK_EVENT[type]!.group))
  const parts = [...whole.map(group => group.label), ...loose]
  if (parts.length <= 2) return parts.join(' and ')
  return `${parts.slice(0, 2).join(', ')} and ${parts.length - 2} more`
}

/** The groups a list touches, in catalog order, with how many of each it takes. */
export function groupsOf(list: readonly string[]) {
  const types = eventsOf(list)
  return WEBHOOK_GROUPS.map((group) => {
    const all = WEBHOOK_EVENTS.filter(entry => entry.group === group.key)
    const taken = all.filter(entry => types.includes(entry.type))
    return { group, taken: taken.length, total: all.length }
  }).filter(entry => entry.taken > 0)
}

// --- Destinations ---------------------------------------------------------------------------

export type WebhookStatus = 'enabled' | 'disabled'

/** What a destination is, as a badge: enabled and answering, enabled and failing, or off. */
export type WebhookHealth = 'healthy' | 'failing' | 'disabled'

export const WEBHOOK_HEALTH: Record<WebhookHealth, { label: string, tone: BadgeTone, dot: string }> = {
  healthy: { label: 'Enabled', tone: 'success', dot: 'bg-success' },
  failing: { label: 'Failing', tone: 'error', dot: 'bg-error' },
  disabled: { label: 'Disabled', tone: 'neutral', dot: 'bg-ink/30' }
}

export interface WebhookHeader {
  name: string
  value: string
  /** A token: stored encrypted, and never shown again once saved, in a delivery or here. */
  secret?: boolean
}

export interface WebhookSecret {
  /** The whole secret. Shown on request, never in a list. */
  value: string
  created_at: string
  /** The secret it replaced, still signing beside it until this moment. */
  previous?: { value: string, expires_at: string }
}

export interface WebhookHistoryEntry {
  at: string
  actor: string
  text: string
}

export interface WebhookDestination {
  id: string
  name: string
  description: string
  url: string
  /** Event types, or `*` for every event. */
  events: string[]
  /** Whose events it hears: production's alone, or test builds' too. */
  environments: EnvironmentKind[]
  status: WebhookStatus
  /** Why it is off: turned off by somebody, or by PlayerSense after three days of failures. */
  disabled_reason?: 'manual' | 'failing'
  /** The payload version its events are written in. */
  version: string
  /** Sent with every delivery, for a gateway in front of the URL that wants its own token. */
  headers: WebhookHeader[]
  /** At most this many deliveries a second; null sends as fast as they come. */
  rate_limit: number | null
  secret: WebhookSecret
  created_at: string
  updated_at: string
  updated_by: string
  history: WebhookHistoryEntry[]
}

/** The payload versions, newest first. A destination keeps its own until it is moved on. */
export const WEBHOOK_VERSIONS = [
  { value: '2026-09-01', label: '2026-09-01', note: 'Latest. Subscriptions carry the store\'s grace period.' },
  { value: '2026-04-15', label: '2026-04-15', note: 'Prices as { amount, currency, usd } rather than cents.' },
  { value: '2025-11-01', label: '2025-11-01', note: 'The first version.' }
] as const

export const LATEST_VERSION = WEBHOOK_VERSIONS[0].value

/** Why a URL will not do, or null. The server says the same before it saves one. */
export function urlProblem(text: string): string | null {
  const value = text.trim()
  if (!value) return 'A URL to send to'
  let url: URL
  try {
    url = new URL(value)
  } catch {
    return 'Not a URL yet: it starts with https://'
  }
  if (url.protocol !== 'https:') return 'Only https:// URLs: events carry player ids and purchases'
  const host = url.hostname
  if (host === 'localhost' || host.endsWith('.local') || host.endsWith('.internal') || /^(?:127|10|192\.168|169\.254)\./.test(host) || /^172\.(?:1[6-9]|2\d|3[01])\./.test(host)) {
    return 'A public address: PlayerSense cannot reach localhost or a private network'
  }
  if (url.username || url.password) return 'No user name or password in the URL. Send one as a header instead'
  return null
}

/** The host a URL points at, for a line with no room for the whole of it. */
export function hostOf(text: string): string {
  try {
    return new URL(text).host
  } catch {
    return text
  }
}

// --- Deliveries -----------------------------------------------------------------------------

export type DeliveryStatus = 'succeeded' | 'retrying' | 'failed'

export const DELIVERY_STATUS: Record<DeliveryStatus, { label: string, tone: BadgeTone, note: string }> = {
  succeeded: { label: 'Delivered', tone: 'success', note: 'Your server answered 2xx.' },
  retrying: { label: 'Retrying', tone: 'warning', note: 'Not delivered yet. It will be tried again.' },
  failed: { label: 'Failed', tone: 'error', note: 'Gave up after three days of tries. Resend it once your server is fixed.' }
}

export const DELIVERY_STATUSES = Object.keys(DELIVERY_STATUS) as DeliveryStatus[]

export interface DeliveryAttempt {
  at: number
  /** The HTTP status; null when no answer came at all. */
  code: number | null
  /** What went wrong before an answer: "Timed out after 10 s", "Connection refused". */
  error?: string
  ms: number
  /** The answer's body, cut at 4 KB. */
  body: string
  /** Sent by somebody from the dashboard, not by the retry schedule. */
  manual?: boolean
}

export interface WebhookDelivery {
  id: string
  destinationId: string
  eventId: string
  type: string
  environment: EnvironmentKind
  created: number
  status: DeliveryStatus
  attempts: DeliveryAttempt[]
  /** When the next try is due, for a delivery still retrying. */
  next?: number
  /** Sent from the dashboard as a test, with a made-up event. */
  test?: boolean
}

/**
 * When each try is made, after the first failed one: a minute, five, half an hour, then
 * hours, then a day at a time, until three days have gone. Each wait is stretched by up to
 * a tenth at random, so a server coming back is not met by every retry at once.
 */
export const RETRY_AFTER_MS = [60_000, 300_000, 1_800_000, 7_200_000, 21_600_000, 43_200_000, 86_400_000, 86_400_000] as const

/** Every try a delivery gets: the first, and one after each wait above. */
export const WEBHOOK_TRIES: number = RETRY_AFTER_MS.length + 1

/** "1 min", "30 min", "6 h", "1 day": a wait on the retry schedule. */
export function waitWords(ms: number): string {
  if (ms >= 86_400_000) return `${Math.round(ms / 86_400_000)} ${ms >= 2 * 86_400_000 ? 'days' : 'day'}`
  if (ms >= 3_600_000) return `${Math.round(ms / 3_600_000)} h`
  return `${Math.round(ms / 60_000)} min`
}

/** An attempt's answer in a word or two: "200 OK", "503", "Timed out". */
export function answerWords(attempt: DeliveryAttempt): string {
  if (attempt.code === null) return attempt.error ?? 'No answer'
  return HTTP_WORDS[attempt.code] ? `${attempt.code} ${HTTP_WORDS[attempt.code]}` : String(attempt.code)
}

const HTTP_WORDS: Record<number, string> = {
  200: 'OK',
  201: 'Created',
  202: 'Accepted',
  204: 'No Content',
  400: 'Bad Request',
  401: 'Unauthorized',
  403: 'Forbidden',
  404: 'Not Found',
  408: 'Request Timeout',
  410: 'Gone',
  429: 'Too Many Requests',
  500: 'Internal Server Error',
  502: 'Bad Gateway',
  503: 'Service Unavailable',
  504: 'Gateway Timeout'
}

export const answerOk = (attempt: DeliveryAttempt) => attempt.code !== null && attempt.code >= 200 && attempt.code < 300

/** "82 ms", "1.4 s". */
export function msWords(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(ms >= 10_000 ? 0 : 1)} s` : `${Math.round(ms)} ms`
}

// --- What a delivery looks like on the wire -------------------------------------------------

/** The headers every delivery carries (§51), and what the receiving server does with each. */
export const SIGNATURE_HEADERS = [
  { name: 'X-PlayerSense-Id', note: 'The event\'s id. The same on every try of it: keep the ones you have handled and ignore a repeat.' },
  { name: 'X-PlayerSense-Timestamp', note: 'When this try was signed, in Unix seconds. Refuse one older than five minutes.' },
  { name: 'X-PlayerSense-Signature', note: 'v1= and the HMAC-SHA256 of the timestamp, a dot and the raw body, keyed with the signing secret.' }
] as const

/** A delivery's request headers, as they are sent. */
export function requestHeaders(delivery: WebhookDelivery, destination: WebhookDestination, attempt: DeliveryAttempt) {
  const seconds = Math.floor(attempt.at / 1000)
  return [
    { name: 'Content-Type', value: 'application/json' },
    { name: 'User-Agent', value: 'PlayerSense-Webhooks/1.0' },
    { name: 'X-PlayerSense-Id', value: delivery.eventId },
    { name: 'X-PlayerSense-Timestamp', value: String(seconds) },
    { name: 'X-PlayerSense-Signature', value: `v1=${fakeHex(`${destination.secret.value}${seconds}${delivery.eventId}`)}` },
    ...destination.headers.filter(header => header.name.trim()).map(header => ({ name: header.name, value: header.secret ? '••••••••' : header.value }))
  ]
}

/** A stand-in for the signature: 64 hex digits that are the same for the same input. */
function fakeHex(text: string): string {
  let out = ''
  let state = 2166136261
  for (let round = 0; out.length < 64; round++) {
    for (const char of `${text}:${round}`) state = Math.imul(state ^ char.charCodeAt(0), 16777619) >>> 0
    out += state.toString(16).padStart(8, '0')
  }
  return out.slice(0, 64)
}

// --- Verifying a delivery, in the studio's own server ---------------------------------------

export type VerifyLanguage = 'javascript' | 'python' | 'go' | 'csharp'

export const VERIFY_LANGUAGES: { value: VerifyLanguage, label: string }[] = [
  { value: 'javascript', label: 'Node.js' },
  { value: 'python', label: 'Python' },
  { value: 'go', label: 'Go' },
  { value: 'csharp', label: 'C#' }
]

/** A receiving handler that checks the signature and the age, in each language. */
export const VERIFY_CODE: Record<VerifyLanguage, string> = {
  javascript: `import crypto from 'node:crypto'
import express from 'express'

const secret = process.env.PLAYERSENSE_WEBHOOK_SECRET

// The raw body: the signature is over the bytes as sent, not re-encoded JSON.
app.post('/playersense/webhooks', express.raw({ type: 'application/json' }), (req, res) => {
  const timestamp = req.header('X-PlayerSense-Timestamp')
  const signature = req.header('X-PlayerSense-Signature') ?? ''

  // Older than five minutes: a replay, or a clock far out.
  if (Math.abs(Date.now() / 1000 - Number(timestamp)) > 300) return res.sendStatus(400)

  const expected = crypto.createHmac('sha256', secret)
    .update(\`\${timestamp}.\${req.body}\`)
    .digest('hex')

  // While a secret is being rolled, both sign: accept either.
  const valid = signature.split(',').some(part =>
    part.startsWith('v1=') && crypto.timingSafeEqual(Buffer.from(part.slice(3)), Buffer.from(expected)))
  if (!valid) return res.sendStatus(400)

  const event = JSON.parse(req.body)
  // Handle it once: the same event can arrive more than once.
  queue.add(event.id, event)
  res.sendStatus(200)
})`,
  python: `import hashlib, hmac, os, time
from flask import Flask, request, abort

secret = os.environ["PLAYERSENSE_WEBHOOK_SECRET"].encode()

@app.post("/playersense/webhooks")
def playersense():
    timestamp = request.headers["X-PlayerSense-Timestamp"]
    signature = request.headers.get("X-PlayerSense-Signature", "")

    # Older than five minutes: a replay, or a clock far out.
    if abs(time.time() - int(timestamp)) > 300:
        abort(400)

    # The raw body: the signature is over the bytes as sent.
    signed = timestamp.encode() + b"." + request.get_data()
    expected = hmac.new(secret, signed, hashlib.sha256).hexdigest()

    # While a secret is being rolled, both sign: accept either.
    if not any(part.startswith("v1=") and hmac.compare_digest(part[3:], expected)
               for part in signature.split(",")):
        abort(400)

    event = request.get_json()
    # Handle it once: the same event can arrive more than once.
    queue.add(event["id"], event)
    return "", 200`,
  go: `func playersense(w http.ResponseWriter, r *http.Request) {
    body, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "read", http.StatusBadRequest)
        return
    }
    timestamp := r.Header.Get("X-PlayerSense-Timestamp")

    // Older than five minutes: a replay, or a clock far out.
    seconds, err := strconv.ParseInt(timestamp, 10, 64)
    if err != nil || math.Abs(float64(time.Now().Unix()-seconds)) > 300 {
        http.Error(w, "stale", http.StatusBadRequest)
        return
    }

    mac := hmac.New(sha256.New, []byte(os.Getenv("PLAYERSENSE_WEBHOOK_SECRET")))
    mac.Write([]byte(timestamp + "."))
    mac.Write(body)
    expected := hex.EncodeToString(mac.Sum(nil))

    // While a secret is being rolled, both sign: accept either.
    valid := false
    for _, part := range strings.Split(r.Header.Get("X-PlayerSense-Signature"), ",") {
        if strings.HasPrefix(part, "v1=") && hmac.Equal([]byte(part[3:]), []byte(expected)) {
            valid = true
        }
    }
    if !valid {
        http.Error(w, "signature", http.StatusBadRequest)
        return
    }

    // Handle it once: the same event can arrive more than once.
    enqueue(body)
    w.WriteHeader(http.StatusOK)
}`,
  csharp: `[HttpPost("playersense/webhooks")]
public async Task<IActionResult> PlayerSense()
{
    using var reader = new StreamReader(Request.Body);
    var body = await reader.ReadToEndAsync();
    var timestamp = Request.Headers["X-PlayerSense-Timestamp"].ToString();
    var signature = Request.Headers["X-PlayerSense-Signature"].ToString();

    // Older than five minutes: a replay, or a clock far out.
    if (Math.Abs(DateTimeOffset.UtcNow.ToUnixTimeSeconds() - long.Parse(timestamp)) > 300)
        return BadRequest();

    var secret = Encoding.UTF8.GetBytes(Environment.GetEnvironmentVariable("PLAYERSENSE_WEBHOOK_SECRET")!);
    using var hmac = new HMACSHA256(secret);
    var expected = Convert.ToHexString(hmac.ComputeHash(Encoding.UTF8.GetBytes($"{timestamp}.{body}"))).ToLowerInvariant();

    // While a secret is being rolled, both sign: accept either.
    var valid = signature.Split(',').Any(part => part.StartsWith("v1=") &&
        CryptographicOperations.FixedTimeEquals(Encoding.UTF8.GetBytes(part[3..]), Encoding.UTF8.GetBytes(expected)));
    if (!valid) return BadRequest();

    // Handle it once: the same event can arrive more than once.
    await queue.AddAsync(body);
    return Ok();
}`
}
