// Package chdb opens a cell's ClickHouse connection.
//
// The native protocol rather than database/sql, because the writer inserts column blocks:
// `PrepareBatch` appends into columnar buffers and sends one block, which is the shape
// ClickHouse wants and the shape a row-oriented driver has to reassemble.
package chdb

import (
	"context"
	"errors"
	"fmt"

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
