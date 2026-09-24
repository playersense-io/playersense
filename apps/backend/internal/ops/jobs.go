package ops

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/jobs"
	"playersense.dev/backend/internal/schemas"
)

// Jobs reads the worker's passes over [from, to] from job_runs (00020): one summary per job, every
// job listed even when it has not run, and the latest two hundred passes.
func (c *Console) Jobs(ctx context.Context, from, to time.Time, job string, failedOnly bool) ([]schemas.JobSummaryOut, []schemas.JobRunOut, error) {
	end := to.AddDate(0, 0, 1)
	byJob := map[string]*schemas.JobSummaryOut{}
	for _, name := range jobs.Names() {
		byJob[name] = &schemas.JobSummaryOut{Job: name}
	}

	if err := c.each(ctx, `
		SELECT job, count(), countIf(NOT ok),
		       max(started_at), argMax(ok, started_at),
		       maxIf(started_at, ok), maxIf(started_at, NOT ok), argMaxIf(error, started_at, NOT ok),
		       avg(dateDiff('millisecond', started_at, finished_at)),
		       max(dateDiff('millisecond', started_at, finished_at)),
		       argMax(counts, started_at)
		FROM job_runs
		WHERE started_at >= ? AND started_at < ?
		GROUP BY job`,
		[]any{timestamp(from), timestamp(end)}, func(rows driver.Rows) error {
			var (
				name                        string
				runs, failures              uint64
				last, lastSuccess, lastFail time.Time
				lastOK                      bool
				lastError                   string
				avg                         float64
				maxMs                       int64
				counts                      map[string]int64
			)
			if err := rows.Scan(&name, &runs, &failures, &last, &lastOK, &lastSuccess, &lastFail,
				&lastError, &avg, &maxMs, &counts); err != nil {
				return err
			}
			one, found := byJob[name]
			if !found {
				one = &schemas.JobSummaryOut{Job: name}
				byJob[name] = one
			}
			one.Runs, one.Failures, one.LastOK, one.LastError = runs, failures, lastOK, lastError
			one.AvgMs, one.MaxMs, one.LastCounts = avg, maxMs, counts
			one.LastRunAt, one.LastSuccessAt, one.LastFailureAt = when(last), when(lastSuccess), when(lastFail)
			return nil
		}); err != nil {
		return nil, nil, err
	}
	summaries := make([]schemas.JobSummaryOut, 0, len(byJob))
	for _, name := range jobs.Names() {
		summaries = append(summaries, *byJob[name])
		delete(byJob, name)
	}
	for _, rest := range byJob {
		summaries = append(summaries, *rest)
	}

	query := `
		SELECT job, started_at, finished_at, host, ok, error, counts
		FROM job_runs
		WHERE started_at >= ? AND started_at < ?`
	args := []any{timestamp(from), timestamp(end)}
	if job != "" {
		query += ` AND job = ?`
		args = append(args, job)
	}
	if failedOnly {
		query += ` AND NOT ok`
	}
	query += ` ORDER BY started_at DESC LIMIT 200`
	recent := []schemas.JobRunOut{}
	if err := c.each(ctx, query, args, func(rows driver.Rows) error {
		var run schemas.JobRunOut
		if err := rows.Scan(&run.Job, &run.StartedAt, &run.FinishedAt, &run.Host, &run.OK, &run.Error,
			&run.Counts); err != nil {
			return err
		}
		recent = append(recent, run)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return summaries, recent, nil
}

// when is a time ClickHouse returns as the epoch when there was none, as nil.
func when(t time.Time) *time.Time {
	if t.Unix() <= 0 {
		return nil
	}
	t = t.UTC()
	return &t
}
