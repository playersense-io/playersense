/**
 * A table written out as CSV and handed to the reader as a file, for the analyst who
 * takes a figure somewhere the panel does not go. RFC 4180: comma-separated, CRLF between
 * rows, a field quoted when it holds a comma, a quote or a line break, and a quote inside
 * one doubled.
 *
 * A figure is written as the number, never as the panel formats it: "0.346", not "34.6%",
 * and 82429, not "82.4k", so a spreadsheet reads it as a number. An empty cell is a figure
 * not known or withheld; which of the two is the header's to say.
 */

export type CsvCell = string | number | null | undefined

function field(cell: CsvCell): string {
  if (cell === null || cell === undefined) return ''
  const text = typeof cell === 'number' ? (Number.isFinite(cell) ? String(cell) : '') : cell
  return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

export function toCsv(rows: readonly (readonly CsvCell[])[]): string {
  return rows.map(row => row.map(field).join(',')).join('\r\n')
}

/**
 * Saves the rows as `name`.csv. A byte-order mark first, because Excel opens a file
 * without one in the machine's legacy code page and mangles every name that is not ASCII.
 */
export function downloadCsv(name: string, rows: readonly (readonly CsvCell[])[]) {
  const blob = new Blob(['﻿', toCsv(rows)], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = name.endsWith('.csv') ? name : `${name}.csv`
  link.click()
  URL.revokeObjectURL(url)
}

/** "Onboarding, 5 steps" → "onboarding-5-steps": a file name every system accepts. */
export function fileSlug(text: string): string {
  return text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'export'
}
