package query

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// Flag insights (00017): every read from feature_exposures_daily.

// maxFlags bounds the flags list.
const maxFlags = 2000

// FlagExposures lists every flag the game read in the window, with the window before and each day.
func (e *Engine) FlagExposures(ctx context.Context, scope Scope, w Window) (*schemas.FlagExposureListOut, error) {
	out := &schemas.FlagExposureListOut{From: w.From, To: w.To, SuppressionThreshold: e.k}
	withheld := Suppressed{Threshold: e.k}
	cut := date(w.From)

	type sum struct {
		exposures, previous, players uint64
		daily                        []uint64
	}
	flags := map[string]*sum{}
	var order []string

	b := from(factExposures)
	b.column("toString(flag)", "f").
		columnWith("sumIf(exposures, day >= ?)", "cur", cut).
		columnWith("sumIf(exposures, day < ?)", "prev", cut).
		columnWith("uniqCombinedMergeIf(12)(players, day >= ?)", "pl", cut)
	scopeAnd(b, scope)
	b.between("day", date(w.Previous().From), date(w.To))
	b.group("f").order("cur DESC", "f").withLimit(maxFlags)
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var flag string
		s := &sum{daily: make([]uint64, w.Days())}
		if err := rows.Scan(&flag, &s.exposures, &s.previous, &s.players); err != nil {
			return err
		}
		flags[flag] = s
		order = append(order, flag)
		return nil
	}); err != nil {
		return nil, failed("feature_exposures_daily", err)
	}

	d := from(factExposures)
	d.column("toString(flag)", "f").column("toDateTime(day, 'UTC')", "d").column("sum(exposures)", "n")
	scopeAnd(d, scope)
	d.between("day", date(w.From), date(w.To))
	d.groupOn("f", "d").withLimit(e.limits.MaxRows)
	if err := e.read(ctx, d.statement(), func(rows driver.Rows) error {
		var flag string
		var at time.Time
		var n uint64
		if err := rows.Scan(&flag, &at, &n); err != nil {
			return err
		}
		if s := flags[flag]; s != nil {
			if i := int(at.UTC().Sub(w.From).Hours() / 24); i >= 0 && i < len(s.daily) {
				s.daily[i] = n
			}
		}
		return nil
	}); err != nil {
		return nil, failed("feature_exposures_daily", err)
	}

	for _, flag := range order {
		s := flags[flag]
		out.Flags = append(out.Flags, schemas.FlagExposureSumOut{
			Flag: flag, Exposures: s.exposures, PreviousExposures: s.previous,
			Players: shown(&withheld, s.players), Daily: s.daily,
		})
	}
	out.SuppressedCells = withheld.Cells
	return out, nil
}

// FlagExposure answers one flag's insights. version narrows to one config version when above zero.
func (e *Engine) FlagExposure(ctx context.Context, scope Scope, flag string, w Window, version int) (*schemas.FlagExposureOut, error) {
	out := &schemas.FlagExposureOut{Flag: flag, From: w.From, To: w.To, Approximate: true, SuppressionThreshold: e.k}
	withheld := Suppressed{Threshold: e.k}

	start := func(columns func(*builder)) *builder {
		b := from(factExposures)
		columns(b)
		scopeAnd(b, scope)
		b.eq("flag", flag).between("day", date(w.From), date(w.To))
		if version > 0 {
			b.eq("config_version", version)
		}
		return b
	}

	var defaults uint64
	values := start(func(b *builder) {
		b.column("value", "v").column("published", "pub").column("sum(exposures)", "n").
			column("uniqCombinedMerge(12)(players)", "pl")
	})
	values.group("v", "pub").order("n DESC", "v").withLimit(1000)
	if err := e.read(ctx, values.statement(), func(rows driver.Rows) error {
		var r schemas.FlagValueOut
		var published uint8
		var players uint64
		if err := rows.Scan(&r.Value, &published, &r.Exposures, &players); err != nil {
			return err
		}
		r.Published = published == 1
		r.Players = shown(&withheld, players)
		out.Exposures += r.Exposures
		if !r.Published {
			defaults += r.Exposures
		}
		out.Values = append(out.Values, r)
		return nil
	}); err != nil {
		return nil, failed("feature_exposures_daily", err)
	}
	if len(out.Values) == 0 {
		return nil, ErrNotFound
	}
	out.DefaultShare = float64(defaults) / float64(out.Exposures)

	days := start(func(b *builder) {
		b.column("toDateTime(day, 'UTC')", "d").column("value", "v").column("published", "pub").
			column("sum(exposures)", "n").column("uniqCombinedMerge(12)(players)", "pl")
	})
	days.groupOn("d", "v", "pub").withLimit(e.limits.MaxRows)
	if err := e.read(ctx, days.statement(), func(rows driver.Rows) error {
		var r schemas.FlagValueDayOut
		var published uint8
		var players uint64
		if err := rows.Scan(&r.Day, &r.Value, &published, &r.Exposures, &players); err != nil {
			return err
		}
		r.Published = published == 1
		r.Players = shown(&withheld, players)
		out.Days = append(out.Days, r)
		return nil
	}); err != nil {
		return nil, failed("feature_exposures_daily", err)
	}

	versions := start(func(b *builder) {
		b.column("config_version", "cv").column("sum(exposures)", "n").
			column("uniqCombinedMerge(12)(players)", "pl")
	})
	versions.groupOn("cv").withLimit(1000)
	if err := e.read(ctx, versions.statement(), func(rows driver.Rows) error {
		var r schemas.FlagVersionOut
		var players uint64
		if err := rows.Scan(&r.ConfigVersion, &r.Exposures, &players); err != nil {
			return err
		}
		r.Players = shown(&withheld, players)
		out.Versions = append(out.Versions, r)
		return nil
	}); err != nil {
		return nil, failed("feature_exposures_daily", err)
	}

	builds := start(func(b *builder) {
		b.column("toString(app_version)", "av").column("sum(exposures)", "n").
			column("sumIf(exposures, published = 0)", "fallback").
			column("uniqCombinedMerge(12)(players)", "pl")
	})
	builds.group("av").order("n DESC", "av").withLimit(200)
	if err := e.read(ctx, builds.statement(), func(rows driver.Rows) error {
		var r schemas.FlagAppVersionOut
		var players uint64
		if err := rows.Scan(&r.AppVersion, &r.Exposures, &r.DefaultExposures, &players); err != nil {
			return err
		}
		r.Players = shown(&withheld, players)
		out.AppVersions = append(out.AppVersions, r)
		return nil
	}); err != nil {
		return nil, failed("feature_exposures_daily", err)
	}

	out.SuppressedCells = withheld.Cells
	return out, nil
}
