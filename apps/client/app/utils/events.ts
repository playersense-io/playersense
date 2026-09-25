// The project's events as the pickers and the catalogue show them: an event's category, its
// description and the properties it carries, from the analytics events endpoint and the
// definitions a studio writes (…/event-definitions).

export type EventCategory = 'session' | 'progression' | 'economy' | 'engagement' | 'custom'

export interface EventProperty {
  key: string
  type: 'string' | 'number' | 'boolean'
  /** Share of this event's occurrences that carry the property, 0–1. */
  fill: number
  values: readonly string[]
}

export interface EventDefinition {
  name: string
  category: EventCategory
  description: string
  /** Occurrences per active player per day. */
  rate: number
  /** Today against the previous period. */
  pace: number
  properties: readonly EventProperty[]
}

export const EVENT_CATEGORY_LABELS: Record<EventCategory, string> = {
  session: 'Session',
  progression: 'Progression',
  economy: 'Economy',
  engagement: 'Engagement',
  custom: 'Custom'
}

/** A theme colour per category, for the category tag: chart slots, never outcome colours. */
export const EVENT_CATEGORY_TONES: Record<EventCategory, string> = {
  session: 'chart-1',
  progression: 'chart-3',
  economy: 'chart-4',
  engagement: 'chart-5',
  custom: 'chart-6'
}
