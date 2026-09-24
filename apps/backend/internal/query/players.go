package query

import (
	"context"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/schemas"
)

// The Players page: a directory of installs from player_lifetime, and one player's page from
// the lifetime facts, their days and their latest events.
//
// Every read here names somebody, so the routes need player.read, and none of them is served
// for a kids_strict project (refuseKids).

// PlayerFilter is the directory's filters, already validated.
type PlayerFilter struct {
	Status, Platform, Country, Spend, Sort string
	Limit, Offset                          int
}

// refuseKids refuses a read of one player on a kids_strict project (§29.15, §98).
//
// That tier gives every batch a fresh installation identifier so a child cannot be followed from
// one day to the next. A directory of those identifiers would list every batch as a player, and a
// timeline would be the very thing the tier exists to make impossible.
func refuseKids(scope Scope) error {
	if scope.PolicyTier == "kids_strict" {
		return refuse("project",
			"this project is on the kids_strict collection tier, which keeps no persistent "+
				"identifier, so no individual player can be listed or followed; the aggregate "+
				"pages are available")
	}
	return nil
}

// lifetimeColumns reads one install's life out of player_lifetime's partial rows.
//
// The three arrival attributes come from one argMin state (§29.3), so they are always from the
// same event. ClickHouse computes an aggregate named three times once.
func lifetimeColumns(b *builder) *builder {
	return b.column("toString(any(device_key))", "install").
		column("tupleElement(argMinMerge(first_attrs), 1)", "platform").
		column("tupleElement(argMinMerge(first_attrs), 2)", "country").
		column("tupleElement(argMinMerge(first_attrs), 3)", "version").
		column("min(first_seen)", "first").
		column("max(last_seen)", "last").
		column("toUInt64(uniqMerge(active_days))", "days").
		column("sum(sessions)", "sessions").
		column("sum(playtime_ms)", "playtime").
		column("sum(revenue_micros)", "revenue").
		column("sum(purchases)", "purchases").
		column("min(first_purchase_at)", "first_purchase").
		column("max(last_purchase_at)", "last_purchase")
}

func scanLifetime(rows driver.Rows, now time.Time) (schemas.PlayerSummaryOut, error) {
	var p schemas.PlayerSummaryOut
	if err := rows.Scan(&p.InstallID, &p.Platform, &p.Country, &p.AppVersion, &p.FirstSeen, &p.LastSeen,
		&p.ActiveDays, &p.Sessions, &p.PlaytimeMs, &p.RevenueMicros, &p.Purchases,
		&p.FirstPurchaseAt, &p.LastPurchaseAt); err != nil {
		return p, err
	}
	p.Status = statusOf(p.LastSeen, now)
	return p, nil
}

// statusOf is the directory's reading of how long ago somebody last played.
func statusOf(last, now time.Time) string {
	switch days := now.Sub(last).Hours() / 24; {
	case days >= schemas.PlayerChurnedAfterDays:
		return "churned"
	case days >= schemas.PlayerLapsedAfterDays:
		return "lapsed"
	default:
		return "active"
	}
}

// Players reads one page of the directory.
//
// player_lifetime holds each device as several partial rows until its parts merge, so the read
// groups by the whole sort key and runs with optimize_aggregation_in_order: each device is
// finished the moment the next begins, and memory holds one device plus the page rather than a
// hash table of the project. The filters are on the finished device, which is the only place a
// lifetime total or a last visit is true.
func (e *Engine) Players(ctx context.Context, scope Scope, f PlayerFilter, now time.Time) (*schemas.PlayerListOut, error) {
	if err := refuseKids(scope); err != nil {
		return nil, err
	}

	b := from(factPlayers)
	lifetimeColumns(b)
	scopeAnd(b, scope)
	b.group("project_seq", "environment_seq", "device_id")

	lapsed := timestamp(now.AddDate(0, 0, -schemas.PlayerLapsedAfterDays))
	churned := timestamp(now.AddDate(0, 0, -schemas.PlayerChurnedAfterDays))
	switch f.Status {
	case "active":
		b.havingThat("last >= ?", lapsed)
	case "lapsed":
		b.havingThat("last < ? AND last >= ?", lapsed, churned)
	case "churned":
		b.havingThat("last < ?", churned)
	}
	if f.Platform != "" {
		b.havingThat("platform = ?", f.Platform)
	}
	if f.Country != "" {
		b.havingThat("country = ?", f.Country)
	}
	switch f.Spend {
	case "paying":
		b.havingThat("revenue > 0")
	case "never":
		b.havingThat("revenue <= 0")
	}

	order := map[string]string{
		"first_seen": "first DESC", "revenue": "revenue DESC", "playtime": "playtime DESC",
		"sessions": "sessions DESC", "active_days": "days DESC",
	}[f.Sort]
	if order == "" {
		order = "last DESC"
	}
	// One more than the page, which is how the answer knows whether there is a next one without
	// counting every match.
	b.order(order, "install").withLimit(f.Limit + 1).page(f.Offset)

	out := &schemas.PlayerListOut{}
	if err := e.readInOrder(ctx, b.statement(), func(rows driver.Rows) error {
		p, err := scanLifetime(rows, now)
		if err != nil {
			return err
		}
		out.Players = append(out.Players, p)
		return nil
	}); err != nil {
		return nil, failed("player_lifetime", err)
	}
	if len(out.Players) > f.Limit {
		out.Players = out.Players[:f.Limit]
		out.NextOffset = f.Offset + f.Limit
	}
	return out, nil
}

// maxAccountInstalls bounds how many installs an account's page reads.
const maxAccountInstalls = 20

// Player reads one player's page, by install id or by the studio's player id.
//
// An install is found by its surrogate, computed by ClickHouse from the same three values
// events_raw computes it from, so every read below is a lookup on the sort key rather than a scan.
// An account's installs come from the sessions it played in the last ninety days, then the same.
func (e *Engine) Player(ctx context.Context, scope Scope, id string, now time.Time) (*schemas.PlayerProfileOut, error) {
	if err := refuseKids(scope); err != nil {
		return nil, err
	}

	out := &schemas.PlayerProfileOut{ID: id}
	if install, err := uuid.Parse(id); err == nil {
		out.Kind = "install"
		out.Installs = []string{install.String()}
	} else {
		out.Kind = "account"
		installs, err := e.accountInstalls(ctx, scope, id, now)
		if err != nil {
			return nil, err
		}
		out.Installs = installs
	}

	summary, found, err := e.lifetime(ctx, scope, out.Kind, id, out.Installs, now)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	out.Summary = summary

	if len(out.Installs) == 0 {
		return out, nil
	}
	if out.Days, err = e.playerDays(ctx, scope, out.Installs, now); err != nil {
		return nil, err
	}
	if out.Events, err = e.playerEvents(ctx, scope, out.Installs, now); err != nil {
		return nil, err
	}
	return out, nil
}

// deviceIn restricts a statement to the surrogates of some installs.
//
// sipHash64(project_seq, environment_seq, install) is how events_raw materializes device_id, so
// computing it here from the same typed values finds the same rows, and the statement stays a
// lookup on the sort key. Never reimplemented in Go: a second implementation of the hash would be
// a key that has to agree exactly and one day would not.
func deviceIn(b *builder, scope Scope, installs []string, prewhere bool) {
	marks := make([]string, len(installs))
	args := make([]any, 0, 3*len(installs))
	for i, install := range installs {
		marks[i] = "sipHash64(toUInt32(?), toUInt16(?), ?)"
		args = append(args, scope.ProjectSeq, scope.EnvironmentSeq, install)
	}
	predicate := "device_id IN (" + strings.Join(marks, ", ") + ")"
	if prewhere {
		b.pre(predicate, args...)
	} else {
		b.and(predicate, args...)
	}
}

// accountInstalls lists the installs an account played on in the last ninety days.
func (e *Engine) accountInstalls(ctx context.Context, scope Scope, player string, now time.Time) ([]string, error) {
	b := from(factSessions)
	b.column("toString(any(install_id))", "install").column("max(ended_at)", "last")
	scopeAnd(b, scope)
	b.gte("activity_date", date(now.UTC().AddDate(0, 0, -90))).eq("player_id", player)
	b.group("device_id").order("last DESC").withLimit(maxAccountInstalls)

	var out []string
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var install string
		var last time.Time
		if err := rows.Scan(&install, &last); err != nil {
			return err
		}
		out = append(out, install)
		return nil
	}); err != nil {
		return nil, failed("sessions", err)
	}
	return out, nil
}

// lifetime reads an install's life from player_lifetime, or an account's from account_lifetime.
func (e *Engine) lifetime(ctx context.Context, scope Scope, kind, id string, installs []string, now time.Time) (schemas.PlayerSummaryOut, bool, error) {
	var out schemas.PlayerSummaryOut
	found := false

	if kind == "install" {
		b := from(factPlayers)
		lifetimeColumns(b)
		scopeAnd(b, scope)
		deviceIn(b, scope, installs, false)
		b.group("device_id")
		err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
			p, err := scanLifetime(rows, now)
			if err != nil {
				return err
			}
			out, found = p, true
			return nil
		})
		if err != nil {
			return out, false, failed("player_lifetime", err)
		}
		return out, found, nil
	}

	// account_lifetime is keyed on the same kind of surrogate, of the player id.
	b := from(factAccounts)
	b.column("min(first_seen)", "first").
		column("max(last_seen)", "last").
		column("toUInt64(uniqMerge(active_days))", "days").
		column("sum(sessions)", "sessions").
		column("sum(playtime_ms)", "playtime").
		column("sum(revenue_micros)", "revenue").
		column("sum(purchases)", "purchases").
		column("min(first_purchase_at)", "first_purchase").
		column("max(last_purchase_at)", "last_purchase")
	scopeAnd(b, scope)
	b.and("account_id = sipHash64(toUInt32(?), toUInt16(?), ?)", scope.ProjectSeq, scope.EnvironmentSeq, id)
	b.group("account_id")
	err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		if err := rows.Scan(&out.FirstSeen, &out.LastSeen, &out.ActiveDays, &out.Sessions,
			&out.PlaytimeMs, &out.RevenueMicros, &out.Purchases,
			&out.FirstPurchaseAt, &out.LastPurchaseAt); err != nil {
			return err
		}
		out.Status = statusOf(out.LastSeen, now)
		found = true
		return nil
	})
	if err != nil {
		return out, false, failed("account_lifetime", err)
	}
	return out, found, nil
}

// playerDays reads the installs' days from player_activity_daily, summed across installs.
func (e *Engine) playerDays(ctx context.Context, scope Scope, installs []string, now time.Time) ([]schemas.PlayerDayOut, error) {
	b := from(factPlayerDays)
	b.column("toDateTime(activity_date, 'UTC')", "d").
		column("sum(sessions)", "s").
		column("sum(playtime_ms)", "pt").
		column("sum(events)", "ev").
		column("sum(revenue_micros)", "rev").
		column("sum(purchases)", "pu")
	scopeAnd(b, scope)
	b.gte("activity_date", date(now.UTC().AddDate(0, 0, -89)))
	deviceIn(b, scope, installs, false)
	b.groupOn("d").withLimit(100)

	var out []schemas.PlayerDayOut
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var d schemas.PlayerDayOut
		if err := rows.Scan(&d.Day, &d.Sessions, &d.PlaytimeMs, &d.Events, &d.RevenueMicros, &d.Purchases); err != nil {
			return err
		}
		out = append(out, d)
		return nil
	}); err != nil {
		return nil, failed("player_activity_daily", err)
	}
	return out, nil
}

// playerEvents reads the installs' newest events from events_raw.
//
// device_id is after the event name in the sort key, so within each (day, event name) run the rows
// are ordered by it and ClickHouse's key analysis finds the device's granules without a skip
// index; PREWHERE then reads the device column alone before the rest. Still a granule per (day,
// event name) at least, so the last day is read first and the week only when the day did not
// fill the page, as an event's occurrences are.
func (e *Engine) playerEvents(ctx context.Context, scope Scope, installs []string, now time.Time) ([]schemas.EventOccurrenceOut, error) {
	const page = 100
	today := now.UTC().Truncate(24 * time.Hour)
	var found []schemas.EventOccurrenceOut
	for _, days := range []int{1, 6} {
		b := rawOccurrences(scope)
		b.gte("event_date", date(today.AddDate(0, 0, -days)))
		deviceIn(b, scope, installs, true)
		b.order("event_time DESC").withLimit(page)
		var err error
		if found, err = e.occurrences(ctx, b); err != nil {
			return nil, err
		}
		if len(found) >= page {
			break
		}
	}
	return found, nil
}
