package query

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Report sources: the rows a report's Source node hands on, one query each, bounded by a range of
// days and a row limit, each from the fact built for it. A report's steps (filter, join, group,
// formula, sort) then run over these rows in the reports package, in memory that the limit bounds,
// so nothing here joins and nothing reads more than it may.

// ReportRow is one row, keyed as the dashboard's report builder names its columns: text as
// strings, numbers as float64, times as milliseconds since the epoch.
type ReportRow = map[string]any

// ReportSource reads one source's rows over the last days days, newest first.
func (e *Engine) ReportSource(ctx context.Context, scope Scope, source string, days int, event string, limit int,
	now time.Time) ([]ReportRow, error) {
	if err := refuseKids(scope); err != nil {
		return nil, err
	}
	since := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -days+1)
	var out []ReportRow

	switch source {
	case "events":
		b := from(factRaw)
		b.column("toString(event_name)", "event").
			column("if(player_id IS NULL OR player_id = '', toString(install_id), player_id)", "player").
			column("toString(country_code)", "country").
			column("toString(platform)", "platform").
			column("toUnixTimestamp64Milli(event_time)", "at").
			column("props_int['level']", "level")
		rawScope(b, scope)
		b.gte("event_date", date(since))
		if event != "" {
			b.eq("event_name", event)
		}
		b.order("event_time DESC").withLimit(limit)
		err := e.readRaw(ctx, b.statement(), func(rows driver.Rows) error {
			var name, player, country, platform string
			var at, level int64
			if err := rows.Scan(&name, &player, &country, &platform, &at, &level); err != nil {
				return err
			}
			out = append(out, ReportRow{"event": name, "player_id": player, "country": country, "platform": platform,
				"at": at, "level": float64(level)})
			return nil
		})
		if err != nil {
			return nil, failed("events_raw", err)
		}

	case "players":
		b := from(factPlayers)
		lifetimeColumns(b)
		scopeAnd(b, scope)
		b.group("project_seq", "environment_seq", "device_id")
		b.havingThat("last >= ?", timestamp(since))
		b.withLimit(limit)
		err := e.readInOrder(ctx, b.statement(), func(rows driver.Rows) error {
			p, err := scanLifetime(rows, now)
			if err != nil {
				return err
			}
			payer := "no"
			if p.RevenueMicros > 0 {
				payer = "yes"
			}
			out = append(out, ReportRow{"player_id": p.InstallID, "country": p.Country, "platform": p.Platform,
				"first_seen": p.FirstSeen.UnixMilli(), "sessions": float64(p.Sessions),
				"spend": float64(p.RevenueMicros) / 1e6, "payer": payer})
			return nil
		})
		if err != nil {
			return nil, failed("player_lifetime", err)
		}

	case "purchases":
		b := from(factPurchases)
		b.column("toString(event_id)", "id").
			column("if(player_id IS NULL OR player_id = '', toString(install_id), player_id)", "player").
			column("toString(product_id)", "product").
			column("revenue_micros", "rev").
			column("toString(country_code)", "country").
			column("toString(store)", "store").
			column("toUnixTimestamp64Milli(event_time)", "at")
		scopeAnd(b, scope)
		b.gte("purchase_date", date(since))
		b.order("purchase_date DESC", "event_time DESC").withLimit(limit)
		b.deduplicated()
		err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
			var id, player, product, country, store string
			var rev, at int64
			if err := rows.Scan(&id, &player, &product, &rev, &country, &store, &at); err != nil {
				return err
			}
			out = append(out, ReportRow{"purchase_id": id, "player_id": player, "product": product,
				"price": float64(rev) / 1e6, "country": country, "store": store, "status": "recorded", "at": at})
			return nil
		})
		if err != nil {
			return nil, failed("purchases", err)
		}

	case "sessions":
		b := from(factSessions)
		b.column("any(session_id)", "sid").
			column("toString(anyLast(player_id))", "pl").
			column("toString(any(install_id))", "inst").
			column("any(country_code)", "country").
			column("any(platform)", "platform").
			column("toUnixTimestamp64Milli(min(started_at))", "st").
			column("sum(playtime_ms)", "pt")
		scopeAnd(b, scope)
		b.gte("activity_date", date(since))
		b.group("project_seq", "environment_seq", "activity_date", "device_id", "session_key")
		b.order("st DESC").withLimit(limit)
		err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
			var sid, player, install, country, platform string
			var started int64
			var playtime uint64
			if err := rows.Scan(&sid, &player, &install, &country, &platform, &started, &playtime); err != nil {
				return err
			}
			if player == "" || player == "\\N" {
				player = install
			}
			out = append(out, ReportRow{"session_id": sid, "player_id": player, "country": country, "platform": platform,
				"at": started, "minutes": float64(playtime) / 60000})
			return nil
		})
		if err != nil {
			return nil, failed("sessions", err)
		}

	default:
		return nil, fmt.Errorf("%q is not a report source", source)
	}
	return out, nil
}
