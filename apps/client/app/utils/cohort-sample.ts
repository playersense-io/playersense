// Sample data for the Cohorts page, generated in the browser. Shaped after what it will
// query once the API client is wired in:
//
//   POST …/analytics/production/cohorts/query  metric retention | playtime | revenue |
//     ltv | payer_conversion, basis=install, retention_basis for retention (never
//     inferred — project.md §29.10), cohort { from, to, grain day | week | month }, the
//     offsets on screen, at most one breakdown dimension (§57.1: never two at once), and
//     filters on the others. Rows are (cohort, offset[, dimension value]) with the
//     cohort's size, players, playtime_ms, revenue_micros and its running total, and
//     payers; a cell under the suppression threshold is dropped, not zeroed.
//
// Sessions per install and counts of any event per install are not metrics the query has
// yet: they need a cohort fact per event name, which the derivation does not write.
//
// One set of players underneath every metric — the same cohorts retain, play, spend and
// convert — so switching metric reads the same game another way rather than a new one.
// Seeded, so a reload draws the same cohorts.

import type { EventDefinition } from './events-sample'

export type CohortMetric
  = | 'retention' | 'churn' | 'active_players' | 'active_days' | 'sessions' | 'sessions_active' | 'session_length'
    | 'playtime' | 'playtime_active'
    | 'events' | 'event_share'
    | 'revenue' | 'ad_revenue' | 'revenue_all' | 'arpdau' | 'arppu' | 'payers' | 'purchases' | 'purchases_payer'
    | 'crash_rate'
export type CohortRange = '30d' | '90d' | '180d'
export type CohortBasis = 'classic' | 'rolling'
export type CohortDimension = 'install_platform' | 'install_country' | 'first_app_version'
export type CohortSplit = 'none' | CohortDimension
export type CohortFilters = Record<CohortDimension, string[]>

/** Cells counting fewer players than this are withheld by the API: its default k (§32.2). */
export const COHORT_THRESHOLD = 5

export const RANGE_DAYS: Record<CohortRange, number> = { '30d': 30, '90d': 90, '180d': 180 }

/**
 * The sticky install dimensions the API breaks down and filters by: each value's share of
 * installs, and how well its players retain and spend against the average.
 */
export const COHORT_DIMENSIONS: Record<CohortDimension, { label: string, noun: string, values: readonly (readonly [string, number, number])[] }> = {
  install_platform: {
    label: 'Platform',
    noun: 'platform',
    values: [['iOS', 0.45, 1.1], ['Android', 0.42, 0.9], ['Steam', 0.1, 1.18], ['Web', 0.03, 0.78]]
  },
  // The markets a mobile game's installs come from, by ISO code, and the rest together.
  install_country: {
    label: 'Country',
    noun: 'country',
    values: [
      ['US', 0.2, 1.14], ['IN', 0.06, 0.7], ['BR', 0.09, 0.82], ['DE', 0.05, 1.05], ['JP', 0.05, 1.22],
      ['ID', 0.045, 0.72], ['TR', 0.04, 0.86], ['GB', 0.04, 1.08], ['FR', 0.035, 1.02], ['MX', 0.035, 0.85],
      ['KR', 0.03, 1.18], ['RU', 0.03, 0.8], ['CA', 0.025, 1.1], ['PH', 0.025, 0.75], ['VN', 0.025, 0.76],
      ['AU', 0.02, 1.12], ['IT', 0.02, 0.98], ['ES', 0.02, 0.97], ['TH', 0.02, 0.8], ['PL', 0.015, 0.9],
      ['NL', 0.012, 1.05], ['SA', 0.012, 1], ['AR', 0.012, 0.8], ['CO', 0.01, 0.78], ['Other', 0.079, 0.92]
    ]
  },
  first_app_version: {
    label: 'First app version',
    noun: 'version',
    values: [['2.5.1', 0.12, 1.08], ['2.5.0', 0.3, 1.06], ['2.4.1', 0.33, 1], ['2.4.0', 0.17, 0.9], ['2.3.2', 0.08, 0.85]]
  }
}

const regionNames = new Intl.DisplayNames(['en'], { type: 'region' })

/** A segment as a reader names it: "Brazil" for BR, the rest of the countries together, a version as it is. */
export function segmentName(dimension: CohortDimension, value: string): string {
  if (dimension !== 'install_country') return value
  if (value === 'Other') return 'Other countries'
  return regionNames.of(value) ?? value
}

export const DIMENSION_KEYS = Object.keys(COHORT_DIMENSIONS) as CohortDimension[]

const DAY = 86_400_000

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

/** The share of a cohort active on day `day`: a power-law decay, rolling above classic. */
function active(day: number, basis: CohortBasis, quality: number): number {
  if (day === 0) return 1
  const [scale, decay] = basis === 'classic' ? [0.42, 0.47] : [0.55, 0.4]
  return Math.min(1, scale * quality * day ** -decay)
}

/** Minutes an active player plays on day `day`: the ones who stay, play longer. */
const minutesPerActive = (day: number, quality: number) => 26 * (1 + 0.12 * Math.log(1 + day)) * quality ** 0.5

/** Dollars an active player spends on day `day`: starter packs on day 0, then a slow climb. */
const dollarsPerActive = (day: number, quality: number) => (day === 0 ? 0.15 : 0.11 * (1 + 0.25 * Math.log(1 + day) / Math.log(31))) * quality ** 2

export interface CohortSampleOptions {
  metric: CohortMetric
  basis: CohortBasis
  range: CohortRange
  grain: CohortGrain
  split: CohortSplit
  filters: CohortFilters
  /** For the events metric: which event, narrowed how, from which catalogue. */
  event?: { name: string, where: readonly FunnelCondition[], catalogue: readonly EventDefinition[] }
}

/** Sessions an active player starts on day `day`: a few more as the habit forms, and longer ones. */
const sessionsPerActive = (day: number, quality: number) => 2.1 * (1 + 0.04 * Math.log(1 + day)) * quality ** 0.3

/** What a purchase averages, to count purchases from dollars. */
const AVERAGE_PRICE = 6.5

/** Ad impressions an active player sees on day `day`, and what a thousand of them earn. */
const adsPerActive = (day: number) => 5.5 * (1 + 0.05 * Math.log(1 + day))
const ECPM = 12

/**
 * The share of sessions that end in a crash: better builds and better devices crash less,
 * and the players on the devices that crash most are the first to leave.
 */
const crashesPerSession = (day: number, quality: number) => 0.006 / quality ** 2 * (1 - 0.18 * Math.min(1, day / 30))

/**
 * How often an active player does an event on day `day`: its catalogue rate, front-loaded
 * for the first days' tutorial and progression, and narrowed by each condition — the
 * first listed value of a property the commonest.
 */
function eventsPerActive(day: number, event: CohortSampleOptions['event']): number {
  const definition = event?.catalogue.find(entry => entry.name === event.name)
  if (!definition) return 0
  const early = definition.name === 'tutorial_step' ? (day === 0 ? 8 : 0.02) : 1 + 1.5 * Math.exp(-day / 2)
  return definition.rate * early * narrowingOf(event)
}

/** How much an event's conditions narrow it: the first listed value of a property the commonest. */
function narrowingOf(event: CohortSampleOptions['event']): number {
  const definition = event?.catalogue.find(entry => entry.name === event.name)
  if (!definition) return 0
  let narrowed = 1
  for (const condition of event!.where) {
    const property = definition.properties.find(entry => entry.key === condition.property)
    if (!property) continue
    const index = Math.max(0, property.values.indexOf(condition.value))
    narrowed *= property.fill * (0.35 + 0.65 * (property.values.length - index) / property.values.length)
  }
  return narrowed
}

/**
 * The most of a cohort that ever does the event: nearly everyone for a plain one, fewer
 * the narrower its conditions — step 6 of a tutorial is reached by far fewer than step 1 —
 * and more of a better cohort. Doing it a hundred times does not make more people do it.
 */
const reachOf = (event: CohortSampleOptions['event'], quality: number) => Math.min(0.97, 0.92 * narrowingOf(event) ** 0.7 * quality ** 0.6)

/** One day of one cohort, per install: everything any metric is read from. */
interface Day {
  active: number
  retained: number
  /** Not active on day N or any day after: gone. */
  churned: number
  activeDaysSoFar: number
  payers: number
  played: number
  playedSoFar: number
  spent: number
  spentSoFar: number
  sessions: number
  sessionsSoFar: number
  events: number
  eventsSoFar: number
  /** The share of the cohort that has done the event at least once by day N. */
  eventDone: number
  ads: number
  adsSoFar: number
  crashRate: number
}

/**
 * A metric read from a day: the share of the cohort it is about (who came back, who has
 * paid), its figure that day, and its running total where it has one.
 */
function measure(metric: CohortMetric, day: Day): { players: number, value: number, total: number } {
  const everyone = { players: day.retained, total: 0 }
  switch (metric) {
    case 'retention': return { ...everyone, value: day.retained }
    case 'churn': return { players: day.churned, value: day.churned, total: 0 }
    case 'active_players': return { ...everyone, value: day.retained }
    case 'active_days': return { ...everyone, value: day.active, total: day.activeDaysSoFar }
    case 'sessions': return { ...everyone, value: day.sessions, total: day.sessionsSoFar }
    case 'sessions_active': return { ...everyone, value: day.active ? day.sessions / day.active : 0 }
    case 'session_length': return { ...everyone, value: day.sessions ? day.played / day.sessions : 0 }
    case 'playtime': return { ...everyone, value: day.played, total: day.playedSoFar }
    case 'playtime_active': return { ...everyone, value: day.active ? day.played / day.active : 0 }
    case 'events': return { ...everyone, value: day.events, total: day.eventsSoFar }
    case 'event_share': return { players: day.eventDone, value: day.eventDone, total: 0 }
    case 'revenue': return { ...everyone, value: day.spent, total: day.spentSoFar }
    case 'ad_revenue': return { ...everyone, value: day.ads * ECPM / 1000, total: day.adsSoFar * ECPM / 1000 }
    case 'revenue_all': return { ...everyone, value: day.spent + day.ads * ECPM / 1000, total: day.spentSoFar + day.adsSoFar * ECPM / 1000 }
    case 'arpdau': return { ...everyone, value: day.active ? day.spent / day.active : 0 }
    case 'arppu': return { players: day.payers, value: day.payers ? day.spentSoFar / day.payers : 0, total: 0 }
    case 'payers': return { players: day.payers, value: day.payers, total: 0 }
    case 'purchases': return { ...everyone, value: day.spent / AVERAGE_PRICE, total: day.spentSoFar / AVERAGE_PRICE }
    case 'purchases_payer': return { players: day.payers, value: day.payers ? day.spentSoFar / AVERAGE_PRICE / day.payers : 0, total: 0 }
    case 'crash_rate': return { ...everyone, value: day.crashRate }
  }
}

export interface CohortSample {
  /** Newest cohort first; or, split, one row per segment over the whole range. */
  rows: CohortTableRow[]
  /** The same cohorts over the range before, unsplit: what the headline figures move from. */
  previous: CohortTableRow[]
}

/** A population the day-by-day model runs over: how many installs, and how good they are. */
interface Population { share: number, quality: number, name: string }

/** The filtered mix of one dimension: the share of installs it keeps, and its quality. */
function mixOf(dimension: CohortDimension, chosen: readonly string[]): Population {
  const values = COHORT_DIMENSIONS[dimension].values.filter(([value]) => chosen.length === 0 || chosen.includes(value))
  const share = values.reduce((sum, [, weight]) => sum + weight, 0)
  const quality = share ? values.reduce((sum, [, weight, better]) => sum + weight * better, 0) / share : 1
  return { share: chosen.length === 0 ? 1 : share, quality, name: '' }
}

/** Sums over the daily cohorts that make up one row, per offset. */
interface Tally {
  size: number
  mature: number[]
  players: number[]
  value: number[]
  total: number[]
}

const emptyTally = (): Tally => ({
  size: 0,
  mature: COHORT_OFFSETS.map(() => 0),
  players: COHORT_OFFSETS.map(() => 0),
  value: COHORT_OFFSETS.map(() => 0),
  total: COHORT_OFFSETS.map(() => 0)
})

interface Drawn { start: number, label?: string, mark?: SegmentMarkValue, tally: Tally }

/**
 * Every daily cohort of the range, day by day, into the rows it belongs to: its week or
 * month, or its segment. A week or month is known at an offset only once all its days are;
 * a segment, over whichever of its cohorts are old enough.
 */
function draw(options: CohortSampleOptions, populations: readonly Population[], today: number, salt: string): Map<string, Drawn> {
  const days = RANGE_DAYS[options.range]
  const random = seeded(`cohorts:${salt}:${options.range}:${JSON.stringify(options.filters)}`)
  const rows = new Map<string, Drawn>()
  const last = COHORT_OFFSETS[COHORT_OFFSETS.length - 1]!

  for (let index = 0; index < days; index += 1) {
    const at = today - (days - index) * DAY
    const age = Math.floor((today - at) / DAY) - 1
    const weekend = [0, 6].includes(new Date(at).getUTCDay()) ? 1.25 : 1
    const installs = 2_600 * weekend * (0.85 + random() * 0.3)
    const lift = 0.94 + random() * 0.12 + index / days * 0.04

    for (const population of populations) {
      const size = Math.round(installs * population.share)
      if (!size) continue
      const quality = population.quality * lift

      const key = population.name || bucketKey(at, options.grain)
      const row = rows.get(key) ?? {
        start: population.name ? at : bucketStart(at, options.grain),
        label: population.name && options.split !== 'none' ? segmentName(options.split, population.name) : undefined,
        mark: population.name ? markOf(options.split, population.name) : undefined,
        tally: emptyTally()
      }
      if (population.name) row.start = Math.min(row.start, at)
      rows.set(key, row)
      row.tally.size += size

      // Day by day to the last offset old enough, so running totals add every day.
      let playedSoFar = 0
      let spentSoFar = 0
      let sessionsSoFar = 0
      let eventsSoFar = 0
      let adsSoFar = 0
      let activeDays = 0
      let offsetAt = 0
      const until = Math.min(age, last)
      for (let day = 0; day <= until; day += 1) {
        const classic = active(day, 'classic', quality)
        activeDays += classic
        const playedToday = classic * minutesPerActive(day, quality)
        const spentToday = classic * dollarsPerActive(day, quality)
        const sessionsToday = classic * sessionsPerActive(day, quality)
        const eventsToday = classic * eventsPerActive(day, options.event)
        const adsToday = classic * adsPerActive(day)
        adsSoFar += adsToday
        playedSoFar += playedToday
        spentSoFar += spentToday
        sessionsSoFar += sessionsToday
        eventsSoFar += eventsToday
        if (day !== COHORT_OFFSETS[offsetAt]) continue

        const noise = 0.94 + random() * 0.12
        const payers = Math.min(0.6, 0.012 * quality + 1 - Math.exp(-0.0055 * quality ** 2 * activeDays)) * noise
        // A purchase is a payer: who has done one is payer conversion, whatever else is asked.
        const eventDone = options.event?.name === 'purchase' && !options.event.where.length
          ? payers
          : Math.min(0.99, reachOf(options.event, quality) * (1 - Math.exp(-0.9 * eventsSoFar)) * noise)
        const { players, value, total } = measure(options.metric, {
          active: classic,
          // Day 0 is the whole cohort by definition: nothing to vary.
          retained: day === 0 ? 1 : active(day, options.basis, quality) * noise,
          churned: day === 0 ? 0 : Math.min(1, 1 - active(day, 'rolling', quality) * noise),
          activeDaysSoFar: activeDays * noise,
          payers,
          played: playedToday * noise,
          playedSoFar: playedSoFar * noise,
          spent: spentToday * noise,
          spentSoFar: spentSoFar * noise,
          sessions: sessionsToday * noise,
          sessionsSoFar: sessionsSoFar * noise,
          events: eventsToday * noise,
          eventsSoFar: eventsSoFar * noise,
          eventDone,
          ads: adsToday * noise,
          adsSoFar: adsSoFar * noise,
          crashRate: crashesPerSession(day, quality) * noise
        })
        row.tally.mature[offsetAt]! += size
        row.tally.players[offsetAt]! += players * size
        row.tally.value[offsetAt]! += value * size
        row.tally.total[offsetAt]! += total * size
        offsetAt += 1
      }
      // A week or month is known at an offset only once every day in it is: one day too
      // young marks the offset unknown for the whole row, and NaN stays NaN as days add.
      if (!population.name) {
        for (let position = offsetAt; position < COHORT_OFFSETS.length; position += 1) row.tally.mature[position] = Number.NaN
      }
    }
  }
  return rows
}

/** A segment's flag or platform mark, for the two dimensions that have one. */
function markOf(split: CohortSplit, value: string): SegmentMarkValue | undefined {
  if (split === 'install_country') return { kind: 'country', code: value }
  if (split === 'install_platform') return { kind: 'platform', code: value }
  return undefined
}

function bucketStart(at: number, grain: CohortGrain): number {
  if (grain === 'day') return at
  const date = new Date(at)
  if (grain === 'month') return Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), 1)
  // Weeks start on Monday, as the API's week grain does.
  return at - ((date.getUTCDay() + 6) % 7) * DAY
}

const bucketKey = (at: number, grain: CohortGrain) => String(bucketStart(at, grain))

/** A tally written out as table cells: a figure where every day under it is old enough. */
function toRow(key: string, entry: Drawn): CohortTableRow {
  const { tally } = entry
  const cells: Record<number, CohortTableCell> = {}
  COHORT_OFFSETS.forEach((offset, position) => {
    const mature = tally.mature[position]!
    if (!mature || Number.isNaN(mature)) return
    const players = Math.round(tally.players[position]!)
    if (mature < COHORT_THRESHOLD || (players > 0 && players < COHORT_THRESHOLD)) {
      cells[offset] = { state: 'withheld', players: null, value: null, cumulative: null }
      return
    }
    cells[offset] = {
      state: 'ok',
      players,
      value: tally.value[position]! / mature,
      cumulative: tally.total[position]! / mature
    }
  })
  return { key, label: entry.label, mark: entry.mark, start: entry.start, size: tally.size < COHORT_THRESHOLD ? null : tally.size, cells }
}

export function sampleCohorts(options: CohortSampleOptions): CohortSample {
  const today = Math.floor(Date.now() / DAY) * DAY

  // The filtered install mix, one dimension at a time, and the split's values within it.
  const filtered = DIMENSION_KEYS.filter(dimension => dimension !== options.split).map(dimension => mixOf(dimension, options.filters[dimension]))
  const base: Population = {
    name: '',
    share: filtered.reduce((product, mix) => product * mix.share, 1),
    quality: filtered.reduce((product, mix) => product * mix.quality, 1)
  }

  let populations: Population[] = [base]
  if (options.split !== 'none') {
    const chosen = options.filters[options.split]
    populations = COHORT_DIMENSIONS[options.split].values
      .filter(([value]) => chosen.length === 0 || chosen.includes(value))
      .map(([value, share, quality]) => ({ name: value, share: base.share * share, quality: base.quality * quality }))
  }

  const current = draw(options, populations, today, 'now')
  const rows = [...current.entries()].map(([key, entry]) => toRow(key, entry))
  if (options.split === 'none') rows.sort((a, b) => b.start - a.start)

  const unsplit = options.split === 'none'
    ? populations
    : [{ ...base, share: populations.reduce((sum, population) => sum + population.share, 0) }]
  const before = draw(options, unsplit, today - RANGE_DAYS[options.range] * DAY, 'before')
  const previous = [...before.entries()].map(([key, entry]) => toRow(key, entry))

  return { rows, previous }
}

// --- How each metric reads ----------------------------------------------------------------

const minutes = (value: number) => `${writeFigure(value, { decimals: value < 10 ? 1 : 0 })}m`
const dollars = (value: number) => `$${value.toFixed(value < 10 ? 2 : 1)}`
/** Enough places to show a small count at all: 0.003 purchases, not 0. */
const count = (value: number) => writeFigure(value, { decimals: value < 1 ? 3 : value < 10 ? 2 : 1 })

export type CohortMetricGroup = 'Engagement' | 'Events' | 'Monetization' | 'Quality'

export interface CohortMetricDefinition {
  label: string
  group: CohortMetricGroup
  /** A line for the menu: what it answers. */
  note: string
  /** The sentence over the table: what a cell holds. */
  definition: string
  /**
   * What a cell is: a share of the cohort, a headcount, or a figure per install, per
   * player active that day, per payer or per session.
   */
  per: 'share' | 'count' | 'install' | 'active' | 'payer' | 'session'
  format: (value: number) => string
  /** A running total to show as well as the day's figure. */
  accumulates: boolean
  /** How a chart writes it: `scale` turns a fraction into the percent it is drawn as. */
  chart: { prefix?: string, unit?: string, decimals: number, scale?: number }
}

const share = { format: (value: number) => value.toString(), chart: { unit: '%', decimals: 1, scale: 100 } }

/**
 * Every metric the page reads, in the menu's order: what players do, what they do of the
 * events you name, what they pay, and how well the build holds up. Per install counts
 * every install, whether it came back or not, so it falls as players leave; per active
 * player counts only the ones who played that day, so it shows how engaged — or how
 * spending — the players who stayed are.
 */
export const COHORT_METRICS: Record<CohortMetric, CohortMetricDefinition> = {
  retention: {
    label: 'Retention',
    group: 'Engagement',
    note: 'Who comes back',
    definition: 'The share of each cohort active on day N.',
    per: 'share',
    accumulates: false,
    ...share
  },
  churn: {
    label: 'Churned',
    group: 'Engagement',
    note: 'Who is gone for good',
    definition: 'The share of each cohort not active on day N or any day since: the players the game has lost, rather than the ones resting.',
    per: 'share',
    accumulates: false,
    ...share
  },
  active_players: {
    label: 'Active players',
    group: 'Engagement',
    note: 'How many, not what share',
    definition: 'The players of each cohort active on day N, counted: the headcount LiveOps plans an event for.',
    per: 'count',
    format: value => writeFigure(value, { compact: value >= 10_000 }),
    accumulates: false,
    chart: { decimals: 0 }
  },
  active_days: {
    label: 'Days played per install',
    group: 'Engagement',
    note: 'Stickiness, to date',
    definition: 'Days each install was active: every install counted, whether it came back or not. To date, how many of its first N days a player spent in the game.',
    per: 'install',
    format: value => writeFigure(value, { decimals: value < 10 ? 2 : 1 }),
    accumulates: true,
    chart: { decimals: 2 }
  },
  sessions: {
    label: 'Sessions per install',
    group: 'Engagement',
    note: 'How often they open the game',
    definition: 'Sessions started per install: every install counted, whether it came back or not.',
    per: 'install',
    format: count,
    accumulates: true,
    chart: { decimals: 2 }
  },
  sessions_active: {
    label: 'Sessions per active player',
    group: 'Engagement',
    note: 'How often the ones who stayed open it',
    definition: 'Sessions on day N by each player active that day: the habit, among the players who kept it.',
    per: 'active',
    format: count,
    accumulates: false,
    chart: { decimals: 2 }
  },
  session_length: {
    label: 'Session length',
    group: 'Engagement',
    note: 'Minutes a session lasts',
    definition: 'Minutes played on day N over the sessions started that day.',
    per: 'session',
    format: minutes,
    accumulates: false,
    chart: { unit: 'min', decimals: 1 }
  },
  playtime: {
    label: 'Playtime per install',
    group: 'Engagement',
    note: 'Minutes played, spread over everyone',
    definition: 'Minutes played per install: every install counted, whether it came back or not.',
    per: 'install',
    format: minutes,
    accumulates: true,
    chart: { unit: 'min', decimals: 1 }
  },
  playtime_active: {
    label: 'Playtime per active player',
    group: 'Engagement',
    note: 'How long the ones who stayed play',
    definition: 'Minutes played on day N by each player active that day: how engaged the players who stayed are.',
    per: 'active',
    format: minutes,
    accumulates: false,
    chart: { unit: 'min', decimals: 1 }
  },
  events: {
    label: 'Event per install',
    group: 'Events',
    note: 'How often, any event',
    definition: 'How often each install did the event: every install counted, whether it came back or not.',
    per: 'install',
    format: count,
    accumulates: true,
    chart: { decimals: 2 }
  },
  event_share: {
    label: 'Players who did an event',
    group: 'Events',
    note: 'Tutorial done, guild joined, by day N',
    definition: 'The share of each cohort that has done the event at least once by day N.',
    per: 'share',
    accumulates: false,
    ...share
  },
  revenue: {
    label: 'Revenue per install',
    group: 'Monetization',
    note: 'IAP LTV, to date',
    definition: 'Purchase revenue per install, verified purchases only: every install counted, paying or not. To date, it is the LTV.',
    per: 'install',
    format: dollars,
    accumulates: true,
    chart: { prefix: '$', decimals: 2 }
  },
  ad_revenue: {
    label: 'Ad revenue per install',
    group: 'Monetization',
    note: 'IAA, to date',
    definition: 'Ad revenue per install, from the impressions reported and the network\'s eCPM: every install counted.',
    per: 'install',
    format: dollars,
    accumulates: true,
    chart: { prefix: '$', decimals: 3 }
  },
  revenue_all: {
    label: 'Total revenue per install',
    group: 'Monetization',
    note: 'Purchases and ads: blended LTV',
    definition: 'Purchase and ad revenue together, per install: the LTV a hybrid game plans its spend by.',
    per: 'install',
    format: dollars,
    accumulates: true,
    chart: { prefix: '$', decimals: 2 }
  },
  arpdau: {
    label: 'Revenue per active player',
    group: 'Monetization',
    note: 'ARPDAU, by cohort',
    definition: 'Revenue on day N from each player active that day: what a player who is still there is worth, day by day.',
    per: 'active',
    format: dollars,
    accumulates: false,
    chart: { prefix: '$', decimals: 3 }
  },
  arppu: {
    label: 'Revenue per payer',
    group: 'Monetization',
    note: 'ARPPU, to date',
    definition: 'Revenue to day N over the players who had paid by then: how much a payer spends, not how many pay.',
    per: 'payer',
    format: dollars,
    accumulates: false,
    chart: { prefix: '$', decimals: 2 }
  },
  payers: {
    label: 'Payer conversion',
    group: 'Monetization',
    note: 'Who has paid by day N',
    definition: 'The share of each cohort that has made a purchase by day N.',
    per: 'share',
    accumulates: false,
    ...share
  },
  purchases: {
    label: 'Purchases per install',
    group: 'Monetization',
    note: 'Transactions, spread over everyone',
    definition: 'Verified purchases per install: every install counted, paying or not.',
    per: 'install',
    format: count,
    accumulates: true,
    chart: { decimals: 3 }
  },
  purchases_payer: {
    label: 'Purchases per payer',
    group: 'Monetization',
    note: 'How often a payer buys again',
    definition: 'Purchases to day N over the players who had paid by then: one is a first purchase, more is a habit.',
    per: 'payer',
    format: count,
    accumulates: false,
    chart: { decimals: 2 }
  },
  crash_rate: {
    label: 'Crash rate',
    group: 'Quality',
    note: 'Sessions that end in a crash',
    definition: 'The share of sessions on day N that ended in a crash: a build that crashes loses its players days before retention shows it.',
    per: 'session',
    format: value => `${(value * 100).toFixed(2)}%`,
    accumulates: false,
    chart: { unit: '%', decimals: 2, scale: 100 }
  }
}

export const COHORT_METRIC_KEYS = Object.keys(COHORT_METRICS) as CohortMetric[]

/**
 * How the table, the curves and the figures read a metric: a share with the players
 * under it, a headcount added up across cohorts, or a figure per day — or, for the ones
 * that have one, the running total up to the day.
 */
export function cohortModeOf(metric: CohortMetric, accumulated: boolean): CohortMode {
  const definition = COHORT_METRICS[metric]
  if (definition.per === 'share') return rateMode(definition.label)
  if (definition.per === 'count') return countMode(definition.label)
  const label = definition.accumulates && accumulated ? `${definition.label}, to date` : definition.label
  return definition.accumulates && accumulated
    ? cumulativeMode({ label, format: definition.format })
    : valueMode({ label, format: definition.format })
}
