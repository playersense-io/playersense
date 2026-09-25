package query

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// The explorer: the reads behind the Events, Sessions, Players, Revenue, Purchases and flag
// pages (project.md §55), which ask narrower questions than §57's two query shapes.
//
// The rules are the planner's, unchanged:
//
//   - every statement is built by the same single-table builder, so none of them can join;
//   - the tenant is a parameter, injected by scopeAnd before anything else;
//   - every statement runs under a set of limits, and a read of events_raw under the tighter
//     raw set, bounded as well by the tenant key prefix and a day range in the statement;
//   - a count of people below the suppression threshold is withheld rather than shown.
//
// What differs is that these answer one page each rather than one chart, so a method may run
// several statements. Each is its own scan of its own fact; they are never combined in SQL.

// Window is an inclusive range of whole days, in UTC.
type Window struct {
	From time.Time
	To   time.Time
}

// NewWindow truncates both ends to their day.
func NewWindow(from, to time.Time) Window {
	return Window{From: from.UTC().Truncate(24 * time.Hour), To: to.UTC().Truncate(24 * time.Hour)}
}

// Days is how many days the window spans, both ends included.
func (w Window) Days() int { return int(w.To.Sub(w.From).Hours()/24) + 1 }

// Previous is the window of the same length that ends the day before this one starts, which is
// what every "against the previous period" figure is measured against.
func (w Window) Previous() Window {
	n := w.Days()
	return Window{From: w.From.AddDate(0, 0, -n), To: w.From.AddDate(0, 0, -1)}
}

// end is the first instant after the window, for timestamp columns.
func (w Window) end() time.Time { return w.To.AddDate(0, 0, 1) }

// date is a UTC day, as every day column is kept; a time read from Postgres is in local time.
func date(t time.Time) string      { return t.UTC().Format("2006-01-02") }
func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// Suppressed reports what a page withheld and under which threshold (§32.2), in the shape every
// analytics answer already carries it.
type Suppressed struct {
	Cells     int
	Threshold int
}

// withheld reports whether a count of people is too thin to show, and counts it if so.
func (s *Suppressed) withheld(n uint64) bool {
	if thin(n, uint64(s.Threshold)) {
		s.Cells++
		return true
	}
	return false
}

// read runs one statement under the summary limits and hands each row to scan.
func (e *Engine) read(ctx context.Context, stmt Statement, scan func(driver.Rows) error) error {
	return e.readWith(boundBy(ctx, e.limits, nil), stmt, scan)
}

// readRaw runs one statement against events_raw under the raw limits.
func (e *Engine) readRaw(ctx context.Context, stmt Statement, scan func(driver.Rows) error) error {
	return e.readWith(boundBy(ctx, e.raw, nil), stmt, scan)
}

// readInOrder runs a statement that aggregates a lifetime fact by its whole sort key.
//
// optimize_aggregation_in_order lets ClickHouse finish each key as soon as the key changes, so a
// directory of a million devices holds one device at a time rather than a hash table of all of
// them. The ORDER BY and LIMIT after it keep only the page.
func (e *Engine) readInOrder(ctx context.Context, stmt Statement, scan func(driver.Rows) error) error {
	return e.readWith(boundBy(ctx, e.limits, clickhouse.Settings{
		"optimize_aggregation_in_order": 1,
	}), stmt, scan)
}

func (e *Engine) readWith(ctx context.Context, stmt Statement, scan func(driver.Rows) error) error {
	conn, err := e.ch.DB()
	if err != nil {
		return err
	}
	rows, err := conn.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return overLimit(err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return overLimit(rows.Err())
}

// The ClickHouse errors that mean a statement reached one of its limits rather than failed.
const (
	codeTimeout         = 159
	codeTooManyRows     = 158
	codeTooManyBytes    = 307
	codeTooManyRowsOrBy = 396
	codeMemoryLimit     = 241
)

// overLimit turns a statement that reached its limits into a refusal the caller can act on.
//
// That is a fact about the question, not an outage: the same request will fail the same way on
// retry, so answering 503 would invite exactly the retries that make it worse. It is refused with
// what to change instead, which is §32's intent for every query that could be expensive.
func overLimit(err error) error {
	var ex *clickhouse.Exception
	if !errors.As(err, &ex) {
		return err
	}
	switch ex.Code {
	case codeTimeout, codeTooManyRows, codeTooManyBytes, codeTooManyRowsOrBy, codeMemoryLimit:
		return refuse("range",
			"answering this reads more than one question is allowed to (%s); narrow the range, "+
				"the events or the filters", ex.Name)
	}
	return err
}

// ErrNotFound is a thing the page names that the project has never sent: an event name, a
// player, a flag. A 404 rather than an empty page, because an empty page reads as "nothing
// happened" and the truth is "there is no such thing".
var ErrNotFound = errors.New("not found")

// ReadError marks a failure to read a fact, as opposed to a question the facts cannot answer.
type ReadError struct {
	What string
	Err  error
}

func (e *ReadError) Error() string { return fmt.Sprintf("read %s: %v", e.What, e.Err) }
func (e *ReadError) Unwrap() error { return e.Err }

func failed(what string, err error) error { return &ReadError{What: what, Err: err} }

// rawScope injects the tenant into a read of events_raw, whole key prefix first.
func rawScope(b *builder, scope Scope) {
	b.eq("org_seq", scope.OrgSeq)
	scopeAnd(b, scope)
}
