package webhooks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// What the dashboard reads, from ClickHouse, one table per question and none joined.

// Delivery is one delivery as it stands.
type Delivery struct {
	ID          uuid.UUID
	EventID     uuid.UUID
	Type        string
	Environment string
	Created     time.Time
	Attempts    int
	Outcome     string
	LastAt      time.Time
	LastCode    int
	LastError   string
	NextAt      *time.Time
	Manual      bool
	Test        bool
}

// DeliveryFilter narrows a list.
type DeliveryFilter struct {
	Outcomes []string
	Type     string
	// Before is the created time and id of the last delivery of the previous page.
	Before *DeliveryCursor
	Limit  int
}

// DeliveryCursor is where a page ended.
type DeliveryCursor struct {
	Created time.Time
	ID      uuid.UUID
}

// String writes a cursor as the API hands it out.
func (c DeliveryCursor) String() string {
	return c.Created.UTC().Format("2006-01-02T15:04:05.000Z") + "_" + c.ID.String()
}

// ParseCursor reads a cursor back.
func ParseCursor(s string) (*DeliveryCursor, error) {
	at, id, ok := strings.Cut(s, "_")
	if !ok {
		return nil, fmt.Errorf("no separator")
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", at)
	if err != nil {
		return nil, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return &DeliveryCursor{Created: t, ID: u}, nil
}

// Deliveries is one page of an endpoint's deliveries, newest first. FINAL, so each delivery is its
// latest try; read along the sort key, descending, so the newest parts are read first.
func Deliveries(ctx context.Context, conn driver.Conn, projectID int, endpoint uuid.UUID, f DeliveryFilter) ([]Delivery, *DeliveryCursor, error) {
	where := []string{"project_seq = ?", "endpoint_id = ?", "delivery_created >= now64(3) - INTERVAL 30 DAY"}
	args := []any{uint32(projectID), endpoint}
	if len(f.Outcomes) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(f.Outcomes)), ", ")
		where = append(where, "outcome IN ("+marks+")")
		for _, o := range f.Outcomes {
			args = append(args, o)
		}
	}
	if f.Type != "" {
		where = append(where, "type = ?")
		args = append(args, f.Type)
	}
	if f.Before != nil {
		where = append(where, "(delivery_created, delivery_id) < (toDateTime64(?, 3, 'UTC'), toUUID(?))")
		args = append(args, f.Before.Created.UTC().Format("2006-01-02 15:04:05.000"), f.Before.ID.String())
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT delivery_id, event_id, toString(type), toString(environment), delivery_created, attempts,
			toString(outcome), last_at, last_code, toString(last_error), next_at, manual, test
		FROM webhook_deliveries FINAL
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY delivery_created DESC, delivery_id DESC
		LIMIT ` + fmt.Sprint(limit+1)
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("read webhook_deliveries: %w", err)
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var attempts, code uint16
		var manual, test uint8
		if err := rows.Scan(&d.ID, &d.EventID, &d.Type, &d.Environment, &d.Created, &attempts, &d.Outcome, &d.LastAt,
			&code, &d.LastError, &d.NextAt, &manual, &test); err != nil {
			return nil, nil, err
		}
		d.Attempts, d.LastCode, d.Manual, d.Test = int(attempts), int(code), manual == 1, test == 1
		out = append(out, d)
	}
	var next *DeliveryCursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = &DeliveryCursor{Created: last.Created, ID: last.ID}
	}
	return out, next, rows.Err()
}

// Try is one try as the log kept it.
type Try struct {
	Number   int
	At       time.Time
	Code     int
	Error    string
	Ms       int
	Response string
	Outcome  string
	NextAt   *time.Time
	Manual   bool
	Test     bool
}

// DeliveryDetail is a delivery's tries, oldest first, with the event it carried.
type DeliveryDetail struct {
	Delivery
	Tries   []Try
	Payload string
}

// ReadDelivery reads one delivery: its tries, a short range of the attempts' sort key, and its
// event's body, a lookup on the events' key. Two statements, no join.
func ReadDelivery(ctx context.Context, conn driver.Conn, projectID int, endpoint, delivery uuid.UUID) (*DeliveryDetail, error) {
	rows, err := conn.Query(ctx, `
		SELECT attempt, at, status_code, toString(error), duration_ms, response, toString(outcome), next_at, manual, test,
			event_id, toString(type), toString(environment), delivery_created
		FROM webhook_attempts
		WHERE project_seq = ? AND endpoint_id = ? AND delivery_id = ?
		ORDER BY at`, uint32(projectID), endpoint, delivery)
	if err != nil {
		return nil, fmt.Errorf("read webhook_attempts: %w", err)
	}
	out := &DeliveryDetail{Delivery: Delivery{ID: delivery}}
	for rows.Next() {
		var t Try
		var number, code uint16
		var ms uint32
		var manual, test uint8
		if err := rows.Scan(&number, &t.At, &code, &t.Error, &ms, &t.Response, &t.Outcome, &t.NextAt, &manual, &test,
			&out.EventID, &out.Type, &out.Environment, &out.Created); err != nil {
			rows.Close()
			return nil, err
		}
		t.Number, t.Code, t.Ms, t.Manual, t.Test = int(number), int(code), int(ms), manual == 1, test == 1
		out.Tries = append(out.Tries, t)
	}
	rows.Close()
	if len(out.Tries) == 0 {
		return nil, nil
	}
	last := out.Tries[len(out.Tries)-1]
	out.Attempts, out.Outcome, out.LastAt, out.LastCode, out.LastError = last.Number, last.Outcome, last.At, last.Code, last.Error
	out.NextAt, out.Test, out.Manual = last.NextAt, last.Test, last.Manual
	payload, err := EventPayload(ctx, conn, projectID, out.EventID)
	if err != nil {
		return nil, err
	}
	out.Payload = payload
	return out, nil
}

// EventPayload is an event's body as it was sent; empty when it has aged out.
func EventPayload(ctx context.Context, conn driver.Conn, projectID int, event uuid.UUID) (string, error) {
	rows, err := conn.Query(ctx, `SELECT payload FROM webhook_events WHERE project_seq = ? AND event_id = ? LIMIT 1`,
		uint32(projectID), event)
	if err != nil {
		return "", fmt.Errorf("read webhook_events: %w", err)
	}
	defer rows.Close()
	var payload string
	if rows.Next() {
		if err := rows.Scan(&payload); err != nil {
			return "", err
		}
	}
	return payload, rows.Err()
}

// Figures are an endpoint's numbers over a period.
type Figures struct {
	Deliveries uint64
	FirstOK    uint64
	Tries      uint64
	OK         uint64
	Succeeded  uint64
	Failed     uint64
	P50, P95   float64
}

// FirstTry is the share of deliveries that landed at their first try.
func (f Figures) FirstTry() float64 {
	if f.Deliveries == 0 {
		return 1
	}
	return float64(f.FirstOK) / float64(f.Deliveries)
}

// TrySuccess is the share of tries answered 2xx.
func (f Figures) TrySuccess() float64 {
	if f.Tries == 0 {
		return 1
	}
	return float64(f.OK) / float64(f.Tries)
}

// Hour is one hour's tries.
type Hour struct {
	At     time.Time
	OK     uint64
	Failed uint64
}

// Health is an endpoint's last day against the day before, and its tries hour by hour.
func Health(ctx context.Context, conn driver.Conn, projectID int, endpoint uuid.UUID, now time.Time) (Figures, Figures, []Hour, error) {
	var cur, prev Figures
	end := now.UTC().Truncate(time.Hour).Add(time.Hour)
	dayAgo, twoDays := end.Add(-24*time.Hour), end.Add(-48*time.Hour)
	rows, err := conn.Query(ctx, `
		SELECT hour >= ? AS current,
			sum(deliveries), sum(first_ok), sum(tries), sum(ok), sum(succeeded), sum(failed),
			quantilesTDigestMerge(0.5, 0.95)(latency)
		FROM webhook_health_1h
		WHERE project_seq = ? AND endpoint_id = ? AND hour >= ? AND hour < ?
		GROUP BY current`, dayAgo, uint32(projectID), endpoint, twoDays, end)
	if err != nil {
		return cur, prev, nil, fmt.Errorf("read webhook_health_1h: %w", err)
	}
	for rows.Next() {
		var current uint8
		var f Figures
		var q []float32
		if err := rows.Scan(&current, &f.Deliveries, &f.FirstOK, &f.Tries, &f.OK, &f.Succeeded, &f.Failed, &q); err != nil {
			rows.Close()
			return cur, prev, nil, err
		}
		if len(q) == 2 {
			f.P50, f.P95 = float64(q[0]), float64(q[1])
		}
		if current == 1 {
			cur = f
		} else {
			prev = f
		}
	}
	rows.Close()

	hours := make([]Hour, 24)
	for i := range hours {
		hours[i].At = dayAgo.Add(time.Duration(i) * time.Hour)
	}
	hr, err := conn.Query(ctx, `
		SELECT hour, sum(ok), toUInt64(sum(tries) - sum(ok))
		FROM webhook_health_1h
		WHERE project_seq = ? AND endpoint_id = ? AND hour >= ? AND hour < ?
		GROUP BY hour ORDER BY hour`, uint32(projectID), endpoint, dayAgo, end)
	if err != nil {
		return cur, prev, nil, fmt.Errorf("read webhook_health_1h: %w", err)
	}
	defer hr.Close()
	for hr.Next() {
		var at time.Time
		var ok, failed uint64
		if err := hr.Scan(&at, &ok, &failed); err != nil {
			return cur, prev, nil, err
		}
		if i := int(at.Sub(dayAgo) / time.Hour); i >= 0 && i < 24 {
			hours[i].OK, hours[i].Failed = ok, failed
		}
	}
	return cur, prev, hours, hr.Err()
}

// ProjectHealth is each of a project's endpoints' last day, for the list.
func ProjectHealth(ctx context.Context, conn driver.Conn, projectID int, now time.Time) (map[uuid.UUID]Figures, error) {
	rows, err := conn.Query(ctx, `
		SELECT endpoint_id, sum(deliveries), sum(first_ok), sum(tries), sum(ok), sum(succeeded), sum(failed),
			quantilesTDigestMerge(0.5, 0.95)(latency)
		FROM webhook_health_1h
		WHERE project_seq = ? AND hour >= ?
		GROUP BY endpoint_id`, uint32(projectID), now.UTC().Add(-24*time.Hour).Truncate(time.Hour))
	if err != nil {
		return nil, fmt.Errorf("read webhook_health_1h: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]Figures{}
	for rows.Next() {
		var id uuid.UUID
		var f Figures
		var q []float32
		if err := rows.Scan(&id, &f.Deliveries, &f.FirstOK, &f.Tries, &f.OK, &f.Succeeded, &f.Failed, &q); err != nil {
			return nil, err
		}
		if len(q) == 2 {
			f.P50, f.P95 = float64(q[0]), float64(q[1])
		}
		out[id] = f
	}
	return out, rows.Err()
}

// EndpointDay is one endpoint's day across the platform, for staff.
type EndpointDay struct {
	ProjectID int
	Endpoint  uuid.UUID
	Figures
}

// PlatformHealth is the last day across every endpoint: the totals, and the endpoints with the most
// failed tries.
func PlatformHealth(ctx context.Context, conn driver.Conn, now time.Time, limit int) (Figures, []EndpointDay, error) {
	since := now.UTC().Add(-24 * time.Hour).Truncate(time.Hour)
	var total Figures
	rows, err := conn.Query(ctx, `
		SELECT project_seq, endpoint_id, sum(deliveries), sum(first_ok), sum(tries), sum(ok), sum(succeeded), sum(failed)
		FROM webhook_health_1h
		WHERE hour >= ?
		GROUP BY project_seq, endpoint_id`, since)
	if err != nil {
		return total, nil, fmt.Errorf("read webhook_health_1h: %w", err)
	}
	defer rows.Close()
	var all []EndpointDay
	for rows.Next() {
		var e EndpointDay
		var seq uint32
		if err := rows.Scan(&seq, &e.Endpoint, &e.Deliveries, &e.FirstOK, &e.Tries, &e.OK, &e.Succeeded, &e.Failed); err != nil {
			return total, nil, err
		}
		e.ProjectID = int(seq)
		total.Deliveries += e.Deliveries
		total.FirstOK += e.FirstOK
		total.Tries += e.Tries
		total.OK += e.OK
		total.Succeeded += e.Succeeded
		total.Failed += e.Failed
		if e.Tries > e.OK {
			all = append(all, e)
		}
	}
	// Worst first: most failed tries.
	sort.Slice(all, func(i, j int) bool { return all[i].Tries-all[i].OK > all[j].Tries-all[j].OK })
	if len(all) > limit {
		all = all[:limit]
	}
	return total, all, rows.Err()
}
