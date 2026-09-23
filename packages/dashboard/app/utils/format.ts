/**
 * How a figure is written on a statistic tile, and nothing else about it.
 *
 * Pure functions over `Intl.NumberFormat`, so the tile's arithmetic is tested without a
 * DOM. Fixed to English: the panel has one language, and the compact suffixes below are
 * the English ones lowercased. A locale would move the decimal mark and change the
 * suffixes at once, which is a decision for the whole panel rather than this file.
 */

const LOCALE = 'en'

/** The pieces a figure is drawn from, so each can be set at its own size and weight. */
export interface FigureParts {
  /** The digits, grouped, carrying the sign when the value is negative. */
  digits: string
  /** What follows the digits in the same run: `k`, `m`, or nothing. */
  magnitude: string
  /** A unit the format derived, such as `kB`. Empty when the caller's own unit stands. */
  unit: string
}

/**
 * `bytes` is a format rather than a unit because the unit is an output: 1,024 of them
 * is "1 kB" and 1,048,576 is "1 MB", and the caller cannot know which before the number
 * arrives. `money` always writes two fraction digits, because "$31.4" reads as a typo on
 * a bill; the currency sign is the caller's prefix.
 */
export type FigureFormat = 'number' | 'bytes' | 'money'

export interface FigureOptions {
  format?: FigureFormat
  /** 4,212 as 4.2k. Bytes scale regardless. */
  compact?: boolean
  /** Most fraction digits written. Trailing zeros never are. */
  decimals?: number
}

/** What a figure that cannot be written shows: a dash, never `NaN` in the mono face. */
export const NO_FIGURE: FigureParts = { digits: '–', magnitude: '', unit: '' }

/**
 * `Intl` writes 4,212 compactly as "4.2K" and the tile wants "4.2k", so the suffix is
 * read back as a part and mapped rather than lowercased off the string. Rounding across
 * a boundary is `Intl`'s to handle: 999,950 comes back as "1M", not "1000K".
 */
const COMPACT_SUFFIXES: Record<string, string> = { K: 'k', M: 'm', B: 'b', T: 't' }

/** Base 1024, labelled the way an operating system labels it: nobody reads "KiB". */
const BYTE_UNITS = ['B', 'kB', 'MB', 'GB', 'TB', 'PB']
const BYTE_STEP = 1024

export function formatFigure(value: number, options: FigureOptions = {}): FigureParts {
  const { format = 'number', compact = false, decimals = 1 } = options
  if (!Number.isFinite(value)) return NO_FIGURE
  if (format === 'bytes') return formatBytes(value, decimals)
  if (format === 'money') return formatMoney(value)
  if (compact) return formatCompact(value)
  const digits = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: decimals }).format(value)
  return { digits, magnitude: '', unit: '' }
}

/** Never compacted: "$1.2k" is not a figure anybody reconciles against an invoice. */
function formatMoney(value: number): FigureParts {
  const digits = new Intl.NumberFormat(LOCALE, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  }).format(value)
  return { digits, magnitude: '', unit: '' }
}

function formatCompact(value: number): FigureParts {
  const parts = new Intl.NumberFormat(LOCALE, {
    notation: 'compact',
    maximumFractionDigits: 1
  }).formatToParts(value)

  let digits = ''
  let magnitude = ''
  for (const part of parts) {
    if (part.type === 'compact') magnitude += COMPACT_SUFFIXES[part.value] ?? part.value
    else digits += part.value
  }
  return { digits: digits.trim(), magnitude, unit: '' }
}

function formatBytes(value: number, decimals: number): FigureParts {
  const sign = value < 0 ? '-' : ''
  const numbers = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: decimals })

  let scaled = Math.abs(value)
  let tier = 0
  while (scaled >= BYTE_STEP && tier < BYTE_UNITS.length - 1) {
    scaled /= BYTE_STEP
    tier += 1
  }

  // Rounding can carry a value back over the boundary the loop just kept it under:
  // 1,048,575 bytes is 1,023.999 kB and, to one decimal, "1,024 kB". Stepped once more
  // after the fact rather than with a tolerance in the loop, because the tolerance
  // would depend on `decimals`.
  if (
    Number(numbers.format(scaled).replaceAll(',', '')) >= BYTE_STEP
    && tier < BYTE_UNITS.length - 1
  ) {
    scaled /= BYTE_STEP
    tier += 1
  }

  return { digits: sign + numbers.format(scaled), magnitude: '', unit: BYTE_UNITS[tier] ?? 'B' }
}

export interface WrittenFigureOptions extends FigureOptions {
  /** Before the digits: a currency sign. */
  prefix?: string
  /** After them: `ms`, `users`. Bytes name their own and ignore it. */
  unit?: string
}

/**
 * The parts joined into one string, for the places a figure is text rather than markup:
 * an axis tick, a tooltip row, a table cell. A unit is set off by a space, except the
 * percent sign, which English writes closed up. A value that cannot be written is the
 * dash alone, with no unit or sign hung on it.
 */
export function writeFigure(value: number, options: WrittenFigureOptions = {}): string {
  const { prefix = '', unit = '', ...figure } = options
  const parts = formatFigure(value, figure)
  if (parts === NO_FIGURE) return parts.digits

  const suffix = parts.unit || unit
  const gap = suffix === '' || suffix === '%' ? '' : ' '
  return `${prefix}${parts.digits}${parts.magnitude}${gap}${suffix}`
}

/**
 * Movement from `previous` to `current` as a fraction of `previous`, so 0.041 is a rise
 * of 4.1%. Null when there is nothing to compare against: a previous period of zero has
 * no share to move by, and a tile that said "+∞%" would be right and useless.
 */
export function relativeChange(current: number, previous: number): number | null {
  if (!Number.isFinite(current) || !Number.isFinite(previous) || previous === 0) return null
  return (current - previous) / Math.abs(previous)
}

/** "+4.1%", "-12.4%", "0%". The sign is the part a glance reads, so it is always written. */
export function formatChange(change: number): string {
  return new Intl.NumberFormat(LOCALE, {
    style: 'percent',
    signDisplay: 'exceptZero',
    maximumFractionDigits: 1
  }).format(change)
}
