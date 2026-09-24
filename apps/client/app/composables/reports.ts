/**
 * The project's reports, each a graph of nodes and the edges between them. Kept in this
 * browser until the dashboard is wired to a reports API; seeded with the templates so the
 * builder opens on something that runs.
 */
export interface SavedReport {
  id: string
  name: string
  description: string
  nodes: GraphNode[]
  edges: GraphEdge[]
  updated_at: string
}

let shared: Ref<SavedReport[]> | null = null

const fromTemplate = (template: ReportTemplate, id: string): SavedReport => ({
  id,
  name: template.name,
  description: template.note,
  nodes: JSON.parse(JSON.stringify(template.nodes)) as GraphNode[],
  edges: JSON.parse(JSON.stringify(template.edges)) as GraphEdge[],
  updated_at: new Date().toISOString()
})

function seedReports(): SavedReport[] {
  return TEMPLATES.map(template => fromTemplate(template, `rep_${template.key}`))
}

function storedReports(): Ref<SavedReport[]> {
  shared ??= effectScope(true).run(() => useLocalStorage<SavedReport[]>('playersense.reports.v1', seedReports, { deep: false }))!
  return shared
}

export function useReports() {
  const stored = storedReports()

  const reports = computed<SavedReport[]>(() => {
    const value = stored.value
    const valid = Array.isArray(value) && value.every(entry => typeof entry?.id === 'string' && Array.isArray(entry.nodes) && Array.isArray(entry.edges))
    return valid ? value : seedReports()
  })

  const reportById = (id: string) => reports.value.find(entry => entry.id === id) ?? null

  const newId = () => `rep_${Math.random().toString(36).slice(2, 10)}`

  /** A report from a template, or an empty one. Returns its id. */
  function create(template?: ReportTemplate): string {
    const id = newId()
    const report = template
      ? fromTemplate(template, id)
      : { id, name: 'Untitled report', description: '', nodes: [], edges: [], updated_at: new Date().toISOString() }
    stored.value = [report, ...reports.value]
    return id
  }

  function save(id: string, change: Partial<Omit<SavedReport, 'id'>>) {
    stored.value = reports.value.map(entry => (entry.id === id ? { ...entry, ...change, updated_at: new Date().toISOString() } : entry))
  }

  function duplicate(id: string): string | null {
    const report = reportById(id)
    if (!report) return null
    const copy = { ...(JSON.parse(JSON.stringify(report)) as SavedReport), id: newId(), name: `${report.name} (copy)`, updated_at: new Date().toISOString() }
    stored.value = [copy, ...reports.value]
    return copy.id
  }

  function remove(id: string) {
    stored.value = reports.value.filter(entry => entry.id !== id)
  }

  return { reports, reportById, create, save, duplicate, remove }
}
