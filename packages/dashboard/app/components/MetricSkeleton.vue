<script setup lang="ts">
import type { MetricSkeletonShape } from '../utils/components'
/**
 * The metric card while it waits, drawn as bones in the places its parts will take: the
 * figure with its trend beside it, the view switch, the legend, and the view itself as a
 * tinted well holding three groups of rules.
 *
 * It does not draw a chart. A placeholder shaped like bars is a shape a reader tries to
 * read, and it is the wrong one either way, since the same card draws a line, an area, a
 * set of bars or a table and the switch that says which is hidden while it loads. The
 * well is what says something is coming and how tall it is; the groups inside give it a
 * rhythm, a heading and two lines each, so it reads as a page and not as a ruled box. The
 * groups are as many as fit (three, fewer in a short card) spread over the height, so what
 * gives between one card and the next is the air between them and never the rhythm inside one.
 *
 * Vuetify's loader is composed here rather than restyled: `types` registers the card's
 * own bones, and this file sizes the plain divs that come out. `boilerplate` is what
 * stops the sweep; the bones are still, and the card's `aria-busy` and the label on the
 * body are what say it is loading. The block at the bottom is unscoped for the reason
 * MetricChart's tooltip block is, that the markup belongs to a child component and a
 * scoped rule reaches its root and nothing inside it. The two colours are the panel's, in
 * theme.css, beside the reason Vuetify's own is unusable.
 */

const props = withDefaults(
  defineProps<{
    shape?: MetricSkeletonShape
    /** The view's height in pixels, the card's own. Only the body reads it. */
    height?: number
    /** What a screen reader is told is loading. Hidden from it entirely when empty. */
    label?: string
    /** The figure will have a trend beside it, so its bones are drawn too. */
    trend?: boolean
    /** How many keys the legend will hold. Read by the legend alone. */
    count?: number
  }>(),
  { shape: 'body', height: 256, label: '', trend: false, count: 2 }
)

/** The chart has six slots, so a legend never has more keys than that. */
const keys = computed(() => Math.min(Math.max(Math.round(props.count), 1), 6))

/**
 * How many groups the well holds: three where they fit, fewer in a short card. A group is
 * 54px (a heading, two lines and the gaps between), the well's padding 40px, and the air
 * between two groups at least 12px, so a 160px chart takes two and a 256px one takes three.
 * Three in a short well run past its bottom and out of the card.
 */
const groups = computed(() => Math.min(Math.max(Math.floor((props.height - 40 + 12) / 66), 1), 3))

/**
 * What each shape asks the loader for. A list or an `@n` here is laid out flat under the
 * loader's root, which is what lets the root's own gap and alignment reach the bones; a
 * single name would be wrapped in a box of that name first, with the root's gap between
 * it and nothing.
 */
const root = computed(() => {
  switch (props.shape) {
    case 'figure':
      return props.trend ? 'metric-run, metric-trend' : 'metric-run'
    case 'control':
      return 'metric-control'
    case 'legend':
      return `metric-key@${keys.value}`
    default:
      return `metric-group@${groups.value}`
  }
})

/**
 * The card's own bones, as Vuetify composes them: a name holding a list is a box around
 * what it names, `@n` repeats, and a name holding itself is a leaf. The last is what ends
 * the recursion; a leaf pointing anywhere else nests a second bone inside the first.
 */
const BONES = {
  'metric-run': 'metric-run',
  'metric-trend': 'metric-delta, metric-caption',
  'metric-delta': 'metric-delta',
  'metric-caption': 'metric-caption',
  'metric-control': 'metric-control',
  'metric-key': 'metric-swatch, metric-name',
  'metric-swatch': 'metric-swatch',
  'metric-name': 'metric-name',
  'metric-group': 'metric-heading, metric-line@2',
  'metric-heading': 'metric-heading',
  'metric-line': 'metric-line'
}
</script>

<template>
  <v-skeleton-loader
    v-bind="label ? { 'aria-label': label, 'aria-live': 'polite', 'role': 'status' } : { 'aria-hidden': 'true' }"
    :class="['metric-skeleton', {
      'metric-skeleton--figure': shape === 'figure',
      'metric-skeleton--control': shape === 'control',
      'metric-skeleton--legend sm:justify-end': shape === 'legend',
      'metric-skeleton--body': shape === 'body'
    }]"
    :style="{ '--metric-skeleton-height': `${height}px` }"
    :type="root"
    :types="BONES"
    boilerplate
  />
</template>

<style>
/*
 * Everything below is under `.metric-skeleton`, the class this component puts on the
 * loader's root, so none of it reaches a skeleton drawn anywhere else.
 *
 * Vuetify gives every bone `flex: 1 1 100%`, wrapping, and a radius inherited from its
 * parent, so each one used here says its own size and shape. A box that holds other
 * bones is squared off and told not to wrap; a leaf takes the fill.
 */
.metric-skeleton {
  background: transparent;
}

.metric-skeleton .v-skeleton-loader__bone {
  border-radius: 0;
  flex: 0 0 auto;
  flex-wrap: nowrap;
}

.metric-skeleton .v-skeleton-loader__metric-run,
.metric-skeleton .v-skeleton-loader__metric-delta,
.metric-skeleton .v-skeleton-loader__metric-caption,
.metric-skeleton .v-skeleton-loader__metric-control,
.metric-skeleton .v-skeleton-loader__metric-swatch,
.metric-skeleton .v-skeleton-loader__metric-name,
.metric-skeleton .v-skeleton-loader__metric-heading,
.metric-skeleton .v-skeleton-loader__metric-line {
  background: var(--ps-skeleton-bone);
}

/*
 * The figure. The box is `.metric-card__figure`'s own, 1.75rem at 1.15 leading, and the
 * bones sit inside it the way ink sits on a line: everything is pinned to the bottom and
 * lifted by its own margin, so the run covers the digits' height and the trend beside it
 * sits where a line of small text on the same baseline would.
 */
.metric-skeleton--figure {
  align-items: flex-end;
  box-sizing: border-box;
  column-gap: 0.625rem;
  flex-wrap: nowrap;
  height: calc(1.75rem * 1.15);
}

.metric-skeleton .v-skeleton-loader__metric-run {
  border-radius: var(--ps-radius-x-small);
  height: 1.375rem;
  margin-bottom: 0.3125rem;
  width: 7rem;
}

.metric-skeleton .v-skeleton-loader__metric-trend {
  align-items: center;
  column-gap: 0.375rem;
  margin-bottom: 0.4375rem;
}

/* The change and the words after it, so the pair reads as a number and its caption. */
.metric-skeleton .v-skeleton-loader__metric-delta,
.metric-skeleton .v-skeleton-loader__metric-caption {
  border-radius: 999px;
  height: 0.75rem;
}

.metric-skeleton .v-skeleton-loader__metric-delta {
  width: 2.75rem;
}

.metric-skeleton .v-skeleton-loader__metric-caption {
  width: 6.5rem;
}

/* The view switch: two icon segments in a small control, which is what SegmentedControl
   measures. The page's own actions get no bone, because their width is theirs. */
.metric-skeleton--control {
  flex-wrap: nowrap;
}

.metric-skeleton .v-skeleton-loader__metric-control {
  border-radius: var(--ps-radius-x-small);
  height: 2.25rem;
  width: 4.375rem;
}

/*
 * The legend, at the legend's own measures: keys a small control high, a swatch and a
 * name in each, the row wrapping and set to the right on wider screens the way the
 * real one is. The names are ragged by position and the same on every load.
 */
.metric-skeleton--legend {
  column-gap: 1rem;
  row-gap: 0.25rem;
}

.metric-skeleton .v-skeleton-loader__metric-key {
  align-items: center;
  column-gap: 0.375rem;
  height: 1.5rem;
}

.metric-skeleton .v-skeleton-loader__metric-swatch {
  border-radius: 3px;
  height: 0.625rem;
  width: 0.625rem;
}

.metric-skeleton .v-skeleton-loader__metric-name {
  border-radius: 999px;
  height: 0.625rem;
  width: 3.75rem;
}

.metric-skeleton .v-skeleton-loader__metric-key:nth-child(2) .v-skeleton-loader__metric-name {
  width: 3rem;
}

.metric-skeleton .v-skeleton-loader__metric-key:nth-child(3) .v-skeleton-loader__metric-name {
  width: 4.25rem;
}

.metric-skeleton .v-skeleton-loader__metric-key:nth-child(4) .v-skeleton-loader__metric-name {
  width: 3.5rem;
}

.metric-skeleton .v-skeleton-loader__metric-key:nth-child(5) .v-skeleton-loader__metric-name {
  width: 2.75rem;
}

.metric-skeleton .v-skeleton-loader__metric-key:nth-child(6) .v-skeleton-loader__metric-name {
  width: 4rem;
}

/*
 * The body: a well the height the chart or the table will take, tinted a step under the
 * bones so they read as ink on a surface and not as marks floating in the card. Its
 * radius is the empty state's, so the two states of the same slot share an outline.
 */
.metric-skeleton--body {
  background: var(--ps-skeleton-well);
  border-radius: var(--ps-radius-small);
  box-sizing: border-box;
  height: var(--metric-skeleton-height);
  overflow: hidden;
  padding: 1.25rem 1.5rem;
}

/* Spread rather than spaced: the groups are a fixed count and the height is the card's,
   so what gives between one card and the next is the air between them. Stretched because
   the loader centres its children across the axis it does not run on, which in a column
   is their width. */
.metric-skeleton--body,
.metric-skeleton .v-skeleton-loader__metric-group {
  align-items: stretch;
  flex-direction: column;
  flex-wrap: nowrap;
}

.metric-skeleton--body {
  justify-content: space-between;
}

/* A heading and two lines, set close so the three read as one thing. */
.metric-skeleton .v-skeleton-loader__metric-group {
  row-gap: 0.625rem;
}

.metric-skeleton .v-skeleton-loader__metric-heading {
  border-radius: 999px;
  height: 0.875rem;
  width: 24%;
}

.metric-skeleton .v-skeleton-loader__metric-line {
  border-radius: 999px;
  height: 0.625rem;
}

/* Ragged by position, and the same raggedness on every load: a set of headings all one
   length reads as a table's rows, and one that is different each time is one a reader
   looks at twice. The first line of a group runs full and the second stops short, the
   way a paragraph ends. */
.metric-skeleton .v-skeleton-loader__metric-group:nth-child(2) .v-skeleton-loader__metric-heading {
  width: 32%;
}

.metric-skeleton .v-skeleton-loader__metric-group:nth-child(3) .v-skeleton-loader__metric-heading {
  width: 18%;
}

.metric-skeleton .v-skeleton-loader__metric-group:nth-child(1) .v-skeleton-loader__metric-line:last-child {
  width: 76%;
}

.metric-skeleton .v-skeleton-loader__metric-group:nth-child(2) .v-skeleton-loader__metric-line:last-child {
  width: 88%;
}

.metric-skeleton .v-skeleton-loader__metric-group:nth-child(3) .v-skeleton-loader__metric-line:last-child {
  width: 58%;
}
</style>
