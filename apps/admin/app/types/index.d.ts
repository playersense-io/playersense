// Placeholder shape for the staff_access table (project.md §104) — one row
// per staff read of a customer's data, with a ticket reference, not free text.
export interface StaffAccessEntry {
  staffName: string
  organization: string
  ticketReference: string
  accessedAt: string
}
