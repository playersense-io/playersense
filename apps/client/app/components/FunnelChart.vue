<script setup lang="ts">
// A funnel as a row of columns, one per step, each filled to the share of step 1 that got
// that far. Above the fill, hatched, are the players the step lost from the one before, so
// the drop reads as a thing that happened rather than as empty space. Between two columns
// sits what the gap is about: how many of the step before went on, and how long the middle
// one of them took.
//
// Broken down, each column holds a bar per value in the chart palette, each filled against
// its own step 1, so a small platform is compared by how it converts rather than drowned
// by how many players it has. The figures above stay those of every player.
//
// A step too thin to show is withheld (§32.2): its bar is left empty with the threshold
// written in it, never drawn at zero. Pointing at a bar, or tabbing to one, opens a card
// that says the step in words.
const props = defineProps<{
  steps: readonly FunnelStep[]
  total: FunnelSeries
  breakdown: readonly FunnelSeries[]
  threshold: number
}>()

const grouped = computed(() => props.breakdown.length > 0)

/* Nobody entered the first step: no bars to draw, whether the count is zero or held back. */
const empty = computed(() => !props.total.players[0])
const bars = computed(() => (grouped.value ? props.breakdown : [props.total]))

/** A palette slot for each breakdown value, in order; the accent for every player. */
const colourOf = (index: number) => (grouped.value ? `var(--v-theme-chart-${(index % 6) + 1})` : 'var(--v-theme-primary)')

const share = (series: FunnelSeries, step: number): number | null => {
  const first = series.players[0]
  const here = series.players[step]
  return first && here !== null && here !== undefined ? here / first : null
}

/** How many of the step before went on to this one. */
const onward = (series: FunnelSeries, step: number): number | null => {
  const before = series.players[step - 1]
  const here = series.players[step]
  return before && here !== null && here !== undefined ? here / before : null
}

const percent = formatShare

const columns = computed(() => props.steps.map(() => 'minmax(6rem, 1fr)').join(' 4.5rem '))

// --- The card -----------------------------------------------------------------------------

interface ActiveBar { series: number, step: number, el: HTMLElement }

const active = shallowRef<ActiveBar | null>(null)

function show(event: Event, series: number, step: number) {
  active.value = { series, step, el: event.currentTarget as HTMLElement }
}

function hide() {
  active.value = null
}

interface CardRow { label: string, value: string, tone?: 'lost' }

const card = computed(() => {
  const place = active.value
  if (!place) return null
  const series = bars.value[place.series]
  const step = props.steps[place.step]
  if (!series || !step) return null
  const players = series.players[place.step]
  const rows: CardRow[] = []
  let note = ''
  if (players === null || players === undefined) {
    note = `Fewer than ${props.threshold} players, so withheld: no one can be singled out.`
  } else if (place.step === 0) {
    note = 'Everyone who entered the funnel.'
  } else {
    const ofFirst = share(series, place.step)
    const ofBefore = onward(series, place.step)
    const before = series.players[place.step - 1]
    const median = series.medians[place.step]
    if (ofFirst !== null) rows.push({ label: 'Of step 1', value: percent(ofFirst) })
    if (ofBefore !== null) rows.push({ label: `Went on from step ${place.step}`, value: percent(ofBefore) })
    if (before) rows.push({ label: `Lost since step ${place.step}`, value: writeFigure(before - players), tone: 'lost' })
    if (median !== null && median !== undefined) rows.push({ label: 'Median wait', value: formatSeconds(median) })
  }
  return {
    step: place.step + 1,
    event: step.event,
    where: step.where.map(condition => `${condition.property} = ${condition.value}`),
    series: grouped.value ? { name: series.name, colour: colourOf(place.series) } : null,
    figure: players === null || players === undefined ? `<${props.threshold}` : writeFigure(players),
    rows,
    note
  }
})

function spoken(series: FunnelSeries, step: number): string {
  const players = series.players[step]
  const figure = players === null || players === undefined ? `fewer than ${props.threshold} players` : `${writeFigure(players)} players`
  const rate = share(series, step)
  return [grouped.value ? series.name : '', `Step ${step + 1}`, figure, rate === null ? '' : `${percent(rate)} of step 1`].filter(Boolean).join(', ')
}
</script>

<template>
  <NoDataPlaceholder
    v-if="empty"
    :height="240"
    :seed="steps.length"
    label="No data yet"
  />
  <div v-else class="flex flex-col gap-4">
    <ul v-if="grouped" aria-label="Breakdown" class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1.5 p-0">
      <li v-for="(series, index) in bars" :key="series.name" class="flex items-center gap-1.5 text-xs text-ink/70">
        <span :style="{ background: `rgb(${colourOf(index)})` }" aria-hidden="true" class="size-2 rounded-full" />
        {{ series.name }}
      </li>
    </ul>

    <div class="ps-funnel__scroller">
      <div :style="{ gridTemplateColumns: columns }" class="ps-funnel__grid">
        <template v-for="(step, index) in steps" :key="index">
          <div v-if="index > 0" aria-hidden="true" class="ps-funnel__link">
            <span />
            <span />
            <div class="ps-funnel__connector">
              <span class="ps-funnel__onward">
                {{ onward(total, index) === null ? '–' : percent(onward(total, index)!) }}
              </span>
              <span v-if="total.medians[index] !== null" class="ps-funnel__wait">
                {{ formatSeconds(total.medians[index]!) }}
              </span>
            </div>
          </div>

          <div class="ps-funnel__step">
            <div class="flex min-w-0 flex-col gap-0.5">
              <span class="text-[11px] text-ink/50">Step {{ index + 1 }}</span>
              <span class="truncate font-mono text-xs text-ink" :title="step.event">{{ step.event }}</span>
              <span
                v-for="condition in step.where"
                :key="condition.property"
                class="truncate text-[11px] text-ink/55"
              >{{ condition.property }} = {{ condition.value }}</span>
            </div>

            <div class="flex items-baseline gap-2 pt-2">
              <template v-if="total.players[index] !== null">
                <span class="font-mono text-base leading-6 text-ink">
                  {{ share(total, index) === null ? '–' : percent(share(total, index)!) }}
                </span>
                <span class="font-mono text-[11px] text-ink/55">{{ writeFigure(total.players[index]!, { compact: total.players[index]! >= 100_000 }) }}</span>
              </template>
              <span v-else class="font-mono text-base leading-6 text-ink/40">&lt;{{ threshold }}</span>
            </div>

            <div class="ps-funnel__bars">
              <button
                v-for="(series, seriesIndex) in bars"
                :key="series.name"
                :aria-label="spoken(series, index)"
                :style="{ '--bar': colourOf(seriesIndex) }"
                class="ps-funnel__bar"
                type="button"
                @blur="hide"
                @focus="show($event, seriesIndex, index)"
                @pointerenter="show($event, seriesIndex, index)"
                @pointerleave="hide"
              >
                <template v-if="series.players[index] !== null && share(series, index) !== null">
                  <span
                    :style="{ height: `${(index === 0 ? 1 : share(series, index - 1) ?? share(series, index)!) * 100}%` }"
                    class="ps-funnel__lost"
                  />
                  <span :style="{ height: `${share(series, index)! * 100}%` }" class="ps-funnel__kept" />
                </template>
                <span v-else class="ps-funnel__withheld">&lt;{{ threshold }}</span>
              </button>
            </div>
          </div>
        </template>
      </div>
    </div>

    <v-tooltip
      :model-value="card !== null"
      :offset="8"
      :target="active?.el"
      :transition="false"
      content-class="ps-figure-card"
      location="end"
    >
      <div v-if="card" class="flex w-56 flex-col gap-2.5">
        <div class="flex items-start gap-2">
          <span class="mt-px flex size-4 flex-none items-center justify-center rounded-full bg-ink/8 font-mono text-[10px] text-ink/70">
            {{ card.step }}
          </span>
          <div class="flex min-w-0 flex-col">
            <span class="truncate font-mono text-xs text-ink">{{ card.event }}</span>
            <span v-for="condition in card.where" :key="condition" class="truncate text-[11px] text-ink/55">{{ condition }}</span>
          </div>
        </div>

        <div class="flex items-baseline justify-between gap-3">
          <span class="flex items-center gap-1.5 text-xs text-ink/60">
            <span
              v-if="card.series"
              :style="{ background: `rgb(${card.series.colour})` }"
              aria-hidden="true"
              class="size-2 rounded-full"
            />
            {{ card.series ? card.series.name : 'Players' }}
          </span>
          <span class="font-mono text-base leading-6 text-ink">{{ card.figure }}</span>
        </div>

        <dl v-if="card.rows.length" class="m-0 flex flex-col gap-1 border-t border-line pt-2">
          <div v-for="row in card.rows" :key="row.label" class="flex items-baseline justify-between gap-3 text-xs">
            <dt class="text-ink/60">
              {{ row.label }}
            </dt>
            <dd :class="row.tone === 'lost' ? 'text-error' : 'text-ink'" class="m-0 font-mono">
              {{ row.tone === 'lost' ? `−${row.value}` : row.value }}
            </dd>
          </div>
        </dl>
        <p v-if="card.note" class="m-0 border-t border-line pt-2 text-xs text-ink/60">
          {{ card.note }}
        </p>
      </div>
    </v-tooltip>
  </div>
</template>

<style scoped>
/* Room for the ring a bar takes under the pointer, which the scroll box would otherwise
   cut off at the first bar's left edge and the last one's right. */
.ps-funnel__scroller {
  margin: -4px;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  padding: 4px;
}

/* Three rows every column shares through subgrid — the step, its figures, its bars — so a
   step with conditions under its name never lifts its bars above the others. */
.ps-funnel__grid {
  display: grid;
  grid-template-rows: auto auto 14rem;
  row-gap: 8px;
}

.ps-funnel__step,
.ps-funnel__link {
  display: grid;
  grid-row: span 3;
  grid-template-rows: subgrid;
  min-width: 0;
}

.ps-funnel__bars {
  align-items: stretch;
  display: flex;
  gap: 3px;
}

.ps-funnel__bar {
  background: rgba(var(--v-theme-on-surface), 0.03);
  border-radius: var(--ps-radius-x-small);
  cursor: default;
  flex: 1;
  min-width: 6px;
  overflow: hidden;
  position: relative;
  transition: box-shadow 120ms;
}

.ps-funnel__bar:hover,
.ps-funnel__bar:focus-visible {
  box-shadow: 0 0 0 2px rgb(var(--v-theme-on-surface));
  outline: none;
}

.ps-funnel__lost,
.ps-funnel__kept {
  bottom: 0;
  left: 0;
  position: absolute;
  right: 0;
}

/* The players lost since the step before: the bar's own colour, hatched and faint. */
.ps-funnel__lost {
  background: repeating-linear-gradient(
    -45deg,
    rgba(var(--bar), 0.22) 0 3px,
    rgba(var(--bar), 0.06) 3px 7px
  );
}

.ps-funnel__kept {
  background: rgb(var(--bar));
}

.ps-funnel__withheld {
  color: rgba(var(--v-theme-on-surface), 0.4);
  font-family: var(--font-mono);
  font-size: 11px;
  inset: auto 0 8px;
  position: absolute;
  text-align: center;
}

/* The gap between two steps, and what crossing it cost: a rule through the middle of the
   bars, with the share that went on and the median wait sitting on it. */
.ps-funnel__connector {
  align-items: center;
  display: flex;
  flex-direction: column;
  justify-content: center;
  position: relative;
}

.ps-funnel__connector::before {
  border-top: 1px dashed rgba(var(--v-theme-on-surface), 0.18);
  content: "";
  inset: 50% 4px auto;
  position: absolute;
}

/* Under the rule, so the share sits on it and the wait hangs from it. */
.ps-funnel__wait {
  color: rgba(var(--v-theme-on-surface), 0.5);
  font-family: var(--font-mono);
  font-size: 11px;
  position: absolute;
  top: calc(50% + 12px);
}

.ps-funnel__onward {
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 999px;
  color: rgb(var(--v-theme-on-surface));
  font-family: var(--font-mono);
  font-size: 11px;
  line-height: 18px;
  padding: 0 7px;
  position: relative;
}
</style>
