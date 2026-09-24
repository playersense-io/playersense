// Cron, the five-field kind a scheduler reads: minute, hour, day of the month, month, day of
// the week. Parsed into the sets of values each field allows, written out in words, and run
// forward to the next times it fires in a timezone, which is how a report's schedule is shown
// and checked before anything is sent.
//
// Each field takes `*`, a number, a list (1,15), a range (1-5) and a step (*/15, 1-30/5), as
// the Go scheduler the worker uses does. Day names and month names are not taken: numbers
// only, which is what the presets write and what a copied crontab line nearly always holds.

export interface CronFields {
  minutes: number[]
  hours: number[]
  days: number[]
  months: number[]
  weekdays: number[]
  /** Both day fields were restricted: a day matches when either does, as in every cron. */
  dayOr: boolean
}

const RANGES: [number, number][] = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 6]]
const FIELD_NAMES = ['minute', 'hour', 'day of the month', 'month', 'day of the week']

/** The fields, or why the expression is not one. */
export function parseCron(expression: string): CronFields | string {
  const parts = expression.trim().split(/\s+/)
  if (parts.length !== 5) return 'Five fields: minute, hour, day of the month, month, day of the week.'
  const sets: number[][] = []
  for (const [index, part] of parts.entries()) {
    const [low, high] = RANGES[index]!
    const values = new Set<number>()
    for (const piece of part.split(',')) {
      const match = /^(\*|\d+)(?:-(\d+))?(?:\/(\d+))?$/.exec(piece)
      if (!match) return `The ${FIELD_NAMES[index]} field, "${part}", is not one cron reads.`
      const step = match[3] ? Number(match[3]) : 1
      let start = match[1] === '*' ? low : Number(match[1])
      let end = match[1] === '*' ? high : match[2] ? Number(match[2]) : match[3] ? high : start
      /* Sunday is 0, and 7 too. */
      if (index === 4) {
        if (start === 7) start = 0
        if (end === 7) end = 6
      }
      if (start < low || end > high || start > end || step < 1) return `The ${FIELD_NAMES[index]} field runs from ${low} to ${high}.`
      for (let value = start; value <= end; value += step) values.add(value)
    }
    sets.push([...values].sort((a, b) => a - b))
  }
  return {
    minutes: sets[0]!,
    hours: sets[1]!,
    days: sets[2]!,
    months: sets[3]!,
    weekdays: sets[4]!,
    dayOr: parts[2] !== '*' && parts[4] !== '*'
  }
}

/** The presets a schedule is picked from, each a cron line and what it says. */
export const CRON_PRESETS = [
  { value: '0 * * * *', label: 'Every hour' },
  { value: '0 9 * * *', label: 'Every day at 09:00' },
  { value: '0 9 * * 1-5', label: 'Every weekday at 09:00' },
  { value: '0 9 * * 1', label: 'Every Monday at 09:00' },
  { value: '0 9 1 * *', label: 'On the 1st of every month at 09:00' }
] as const

export const CRON_TIMEZONES = [
  { value: 'UTC', label: 'UTC' },
  { value: 'Europe/Istanbul', label: 'Istanbul' },
  { value: 'Europe/London', label: 'London' },
  { value: 'Europe/Berlin', label: 'Berlin' },
  { value: 'America/New_York', label: 'New York' },
  { value: 'America/Los_Angeles', label: 'Los Angeles' },
  { value: 'Asia/Tokyo', label: 'Tokyo' },
  { value: 'Asia/Seoul', label: 'Seoul' }
] as const

const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December']

const pad = (value: number) => String(value).padStart(2, '0')
const ordinal = (day: number) => `${day}${day % 10 === 1 && day !== 11 ? 'st' : day % 10 === 2 && day !== 12 ? 'nd' : day % 10 === 3 && day !== 13 ? 'rd' : 'th'}`
const listed = (items: string[]) => (items.length < 2 ? items.join('') : `${items.slice(0, -1).join(', ')} and ${items.at(-1)}`)

/** A cron line in words: "Every Monday at 09:00", "Every 15 minutes". */
export function describeCron(expression: string): string {
  const preset = CRON_PRESETS.find(entry => entry.value === expression.trim())
  if (preset) return preset.label
  const fields = parseCron(expression)
  if (typeof fields === 'string') return 'Not a schedule yet'
  const [minute, hour, day, month, weekday] = expression.trim().split(/\s+/) as [string, string, string, string, string]

  let when: string
  if (minute.startsWith('*/') && hour === '*') when = `Every ${minute.slice(2)} minutes`
  else if (minute === '*' && hour === '*') when = 'Every minute'
  else if (hour === '*') when = `Every hour at ${listed(fields.minutes.map(value => `:${pad(value)}`))}`
  else if (hour.startsWith('*/')) when = `Every ${hour.slice(2)} hours at :${pad(fields.minutes[0] ?? 0)}`
  else when = `at ${listed(fields.hours.flatMap(value => fields.minutes.slice(0, 2).map(entry => `${pad(value)}:${pad(entry)}`)).slice(0, 4))}`

  const parts: string[] = []
  if (weekday !== '*') {
    const names = fields.weekdays.map(value => WEEKDAYS[value]!)
    parts.push(fields.weekdays.join(',') === '1,2,3,4,5' ? 'every weekday' : `every ${listed(names)}`)
  }
  if (day !== '*') parts.push(`on the ${listed(fields.days.map(ordinal))}`)
  if (month !== '*') parts.push(`in ${listed(fields.months.map(value => MONTHS[value - 1]!))}`)

  const daily = !parts.length && !when.startsWith('Every')
  const text = daily ? `Every day ${when}` : when.startsWith('Every') ? [when, ...parts].join(', ') : `${parts.join(' ')} ${when}`
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/* A time's fields as a clock in a timezone reads them. */
const formatters = new Map<string, Intl.DateTimeFormat>()
function zoned(at: number, timezone: string) {
  let format = formatters.get(timezone)
  if (!format) {
    format = new Intl.DateTimeFormat('en-US', { timeZone: timezone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', weekday: 'short' })
    formatters.set(timezone, format)
  }
  const parts = Object.fromEntries(format.formatToParts(at).map(part => [part.type, part.value]))
  return {
    month: Number(parts.month),
    day: Number(parts.day),
    hour: Number(parts.hour) % 24,
    minute: Number(parts.minute),
    weekday: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(parts.weekday!)
  }
}

/**
 * The next `count` times the schedule fires after `from`. It walks forward a minute at a
 * time only inside an hour that matches; a day or an hour that cannot match is stepped over
 * whole, so a year of Mondays is a few hundred steps, not half a million.
 */
export function nextRuns(expression: string, timezone: string, from: number, count = 5): number[] {
  const fields = parseCron(expression)
  if (typeof fields === 'string') return []
  const runs: number[] = []
  const MINUTE = 60_000
  let at = Math.floor(from / MINUTE) * MINUTE + MINUTE
  const limit = from + 400 * 86_400_000
  while (runs.length < count && at < limit) {
    const time = zoned(at, timezone)
    const dayMatches = fields.dayOr
      ? fields.days.includes(time.day) || fields.weekdays.includes(time.weekday)
      : fields.days.includes(time.day) && fields.weekdays.includes(time.weekday)
    if (!fields.months.includes(time.month) || !dayMatches) {
      at += ((23 - time.hour) * 60 + (60 - time.minute)) * MINUTE
      continue
    }
    if (!fields.hours.includes(time.hour)) {
      at += (60 - time.minute) * MINUTE
      continue
    }
    if (fields.minutes.includes(time.minute)) runs.push(at)
    at += MINUTE
  }
  return runs
}

/** "Mon, Sep 28, 09:00" in the schedule's own timezone. */
export function writeRun(at: number, timezone: string): string {
  return new Intl.DateTimeFormat('en', { timeZone: timezone, weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(at)
}
