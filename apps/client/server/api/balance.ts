// Placeholder data — GET /v1/users/me carries the real one once wired in: each
// membership's `balance` (LiveBalanceOut), null where the role lacks `billing.read`.
// `available_micros` is the headline, or `owed_micros` when that is not zero.
const balance = {
  available_micros: 1_240_500_000,
  owed_micros: 0
}

export default eventHandler(async () => {
  return balance
})
