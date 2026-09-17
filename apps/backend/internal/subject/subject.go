// Package subject turns the identifier on a data-subject request into the rows it reaches
// (project.md §61, §101; docs/compliance/05).
//
// Every data-subject right starts from the same sentence — "the studio supplies the
// identifier it knows the player by" — and immediately runs into §29.2's separation of the
// device from the actor. A person is not one key. They are an install, possibly an account,
// possibly both at different points in the same session, and the tables that hold them are
// keyed on different ones of those. Resolving that once, here, is what stops an erasure and
// an export disagreeing about who they are talking about.
//
// # One identifier, two readings
//
// The readings are deliberately not the same for the two rights, and the difference is a
// disclosure rule rather than an implementation detail. See Match.
//
// # What cannot be resolved
//
// The mapping from a studio's actor key to a device is the event. `events_raw` is bounded by
// the project's retention tier, so a device someone used and abandoned longer ago than that
// cannot be linked to their name by anything PlayerSense holds. `player_lifetime` outlives
// the events (§33.1) and is keyed on the device, so it answers "which device is this
// install" and never "which devices did this player use".
//
// A request naming the install therefore reaches further back than one naming the account.
// That is the mirror of the limit §05 already records about reinstalls, and the reason it
// asks studios for every identifier they hold rather than one.
package subject

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Match says how widely an identifier is read.
//
// The two rights want opposite answers to the same question, which is why this is a
// parameter and not a constant.
//
// **Erasure reads widely.** A request naming an install has to reach the rows produced after
// that install signed in, which carry an actor key the request never mentioned. Erasing only
// the anonymous half would leave the person's account history behind, which is not what
// anybody asking to be forgotten means.
//
// **Access reads narrowly.** The same widening applied to an export would hand the requester
// every event produced by a second person who signed in on that install — a disclosure, and
// a worse failure than an incomplete answer. So an export returns what the named actor did
// and nothing else, and says so in the bundle rather than quietly over-answering.
type Match int

const (
	// Actor matches rows this actor produced, and nothing else.
	Actor Match = iota
	// ActorOrDevice also matches everything from the install the identifier names.
	ActorOrDevice
)

// Predicate is the WHERE fragment for events_raw, with its arguments.
//
// Returned together rather than built at each call site, because the two have to agree: a
// predicate with one placeholder and two arguments is a query that fails at run time, and a
// predicate that resolves a subject differently from the one that deletes them is far worse
// — it is a deletion that reports success over a different set of rows than it looked at.
func (m Match) Predicate(projectSeq uint32, actorKey string) (string, []any) {
	if m == ActorOrDevice {
		return "project_seq = ? AND (actor_key = ? OR device_key = ?)",
			[]any{projectSeq, actorKey, actorKey}
	}
	return "project_seq = ? AND actor_key = ?", []any{projectSeq, actorKey}
}

// Subject is everything the cell can still say about one person.
type Subject struct {
	// Devices are the device identifiers behind the rows this request reaches. Both
	// device-keyed tables — `player_lifetime` and `player_activity_daily` — are addressed
	// by this list, because neither has an actor column to address them by (§29.2).
	Devices []uint64

	// HotRows says whether `events_raw` still holds anything matching.
	//
	// Worth a field of its own because it is what lets a job skip work rather than
	// discover there was none: an erasure stays open for its drain window, and issuing a
	// mutation across every part of a tenant on each of those passes — to delete nothing —
	// is the difference between a background job and an incident.
	HotRows bool

	// First and Last bound where in time this person appears, widened by Margin. Zero when
	// nothing is known, which a caller should read as "no time filter is safe" rather than
	// as an empty range.
	First, Last time.Time
}

// Known reports whether the cell has any trace of this subject left.
func (s Subject) Known() bool { return len(s.Devices) > 0 || s.HotRows }

// Margin widens the resolved window before it is used to select archive objects.
//
// The window comes from what ClickHouse still holds, and the archive expires on nearly the
// same clock: an object's `delete_after` is its newest event plus the retention tier, and a
// row's `expire_at` is its own event time plus the same tier. "Nearly" is the whole reason
// for a margin — `ttl_only_drop_parts` keeps a day's rows until every row in the part has
// expired, and an object is bounded by its newest event rather than by each row's own — so
// the two edges do not line up to the second. Two days each way covers the difference
// without meaningfully widening the scan.
const Margin = 48 * time.Hour

// Resolve finds the devices and the span, from the two places that can still answer.
//
// Two queries, because they fail in opposite directions and the union is what makes a
// data-subject request as complete as the data allows. `events_raw` is the only thing that
// can map an actor key to a device, and it is bounded by retention. `player_lifetime` is not
// bounded by retention but can only be asked about an install.
func Resolve(ctx context.Context, db *sql.DB, projectSeq uint32, actorKey string, match Match) (Subject, error) {
	var out Subject
	seen := map[uint64]struct{}{}

	add := func(device uint64, first, last time.Time) {
		if _, ok := seen[device]; !ok {
			seen[device] = struct{}{}
			out.Devices = append(out.Devices, device)
		}
		if !first.IsZero() && (out.First.IsZero() || first.Before(out.First)) {
			out.First = first
		}
		if !last.IsZero() && last.After(out.Last) {
			out.Last = last
		}
	}

	where, args := match.Predicate(projectSeq, actorKey)
	rows, err := db.QueryContext(ctx, `
		SELECT device_id, min(event_time), max(event_time)
		FROM events_raw
		WHERE `+where+`
		GROUP BY device_id`, args...)
	if err != nil {
		return out, fmt.Errorf("resolve the subject's devices: %w", err)
	}
	for rows.Next() {
		var device uint64
		var first, last time.Time
		if err := rows.Scan(&device, &first, &last); err != nil {
			rows.Close()
			return out, err
		}
		out.HotRows = true
		add(device, first, last)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, fmt.Errorf("resolve the subject's devices: %w", err)
	}

	// Through the view, which is the only correct reader of an AggregatingMergeTree: the
	// stored rows are partial until they merge, so a device's `first_seen` read directly is
	// a partial minimum (§29.6).
	//
	// The devices already found are asked about as well, because a device's lifetime span
	// reaches back further than its surviving events do — and that span is what decides how
	// far back the archive has to be read.
	lifetimeArgs := make([]any, 0, 2+len(out.Devices))
	lifetimeArgs = append(lifetimeArgs, projectSeq, actorKey)
	for _, d := range out.Devices {
		lifetimeArgs = append(lifetimeArgs, d)
	}
	lifetime, err := db.QueryContext(ctx, `
		SELECT device_id, first_seen, last_seen
		FROM player_lifetime_v
		WHERE project_seq = ? AND (device_key = ? OR device_id IN (`+Placeholders(len(out.Devices))+`))`,
		lifetimeArgs...)
	if err != nil {
		return out, fmt.Errorf("resolve the subject's lifetime: %w", err)
	}
	for lifetime.Next() {
		var device uint64
		var first, last time.Time
		if err := lifetime.Scan(&device, &first, &last); err != nil {
			lifetime.Close()
			return out, err
		}
		add(device, first, last)
	}
	err = lifetime.Err()
	lifetime.Close()
	if err != nil {
		return out, fmt.Errorf("resolve the subject's lifetime: %w", err)
	}

	if !out.First.IsZero() {
		out.First = out.First.Add(-Margin)
		out.Last = out.Last.Add(Margin)
	}
	return out, nil
}

// Placeholders renders an IN list of the given length.
//
// A list rather than a subquery, because ClickHouse refuses a subquery inside a mutation —
// the constraint the cohort backfill and the lifetime prune both work around — and because
// a reader built the same way as the writer cannot drift from it.
//
// `NULL` for an empty list, so the clause parses and matches nothing rather than producing a
// syntax error at the one moment there is nothing to do.
func Placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// DeviceArgs flattens a device chunk into a statement's arguments, tenant first.
func DeviceArgs(projectSeq uint32, devices []uint64) []any {
	out := make([]any, 0, 1+len(devices))
	out = append(out, projectSeq)
	for _, d := range devices {
		out = append(out, d)
	}
	return out
}

// ChunkSize is how many device identifiers go into one statement's IN list.
const ChunkSize = 10_000
