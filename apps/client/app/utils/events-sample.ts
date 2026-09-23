// Sample data for the Events pages, generated in the browser. What each part will come from
// once the API client is wired in:
//
//   counts per event name over time, and by platform / country — `events_1m` and
//   `events_1h` through POST …/analytics/production/metrics/query (metric=events,
//   event_name, interval, group_by one dimension);
//   properties, app versions and recent occurrences — the raw events themselves, for which
//   no query path exists yet (project.md §3.5 keeps page loads off `events_raw`).
//
// Seeded by event name and period, so a reload draws the same page.

export type EventPeriod = '24h' | '7d' | '30d'
export type EventCategory = 'session' | 'progression' | 'economy' | 'engagement' | 'custom'

export interface EventProperty {
  key: string
  type: 'string' | 'number' | 'boolean'
  /** Share of this event's occurrences that carry the property, 0–1. */
  fill: number
  values: readonly string[]
}

export interface EventDefinition {
  name: string
  category: EventCategory
  description: string
  /** Occurrences per active player per day. */
  rate: number
  /** Today against the previous period. */
  pace: number
  properties: readonly EventProperty[]
}

export const EVENT_CATEGORY_LABELS: Record<EventCategory, string> = {
  session: 'Session',
  progression: 'Progression',
  economy: 'Economy',
  engagement: 'Engagement',
  custom: 'Custom'
}

/** A theme colour per category, for the category tag: chart slots, never outcome colours. */
export const EVENT_CATEGORY_TONES: Record<EventCategory, string> = {
  session: 'chart-1',
  progression: 'chart-3',
  economy: 'chart-4',
  engagement: 'chart-5',
  custom: 'chart-6'
}

export const EVENT_CATALOGUE: readonly EventDefinition[] = [
  {
    name: 'session_start',
    category: 'session',
    description: 'A player opened the game, or came back to it after 30 minutes away.',
    rate: 2.8,
    pace: 1.08,
    properties: [
      { key: 'entry_point', type: 'string', fill: 1, values: ['app_icon', 'push_notification', 'deep_link', 'widget'] },
      { key: 'is_first_session', type: 'boolean', fill: 1, values: ['false', 'true'] }
    ]
  },
  {
    name: 'session_end',
    category: 'session',
    description: 'A session closed: the game went to the background or quit.',
    rate: 2.6,
    pace: 1.07,
    properties: [
      { key: 'duration_s', type: 'number', fill: 0.97, values: ['912', '340', '1480', '205', '2210'] },
      { key: 'reason', type: 'string', fill: 0.97, values: ['backgrounded', 'quit', 'timeout', 'crash'] }
    ]
  },
  {
    name: 'level_complete',
    category: 'progression',
    description: 'A level was finished, won or lost.',
    rate: 6.1,
    pace: 1.12,
    properties: [
      { key: 'level', type: 'number', fill: 1, values: ['12', '7', '23', '41', '3'] },
      { key: 'result', type: 'string', fill: 1, values: ['win', 'loss', 'abandon'] },
      { key: 'duration_s', type: 'number', fill: 0.99, values: ['184', '96', '312', '58'] },
      { key: 'stars', type: 'number', fill: 0.71, values: ['3', '2', '1'] }
    ]
  },
  {
    name: 'match_end',
    category: 'progression',
    description: 'A multiplayer match finished.',
    rate: 4.3,
    pace: 0.96,
    properties: [
      { key: 'mode', type: 'string', fill: 1, values: ['ranked', 'casual', 'event'] },
      { key: 'placement', type: 'number', fill: 1, values: ['1', '2', '3', '4'] },
      { key: 'mmr_delta', type: 'number', fill: 0.62, values: ['+18', '-12', '+9', '-21'] }
    ]
  },
  {
    name: 'tutorial_step',
    category: 'progression',
    description: 'A step of the first-run tutorial was reached.',
    rate: 1.2,
    pace: 1.31,
    properties: [
      { key: 'step', type: 'number', fill: 1, values: ['1', '2', '3', '4', '5', '6'] },
      { key: 'skipped', type: 'boolean', fill: 1, values: ['false', 'true'] }
    ]
  },
  {
    name: 'purchase',
    category: 'economy',
    description: 'A store purchase, verified server-side against the platform receipt.',
    rate: 0.034,
    pace: 1.1,
    properties: [
      { key: 'product_id', type: 'string', fill: 1, values: ['gems_small', 'battle_pass', 'gems_large', 'starter_pack'] },
      { key: 'price_usd', type: 'number', fill: 1, values: ['4.99', '9.99', '19.99', '1.99'] },
      { key: 'store', type: 'string', fill: 1, values: ['app_store', 'google_play', 'steam'] }
    ]
  },
  {
    name: 'currency_spend',
    category: 'economy',
    description: 'Soft or hard currency was spent in game.',
    rate: 3.4,
    pace: 1.04,
    properties: [
      { key: 'currency', type: 'string', fill: 1, values: ['coins', 'gems'] },
      { key: 'amount', type: 'number', fill: 1, values: ['250', '1200', '40', '5000'] },
      { key: 'sink', type: 'string', fill: 0.93, values: ['upgrade', 'shop', 'revive', 'cosmetic'] }
    ]
  },
  {
    name: 'ad_impression',
    category: 'engagement',
    description: 'A rewarded or interstitial ad was shown.',
    rate: 7.2,
    pace: 1.19,
    properties: [
      { key: 'format', type: 'string', fill: 1, values: ['rewarded', 'interstitial', 'banner'] },
      { key: 'placement', type: 'string', fill: 1, values: ['level_end', 'revive', 'shop', 'daily_bonus'] },
      { key: 'network', type: 'string', fill: 0.88, values: ['admob', 'applovin', 'unity'] }
    ]
  },
  {
    name: 'item_equip',
    category: 'engagement',
    description: 'An item or skin was equipped.',
    rate: 2.9,
    pace: 1.03,
    properties: [
      { key: 'item_id', type: 'string', fill: 1, values: ['sword_07', 'skin_winter', 'shield_02', 'pet_fox'] },
      { key: 'slot', type: 'string', fill: 1, values: ['weapon', 'outfit', 'companion'] }
    ]
  },
  {
    name: 'daily_reward_claim',
    category: 'custom',
    description: 'The daily login reward was collected.',
    rate: 0.62,
    pace: 1.15,
    properties: [
      { key: 'streak_day', type: 'number', fill: 1, values: ['1', '3', '7', '14'] },
      { key: 'reward', type: 'string', fill: 1, values: ['coins_500', 'gems_10', 'chest_rare'] }
    ]
  },
  {
    name: 'guild_join',
    category: 'custom',
    description: 'A player joined a guild.',
    rate: 0.05,
    pace: 0.88,
    properties: [
      { key: 'guild_size', type: 'number', fill: 1, values: ['12', '30', '48'] },
      { key: 'invited', type: 'boolean', fill: 0.54, values: ['true', 'false'] }
    ]
  }
]

const DAILY_PLAYERS = 31_000
const PERIOD_DAYS: Record<EventPeriod, number> = { '24h': 1, '7d': 7, '30d': 30 }

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

export interface EventSummary {
  name: string
  category: EventCategory
  description: string
  count: number
  previous: number
  players: number
  /** Seconds since the last occurrence. */
  lastSeen: number
  properties: number
}

/** Every event name over the period, with the previous period's count beside it. */
export function sampleEventSummaries(period: EventPeriod): EventSummary[] {
  const days = PERIOD_DAYS[period]
  return EVENT_CATALOGUE.map((event) => {
    const random = seeded(`${event.name}:${period}`)
    const count = Math.round(DAILY_PLAYERS * event.rate * days * (0.95 + random() * 0.1))
    // The share of players who send it at all: frequent events reach almost everybody.
    const reach = Math.min(0.97, 0.05 + event.rate / 4)
    return {
      name: event.name,
      category: event.category,
      description: event.description,
      count,
      previous: Math.round(count / event.pace),
      players: Math.round(DAILY_PLAYERS * reach * Math.sqrt(days) * 1.6),
      lastSeen: Math.max(1, Math.round(60 / (event.rate * 4) * (0.5 + random()))),
      properties: event.properties.length
    }
  })
}

export interface EventDetail {
  definition: EventDefinition
  row: EventSummary
  /** Occurrences per bucket: hours for 24h, days otherwise. */
  buckets: { at: number, count: number, previous: number }[]
  interval: 'hour' | 'day'
  platforms: { value: string, count: number }[]
  countries: { value: string, count: number }[]
  versions: { value: string, count: number }[]
  properties: (EventProperty & { distinct: number, top: { value: string, share: number }[] })[]
  recent: { at: number, player: string, platform: string, version: string, country: string, payload: Record<string, string | number | boolean> }[]
}

function split(total: number, shares: readonly (readonly [string, number])[], random: () => number) {
  return shares.map(([value, share]) => ({ value, count: Math.round(total * share * (0.9 + random() * 0.2)) }))
}

function typed(property: EventProperty, value: string): string | number | boolean {
  if (property.type === 'boolean') return value === 'true'
  if (property.type === 'number') return Number(value)
  return value
}

/** One event in full over the period, or null for a name nothing has sent. */
export function sampleEventDetail(name: string, period: EventPeriod): EventDetail | null {
  const definition = EVENT_CATALOGUE.find(event => event.name === name)
  if (!definition) return null
  const row = sampleEventSummaries(period).find(candidate => candidate.name === name)!
  const random = seeded(`${name}:${period}:detail`)

  const interval = period === '24h' ? 'hour' : 'day'
  const step = interval === 'hour' ? 3_600_000 : 86_400_000
  const count = period === '24h' ? 24 : PERIOD_DAYS[period]
  const end = Math.floor(Date.now() / step) * step
  const buckets = Array.from({ length: count }, (_, index) => {
    const at = end - (count - 1 - index) * step
    const hour = new Date(at).getHours()
    const shape = interval === 'hour' ? 0.6 + 0.4 * Math.sin((hour - 13) / 24 * Math.PI * 2) : 1
    const weekend = interval === 'day' && [0, 6].includes(new Date(at).getDay()) ? 1.15 : 1
    const value = row.count / count * shape * weekend * (0.9 + random() * 0.2)
    return { at, count: Math.round(value), previous: Math.round(value / definition.pace * (0.9 + random() * 0.2)) }
  })

  const properties = definition.properties.map((property) => {
    let left = 1
    const top = property.values.slice(0, 4).map((value, index, list) => {
      const share = index === list.length - 1 ? left : left * (0.45 + random() * 0.2)
      left -= share
      return { value, share }
    })
    // Measurements (durations, amounts) take many values; everything else is close to the
    // handful the game defines.
    const measured = /duration|amount|size|delta/.test(property.key)
    const distinct = property.type === 'boolean' ? 2 : measured ? Math.round(40 + random() * 900) : property.values.length + Math.round(random() * 3)
    return { ...property, distinct, top }
  })

  const platforms = ['iOS', 'Android', 'Steam']
  const versions = ['2.14.0', '2.13.2', '2.13.1']
  const countries = ['US', 'JP', 'BR', 'DE', 'TR']
  const recent = Array.from({ length: 12 }, (_, index) => ({
    at: Date.now() - Math.round((index + random()) * row.lastSeen * 1_000),
    player: `pl_${Math.floor(random() * 0xFFFFFF).toString(16).padStart(6, '0')}`,
    platform: platforms[Math.floor(random() * platforms.length)]!,
    version: versions[Math.floor(random() * versions.length * 0.8)]!,
    country: countries[Math.floor(random() * countries.length)]!,
    payload: Object.fromEntries(definition.properties
      .filter(property => random() < property.fill)
      .map(property => [property.key, typed(property, property.values[Math.floor(random() * property.values.length)]!)]))
  }))

  return {
    definition,
    row,
    buckets,
    interval,
    platforms: split(row.count, [['iOS', 0.47], ['Android', 0.42], ['Steam', 0.11]], random),
    countries: split(row.count, [['United States', 0.31], ['Japan', 0.14], ['Brazil', 0.11], ['Germany', 0.09], ['Türkiye', 0.07]], random),
    versions: split(row.count, [['2.14.0', 0.62], ['2.13.2', 0.27], ['2.13.1', 0.08], ['2.12.x', 0.03]], random),
    properties,
    recent
  }
}
