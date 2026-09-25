// Package logs is Cloud Logging (ADR 0050): the game's own log lines, exceptions and crashes,
// written to ClickHouse from a queue of their own, grouped into issues, searched, and metered by
// the gigabyte kept.
//
// The pieces, in the order a record travels through them:
//
//	POST /v1/logs      the gateway bounds and sizes the batch (ingest.NormalizeLogs) and publishes it
//	Queue              a topic of its own, so logs never queue in front of analytics
//	Writer             groups each exception into an issue and inserts the rows
//	logs, logs_volume_1m, log_issues, log_issue_hours    ClickHouse, db/clickhouse/migrations/00025
//	Reader             every question the dashboard asks, each one table and no joins
//	Storage            the bytes a project keeps, per day, for the meter
package logs

import (
	"context"

	"playersense.dev/backend/internal/eventbus"
)

// GroupWriter is the log writer's subscription on the logs topic.
const GroupWriter = "logs-writer"

// Queue is the logs topic.
//
// A type of its own around the same interface, so the application can hold two buses at once and
// a handler asks for the one it means: the framework resolves a dependency by its type, and two
// values of eventbus.Bus would be one dependency declared twice.
type Queue struct {
	eventbus.Bus
}

// NewQueue wraps a bus as the logs topic.
func NewQueue(bus eventbus.Bus) *Queue { return &Queue{Bus: bus} }

// Name tells the two buses apart in start-up and shutdown logs.
func (q *Queue) Name() string { return "logs-queue" }

// Start opens the bus underneath, if it has anything to open.
func (q *Queue) Start(ctx context.Context) error {
	if starter, ok := q.Bus.(interface{ Start(context.Context) error }); ok {
		return starter.Start(ctx)
	}
	return nil
}

// Stop closes the bus underneath, if it has anything to close.
func (q *Queue) Stop(ctx context.Context) error {
	if stopper, ok := q.Bus.(interface{ Stop(context.Context) error }); ok {
		return stopper.Stop(ctx)
	}
	return nil
}
