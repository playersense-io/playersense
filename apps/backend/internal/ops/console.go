// Package ops answers the operations console's analytics (project.md §56, §67): ingestion across
// every tenant, the ClickHouse node's health, and storage.
//
// Separate from package query because the two have opposite rules. Every statement there is a
// tenant's, with the tenant injected before anything else; every statement here is about the
// platform, reads every tenant together or ClickHouse's own system tables, and names no player.
// Keeping them apart means the query package never needs a way to leave its tenant out.
package ops

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/schemas"
)

// Console reads the platform's own numbers.
type Console struct {
	ch *chdb.Conn
}

// New returns a console over one ClickHouse.
func New(conn *chdb.Conn) *Console { return &Console{ch: conn} }

// bounded is every console read's settings: read-only, and small, because these are summaries and
// system tables and a console that could take the node would be the worst tool to have open while
// something is already wrong with it.
func bounded(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"readonly":           1,
		"max_execution_time": 20,
		"max_memory_usage":   uint64(1 << 30),
		"max_rows_to_read":   uint64(500_000_000),
	}))
}

func (c *Console) each(ctx context.Context, query string, args []any, scan func(driver.Rows) error) error {
	conn, err := c.ch.DB()
	if err != nil {
		return err
	}
	rows, err := conn.Query(bounded(ctx), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// Ingestion reads every tenant's ingestion over [from, to], by minute or hour, and the busiest
// projects, from the whole-project rollups (00013). The minute tier keeps seven days; a minute
// series is bounded by the caller to one.
func (c *Console) Ingestion(ctx context.Context, from, to time.Time, interval string, now time.Time) (*schemas.IngestionOut, error) {
	end := to.AddDate(0, 0, 1)
	table, bucket := "activity_1h", "hour"
	if interval == "minute" {
		table, bucket = "activity_1m", "minute"
	}
	out := &schemas.IngestionOut{From: from, To: to, Interval: bucket}

	if err := c.each(ctx, `
		SELECT `+bucket+` AS at, sum(events), sum(payload_bytes),
		       uniqCombinedMerge(12)(batches), uniqCombinedMerge(14)(players),
		       sum(sessions), sum(purchases), sum(late_events),
		       if(sum(events) = 0, 0, sum(lag_ms_sum) / sum(events)), max(lag_ms_max)
		FROM `+table+`
		WHERE `+bucket+` >= ? AND `+bucket+` < ?
		GROUP BY at ORDER BY at`,
		[]any{timestamp(from), timestamp(end)}, func(rows driver.Rows) error {
			var b schemas.IngestionBucketOut
			if err := rows.Scan(&b.At, &b.Events, &b.Bytes, &b.Batches, &b.Players, &b.Sessions,
				&b.Purchases, &b.LateEvents, &b.LagMsAvg, &b.LagMsMax); err != nil {
				return err
			}
			out.Series = append(out.Series, b)
			return nil
		}); err != nil {
		return nil, err
	}

	if err := c.each(ctx, `
		SELECT project_seq, any(org_seq), environment_seq, sum(events), sum(payload_bytes),
		       uniqCombinedMerge(12)(batches), uniqCombinedMerge(14)(players), sum(late_events),
		       if(sum(events) = 0, 0, sum(lag_ms_sum) / sum(events)), max(lag_ms_max)
		FROM activity_1h
		WHERE hour >= ? AND hour < ?
		GROUP BY project_seq, environment_seq
		ORDER BY sum(events) DESC
		LIMIT 100`,
		[]any{timestamp(from), timestamp(end)}, func(rows driver.Rows) error {
			var p schemas.IngestionProjectOut
			if err := rows.Scan(&p.ProjectSeq, &p.OrgSeq, &p.EnvironmentSeq, &p.Events, &p.Bytes,
				&p.Batches, &p.Players, &p.LateEvents, &p.LagMsAvg, &p.LagMsMax); err != nil {
				return err
			}
			out.Projects = append(out.Projects, p)
			return nil
		}); err != nil {
		return nil, err
	}

	// The last five minutes, from the minute tier, which is what "is the pipeline keeping up"
	// means right now.
	var events uint64
	if err := c.each(ctx, `
		SELECT sum(events), if(sum(events) = 0, 0, sum(lag_ms_sum) / sum(events)), max(lag_ms_max), max(minute)
		FROM activity_1m
		WHERE minute >= ?`,
		[]any{timestamp(now.Add(-5 * time.Minute))}, func(rows driver.Rows) error {
			return rows.Scan(&events, &out.Now.LagMsAvg, &out.Now.LagMsMax, &out.Now.LastMinute)
		}); err != nil {
		return nil, err
	}
	out.Now.EventsPerMinute = float64(events) / 5
	return out, nil
}

// Health reads the node's own view of itself from its system tables.
func (c *Console) Health(ctx context.Context, now time.Time) (*schemas.ClickHouseHealthOut, error) {
	out := &schemas.ClickHouseHealthOut{CheckedAt: now, Metrics: map[string]float64{}}

	steps := []struct {
		query string
		scan  func(driver.Rows) error
	}{
		{`SELECT version(), uptime()`, func(rows driver.Rows) error {
			var up uint32
			if err := rows.Scan(&out.Version, &up); err != nil {
				return err
			}
			out.UptimeSeconds = uint64(up)
			return nil
		}},
		{`SELECT table, is_readonly, is_session_expired, queue_size, inserts_in_queue, merges_in_queue,
		         absolute_delay, active_replicas, total_replicas
		  FROM system.replicas WHERE database = currentDatabase() ORDER BY table`, func(rows driver.Rows) error {
			var r schemas.ReplicaOut
			var readOnly, expired uint8
			if err := rows.Scan(&r.Table, &readOnly, &expired, &r.QueueSize, &r.InsertsInQueue,
				&r.MergesInQueue, &r.AbsoluteDelay, &r.ActiveReplicas, &r.TotalReplicas); err != nil {
				return err
			}
			r.ReadOnly, r.SessionExpired = readOnly == 1, expired == 1
			out.Replicas = append(out.Replicas, r)
			return nil
		}},
		{`SELECT table, elapsed, progress, num_parts, total_size_bytes_compressed, is_mutation
		  FROM system.merges WHERE database = currentDatabase() ORDER BY elapsed DESC`, func(rows driver.Rows) error {
			var m schemas.MergeOut
			var mutation uint8
			if err := rows.Scan(&m.Table, &m.Elapsed, &m.Progress, &m.Parts, &m.Bytes, &mutation); err != nil {
				return err
			}
			m.IsMutation = mutation == 1
			out.Merges = append(out.Merges, m)
			return nil
		}},
		{`SELECT table, mutation_id, command, create_time, parts_to_do, latest_fail_reason
		  FROM system.mutations WHERE database = currentDatabase() AND NOT is_done
		  ORDER BY create_time`, func(rows driver.Rows) error {
			var m schemas.MutationOut
			if err := rows.Scan(&m.Table, &m.ID, &m.Command, &m.CreatedAt, &m.PartsToDo, &m.LatestFailed); err != nil {
				return err
			}
			out.Mutations = append(out.Mutations, m)
			return nil
		}},
		{`SELECT table, partition, count() AS n
		  FROM system.parts WHERE database = currentDatabase() AND active
		  GROUP BY table, partition ORDER BY n DESC LIMIT 10`, func(rows driver.Rows) error {
			var p schemas.PartitionOut
			if err := rows.Scan(&p.Table, &p.Partition, &p.Parts); err != nil {
				return err
			}
			out.Hotspots = append(out.Hotspots, p)
			return nil
		}},
		{`SELECT name, path, free_space, total_space FROM system.disks ORDER BY name`, func(rows driver.Rows) error {
			var d schemas.DiskOut
			if err := rows.Scan(&d.Name, &d.Path, &d.Free, &d.Total); err != nil {
				return err
			}
			out.Disks = append(out.Disks, d)
			return nil
		}},
		{`SELECT metric, toFloat64(value) FROM system.asynchronous_metrics
		  WHERE metric IN ('OSMemoryTotal', 'OSMemoryAvailable', 'MemoryResident')
		  UNION ALL
		  SELECT metric, toFloat64(value) FROM system.metrics
		  WHERE metric IN ('Query', 'Merge', 'PartMutation', 'MemoryTracking',
		                   'BackgroundMergesAndMutationsPoolTask', 'ReplicatedFetch')`, func(rows driver.Rows) error {
			var name string
			var value float64
			if err := rows.Scan(&name, &value); err != nil {
				return err
			}
			out.Metrics[name] = value
			return nil
		}},
		{`SELECT name, value, last_error_time, last_error_message FROM system.errors
		  WHERE last_error_time > now() - INTERVAL 1 DAY
		  ORDER BY last_error_time DESC LIMIT 20`, func(rows driver.Rows) error {
			var e schemas.ServerErrorOut
			if err := rows.Scan(&e.Name, &e.Count, &e.LastAt, &e.Message); err != nil {
				return err
			}
			out.Errors = append(out.Errors, e)
			return nil
		}},
	}
	for _, s := range steps {
		if err := c.each(ctx, s.query, nil, s.scan); err != nil {
			return nil, err
		}
	}

	tables, err := c.tables(ctx)
	if err != nil {
		return nil, err
	}
	out.Tables = tables
	return out, nil
}

// tables is active parts by table and disk: which rows sit on the hot volume and which on cold.
func (c *Console) tables(ctx context.Context) ([]schemas.TableOut, error) {
	var out []schemas.TableOut
	err := c.each(ctx, `
		SELECT table, disk_name, count(), uniqExact(partition), sum(rows), sum(bytes_on_disk),
		       sum(data_uncompressed_bytes), max(modification_time)
		FROM system.parts WHERE database = currentDatabase() AND active
		GROUP BY table, disk_name ORDER BY sum(bytes_on_disk) DESC`, nil, func(rows driver.Rows) error {
		var t schemas.TableOut
		if err := rows.Scan(&t.Table, &t.Disk, &t.Parts, &t.Partitions, &t.Rows, &t.BytesOnDisk,
			&t.UncompressedSize, &t.LastModified); err != nil {
			return err
		}
		out = append(out, t)
		return nil
	})
	return out, err
}

// Storage reads storage on disk by table, and each project's metered bytes over [from, to].
func (c *Console) Storage(ctx context.Context, from, to time.Time) (*schemas.StorageOut, error) {
	out := &schemas.StorageOut{From: from, To: to}
	tables, err := c.tables(ctx)
	if err != nil {
		return nil, err
	}
	out.Tables = tables

	var total uint64
	if err := c.each(ctx, `
		SELECT project_seq, any(org_seq), environment_seq, sum(events), sum(payload_bytes)
		FROM activity_1h
		WHERE hour >= ? AND hour < ?
		GROUP BY project_seq, environment_seq
		ORDER BY sum(payload_bytes) DESC
		LIMIT 500`,
		[]any{timestamp(from), timestamp(to.AddDate(0, 0, 1))}, func(rows driver.Rows) error {
			var p schemas.StorageProjectOut
			if err := rows.Scan(&p.ProjectSeq, &p.OrgSeq, &p.EnvironmentSeq, &p.Events, &p.Bytes); err != nil {
				return err
			}
			total += p.Bytes
			out.Projects = append(out.Projects, p)
			return nil
		}); err != nil {
		return nil, err
	}
	for i := range out.Projects {
		if total > 0 {
			out.Projects[i].Share = float64(out.Projects[i].Bytes) / float64(total)
		}
	}
	return out, nil
}
