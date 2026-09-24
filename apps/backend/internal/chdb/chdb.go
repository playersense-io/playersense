// Package chdb opens a cell's ClickHouse connection.
//
// The native protocol rather than database/sql, because the writer inserts column blocks:
// `PrepareBatch` appends into columnar buffers and sends one block, which is the shape
// ClickHouse wants and the shape a row-oriented driver has to reassemble.
package chdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ErrNotStarted reports use of a connection that never opened.
var ErrNotStarted = errors.New("clickhouse is not started")

// Conn is a ClickHouse connection with a lifecycle.
type Conn struct {
	dsn string

	conn driver.Conn
}

// New returns an unconnected client. Construction dials nothing, so the application opens
// it before consuming and closes it after draining.
func New(dsn string) *Conn { return &Conn{dsn: dsn} }

// Name identifies the component in start-up and shutdown logs.
func (c *Conn) Name() string { return "clickhouse" }

// Start dials ClickHouse.
func (c *Conn) Start(ctx context.Context) error {
	// Once, as pgdb.DB.Start is and for its reason: a second start would replace an open
	// connection without closing it.
	if c.conn != nil {
		return nil
	}
	opts, err := clickhouse.ParseDSN(c.dsn)
	if err != nil {
		return fmt.Errorf("parse CLICKHOUSE_DSN: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return fmt.Errorf("open clickhouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		conn.Close()
		return fmt.Errorf("connect to clickhouse: %w", err)
	}
	c.conn = conn
	return nil
}

// Stop closes the connection, after the consumer has stopped.
func (c *Conn) Stop(context.Context) error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Ready reports whether Start has run.
func (c *Conn) Ready() bool { return c != nil && c.conn != nil }

// DB returns the underlying connection, or an error rather than a nil no caller checked.
func (c *Conn) DB() (driver.Conn, error) {
	if !c.Ready() {
		return nil, ErrNotStarted
	}
	return c.conn, nil
}

// SQL is the same database behind database/sql, for callers that execute statements
// rather than append column blocks.
//
// Two handles rather than one because the two shapes genuinely differ. The writer's path
// is columnar — PrepareBatch appends into buffers and sends a block — and that is not
// something database/sql expresses. The derivations are the opposite: a handful of large
// INSERT ... SELECT and ALTER TABLE ... REPLACE PARTITION statements per night, which is
// exactly what database/sql is for and which the native driver's block API cannot express
// at all.
type SQL struct {
	dsn string

	db *sql.DB
}

// NewSQL returns an unopened handle. Construction dials nothing, like Conn.
func NewSQL(dsn string) *SQL { return &SQL{dsn: dsn} }

// Name identifies the component in start-up and shutdown logs.
func (s *SQL) Name() string { return "clickhouse-sql" }

// Start opens the pool and verifies it answers.
func (s *SQL) Start(ctx context.Context) error {
	// Once, as Conn.Start is.
	if s.db != nil {
		return nil
	}
	opts, err := clickhouse.ParseDSN(s.dsn)
	if err != nil {
		return fmt.Errorf("parse CLICKHOUSE_DSN: %w", err)
	}
	db := clickhouse.OpenDB(opts)

	// Bounded for the reason the PostgreSQL pools are: an unbounded pool answers a burst
	// by opening connections until the server refuses them, which converts a slow query
	// into an outage. The derivation runs a few statements at a time and never benefits
	// from more.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("connect to clickhouse: %w", err)
	}
	s.db = db
	return nil
}

// Stop closes the pool, after anything using it has stopped.
func (s *SQL) Stop(context.Context) error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Ready reports whether Start has run.
func (s *SQL) Ready() bool { return s != nil && s.db != nil }

// DB returns the pool, or an error rather than a nil no caller checked.
func (s *SQL) DB() (*sql.DB, error) {
	if !s.Ready() {
		return nil, ErrNotStarted
	}
	return s.db, nil
}
