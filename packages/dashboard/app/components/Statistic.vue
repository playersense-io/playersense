<script setup lang="ts">
/**
 * One figure and what to make of it: the number a screen is about, set in the mono face
 * with its movement beside it, under a small label and over a line of context.
 *
 * The figure is the loud part and everything else steps back from it. A prefix and a unit
 * are set at half its size in the muted ink, so "$4.2k" and "142 ms" read as one figure
 * with a qualifier rather than three words; the label, the comparison and the description
 * are the panel's small text. Mono because a figure that changes should not move what
 * sits beside it, and DM Mono because it is DM Sans's own companion face.
 *
 * `compact` writes 4,212 as 4.2k, and `format="bytes"` scales to whichever unit the
 * number is best read in, which is why bytes are a format and not a unit. A figure still
 * too wide for its tile is shrunk to fit rather than clipped or wrapped: a stat card that
 * shows "12,48" is wrong in a way nobody catches.
 *
 * The comparison is worked out here from `previous` rather than handed in as a
 * percentage, so every tile agrees on what a change of 4.1% is and how it rounds. Which
 * way is good news is the caller's to say, because for a count of failures it is down.
 */
import { fitScale } from '../utils/fit'
import { type FigureFormat, formatFigure } from '../utils/format'
import { TREND_ICONS, TREND_TONES, trendOf } from '../utils/trend'

const props = withDefaults(
  defineProps<{
    label: string
    /** The figure. A string is written as given; null is a tile with nothing to show yet. */
    value: number | string | null
    /** Before the digits at half their size: a currency sign. */
    prefix?: string
    /** After the digits at half their size: `ms`, `users`. Bytes name their own and ignore it. */
    unit?: string
    format?: FigureFormat
    /** 4,212 as 4.2k. Bytes scale regardless. */
    compact?: boolean
    /** Most fraction digits written, for a value that has any. Trailing zeros never are. */
    decimals?: number
    /** The same figure one period ago. Given, the tile shows how far it moved. */
    previous?: number | null
    /** What `previous` is: "vs last week". */
    compare?: string
    /** Which direction is good news. Down, for anything that counts failures. */
    positive?: 'up' | 'down'
    /** A sentence behind the info glyph: what the figure counts, and what it does not. */
    info?: string
    /** A line under the figure. The slot of the same name takes one that carries markup. */
    description?: string
    loading?: boolean
  }>(),
  {
    prefix: '',
    unit: '',
    format: 'number',
    compact: false,
    decimals: 1,
    previous: null,
    compare: 'vs previous period',
    positive: 'up',
    info: '',
    description: '',
    loading: false
  }
)

const labelId = useId()

const figure = computed(() => {
  if (props.value === null) return null
  if (typeof props.value === 'string')
    return { digits: props.value, magnitude: '', unit: props.unit }
  const parts = formatFigure(props.value, {
    format: props.format,
    compact: props.compact,
    decimals: props.decimals
  })
  return { ...parts, unit: parts.unit || props.unit }
})

/* Shared with the metric card, so a tile and the card beside it agree on the arithmetic
   and on which way is good news. */
const trend = computed(() => {
  if (props.previous === null || typeof props.value !== 'number') return null
  return trendOf(props.value, props.previous, props.positive)
})

const row = useTemplateRef<HTMLElement>('row')
const run = useTemplateRef<HTMLElement>('run')
const aside = useTemplateRef<HTMLElement>('aside')
const scale = ref(1)

/**
 * Shrink the figure until it fits, by the rule in `fitScale`. The run's natural width is
 * its measured width over the scale it was measured at, so one pass lands on the answer.
 * Observed rather than computed once: the tile is a grid cell that follows the window,
 * and the mono face swaps in after first paint and changes the width of every glyph. The
 * tolerance keeps a sub-pixel rounding from ringing between two answers.
 */
function fit() {
  const container = row.value
  const content = run.value
  if (!container || !content) return

  const natural = content.getBoundingClientRect().width / scale.value
  const gap = Number.parseFloat(getComputedStyle(container).columnGap) || 0
  const beside = aside.value ? aside.value.getBoundingClientRect().width + gap : 0

  const next = fitScale(natural, container.clientWidth, beside)
  if (Math.abs(next - scale.value) > 0.005) scale.value = next
}

useResizeObserver([row, run, aside], fit)
</script>

<template>
  <v-card
    :aria-labelledby="labelId"
    border
    flat
    role="group"
  >
    <div class="flex flex-col p-4">
      <div class="flex min-w-0 items-center gap-1">
        <!-- `min-w-0` on the span as well as the row: a flex item will not shrink below
             its text without it, and `truncate` then has nothing to truncate. -->
        <span :id="labelId" class="min-w-0 truncate text-[0.8125rem] font-medium text-ink/60">
          {{ label }}
        </span>
        <button
          v-if="info"
          :aria-label="`About ${label}`"
          class="flex size-4 flex-none items-center justify-center rounded-full text-ink/40 transition-colors duration-150 hover:text-ink/80 focus-visible:text-ink/80 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary"
          type="button"
        >
          <Icon aria-hidden="true" class="text-sm" name="material-symbols:info-outline" />
          <v-tooltip
            :text="info"
            activator="parent"
            location="top"
            max-width="260"
            open-delay="200"
          />
        </button>
      </div>

      <div ref="row" class="mt-4 flex flex-wrap items-center gap-x-2.5 gap-y-1">
        <div class="statistic__well min-w-0">
          <span v-if="loading" aria-hidden="true" class="statistic__skeleton bg-ink/8" />
          <span v-else-if="figure === null" class="statistic__run font-mono text-ink/30">&ndash;</span>
          <span
            v-else
            ref="run"
            :style="{ '--statistic-scale': scale }"
            class="statistic__run font-mono text-ink"
          >
            <span v-if="prefix" class="statistic__affix text-ink/55">{{ prefix }}</span>
            <span>{{ figure.digits }}{{ figure.magnitude }}</span>
            <span v-if="figure.unit" class="statistic__affix text-ink/55">
              {{ figure.unit }}
            </span>
          </span>
        </div>

        <!-- Nothing where there was nothing to compare against. A flat arrow, a dash and
             "vs Aug 21" beside a figure of zero is three marks that together say only
             that the question could not be asked, and it reads as a fourth figure rather
             than as an absence. The description under the tile still says what the number
             is; what it stood at last month, when that was nothing, is not a fact worth
             the row. -->
        <p
          v-if="trend?.comparable && !loading"
          ref="aside"
          class="flex items-center gap-1.5 text-xs"
        >
          <span
            :class="TREND_TONES[trend.tone]"
            class="inline-flex items-center gap-0.5 font-mono font-medium tabular-nums"
          >
            <Icon :name="TREND_ICONS[trend.direction]" aria-hidden="true" class="text-sm" />
            {{ trend.text }}
          </span>
          <span class="text-ink/55">{{ compare }}</span>
        </p>
      </div>

      <p v-if="description || $slots.description" class="mt-0.5 text-[0.6875rem] text-ink/55">
        <slot name="description">
          {{ description }}
        </slot>
      </p>
    </div>
  </v-card>
</template>

<style scoped>
/*
 * The figure's size, and everything that scales with it in `em` so one number moves it
 * all. The well keeps the unscaled height whatever the run is drawn at, so a tile whose
 * figure had to shrink stays level with its neighbours, and a skeleton is the same
 * height as the figure it stands in for. Shrinkable so that a figure wider than the row
 * is given the row and not the row plus the overflow.
 */
.statistic__well {
  --statistic-size: 1.5rem;
  --statistic-leading: 1.25;

  align-items: center;
  display: flex;
  flex: 0 1 auto;
  min-height: calc(var(--statistic-size) * var(--statistic-leading));
  overflow: hidden;
}

/* Medium is DM Mono's heaviest weight; there is no bold to fall through to. The
   tracking is tightened a touch because a mono face at display size opens up. */
.statistic__run {
  --statistic-scale: 1;

  /*
   * The affixes centre on the figure rather than sitting on its baseline.
   *
   * They are drawn at half the size, so a shared baseline puts the currency mark down at
   * the foot of a 24px number with its whole height above — which reads as a symbol that
   * has slipped off the figure rather than one belonging to it. Centred, the mark sits in
   * the middle of the digits it qualifies, and the unit on the other end sits at the same
   * height as the mark on the front.
   */
  align-items: center;
  display: inline-flex;
  /* One gap for both ends rather than a margin on the unit and nothing on the mark. It is
     in `em`, so it holds its proportion as the figure shrinks to fit its row. */
  gap: 0.2em;
  font-size: calc(var(--statistic-size) * var(--statistic-scale));
  font-weight: 500;
  letter-spacing: -0.02em;
  line-height: var(--statistic-leading);
  white-space: nowrap;
}

.statistic__affix {
  font-size: 0.5em;
  letter-spacing: 0;
}

.statistic__skeleton {
  border-radius: var(--ps-radius-x-small);
  height: calc(var(--statistic-size) * 0.8);
  width: 5.5em;
}

@media (prefers-reduced-motion: no-preference) {
  .statistic__skeleton {
    animation: statistic-pulse 1.6s ease-in-out infinite;
  }
}

@keyframes statistic-pulse {
  50% {
    opacity: 0.45;
  }
}
</style>
