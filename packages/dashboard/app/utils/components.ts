// The props' own types for the components in this layer, here rather than exported from
// each .vue so the apps get them auto-imported like everything else under utils/.

export interface ChoiceOption {
  value: string
  label: string
  /** What choosing it means, in a sentence. */
  note?: string
  /** Written out in full so @nuxt/icon bundles it. */
  icon?: string
}

/** One fact in a `FactGrid`. */
export interface Fact {
  key: string
  label: string
  /** Already formatted. A fact grid renders; it does not decide how a date reads. */
  value: string
  /** Identifiers, endpoints and figures. Words are not mono. */
  mono?: boolean
  /** The sentence behind the glyph: what this is, and what it is not. */
  info?: string
  /** One short line under the value. Not a paragraph, and never a second sentence. */
  note?: string
  /** The screen that owns this fact. Renders the value as a link. */
  to?: string
  /** What the copy button puts on the clipboard, which is never the display value. */
  copy?: string
  /** A status dot's colour class: `bg-success`, `bg-warning`, `bg-error`, `bg-ink/30`. */
  dot?: string
}

export type MetricSkeletonShape = 'figure' | 'control' | 'legend' | 'body'

export interface RecordColumn {
  key: string
  label: string
  /** A CSS grid track: `1fr`, `132px`, `minmax(0, 1.5fr)`. */
  width: string
  align?: 'start' | 'end'
  /** Draws the label as a sort control. The caller owns what the key orders by. */
  sortable?: boolean
}

/** One tab of a `PageTabs`: the value the query keeps, what it says, and a count if it has one. */
export interface PageTab {
  value: string
  label: string
  count?: number | string
}

export interface RecordTab {
  key: string
  label: string
  count: number
}

export interface GhostSelectOption {
  value: string
  label: string
  /** The heading it is listed under; consecutive options with the same one share it. */
  group?: string
  /** A line under the label, quieter: what choosing it shows. */
  note?: string
}

export interface SettingsTab {
  /** The route it leads to. These are real URLs, which is what makes this navigation. */
  to: string
  title: string
  /** Whose setting it is: "This project", "Organisation", "You". */
  scope?: string
  /** Written out in full so @nuxt/icon bundles it. */
  icon: string
}

export interface MetricOption {
  value: string
  label: string
  /** What this metric counts, shown under the title while it is the one chosen. */
  description?: string
}

export type MetricView = 'chart' | 'table'

export interface Step {
  id: string
  label: string
}

export type BadgeTone = 'success' | 'warning' | 'error' | 'neutral'

export interface SegmentedOption {
  value: string
  /** The words in the segment. Absent, the segment is the icon alone and `title` names it. */
  label?: string
  /** A glyph, written out in full so @nuxt/icon finds it. */
  icon?: string
  /** What a screen reader hears and, for a segment without a label, what the tooltip says. */
  title?: string
  disabled?: boolean
}

/** One group of choices in a `FilterMenu`: "Category", with its values. */
export interface FilterGroup {
  key: string
  label: string
  options: readonly { value: string, label: string }[]
}

/** A filter in force, as a `TableToolbar` chip. `key` is what removing it hands back. */
export interface ActiveFilter {
  key: string
  label: string
}
