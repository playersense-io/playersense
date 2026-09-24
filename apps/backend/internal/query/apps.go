package query

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// Apps reads where a project's events come from, every environment together (00022): a few rows
// per platform and SDK, so it is a key-prefix read of a table that never grows with traffic.
func (e *Engine) Apps(ctx context.Context, projectSeq uint32) ([]schemas.ProjectAppOut, error) {
	b := from(factApps)
	b.column("platform", "pl").
		column("sdk_name", "sdk").
		column("min(first_seen)", "first").
		column("max(last_seen)", "last").
		column("sum(events)", "n").
		column("argMaxMerge(app_version)", "app").
		column("argMaxMerge(sdk_version)", "sdkv")
	b.eq("project_seq", projectSeq)
	b.group("pl", "sdk").order("first", "pl", "sdk").withLimit(200)

	out := []schemas.ProjectAppOut{}
	err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var app schemas.ProjectAppOut
		if err := rows.Scan(&app.Platform, &app.SDK, &app.FirstSeen, &app.LastSeen, &app.Events,
			&app.AppVersion, &app.SDKVersion); err != nil {
			return err
		}
		out = append(out, app)
		return nil
	})
	if err != nil {
		return nil, failed("project_apps", err)
	}
	return out, nil
}
