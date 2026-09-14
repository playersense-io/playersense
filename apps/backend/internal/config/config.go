// Package config holds the settings every PlayerSense binary reads at start-up.
package config

import (
	"muzak.dev/framework"
)

// Settings is the service configuration, read once at start-up from the environment and
// from a .env file when one is present.
//
// Every connection string carries its own port rather than composing one from a host and
// a port setting, so that the ports declared in .env are the only place a port appears.
type Settings struct {
	// ClickHouseDSN addresses the analytics database (§13).
	ClickHouseDSN string `env:"CLICKHOUSE_DSN" required:"true" secret:"true"`
	// PostgresDSN addresses the control plane (§11).
	PostgresDSN string `env:"POSTGRES_DSN" required:"true" secret:"true"`
	// ValkeyDSN addresses the cache and the device-state store (§12).
	ValkeyDSN string `env:"VALKEY_DSN"`

	// MigrationsDir is the repository-root directory holding both migration sets.
	MigrationsDir string `env:"MIGRATIONS_DIR" default:"db"`
}

// Load reads the configuration, reporting every problem at once.
func Load(envFiles ...string) (Settings, error) {
	sources := make([]muzak.ConfigOption, 0, len(envFiles))
	for _, f := range envFiles {
		sources = append(sources, muzak.EnvFile(f))
	}
	return muzak.LoadConfig[Settings](sources...)
}

// MustLoad is Load for a program that cannot run without its configuration.
func MustLoad(envFiles ...string) Settings {
	settings, err := Load(envFiles...)
	if err != nil {
		panic(err)
	}
	return settings
}
