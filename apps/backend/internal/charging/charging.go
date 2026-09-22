// Package charging bills tracked players as they arrive (ADR 0039).
//
// Two halves joined by a queue, and the queue is the design:
//
//	accrue  the event bus, per delivery: which players are new this month, what their
//	        positions cost, and a usage_charges row saying so. The wallet is not touched.
//	drain   every few seconds: each account's unsettled charges, netted into one spend from
//	        the wallet, and the rows marked settled in the same transaction.
//
// # The four promises, and what keeps each
//
// **A player is never charged twice.** "New" is decided by one thing: whether an INSERT … ON CONFLICT
// DO NOTHING into billable_player inserted a row. A redelivered batch, a replayed delivery after a
// crash, and a second consumer racing the first all find the row and count nothing — there is no
// read-then-write window to double-count in. Behind that, usage_charges is unique on (account,
// month, first_unit), and every ledger entry carries an idempotency key.
//
// **A charge is never lost.** Counting a player and queueing their charge are one transaction, so
// a counted player is an owed charge. Paying a charge and marking it settled are one transaction, so
// a crash between them rolls back both and the next drain pays it. An account that cannot pay does
// not refuse the charge — the players were already served — it owes it (wallet.ChargeUsage).
//
// **The database is not the bottleneck.** A player already counted this month costs a Valkey MGET
// and nothing else; only players PostgreSQL has not seen reach it, one batched INSERT per account
// per delivery. Valkey is written only after PostgreSQL commits, so losing it costs speed and never
// correctness (§65): a missing key is one more lookup that finds the row already there. The drain
// is a bounded batch on an interval, over a partial index that holds only unsettled rows, and takes
// accounts with SKIP LOCKED so two drains never wait for each other.
//
// **Ingestion never waits for any of this.** The charger is its own consumer group in its own
// binary. The SDK's batch was answered 202 when it reached the queue, and nothing on the ingestion
// path reads a balance, a price or an account state.
package charging

import (
	"crypto/hmac"
	"crypto/sha256"
	"time"
)

// DigestSize is how much of the HMAC is kept: 128 bits, which makes a collision between two
// players of one account in one month a thing that does not happen.
const DigestSize = 16

// Digest names a player for billing, and nothing else.
//
// A keyed HMAC over the month and the device. The key keeps the billing table from being joined to
// events_raw by anyone who holds only the table; the month keeps a player's January and February
// digests unrelated, so even with the key it cannot follow a person across months.
//
// The device is the same identity the dashboard's monthly active players counts (`uniqExact
// (device_id)` per project and environment in metrics_monthly), so the invoice and the dashboard
// count the same thing (§38).
func Digest(key []byte, period time.Time, projectID, environmentID, installID string) []byte {
	mac := hmac.New(sha256.New, key)
	for _, part := range []string{"v1", period.UTC().Format("2006-01"), projectID, environmentID, installID} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return mac.Sum(nil)[:DigestSize]
}

// PeriodOf is the billing month an instant falls in: the first of the month, UTC.
//
// Charging keys on when PlayerSense *received* the batch, not on the events' own timestamps. An SDK
// can backdate an event by up to seven days (§19.2), and pricing on that would let a client move a
// player into a month that has already been paid for. So a batch of late events from the 31st that
// arrives on the 1st is a player in the new month here, and an activity on the 31st in the
// dashboard's metrics_monthly. The two counts agree everywhere except that seam — which is recorded
// in docs/compliance/10-open-items.md rather than left for somebody to discover.
func PeriodOf(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
}
