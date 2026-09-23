import { CHART_SLOTS, type MetricPalette } from '../utils/metric-chart'

/**
 * The colours a chart and its legend draw with, read off the theme.
 *
 * Read rather than declared, because a canvas inherits nothing from a stylesheet and
 * the legend beside it has to match the canvas to the pixel. Recomputed rather than read
 * once: `current` is reactive and the system theme can flip while the page is open.
 *
 * A theme colour is a string in every theme we define, but Vuetify 4 also accepts RGB
 * and HSV objects, and ECharts takes neither. A slot that is not a string, or is not
 * there, draws in the ink: visibly wrong, which beats coercing an object into
 * "[object Object]" and drawing nothing.
 */
export function useMetricPalette() {
  const theme = useTheme()

  return computed<MetricPalette>(() => {
    const colors = theme.current.value.colors
    const read = (name: string, fallback: string) => {
      const color = colors[name]
      return typeof color === 'string' ? color : fallback
    }

    const ink = read('on-surface', '#000000')
    return {
      ink,
      surface: read('surface', '#ffffff'),
      series: CHART_SLOTS.map(slot => read(slot, ink))
    }
  })
}
