// Placeholder — the staff_access table (project.md §104) doesn't exist yet.
// Nothing writes it today, which is exactly the gap todos.md §3 tracks.
const entries = [
  { staffName: 'Priya Natarajan', organization: 'Acme Games', ticketReference: 'SUP-1042', accessedAt: '2026-09-18T14:02:00Z' },
  { staffName: 'Jordan Ashford', organization: 'Nova Interactive', ticketReference: 'SUP-1039', accessedAt: '2026-09-17T09:41:00Z' }
]

export default eventHandler(async () => {
  return entries
})
