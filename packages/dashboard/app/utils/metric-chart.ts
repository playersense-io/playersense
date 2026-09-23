/**
 * The ECharts option for a metric card, built from the card's series and the theme's
 * colours and nothing else.
 *
 * Pure and outside the component so the decisions that make a chart ours are testable
 * without a canvas: which gridlines are dashed, what an axis tick says, what the tooltip
 * lists and in which order. The component's job is reduced to reading the theme and
 * handing the answer to ECharts.
 *
 * A chart draws to a canvas, outside every stylesheet, so nothing here inherits: the
 * faces are named, the ink is a colour string, and the opacities are numbers ECharts is
 * given rather than classes it could read.
 */
import type { TooltipComponentFormatterCallbackParams } from 'echarts'
import type { BarSeriesOption, LineSeriesOption } from 'echarts/charts'
import { type WrittenFigureOptions, writeFigure } from './format'

export type MetricChartType = 'line' | 'area' | 'bar'

/**
 * A palette slot by number, or the comparison grey for a series that is there to be
 * measured against rather than read: last period beside this one.
 */
export type MetricTone = 1 | 2 | 3 | 4 | 5 | 6 | 'muted'

export interface MetricSeries {
  name: string
  /** One value per category. Null is a gap: the line breaks and the bar is absent. */
  data: readonly (number | null)[]
  /** How this series is drawn. The chart's own type when absent. */
  type?: MetricChartType
  /**
   * Which colour. The series' position in the list when absent, so a series keeps its
   * colour when the one before it is hidden: colour follows the metric, never its rank.
   */
  tone?: MetricTone
  /** Bars sharing a name pile up. Lines and areas ignore it. */
  stack?: string
  /** Drawn dashed: the previous period beside the current one. */
  dashed?: boolean
}

export interface MetricChartInput {
  /** The tick under each point. */
  categories: readonly string[]
  /** What the tooltip calls each category; the tick when absent. */
  titles?: readonly string[]
  series: readonly MetricSeries[]
  type?: MetricChartType
  /** Series names switched off in the legend. */
  hidden?: readonly string[]
  /** How a value is written, on the axis, in the tooltip and in the table. */
  format?: WrittenFigureOptions
  /** Off under `prefers-reduced-motion`. */
  animate?: boolean
}

export interface MetricPalette {
  /** The theme's on-surface, as hex. Axis text, gridlines and the comparison grey are tints of it. */
  ink: string
  /** The card's surface, as hex: the ring around a marker and the gap between stacked bars. */
  surface: string
  /** The six categorical slots, in order. */
  series: readonly string[]
}

/** The theme keys the six slots are read from, in the order they are assigned. */
export const CHART_SLOTS = [
  'chart-1',
  'chart-2',
  'chart-3',
  'chart-4',
  'chart-5',
  'chart-6'
] as const

/* The panel's faces, named the way the canvas needs them. `theme.css` loads both. */
export const SANS = '"DM Sans Variable", ui-sans-serif, system-ui, sans-serif'
export const MONO = '"DM Mono", ui-monospace, "SF Mono", Menlo, Consolas, monospace'

/** The comparison grey: ink at a third, so it sits behind every real series. */
export const MUTED_ALPHA = 0.32

/* The gridlines and the axis text, as opacities of the ink so both themes get them
   from one number each. Vertical rules are dashed and a touch stronger, because a dash
   covers half its length and reads lighter than a solid line of the same colour. */
const GRID_HORIZONTAL_ALPHA = 0.07
const GRID_VERTICAL_ALPHA = 0.1
const AXIS_LABEL_ALPHA = 0.5
const BASELINE_ALPHA = 0.12

/** The ECharts tooltip element carries this class; the styles are in MetricChart.vue. */
export const TOOLTIP_CLASS = 'metric-tooltip'

/**
 * A colour at a given opacity, as the `rgba()` string a canvas takes. Hex in three, four,
 * six or eight digits, or an `rgb()`/`rgba()` whose alpha is replaced. Anything else is
 * handed back untouched rather than turned into an invisible mark.
 */
export function alpha(color: string, opacity: number): string {
  const functional = /^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/.exec(color)
  if (functional) return `rgba(${functional[1]}, ${functional[2]}, ${functional[3]}, ${opacity})`

  const raw = color.trim().replace(/^#/, '')
  if (!/^[0-9a-f]{3,8}$/i.test(raw) || raw.length === 5 || raw.length === 7) return color
  const wide = raw.length <= 4 ? [...raw].map(digit => digit + digit).join('') : raw
  const r = Number.parseInt(wide.slice(0, 2), 16)
  const g = Number.parseInt(wide.slice(2, 4), 16)
  const b = Number.parseInt(wide.slice(4, 6), 16)
  return `rgba(${r}, ${g}, ${b}, ${opacity})`
}

export function colorOf(series: MetricSeries, index: number, palette: MetricPalette): string {
  const tone = series.tone ?? index + 1
  if (tone === 'muted') return alpha(palette.ink, MUTED_ALPHA)
  return palette.series[tone - 1] ?? palette.ink
}

/** Text from outside goes into markup here, and a series name is text from outside. */
export function escapeHtml(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll('\'', '&#39;')
}

/**
 * The tooltip for one category, as markup ECharts places beside the pointer. Built
 * from the input rather than from the callback's parameters, because those omit a
 * series whose value at that point is null and the readout should still list it, with
 * a dash, so the reader knows it was not forgotten.
 *
 * One readout, every series, in the legend's order; the value is the loud part and the
 * name steps back, which is the legend's hierarchy inverted, because here the reader has
 * the series and wants the number. Stacked bars end with their total, since a stack's
 * height is the one figure the segments do not say.
 */
export function renderTooltip(
  index: number,
  input: MetricChartInput,
  palette: MetricPalette
): string {
  const hidden = new Set(input.hidden ?? [])
  const title = input.titles?.[index] ?? input.categories[index] ?? ''
  const rows: string[] = []
  let stacked = 0
  let total = 0

  input.series.forEach((series, position) => {
    if (hidden.has(series.name)) return
    const kind = series.type ?? input.type ?? 'line'
    const value = series.data[index] ?? null
    const written = value === null ? '–' : writeFigure(value, input.format)
    const key
      = kind === 'line'
        ? `${TOOLTIP_CLASS}__key--line${series.dashed ? ` ${TOOLTIP_CLASS}__key--dashed` : ''}`
        : `${TOOLTIP_CLASS}__key--fill`

    rows.push(
      `<li class="${TOOLTIP_CLASS}__row">`
      + `<span class="${TOOLTIP_CLASS}__key ${key}" style="--metric-key: ${escapeHtml(colorOf(series, position, palette))}"></span>`
      + `<span class="${TOOLTIP_CLASS}__name">${escapeHtml(series.name)}</span>`
      + `<span class="${TOOLTIP_CLASS}__value">${escapeHtml(written)}</span>`
      + '</li>'
    )

    if (kind === 'bar' && series.stack !== undefined) {
      stacked += 1
      total += value ?? 0
    }
  })

  const footer
    = stacked > 1
      ? `<div class="${TOOLTIP_CLASS}__total">`
      + `<span class="${TOOLTIP_CLASS}__name">Total</span>`
      + `<span class="${TOOLTIP_CLASS}__value">${escapeHtml(writeFigure(total, input.format))}</span>`
      + '</div>'
      : ''

  return (
    `<div class="${TOOLTIP_CLASS}__card">`
    + `<div class="${TOOLTIP_CLASS}__title">${escapeHtml(title)}</div>`
    + `<ul class="${TOOLTIP_CLASS}__rows">${rows.join('')}</ul>`
    + footer
    + '</div>'
  )
}

function pointIndex(params: TooltipComponentFormatterCallbackParams): number {
  const first = Array.isArray(params) ? params[0] : params
  return first?.dataIndex ?? 0
}

/** What every series carries whatever its shape. A muted one sits under the rest. */
function commonSeries(entry: MetricSeries, color: string) {
  return { name: entry.name, data: [...entry.data], color, z: entry.tone === 'muted' ? 1 : 2 }
}

/**
 * Thin bars, rounded at the data end and square at the baseline. Only the top segment
 * of a stack is rounded: a rounded segment under another would leave a notch at the
 * join. Segments are parted by a hairline of the surface, which is a gap and not a
 * stroke; two neighbours a step apart read as two because of it.
 */
function barSeries(
  entry: MetricSeries,
  color: string,
  palette: MetricPalette,
  topOfStack: boolean
): BarSeriesOption {
  const stacked = entry.stack !== undefined
  return {
    ...commonSeries(entry, color),
    type: 'bar',
    ...(entry.stack === undefined ? {} : { stack: entry.stack }),
    barMaxWidth: 24,
    barCategoryGap: '40%',
    barGap: '15%',
    itemStyle: {
      color,
      borderRadius: !stacked || topOfStack ? [4, 4, 0, 0] : 0,
      borderColor: palette.surface,
      borderWidth: stacked ? 1 : 0
    },
    emphasis: { focus: 'none' }
  }
}

/**
 * A two-pixel line with round joins, eased through its points but never past them.
 * Symbols show only under the pointer, ringed in the surface so one stays legible where
 * it crosses another line. An area is the same line over a wash of its own colour that
 * fades to nothing at the baseline.
 */
function lineSeries(
  entry: MetricSeries,
  color: string,
  palette: MetricPalette,
  filled: boolean
): LineSeriesOption {
  return {
    ...commonSeries(entry, color),
    type: 'line',
    smooth: 0.3,
    smoothMonotone: 'x',
    connectNulls: false,
    showSymbol: false,
    symbol: 'circle',
    symbolSize: 8,
    lineStyle: { width: 2, cap: 'round', join: 'round', type: entry.dashed ? [4, 4] : 'solid' },
    itemStyle: { color, borderColor: palette.surface, borderWidth: 2 },
    emphasis: { focus: 'none', scale: 1.25 },
    ...(filled
      ? {
          areaStyle: {
            color: {
              type: 'linear',
              x: 0,
              y: 0,
              x2: 0,
              y2: 1,
              colorStops: [
                { offset: 0, color: alpha(color, 0.18) },
                { offset: 1, color: alpha(color, 0) }
              ]
            }
          }
        }
      : {})
  }
}

export function buildMetricOption(input: MetricChartInput, palette: MetricPalette): ECOption {
  const type = input.type ?? 'line'
  const hidden = new Set(input.hidden ?? [])
  const kinds = input.series.map(series => series.type ?? type)
  const hasBars = kinds.includes('bar')
  const format = input.format ?? {}

  // The last series in each stack is the one on top.
  const topOfStack = new Map<string, number>()
  input.series.forEach((series, index) => {
    if (kinds[index] === 'bar' && series.stack !== undefined) topOfStack.set(series.stack, index)
  })

  const series = input.series.map((entry, index): LineSeriesOption | BarSeriesOption => {
    const kind = kinds[index] ?? type
    const color = colorOf(entry, index, palette)
    return kind === 'bar'
      ? barSeries(entry, color, palette, topOfStack.get(entry.stack ?? '') === index)
      : lineSeries(entry, color, palette, kind === 'area')
  })

  return {
    animation: input.animate ?? true,
    animationDuration: 400,
    animationDurationUpdate: 300,
    animationEasing: 'cubicOut',
    animationEasingUpdate: 'cubicOut',
    textStyle: { fontFamily: SANS },

    // Tight to the card: the axis labels are kept inside these bounds by ECharts 6's
    // own `outerBoundsMode`, so nothing here reserves room for them.
    grid: { top: 8, right: 8, bottom: 0, left: 0 },

    // Hidden, because the legend the reader sees is HTML in the card, set in the
    // panel's own type; this one only holds which series are switched off.
    legend: {
      show: false,
      selected: Object.fromEntries(
        input.series.map(entry => [entry.name, !hidden.has(entry.name)])
      )
    },

    tooltip: {
      trigger: 'axis',
      renderMode: 'html',
      className: TOOLTIP_CLASS,
      // ECharts draws no box of its own; the card inside the markup carries the look.
      backgroundColor: 'transparent',
      borderWidth: 0,
      padding: 0,
      extraCssText: 'box-shadow: none;',
      transitionDuration: 0.15,
      confine: true,
      axisPointer: hasBars
        ? { type: 'shadow', shadowStyle: { color: alpha(palette.ink, 0.04) } }
        : {
            type: 'line',
            z: 0,
            lineStyle: { color: alpha(palette.ink, 0.2), width: 1, type: 'solid' }
          },
      formatter: params => renderTooltip(pointIndex(params), input, palette)
    },

    xAxis: {
      type: 'category',
      data: [...input.categories],
      // Bars need a slot to stand in; a line starts on the axis's first tick.
      boundaryGap: hasBars,
      axisLine: { show: true, lineStyle: { color: alpha(palette.ink, BASELINE_ALPHA), width: 1 } },
      axisTick: { show: false },
      axisLabel: {
        color: alpha(palette.ink, AXIS_LABEL_ALPHA),
        fontFamily: MONO,
        fontSize: 11,
        margin: 12,
        hideOverlap: true
      },
      // Vertical rules are dashed, horizontal ones solid: the eye follows a solid line
      // to read a value off the axis, and a dashed one only marks where a tick is.
      splitLine: {
        show: true,
        lineStyle: { color: alpha(palette.ink, GRID_VERTICAL_ALPHA), width: 1, type: [4, 4] }
      },
      axisPointer: { label: { show: false } }
    },

    yAxis: {
      type: 'value',
      splitNumber: 4,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: {
        color: alpha(palette.ink, AXIS_LABEL_ALPHA),
        fontFamily: MONO,
        fontSize: 11,
        margin: 12,
        formatter: (value: number) => writeFigure(value, { ...format, compact: true })
      },
      splitLine: {
        show: true,
        lineStyle: { color: alpha(palette.ink, GRID_HORIZONTAL_ALPHA), width: 1, type: 'solid' }
      },
      axisPointer: { show: false }
    },

    series
  }
}
