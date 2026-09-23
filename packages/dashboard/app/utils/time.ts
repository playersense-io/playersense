/**
 * Dates and durations as the panel writes them, through dayjs. Fixed to English like
 * the figures in `format.ts`, and for the same reason: a table of timestamps is read
 * against itself, and one that changed shape with the reader's locale would sort
 * visually one way and actually another.
 *
 * Relative time comes in two voices. The compact one is for columns, where "4m ago"
 * has to fit beside a date and every row must be the same width; it is dayjs's own
 * arithmetic under a locale that only shortens the words. The other is dayjs's plain
 * English, "4 minutes ago", for a sentence.
 */
import dayjs from 'dayjs'
import en from 'dayjs/locale/en'
import relativeTimePlugin from 'dayjs/plugin/relativeTime'

dayjs.extend(relativeTimePlugin)

/**
 * The compact voice. Under a minute is "just now" rather than "0m ago", which is what
 * the two functions are for: dayjs hands them the unit and they decide the frame.
 */
const COMPACT_LOCALE = 'en-compact'

/* dayjs runs `future` and `past` as functions when given them (the plugin checks for one
   before it substitutes `%s`), which is what lets "now" escape the "ago" frame; only its
   typings still say string, hence the cast. The tests hold both frames. */
const COMPACT_RELATIVE = {
  future: (span: string) => (span === 'now' ? 'now' : `in ${span}`),
  past: (span: string) => (span === 'now' ? 'just now' : `${span} ago`)
} as unknown as { future: string, past: string }

dayjs.locale(
  COMPACT_LOCALE,
  {
    ...en,
    name: COMPACT_LOCALE,
    relativeTime: {
      ...COMPACT_RELATIVE,
      s: 'now',
      m: '1m',
      mm: '%dm',
      h: '1h',
      hh: '%dh',
      d: '1d',
      dd: '%dd',
      M: '1mo',
      MM: '%dmo',
      y: '1y',
      yy: '%dy'
    }
  },
  true
)

/** "Sep 9, 2026, 14:03". Twenty-four hour, because a log is read against a clock. */
export function formatDateTime(iso: string): string {
  return dayjs(iso).format('MMM D, YYYY, HH:mm')
}

/** "Sep 9, 2026". */
export function formatDate(iso: string): string {
  return dayjs(iso).format('MMM D, YYYY')
}

/** For a column: "4m ago", "3d ago", "in 2mo", "just now". */
export function relativeTime(iso: string, now: Date): string {
  return dayjs(iso).locale(COMPACT_LOCALE).from(dayjs(now))
}

/** For a sentence: "4 minutes ago", "3 days ago", "in 2 months", "a few seconds ago". */
export function relativeTimeWords(iso: string, now: Date): string {
  return dayjs(iso).from(dayjs(now))
}

/** Whole days between two instants, floored, for "banned for 7 days" arithmetic. */
export function daysBetween(from: Date, to: Date): number {
  return dayjs(to).diff(dayjs(from), 'day')
}

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
const WEEK = 7 * DAY
const MONTH = 30 * DAY
const YEAR = 365 * DAY

export const DURATIONS = { MINUTE, HOUR, DAY, WEEK, MONTH, YEAR } as const
