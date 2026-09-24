// Package jobs records each pass of the worker's jobs, for the operations console's retention
// jobs page (project.md §56, §67).
//
// Best effort, and on purpose: a pass that did its work and could not write its record has still
// done its work, and failing it for the record would retry an erasure to say so. A record that
// could not be written is logged, which is where a missing row would be explained.
package jobs

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"time"
)

// The jobs, as the console names them.
const (
	Derivation    = "derivation"
	Erasure       = "erasure"
	Export        = "export"
	ArchiveExpiry = "archive_expiry"
	UsageMeter    = "usage_meter"
	CreditExpiry  = "credit_expiry"

	RetentionEnforce = "retention_enforce"
	ProjectDeletion  = "project_deletion"
)

// Names is every job, in the order the worker runs them.
func Names() []string {
	return []string{Derivation, Erasure, Export, ProjectDeletion, RetentionEnforce, ArchiveExpiry, UsageMeter, CreditExpiry}
}

// Run is one pass of one job.
type Run struct {
	Job      string
	Started  time.Time
	Finished time.Time
	Err      error
	Counts   map[string]int64
}

// Recorder keeps runs. Nil records nothing, so a test or a one-off pass need not configure one.
type Recorder interface {
	Record(ctx context.Context, run Run)
}

// Pool supplies the ClickHouse handle, resolved per write for the reason scheduler.Pool gives:
// the application opens the pool after the recorder is built.
type Pool interface {
	DB() (*sql.DB, error)
}

// ClickHouse writes runs to job_runs (00020).
type ClickHouse struct {
	Pool Pool
	Log  *slog.Logger
	Host string
}

// NewClickHouse returns a recorder that names this machine as the host.
func NewClickHouse(pool Pool, log *slog.Logger) *ClickHouse {
	host, _ := os.Hostname()
	if log == nil {
		log = slog.Default()
	}
	return &ClickHouse{Pool: pool, Log: log, Host: host}
}

// Record writes one run, and logs rather than returns a failure.
func (c *ClickHouse) Record(ctx context.Context, run Run) {
	db, err := c.Pool.DB()
	if err != nil {
		c.Log.Warn("could not record a job run", "job", run.Job, "error", err)
		return
	}
	message := ""
	if run.Err != nil {
		message = run.Err.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	counts := run.Counts
	if counts == nil {
		counts = map[string]int64{}
	}
	// Its own short deadline: a shutdown that cancelled the pass should still get its record.
	write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(write,
		`INSERT INTO job_runs (job, started_at, finished_at, host, ok, error, counts) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.Job, run.Started.UTC(), run.Finished.UTC(), c.Host, run.Err == nil, message, counts); err != nil {
		c.Log.Warn("could not record a job run", "job", run.Job, "error", err)
	}
}

// Time runs fn as job and records it: when it started and finished, whether it failed, and the
// counts it reports.
func Time(ctx context.Context, r Recorder, job string, now func() time.Time, fn func() (map[string]int64, error)) error {
	started := now()
	counts, err := fn()
	if r != nil {
		r.Record(ctx, Run{Job: job, Started: started, Finished: now(), Err: err, Counts: counts})
	}
	return err
}
