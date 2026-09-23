/**
 * The small lines under a figure: a real one drawn from a series, and the stand-in drawn
 * when there is no series to draw. Both are SVG path data from pure functions, so a tile
 * can inline them without a chart library and a test can hold them still.
 *
 * The stand-in is a seeded random walk rather than a flat line, because a flat line
 * says "zero" where the truth is "nothing recorded yet", and a wave that is faint and
 * unlabelled says "a line will go here". It is seeded so a tile draws the same wave on
 * every visit and two tiles side by side draw different ones.
 */

/** The line through `values`, scaled to the box, with `pad` kept clear above and below. */
export function sparklinePath(
  values: readonly number[],
  width: number,
  height: number,
  pad = 2
): string {
  if (values.length === 0) return ''
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const step = values.length > 1 ? width / (values.length - 1) : 0
  return values
    .map((value, index) => {
      const x = values.length > 1 ? index * step : width / 2
      const y = height - pad - ((value - min) / span) * (height - pad * 2)
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
    })
    .join(' ')
}

/** The line closed down to the base, for the fill under it. */
export function areaPath(line: string, width: number, height: number): string {
  if (line === '') return ''
  return `${line} L${width},${height} L0,${height} Z`
}

/** mulberry32: a small seeded generator, so a placeholder holds still between visits. */
function seeded(seed: number): () => number {
  let state = seed >>> 0
  return () => {
    state = (state + 0x6d2b79f5) >>> 0
    let t = state
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

/**
 * A gentle wave across the box: sixty steps of a walk that is pulled back towards the
 * middle, so it wanders without leaving the band it starts in.
 */
export function noDataValues(seed: number, count = 60): number[] {
  const random = seeded(seed)
  const values: number[] = []
  let level = 0.5
  let drift = 0
  for (let i = 0; i < count; i += 1) {
    drift = drift * 0.7 + (random() - 0.5) * 0.08 + (0.5 - level) * 0.05
    level = Math.min(0.9, Math.max(0.1, level + drift))
    values.push(level)
  }
  return values
}

export function noDataPath(seed: number, width: number, height: number): string {
  return sparklinePath(noDataValues(seed), width, height, height * 0.25)
}
