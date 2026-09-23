// A/B tests as the dashboard holds them, and the arithmetic that turns what players did into
// an answer somebody can act on.
//
// An experiment is built on a flag: each variant is a value the flag serves, and the split is
// the share of the test's audience that gets each one. The server already decides who is in
// which variant (a hash of the install, so a player stays in theirs), and the SDK already
// records which one a player saw (`feature_exposure`); what is new here is the experiment as a
// thing with a question, a start, an end and an answer. None of it is stored by the API yet
// (project.md §44 plans it); utils/experiments-sample.ts stands in for the results.
//
// The statistics are the plainest that answer the question honestly: each variant's mean and
// its spread, the difference from the control with a 95% range, and the chance that the
// variant is really better (a normal approximation, which at thousands of players a variant is
// sound). A result is only called once the test has run its planned length, because a lead in
// the first days of a test is mostly noise and stopping the moment it looks good is how a test
// lies.

export type ExperimentStatus = 'draft' | 'running' | 'concluded' | 'stopped'

export type MetricKey
  = | 'revenue_per_player'
    | 'payer_conversion'
    | 'd1_retention'
    | 'd7_retention'
    | 'sessions_per_player'
    | 'playtime_minutes'
    | 'ad_revenue_per_player'

export interface MetricDef {
  key: MetricKey
  label: string
  /** How the figure is written: a share, dollars, a count, minutes. */
  kind: 'rate' | 'money' | 'count' | 'minutes'
  /** One sentence on what it counts. */
  note: string
}

export const EXPERIMENT_METRICS: Record<MetricKey, MetricDef> = {
  revenue_per_player: { key: 'revenue_per_player', label: 'Revenue per player', kind: 'money', note: 'Verified purchases in the test, in USD, over every player in the variant, payers or not.' },
  payer_conversion: { key: 'payer_conversion', label: 'Payer conversion', kind: 'rate', note: 'The share of the variant\'s players who bought anything during the test.' },
  d1_retention: { key: 'd1_retention', label: 'Day-1 retention', kind: 'rate', note: 'The share who came back the day after they entered the test.' },
  d7_retention: { key: 'd7_retention', label: 'Day-7 retention', kind: 'rate', note: 'The share who played on the seventh day after they entered the test.' },
  sessions_per_player: { key: 'sessions_per_player', label: 'Sessions per player', kind: 'count', note: 'Sessions played during the test, per player.' },
  playtime_minutes: { key: 'playtime_minutes', label: 'Playtime per player', kind: 'minutes', note: 'Minutes played during the test, per player.' },
  ad_revenue_per_player: { key: 'ad_revenue_per_player', label: 'Ad revenue per player', kind: 'money', note: 'Estimated ad revenue during the test, per player.' }
}

export const METRIC_KEYS = Object.keys(EXPERIMENT_METRICS) as MetricKey[]

export interface VariantDef {
  id: string
  name: string
  /** What the experiment's flag serves this variant. */
  value: Json
  /** Percent of the test's audience. The variants add up to 100. */
  weight: number
  control: boolean
}

export interface ExperimentEvent {
  at: string
  actor: string
  text: string
}

export interface ExperimentDef {
  id: string
  name: string
  /** What the team expects, in a sentence: the thing the result answers. */
  hypothesis: string
  flagKey: string
  /** Who is in the test, in words: "New players on Android". */
  audience: string
  status: ExperimentStatus
  primaryMetric: MetricKey
  /** Metrics that must not get worse, whatever the primary does. */
  guardrails: MetricKey[]
  variants: VariantDef[]
  plannedDays: number
  created_at: string
  started_at?: string
  ended_at?: string
  /** The variant that won, once somebody decided; null for "no clear winner". */
  winner?: string | null
  /** The variant's value was made the flag's for everybody. */
  shipped?: boolean
  /** Why it was stopped early, or what was decided. */
  conclusion?: string
  events: ExperimentEvent[]
}

export const EXPERIMENT_STATUS: Record<ExperimentStatus, { label: string, tone: BadgeTone }> = {
  draft: { label: 'Draft', tone: 'neutral' },
  running: { label: 'Running', tone: 'success' },
  concluded: { label: 'Finished', tone: 'neutral' },
  stopped: { label: 'Stopped early', tone: 'warning' }
}

/** The fewest days before a result is worth reading: a week covers every day of the week once. */
export const MIN_DAYS = 7

/** How sure a result has to be before it is called. */
export const CONFIDENCE = 0.95

// --- Arithmetic ----------------------------------------------------------------------------

/** A variant's figure for one metric: how many players, the mean, and the spread. */
export interface VariantStat {
  n: number
  mean: number
  sd: number
}

/** The standard normal's cumulative distribution, by Abramowitz and Stegun's 7.1.26. */
export function normalCdf(z: number): number {
  const t = 1 / (1 + 0.3275911 * Math.abs(z) / Math.SQRT2)
  const poly = t * (0.254829592 + t * (-0.284496736 + t * (1.421413741 + t * (-1.453152027 + t * 1.061405429))))
  const erf = 1 - poly * Math.exp(-(z * z) / 2)
  return z >= 0 ? (1 + erf) / 2 : (1 - erf) / 2
}

export interface Comparison {
  /** Relative to the control: 0.084 is 8.4% more. */
  lift: number
  /** The 95% range of the lift. */
  low: number
  high: number
  /** The chance the variant is really better than the control. */
  chance: number
  /** The absolute difference, for a rate in points. */
  difference: number
}

export function compareVariants(control: VariantStat, variant: VariantStat): Comparison {
  const difference = variant.mean - control.mean
  const se = Math.sqrt(control.sd ** 2 / Math.max(control.n, 1) + variant.sd ** 2 / Math.max(variant.n, 1)) || 1e-9
  const base = control.mean || 1e-9
  return {
    lift: difference / base,
    low: (difference - 1.96 * se) / base,
    high: (difference + 1.96 * se) / base,
    chance: normalCdf(difference / se),
    difference
  }
}

// --- In words ------------------------------------------------------------------------------

/** A metric's value as the table writes it: "38.2%", "$0.42", "9.1", "64 min". */
export function writeMetric(metric: MetricKey, value: number): string {
  const kind = EXPERIMENT_METRICS[metric].kind
  if (kind === 'rate') return `${(value * 100).toFixed(1)}%`
  if (kind === 'money') return `$${value.toFixed(value < 1 ? 3 : 2)}`
  if (kind === 'minutes') return `${Math.round(value)} min`
  return value.toFixed(1)
}

/** A lift as a signed percent: "+8.4%", "-1.2%". A rate's also in points: "+2.1 pts". */
export function writeLift(metric: MetricKey, comparison: Comparison): string {
  if (EXPERIMENT_METRICS[metric].kind === 'rate') {
    const points = comparison.difference * 100
    return `${points >= 0 ? '+' : ''}${points.toFixed(1)} pts`
  }
  const percent = comparison.lift * 100
  return `${percent >= 0 ? '+' : ''}${percent.toFixed(1)}%`
}

export const writeChance = (chance: number) => `${Math.round(chance * 100)}%`

/** Whether a comparison is a sure gain, a sure loss or neither, at the confidence above. */
export function outcomeOf(comparison: Comparison): 'better' | 'worse' | 'unclear' {
  if (comparison.chance >= CONFIDENCE) return 'better'
  if (comparison.chance <= 1 - CONFIDENCE) return 'worse'
  return 'unclear'
}

const DAY = 86_400_000

/** Whole days an experiment has run, or ran. */
export function daysRun(experiment: ExperimentDef, now = Date.now()): number {
  if (!experiment.started_at) return 0
  const end = experiment.ended_at ? Date.parse(experiment.ended_at) : now
  return Math.max(0, Math.floor((end - Date.parse(experiment.started_at)) / DAY))
}

/** "Sep 2", or "Sep 2, 2025" outside this year. */
export function shortDate(iso: string): string {
  const at = new Date(iso)
  const sameYear = at.getFullYear() === new Date().getFullYear()
  return at.toLocaleDateString('en', { month: 'short', day: 'numeric', ...(sameYear ? {} : { year: 'numeric' }) })
}

/** When it ran, in words: "Sep 2 to Sep 23, 21 days", "Since Sep 2, day 12 of 21". */
export function whenWords(experiment: ExperimentDef): string {
  if (!experiment.started_at) return `Not started. Planned for ${experiment.plannedDays} days.`
  const days = daysRun(experiment)
  if (!experiment.ended_at) return `Since ${shortDate(experiment.started_at)}, day ${days + 1} of ${experiment.plannedDays}`
  return `${shortDate(experiment.started_at)} to ${shortDate(experiment.ended_at)}, ${days} ${days === 1 ? 'day' : 'days'}`
}

// --- The answer ----------------------------------------------------------------------------

/** Every variant but the control against the control, metric by metric. */
export interface VariantAgainstControl {
  variant: VariantDef
  players: number
  comparisons: Record<MetricKey, Comparison>
}

export type VerdictKind = 'draft' | 'collecting' | 'leading' | 'ready' | 'unclear' | 'control' | 'won' | 'no-winner' | 'stopped'

export interface Verdict {
  kind: VerdictKind
  /** The answer, in a few words: "Regional price is winning". */
  title: string
  /** Why, in a sentence or two, with the figures. */
  detail: string
  /** The variant the answer is about, when there is one. */
  variant?: VariantDef
  /** Guardrails a variant made worse, said out loud whatever the primary did. */
  breaches: { variant: VariantDef, metric: MetricKey, comparison: Comparison }[]
}

export function againstControl(results: ExperimentResults): { control: VariantResult, others: VariantAgainstControl[] } | null {
  const control = results.variants.find(entry => entry.variant.control) ?? results.variants[0]
  if (!control) return null
  const others = results.variants.filter(entry => entry !== control).map(entry => ({
    variant: entry.variant,
    players: entry.players,
    comparisons: Object.fromEntries(METRIC_KEYS.map(metric => [metric, compareVariants(control.stats[metric], entry.stats[metric])])) as Record<MetricKey, Comparison>
  }))
  return { control, others }
}

/**
 * The one sentence a results page opens with. It answers what somebody opening a test wants
 * to know, in the order they want it: has it started, is it too early, who is ahead and how
 * sure, and, once it is over, what was decided.
 */
export function verdictOf(experiment: ExperimentDef, results: ExperimentResults | null): Verdict {
  const metric = EXPERIMENT_METRICS[experiment.primaryMetric]
  if (experiment.status === 'draft' || !results) {
    return { kind: 'draft', title: 'Not started yet', detail: `Once it starts, players are split between ${experiment.variants.length} variants and ${metric.label.toLowerCase()} is compared for ${experiment.plannedDays} days.`, breaches: [] }
  }
  const split = againstControl(results)
  if (!split) return { kind: 'unclear', title: 'No results', detail: '', breaches: [] }
  const { control, others } = split

  const breaches = others.flatMap(entry => experiment.guardrails
    .filter(guardrail => outcomeOf(entry.comparisons[guardrail]) === 'worse')
    .map(guardrail => ({ variant: entry.variant, metric: guardrail, comparison: entry.comparisons[guardrail] })))

  const best = [...others].sort((a, b) => b.comparisons[experiment.primaryMetric].chance - a.comparisons[experiment.primaryMetric].chance)[0]
  const days = daysRun(experiment)
  const figures = (entry: VariantAgainstControl) => {
    const comparison = entry.comparisons[experiment.primaryMetric]
    return `${writeLift(experiment.primaryMetric, comparison)} ${metric.label.toLowerCase()} against ${control.variant.name}, a ${writeChance(comparison.chance)} chance it is really better`
  }

  if (experiment.status === 'stopped') {
    return { kind: 'stopped', title: `Stopped early, on day ${days}`, detail: experiment.conclusion ?? 'Stopped before its planned end, so its result is not a verdict.', breaches }
  }
  if (experiment.status === 'concluded') {
    const winner = experiment.variants.find(entry => entry.id === experiment.winner)
    if (winner) {
      const entry = others.find(other => other.variant.id === winner.id)
      return { kind: 'won', title: `${winner.name} won`, detail: experiment.conclusion ?? (entry ? `${figures(entry)}.` : ''), variant: winner, breaches }
    }
    return { kind: 'no-winner', title: 'No clear winner', detail: experiment.conclusion ?? `No variant moved ${metric.label.toLowerCase()} by more than its noise.`, breaches }
  }

  if (days < MIN_DAYS) {
    return { kind: 'collecting', title: 'Too early to call', detail: `Day ${days + 1} of ${experiment.plannedDays}. Results settle after the first week: a lead before then is mostly noise, whichever way it points.`, breaches }
  }
  if (!best) return { kind: 'unclear', title: 'No results', detail: '', breaches }
  const outcome = outcomeOf(best.comparisons[experiment.primaryMetric])
  const finished = days >= experiment.plannedDays
  if (outcome === 'better') {
    return finished
      ? { kind: 'ready', title: `${best.variant.name} won`, detail: `${figures(best)}. The test has run its planned ${experiment.plannedDays} days: finish it and ship the winner.`, variant: best.variant, breaches }
      : { kind: 'leading', title: `${best.variant.name} is winning`, detail: `${figures(best)}. Let it run to day ${experiment.plannedDays} to be sure the lead holds.`, variant: best.variant, breaches }
  }
  if (others.every(entry => outcomeOf(entry.comparisons[experiment.primaryMetric]) === 'worse')) {
    return { kind: 'control', title: `${control.variant.name} is winning`, detail: `Every variant is doing worse than the control on ${metric.label.toLowerCase()}.`, variant: control.variant, breaches }
  }
  return {
    kind: 'unclear',
    title: finished ? 'No clear winner' : 'No clear leader yet',
    detail: `${best.variant.name} is closest: ${figures(best)}. ${finished ? 'The planned days are up; keep the control or test a bigger change.' : `${Math.max(0, experiment.plannedDays - days - 1)} days to go.`}`,
    variant: best.variant,
    breaches
  }
}
