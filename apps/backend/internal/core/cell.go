package core

import (
	"context"
	"fmt"
	"log/slog"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/cell"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/pgdb"
)

// CellIdentity is which data plane a process belongs to (§6).
//
// Published as a singleton so a handler can ask without reading configuration, and a
// distinct type rather than a bare string so it cannot be confused with any other
// singleton string in the container.
type CellIdentity struct {
	Slug      string
	Region    string
	IngestURL string
	QueryURL  string
	ConfigURL string
}

// CellFrom reads the identity out of the configuration.
func CellFrom(settings config.Settings) CellIdentity {
	return CellIdentity{
		Slug:      settings.Cell,
		Region:    settings.CellRegion,
		IngestURL: settings.CellIngestURL,
		QueryURL:  settings.CellQueryURL,
		ConfigURL: settings.ConfigAPIPublicURL,
	}
}

// CellCheck confirms at start-up that this process's cell exists in the registry and
// agrees with how the process is configured.
//
// A typo in CELL is otherwise silent and expensive. Keys would resolve, batches would be
// accepted, and every one of them would be written under a slug that names nothing: into a
// Valkey keyspace nobody reads, a ClickHouse replica path nobody shares, and an event bus
// no writer is filtering for. Nothing fails; the data simply goes somewhere else.
//
// It dials the registry itself rather than borrowing the application's pool, because
// lifecycle components start in parallel — one that reads another's connection is one that
// works until the scheduler happens to run them in the other order.
type CellCheck struct {
	identity CellIdentity
	dsn      string
}

// NewCellCheck returns the start-up check for the configured cell.
func NewCellCheck(settings config.Settings) *CellCheck {
	return &CellCheck{identity: CellFrom(settings), dsn: settings.GlobalPostgresDSN}
}

// Name identifies the component in start-up logs.
func (c *CellCheck) Name() string { return "cell-registration" }

// Stop has nothing to release: the check closes its connection before it returns.
func (c *CellCheck) Stop(context.Context) error { return nil }

// Start reads the registry and compares it with the configuration.
//
// An unreachable registry is a warning, not a refusal. §64 requires ingestion to keep
// working through a control-plane outage, and a gateway that will not boot while the
// control plane is down turns a degraded minute into a dead one — the cache in Valkey is
// still warm and batches can still be accepted from it.
//
// A registry that answers and disagrees is a different matter. That is a misconfiguration
// rather than an outage, and booting anyway would be choosing to write player data into a
// jurisdiction nobody selected.
func (c *CellCheck) Start(ctx context.Context) error {
	db := pgdb.NewGlobal(c.dsn)
	if err := db.Start(ctx); err != nil {
		slog.Default().Warn("could not reach the cell registry; continuing on the configured identity",
			"cell", c.identity.Slug, "error", err)
		return nil
	}
	defer db.Close()

	home, err := db.Client.Cell.Query().Where(cell.Slug(c.identity.Slug)).Only(ctx)
	if err != nil {
		if global.IsNotFound(err) {
			return fmt.Errorf("CELL is %q, which is not in the registry; "+
				"run `migrate cell up` to provision it, or correct the setting",
				c.identity.Slug)
		}
		slog.Default().Warn("could not read the cell registry; continuing on the configured identity",
			"cell", c.identity.Slug, "error", err)
		return nil
	}

	if home.Region != c.identity.Region {
		return fmt.Errorf("cell %s is registered in region %q but CELL_REGION says %q; "+
			"one of the two is wrong, and guessing which would place player data under the wrong law",
			c.identity.Slug, home.Region, c.identity.Region)
	}

	slog.Default().Info("cell confirmed",
		"cell", home.Slug, "region", home.Region, "status", home.Status,
		"ingest_url", home.IngestURL, "query_url", home.QueryURL,
		"config_url", home.ConfigURL)
	return nil
}
