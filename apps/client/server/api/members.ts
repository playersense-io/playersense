// Placeholder data — GET /v1/organizations/{org}/members on control-api is the
// real source once wired in. Role keys match the six seeded roles
// (apps/backend/internal/authz/roles.go): owner, admin, liveops_manager,
// developer, growth_manager, billing.
const members = [
  { name: 'Jordan Ashford', username: 'jordan', role: 'owner' },
  { name: 'Priya Natarajan', username: 'priya', role: 'admin' },
  { name: 'Sam Okafor', username: 'sam', role: 'liveops_manager' },
  { name: 'Elena Vasquez', username: 'elena', role: 'developer' },
  { name: 'Marcus Bellweather', username: 'marcus', role: 'growth_manager' },
  { name: 'Riko Tanaka', username: 'riko', role: 'billing' }
]

export default eventHandler(async () => {
  return members
})
