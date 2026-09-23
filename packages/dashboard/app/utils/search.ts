/**
 * What a search box matched, cut out of the text so it can be marked: every place the
 * needle occurs, case aside, and the text between. A reader who sees why an option is
 * still in the list trusts the search that left it there.
 */
export interface MatchPart {
  text: string
  match: boolean
}

export function matchParts(text: string, needle: string): MatchPart[] {
  const wanted = needle.trim().toLowerCase()
  if (!wanted) return [{ text, match: false }]
  const parts: MatchPart[] = []
  const lower = text.toLowerCase()
  let at = 0
  for (let found = lower.indexOf(wanted); found !== -1; found = lower.indexOf(wanted, at)) {
    if (found > at) parts.push({ text: text.slice(at, found), match: false })
    parts.push({ text: text.slice(found, found + wanted.length), match: true })
    at = found + wanted.length
  }
  if (at < text.length) parts.push({ text: text.slice(at), match: false })
  return parts
}
