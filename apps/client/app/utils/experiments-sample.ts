// Sample data for Growth > Experiments. What it stands in for:
//
//   The experiments are what an experiments table will hold (project.md §11 lists one; nothing
//   is built). The results are what a query over `feature_exposure` joined to each player's
//   purchases and sessions will answer (§44): for each variant, how many players entered and
//   what they did from then on. Kept in this browser until both exist.
//
// Each sample test has a true effect per variant and metric, and the figures are drawn around
// it with the noise a variant of that size really has, so a small test wobbles, a big one
// settles, and the lead in the first days is often not the lead at the end. Seeded by the
// experiment's id, so a reload draws the same results.

const DAY = 86_400_000

const ago = (days: number) => new Date(Date.now() - days * DAY).toISOString()

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

/** A draw from the standard normal, by Box and Muller. */
const gauss = (random: () => number) => Math.sqrt(-2 * Math.log(random() || 1e-12)) * Math.cos(2 * Math.PI * random())

/** A player of this game, before any test: each metric's mean and spread over a test window. */
const BASELINE: Record<MetricKey, { mean: number, sd?: number }> = {
  revenue_per_player: { mean: 0.42, sd: 3.6 },
  payer_conversion: { mean: 0.031 },
  d1_retention: { mean: 0.38 },
  d7_retention: { mean: 0.14 },
  sessions_per_player: { mean: 9.2, sd: 7 },
  playtime_minutes: { mean: 64, sd: 55 },
  ad_revenue_per_player: { mean: 0.09, sd: 0.12 }
}

const spreadOf = (metric: MetricKey, mean: number) =>
  EXPERIMENT_METRICS[metric].kind === 'rate' ? Math.sqrt(mean * (1 - mean)) : (BASELINE[metric].sd ?? mean)

// --- The tests ----------------------------------------------------------------------------

type Effects = Partial<Record<MetricKey, number>>

interface SampleExperiment extends ExperimentDef {
  /** The true effect of each variant, relative to the control, by metric. */
  truth: Record<string, Effects>
  /** Eligible players a day, before the test's traffic share lets some of them in. */
  enrolment: number
}

const variant = (id: string, name: string, value: Json, weight: number, control = false): VariantDef => ({ id, name, value, weight, control })

const event = (days: number, actor: string, text: string): ExperimentEvent => ({ at: ago(days), actor, text })

const SAMPLES: SampleExperiment[] = [
  {
    id: 'exp-battle-pass-regional',
    traffic: 50,
    name: 'Regional battle pass price',
    hypothesis: 'A lower battle pass price where regional pricing applies sells enough more passes to earn more per player.',
    flagKey: 'battle_pass.price_tier',
    audience: 'Players in emerging markets',
    status: 'running',
    primaryMetric: 'revenue_per_player',
    guardrails: ['d7_retention'],
    variants: [variant('a', 'Standard price', 'standard', 50, true), variant('b', 'Regional price', 'regional', 50)],
    plannedDays: 35,
    created_at: ago(35),
    started_at: ago(33),
    events: [event(35, 'mira@acme.games', 'Created'), event(33, 'mira@acme.games', 'Started: 50% standard, 50% regional')],
    truth: { b: { revenue_per_player: 0.2, payer_conversion: 0.6, d7_retention: 0.01 } },
    enrolment: 5200
  },
  {
    id: 'exp-skip-tutorial',
    traffic: 100,
    name: 'Skip button on the tutorial',
    hypothesis: 'Letting experienced players skip the tutorial keeps more of them past the first day.',
    flagKey: 'tutorial.skip_button',
    audience: 'Players who have not finished the tutorial',
    status: 'running',
    primaryMetric: 'd1_retention',
    guardrails: ['d7_retention', 'revenue_per_player'],
    variants: [variant('a', 'No skip button', false, 50, true), variant('b', 'Skip button', true, 50)],
    plannedDays: 28,
    created_at: ago(21),
    started_at: ago(19),
    events: [event(21, 'kaan@acme.games', 'Created'), event(19, 'kaan@acme.games', 'Started: 50% without, 50% with the skip button')],
    truth: { b: { d1_retention: 0.035, d7_retention: 0.012, sessions_per_player: 0.02 } },
    enrolment: 1900
  },
  {
    id: 'exp-halloween-banner',
    traffic: 20,
    name: 'Halloween banner on the main menu',
    hypothesis: 'A banner for the Halloween event on the main menu brings players back to it more often.',
    flagKey: 'events.halloween_banner',
    audience: 'Players at level 10 or above',
    status: 'draft',
    primaryMetric: 'sessions_per_player',
    guardrails: ['revenue_per_player'],
    variants: [variant('a', 'No banner', false, 50, true), variant('b', 'Banner', true, 50)],
    plannedDays: 14,
    created_at: ago(2),
    events: [event(2, 'hmmusicproducing@gmail.com', 'Created')],
    truth: {},
    enrolment: 15000
  },
  {
    id: 'exp-daily-reward',
    traffic: 30,
    name: 'Daily reward size',
    hypothesis: 'A bigger daily login chest keeps more players to their second week.',
    flagKey: 'economy.daily_reward_gems',
    audience: 'Every player',
    status: 'concluded',
    primaryMetric: 'd7_retention',
    guardrails: ['revenue_per_player'],
    variants: [variant('a', '50 gems', 50, 34, true), variant('b', '80 gems', 80, 33), variant('c', '100 gems', 100, 33)],
    plannedDays: 28,
    created_at: ago(72),
    started_at: ago(70),
    ended_at: ago(42),
    winner: 'b',
    shipped: true,
    conclusion: '80 gems kept more players to day 7 without costing purchases. 100 gems kept as many, but players bought fewer gems.',
    events: [event(72, 'deniz@acme.games', 'Created'), event(70, 'deniz@acme.games', 'Started: a third each on 50, 80 and 100 gems'), event(42, 'deniz@acme.games', 'Finished: 80 gems won'), event(41, 'deniz@acme.games', 'Shipped 80 gems to every player')],
    truth: { b: { d7_retention: 0.06, revenue_per_player: -0.01, sessions_per_player: 0.03 }, c: { d7_retention: 0.065, revenue_per_player: -0.08, payer_conversion: -0.07 } },
    enrolment: 20600
  },
  {
    id: 'exp-store-carousel',
    traffic: 50,
    name: 'Carousel storefront',
    hypothesis: 'Showing offers one at a time in a carousel sells more than the grid.',
    flagKey: 'store.layout',
    audience: 'Phones',
    status: 'concluded',
    primaryMetric: 'revenue_per_player',
    guardrails: ['payer_conversion'],
    variants: [variant('a', 'Grid', 'grid', 50, true), variant('b', 'Carousel', 'carousel', 50)],
    plannedDays: 28,
    created_at: ago(97),
    started_at: ago(95),
    ended_at: ago(67),
    winner: null,
    conclusion: 'No clear difference in four weeks. The grid stays; the carousel ships slowly as a design change rather than a revenue one.',
    events: [event(97, 'mira@acme.games', 'Created'), event(95, 'mira@acme.games', 'Started: 50% grid, 50% carousel'), event(67, 'mira@acme.games', 'Finished: no clear winner')],
    truth: { b: { revenue_per_player: 0.008, payer_conversion: 0.01 } },
    enrolment: 7600
  },
  {
    id: 'exp-starter-discount',
    traffic: 100,
    name: 'Starter pack discount',
    hypothesis: '30% off the starter pack turns more new players into payers, and pays for itself.',
    flagKey: 'offers.starter_pack_discount',
    audience: 'New players who have not paid',
    status: 'concluded',
    primaryMetric: 'payer_conversion',
    guardrails: ['revenue_per_player'],
    variants: [variant('a', 'Full price', 0, 50, true), variant('b', '30% off', 30, 50)],
    plannedDays: 21,
    created_at: ago(132),
    started_at: ago(130),
    ended_at: ago(109),
    winner: 'b',
    shipped: true,
    conclusion: 'A third more new players paid, and revenue per player rose with them.',
    events: [event(132, 'kaan@acme.games', 'Created'), event(130, 'kaan@acme.games', 'Started'), event(109, 'kaan@acme.games', 'Finished: 30% off won'), event(108, 'kaan@acme.games', 'Shipped 30% off to new players')],
    truth: { b: { payer_conversion: 0.34, revenue_per_player: 0.09 } },
    enrolment: 2400
  },
  {
    id: 'exp-energy-refill',
    traffic: 25,
    name: 'Faster energy refill',
    hypothesis: 'Refilling energy in 6 minutes instead of 8 makes players play more sessions.',
    flagKey: 'economy.energy_refill_seconds',
    audience: 'Every player',
    status: 'stopped',
    primaryMetric: 'sessions_per_player',
    guardrails: ['revenue_per_player'],
    variants: [variant('a', '8 minutes', 480, 50, true), variant('b', '6 minutes', 360, 50)],
    plannedDays: 21,
    created_at: ago(162),
    started_at: ago(160),
    ended_at: ago(150),
    winner: null,
    conclusion: 'Stopped on day 10: revenue per player fell by a fifth, as players stopped buying refills. Sessions barely moved.',
    events: [event(162, 'deniz@acme.games', 'Created'), event(160, 'deniz@acme.games', 'Started'), event(150, 'deniz@acme.games', 'Stopped early: revenue guardrail')],
    truth: { b: { sessions_per_player: 0.03, revenue_per_player: -0.22, payer_conversion: -0.15 } },
    enrolment: 24000
  },
  {
    id: 'exp-interstitials',
    traffic: 40,
    name: 'Fewer interstitial ads',
    hypothesis: 'An interstitial every 4 matches instead of every 3 keeps more players without losing much ad revenue.',
    flagKey: 'ads.interstitial_every_n_matches',
    audience: 'Players who have never paid',
    status: 'concluded',
    primaryMetric: 'd7_retention',
    guardrails: ['ad_revenue_per_player'],
    variants: [variant('a', 'Every 3 matches', 3, 50, true), variant('b', 'Every 4 matches', 4, 50)],
    plannedDays: 28,
    created_at: ago(207),
    started_at: ago(205),
    ended_at: ago(177),
    winner: 'b',
    shipped: true,
    conclusion: 'Day-7 retention rose and ad revenue per player held within its noise.',
    events: [event(207, 'mira@acme.games', 'Created'), event(205, 'mira@acme.games', 'Started'), event(177, 'mira@acme.games', 'Finished: every 4 matches won'), event(176, 'mira@acme.games', 'Shipped every 4 matches')],
    truth: { b: { d7_retention: 0.07, ad_revenue_per_player: -0.03, sessions_per_player: 0.03 } },
    enrolment: 13000
  }
]

/** The sample experiments as a new visit finds them. The truths stay here; the store gets the rest. */
export function seedExperiments(): ExperimentDef[] {
  return SAMPLES.map(({ truth: _truth, enrolment: _enrolment, ...experiment }) => JSON.parse(JSON.stringify(experiment)) as ExperimentDef)
}

// --- Results ------------------------------------------------------------------------------

export interface VariantResult {
  variant: VariantDef
  players: number
  stats: Record<MetricKey, VariantStat>
}

export interface ExperimentResults {
  /** Day starts, from the first day of the test to the last one with data. */
  days: number[]
  variants: VariantResult[]
  /** Per variant and metric, the running mean at the end of each day. */
  daily: Record<string, { players: number[], metrics: Record<MetricKey, number[]> }>
}

/**
 * What the test has measured so far: for each variant, its players and each metric, and how
 * each metric's running mean moved day by day. Null for a test that has not started.
 */
export function sampleExperimentResults(experiment: ExperimentDef): ExperimentResults | null {
  if (!experiment.started_at) return null
  const sample = SAMPLES.find(entry => entry.id === experiment.id)
  const truth = sample?.truth ?? {}
  const enrolment = sample?.enrolment ?? 2500
  const random = seeded(`experiment:${experiment.id}`)
  const days = Math.max(1, daysRun(experiment))
  const start = Date.parse(experiment.started_at)
  const dayStarts = Array.from({ length: days }, (_, index) => start + index * DAY)

  const daily: ExperimentResults['daily'] = {}
  const variants: VariantResult[] = experiment.variants.map((entry) => {
    const effects = truth[entry.id] ?? {}
    const players = dayStarts.map((_, index) => Math.round(enrolment * ((experiment.traffic ?? 100) / 100) * (entry.weight / 100) * (index + 1) * (0.94 + random() * 0.12)))
    const metrics = {} as Record<MetricKey, number[]>
    const stats = {} as Record<MetricKey, VariantStat>
    for (const metric of METRIC_KEYS) {
      const mean = BASELINE[metric].mean * (1 + (effects[metric] ?? 0))
      const sd = spreadOf(metric, mean)
      // A noise that wanders and settles: each day's is mostly the day before's.
      let z = gauss(random)
      metrics[metric] = players.map((n) => {
        z = 0.8 * z + 0.6 * gauss(random)
        return Math.max(0, mean + z * sd / Math.sqrt(Math.max(n, 1)))
      })
      stats[metric] = { n: players[players.length - 1]!, mean: metrics[metric][metrics[metric].length - 1]!, sd }
    }
    daily[entry.id] = { players, metrics }
    return { variant: entry, players: players[players.length - 1]!, stats }
  })

  return { days: dayStarts, variants, daily }
}

// --- The game, day by day -----------------------------------------------------------------

export interface DayTotals {
  players: number
  newPlayers: number
  revenue: number
}

/**
 * The whole game's day, for the strip over the calendar: players, new players and revenue, so
 * a test can be read against the week it ran in. Null for a day that has not happened. Stands
 * in for the metrics query's daily totals (dau, installs, revenue).
 */
export function sampleDayTotals(dayStart: number): DayTotals | null {
  if (dayStart > Date.now()) return null
  const random = seeded(`day:${new Date(dayStart).toISOString().slice(0, 10)}`)
  const weekday = new Date(dayStart).getDay()
  const weekend = weekday === 0 || weekday === 6 ? 1.14 : 1
  const players = Math.round(48_000 * weekend * (0.94 + random() * 0.12))
  return {
    players,
    newPlayers: Math.round(players * (0.06 + random() * 0.02)),
    revenue: Math.round(players * 0.41 * (0.8 + random() * 0.45))
  }
}
