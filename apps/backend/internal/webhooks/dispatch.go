package webhooks

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/role"
	"playersense.dev/backend/ent/webhookendpoint"
	"playersense.dev/backend/ent/webhookretry"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/pgdb"
)

// Dispatcher delivers events, in the worker. Several may run, in one process or many: every claim
// is FOR UPDATE SKIP LOCKED with a lease, so two never take the same row, and a row whose worker
// died is taken again once its lease passes.
//
// Each pass takes a batch of new events and a batch of retries that are due. A delivery is tried
// at once, on a pool of Workers shared by every endpoint, each endpoint allowed PerEndpoint at a
// time: an endpoint that is slow fills its own slots and nobody else's. A delivery that finds its
// endpoint failing, or its slots still full after two seconds (a slow endpoint, not a burst), is not
// tried and waits in webhook_retries, which costs it a row instead of a slot held for ten seconds. An endpoint that fails five times running
// is left alone for thirty seconds, then a minute, doubling to ten, and tried with one delivery
// before the rest follow; its deliveries wait meanwhile without spending their tries.
type Dispatcher struct {
	DB          *pgdb.DB
	Deliver     *Deliverer
	Log         *AttemptLog
	Mail        mail.Sender
	Dashboard   string
	Workers     int
	PerEndpoint int
	Batch       int
	Idle        time.Duration
	// EndpointTTL is how long an endpoint's settings are kept before being read again.
	EndpointTTL time.Duration
	// BusyWait is how long a delivery waits for a busy endpoint's slot before it is held back.
	BusyWait time.Duration
	Logger   *slog.Logger
	// Alerts, when set, hears every event taken, for the project's notification rules
	// (internal/alerts). Nil in a deployment without them.
	Alerts AlertSink

	// Counts since the process started, for its metrics: tries made and landed, and deliveries
	// held back without a try.
	Tried, Landed, Held atomic.Int64

	once   sync.Once
	pool   chan struct{}
	mu     sync.Mutex
	cache  map[int]*projectEndpoints
	states map[int]*endpointState
	cancel context.CancelFunc
	done   chan struct{}
}

type projectEndpoints struct {
	at        time.Time
	endpoints []*ent.WebhookEndpoint
}

// endpointState is what this process knows of one endpoint between reads of it.
type endpointState struct {
	slots        chan struct{}
	mu           sync.Mutex
	fails        int
	openUntil    time.Time
	probing      bool
	failingSince *time.Time
}

const (
	breakAfter = 5
	breakMin   = 30 * time.Second
	breakMax   = 10 * time.Minute
	lease      = 90 * time.Second
)

func (d *Dispatcher) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

func (d *Dispatcher) init() {
	d.once.Do(func() {
		if d.Workers <= 0 {
			d.Workers = 256
		}
		if d.PerEndpoint <= 0 {
			d.PerEndpoint = 16
		}
		if d.Batch <= 0 {
			d.Batch = 500
		}
		if d.Idle <= 0 {
			d.Idle = 250 * time.Millisecond
		}
		if d.EndpointTTL <= 0 {
			d.EndpointTTL = 5 * time.Second
		}
		if d.BusyWait <= 0 {
			d.BusyWait = busyWait
		}
		d.pool = make(chan struct{}, d.Workers)
		d.cache = map[int]*projectEndpoints{}
		d.states = map[int]*endpointState{}
	})
}

// Name identifies the component in start-up and shutdown logs.
func (d *Dispatcher) Name() string { return "webhook-dispatcher" }

// Start begins dispatching in the background.
func (d *Dispatcher) Start(context.Context) error {
	d.init()
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done = cancel, make(chan struct{})
	go func() {
		defer close(d.done)
		for ctx.Err() == nil {
			n, err := d.Pass(ctx)
			if err != nil && ctx.Err() == nil {
				d.log().Error("a webhook pass failed; tried again shortly", "error", err)
			}
			if n == 0 || err != nil {
				select {
				case <-ctx.Done():
				case <-time.After(d.Idle):
				}
			}
		}
	}()
	return nil
}

// Stop ends dispatching, waiting for the pass in flight.
func (d *Dispatcher) Stop(ctx context.Context) error {
	if d.cancel == nil {
		return nil
	}
	d.cancel()
	select {
	case <-d.done:
	case <-ctx.Done():
	}
	return nil
}

// Pass is one batch of new events and one of due retries. It answers how many rows it took.
func (d *Dispatcher) Pass(ctx context.Context) (int, error) {
	d.init()
	var wg sync.WaitGroup
	events, err := d.claimEvents(ctx)
	if err != nil {
		return 0, err
	}
	retries, err := d.claimRetries(ctx)
	if err != nil {
		return 0, err
	}
	// Queued for people before anything is delivered to endpoints: a failure here returns before
	// any try, so the batch is taken again whole once its lease passes and no endpoint hears an
	// event twice. Queuing twice is harmless; its rows are keyed by the event.
	if d.Alerts != nil {
		for _, ev := range events {
			if err := d.Alerts.Enqueue(ctx, ClaimedEvent{EventUID: ev.uid, ProjectID: ev.projectID,
				Environment: ev.environment, Type: ev.kind, Body: ev.body, CreatedAt: ev.createdAt}); err != nil {
				return 0, fmt.Errorf("queue notifications for %s: %w", ev.kind, err)
			}
		}
	}
	for _, ev := range events {
		targets, err := d.targetsFor(ctx, ev.projectID, ev.kind, ev.environment)
		if err != nil {
			return 0, err
		}
		logged := &LoggedEvent{ProjectID: ev.projectID, EventID: ev.uid, Type: ev.kind, Environment: ev.environment,
			CreatedAt: ev.createdAt, Body: ev.body}
		for _, t := range targets {
			j := &job{target: t, projectID: ev.projectID, eventID: ev.uid, kind: ev.kind, environment: ev.environment,
				body: ev.body, deliveryID: uuid.New(), created: time.Now().UTC(), event: logged}
			logged = nil // the event's body is logged once, with its first try
			d.run(ctx, &wg, j)
		}
	}
	for _, r := range retries {
		t, ok, err := d.targetByID(ctx, r.ProjectID, r.EndpointID)
		if err != nil {
			return 0, err
		}
		if !ok {
			// Its endpoint was deleted or turned off, which cancels what it was owed.
			_ = d.DB.Client.WebhookRetry.DeleteOneID(r.ID).Exec(ctx)
			continue
		}
		j := &job{target: t, projectID: r.ProjectID, eventID: r.EventUID, kind: r.Type, environment: r.Environment,
			body: r.Body, deliveryID: r.UID, created: r.FirstAt, made: r.Attempts, retry: r, manual: r.Manual}
		d.run(ctx, &wg, j)
	}
	wg.Wait()
	if len(events) > 0 {
		ids := make([]int64, len(events))
		for i, ev := range events {
			ids[i] = int64(ev.id)
		}
		if _, err := d.DB.DB().ExecContext(ctx, `DELETE FROM webhook_outboxes WHERE id = ANY($1)`, ids); err != nil {
			return 0, fmt.Errorf("delete dispatched events: %w", err)
		}
	}
	return len(events) + len(retries), nil
}

// AlertSink hears every event the dispatcher takes, for notification rules.
type AlertSink interface {
	Enqueue(ctx context.Context, e ClaimedEvent) error
}

// ClaimedEvent is an event as the dispatcher took it.
type ClaimedEvent struct {
	EventUID    uuid.UUID
	ProjectID   int
	Environment string
	Type        string
	Body        []byte
	CreatedAt   time.Time
}

type claimedEvent struct {
	id          int
	uid         uuid.UUID
	projectID   int
	environment string
	kind        string
	body        []byte
	createdAt   time.Time
}

func (d *Dispatcher) claimEvents(ctx context.Context) ([]claimedEvent, error) {
	rows, err := d.DB.DB().QueryContext(ctx, `
		UPDATE webhook_outboxes SET claimed_until = now() + $2::interval
		WHERE id IN (
			SELECT id FROM webhook_outboxes
			WHERE claimed_until IS NULL OR claimed_until < now()
			ORDER BY id LIMIT $1
			FOR UPDATE SKIP LOCKED)
		RETURNING id, uid, project_id, environment, type, body, created_at`, d.Batch, fmt.Sprintf("%d seconds", int(lease.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("claim events: %w", err)
	}
	defer rows.Close()
	var out []claimedEvent
	for rows.Next() {
		var e claimedEvent
		if err := rows.Scan(&e.id, &e.uid, &e.projectID, &e.environment, &e.kind, &e.body, &e.createdAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (d *Dispatcher) claimRetries(ctx context.Context) ([]*ent.WebhookRetry, error) {
	rows, err := d.DB.DB().QueryContext(ctx, `
		UPDATE webhook_retries SET claimed_until = now() + $2::interval
		WHERE id IN (
			SELECT id FROM webhook_retries
			WHERE next_at <= now() AND (claimed_until IS NULL OR claimed_until < now())
			ORDER BY next_at LIMIT $1
			FOR UPDATE SKIP LOCKED)
		RETURNING id`, d.Batch, fmt.Sprintf("%d seconds", int(lease.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("claim retries: %w", err)
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}
	return d.DB.Client.WebhookRetry.Query().Where(webhookretry.IDIn(ids...)).All(ctx)
}

// endpointsOf is a project's enabled endpoints, read at most every EndpointTTL.
func (d *Dispatcher) endpointsOf(ctx context.Context, projectID int) ([]*ent.WebhookEndpoint, error) {
	d.mu.Lock()
	cached := d.cache[projectID]
	d.mu.Unlock()
	if cached != nil && time.Since(cached.at) < d.EndpointTTL {
		return cached.endpoints, nil
	}
	found, err := d.DB.Client.WebhookEndpoint.Query().Where(webhookendpoint.ProjectID(projectID),
		webhookendpoint.Enabled(true)).All(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.cache[projectID] = &projectEndpoints{at: time.Now(), endpoints: found}
	d.mu.Unlock()
	for _, e := range found {
		st := d.stateOf(e.ID)
		st.mu.Lock()
		st.failingSince = e.FailingSince
		st.mu.Unlock()
	}
	return found, nil
}

func (d *Dispatcher) targetsFor(ctx context.Context, projectID int, kind, env string) ([]Target, error) {
	endpoints, err := d.endpointsOf(ctx, projectID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []Target
	for _, e := range endpoints {
		if Hears(e.Events, kind) && HearsFrom(e.Environments, env) {
			out = append(out, TargetOf(e, now))
		}
	}
	return out, nil
}

func (d *Dispatcher) targetByID(ctx context.Context, projectID, endpointID int) (Target, bool, error) {
	endpoints, err := d.endpointsOf(ctx, projectID)
	if err != nil {
		return Target{}, false, err
	}
	for _, e := range endpoints {
		if e.ID == endpointID {
			return TargetOf(e, time.Now()), true, nil
		}
	}
	return Target{}, false, nil
}

func (d *Dispatcher) stateOf(endpointID int) *endpointState {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.states[endpointID]
	if st == nil {
		st = &endpointState{slots: make(chan struct{}, d.PerEndpoint)}
		d.states[endpointID] = st
	}
	return st
}

// gate says whether a try may go to an endpoint now, or when to come back: a failing endpoint is
// left alone for a while, then lets one try through, and the rest wait for its answer. probe is
// whether this try is that one.
func (st *endpointState) gate(now time.Time) (ok, probe bool, back time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.fails < breakAfter {
		return true, false, time.Time{}
	}
	if now.Before(st.openUntil) || st.probing {
		back = st.openUntil
		if !back.After(now) {
			back = now.Add(breakMin)
		}
		return false, false, back
	}
	st.probing = true
	return true, true, time.Time{}
}

// acquire takes one of the endpoint's slots, waiting up to a moment: a burst to a healthy endpoint
// waits its turn, and only an endpoint still busy after that (a slow one) has deliveries held back.
func (st *endpointState) acquire(wait time.Duration, probe bool) bool {
	select {
	case st.slots <- struct{}{}:
		return true
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case st.slots <- struct{}{}:
		return true
	case <-timer.C:
		if probe {
			st.mu.Lock()
			st.probing = false
			st.mu.Unlock()
		}
		return false
	}
}

// busyWait is how long a delivery waits for a busy endpoint's slot before it is held back.
const busyWait = 2 * time.Second

func (st *endpointState) release(ok bool, now time.Time) {
	<-st.slots
	st.mu.Lock()
	defer st.mu.Unlock()
	st.probing = false
	if ok {
		st.fails, st.openUntil = 0, time.Time{}
		return
	}
	st.fails++
	if st.fails >= breakAfter {
		wait := breakMin << min(st.fails-breakAfter, 5)
		st.openUntil = now.Add(min(wait, breakMax))
	}
}

// job is one delivery's try.
type job struct {
	target      Target
	projectID   int
	eventID     uuid.UUID
	kind        string
	environment string
	body        []byte
	deliveryID  uuid.UUID
	created     time.Time
	made        int
	retry       *ent.WebhookRetry
	manual      bool
	event       *LoggedEvent
}

func (d *Dispatcher) run(ctx context.Context, wg *sync.WaitGroup, j *job) {
	st := d.stateOf(j.target.EndpointID)
	ok, probe, back := st.gate(time.Now())
	if !ok {
		d.hold(ctx, j, back.Add(time.Duration(rand.Int64N(int64(5*time.Second)))))
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// The endpoint's slot first, then the pool's: a slow endpoint waits on its own slots and
		// holds none of the pool while it does.
		if !st.acquire(d.BusyWait, probe) {
			d.hold(ctx, j, time.Now().Add(time.Second+time.Duration(rand.Int64N(int64(5*time.Second)))))
			return
		}
		d.pool <- struct{}{}
		tryCtx, cancel := context.WithTimeout(ctx, Timeout+2*time.Second)
		res := d.Deliver.Send(tryCtx, j.target, j.eventID, j.body)
		cancel()
		<-d.pool
		d.Tried.Add(1)
		if res.OK() {
			d.Landed.Add(1)
		}
		st.release(res.OK(), time.Now())
		d.finish(ctx, st, j, res)
	}()
}

// hold keeps a delivery for later without a try: its endpoint is busy or failing.
func (d *Dispatcher) hold(ctx context.Context, j *job, at time.Time) {
	d.Held.Add(1)
	var err error
	if j.retry != nil {
		err = d.DB.Client.WebhookRetry.UpdateOneID(j.retry.ID).SetNextAt(at).ClearClaimedUntil().Exec(ctx)
	} else {
		err = d.DB.Client.WebhookRetry.Create().SetUID(j.deliveryID).SetEndpointID(j.target.EndpointID).
			SetProjectID(j.projectID).SetEventUID(j.eventID).SetType(j.kind).SetEnvironment(j.environment).
			SetBody(j.body).SetFirstAt(j.created).SetNextAt(at).Exec(ctx)
		if err == nil && j.event != nil {
			// The event's body is logged now: its first try may be made by another pass.
			d.Log.RecordEvent(*j.event)
		}
	}
	if err != nil {
		d.log().Error("a held webhook delivery could not be kept", "endpoint", j.target.EndpointUID, "error", err)
	}
}

// finish records a try and decides what comes next: nothing, another try, or giving up.
func (d *Dispatcher) finish(ctx context.Context, st *endpointState, j *job, res Result) {
	n := j.made + 1
	a := Attempt{ProjectID: j.projectID, EndpointUID: j.target.EndpointUID, DeliveryID: j.deliveryID, EventID: j.eventID,
		Type: j.kind, Environment: j.environment, DeliveryCreated: j.created, Number: n, Result: res, Manual: j.manual}
	now := time.Now().UTC()
	if res.OK() {
		a.Outcome = Succeeded
		if j.retry != nil {
			_ = d.DB.Client.WebhookRetry.DeleteOneID(j.retry.ID).Exec(ctx)
		}
		d.recovered(ctx, st, j.target.EndpointID)
		d.Log.Record(a, j.event)
		return
	}

	next, again := NextTry(n, now)
	if j.manual {
		again = false // a resend from the dashboard is one try
	}
	var err error
	switch {
	case again && j.retry != nil:
		a.Outcome, a.NextAt = Retrying, &next
		err = d.DB.Client.WebhookRetry.UpdateOneID(j.retry.ID).SetAttempts(n).SetNextAt(next).ClearClaimedUntil().Exec(ctx)
	case again:
		a.Outcome, a.NextAt = Retrying, &next
		err = d.DB.Client.WebhookRetry.Create().SetUID(j.deliveryID).SetEndpointID(j.target.EndpointID).
			SetProjectID(j.projectID).SetEventUID(j.eventID).SetType(j.kind).SetEnvironment(j.environment).
			SetBody(j.body).SetFirstAt(j.created).SetAttempts(n).SetNextAt(next).Exec(ctx)
	default:
		a.Outcome = Failed
		if j.retry != nil {
			err = d.DB.Client.WebhookRetry.DeleteOneID(j.retry.ID).Exec(ctx)
		}
	}
	if err != nil {
		d.log().Error("a webhook retry could not be kept", "endpoint", j.target.EndpointUID, "error", err)
	}
	d.Log.Record(a, j.event)
	d.failing(ctx, st, j.target, now)
}

// recovered clears an endpoint's failing streak, writing only when there was one.
func (d *Dispatcher) recovered(ctx context.Context, st *endpointState, endpointID int) {
	st.mu.Lock()
	was := st.failingSince
	st.failingSince = nil
	st.mu.Unlock()
	if was != nil {
		_ = d.DB.Client.WebhookEndpoint.UpdateOneID(endpointID).ClearFailingSince().Exec(ctx)
	}
}

// failing starts an endpoint's failing streak, or turns it off once the streak has lasted three
// days.
func (d *Dispatcher) failing(ctx context.Context, st *endpointState, t Target, now time.Time) {
	st.mu.Lock()
	since := st.failingSince
	if since == nil {
		st.failingSince = &now
	}
	st.mu.Unlock()
	if since == nil {
		_ = d.DB.Client.WebhookEndpoint.Update().Where(webhookendpoint.ID(t.EndpointID), webhookendpoint.FailingSinceIsNil()).
			SetFailingSince(now).Exec(ctx)
		return
	}
	if now.Sub(*since) < DisableAfter {
		return
	}
	if err := d.Disable(ctx, t.EndpointID, now); err != nil {
		d.log().Error("a failing webhook endpoint could not be turned off", "endpoint", t.EndpointUID, "error", err)
	}
}

// Disable turns an endpoint off after three days of failures, cancels what it was owed, and tells
// the organization's owners and admins.
func (d *Dispatcher) Disable(ctx context.Context, endpointID int, now time.Time) error {
	n, err := d.DB.Client.WebhookEndpoint.Update().Where(webhookendpoint.ID(endpointID), webhookendpoint.Enabled(true)).
		SetEnabled(false).SetDisabledReason("failing").SetDisabledAt(now).Save(ctx)
	if err != nil || n == 0 {
		return err
	}
	e, err := d.DB.Client.WebhookEndpoint.Get(ctx, endpointID)
	if err != nil {
		return err
	}
	owed, err := d.DB.Client.WebhookRetry.Query().Where(webhookretry.EndpointID(endpointID)).All(ctx)
	if err != nil {
		return err
	}
	if _, err := d.DB.Client.WebhookRetry.Delete().Where(webhookretry.EndpointID(endpointID)).Exec(ctx); err != nil {
		return err
	}
	for _, a := range Canceled(owed, e.UID, "Canceled: the endpoint was turned off after three days of failures", now) {
		d.Log.Record(a, nil)
	}
	d.mu.Lock()
	delete(d.cache, e.ProjectID)
	d.mu.Unlock()
	d.log().Warn("turned off a webhook endpoint after three days of failures", "endpoint", e.UID, "url", e.URL)
	if d.Mail == nil {
		return nil
	}
	p, err := d.DB.Client.Project.Query().Where(project.ID(e.ProjectID)).Only(ctx)
	if err != nil {
		return err
	}
	owners, err := d.DB.Client.OrganizationMember.Query().Where(organizationmember.OrganizationID(p.OrganizationID),
		organizationmember.HasRoleWith(role.KeyIn("owner", "admin"))).WithUser().All(ctx)
	if err != nil {
		return err
	}
	link := strings.TrimRight(d.Dashboard, "/") + "/webhooks/" + e.UID.String()
	for _, m := range owners {
		if m.Edges.User == nil {
			continue
		}
		_ = d.Mail.Send(ctx, mail.Message{Kind: mail.WebhookDisabled, To: m.Edges.User.Email,
			Subject: "A webhook endpoint of " + p.Name + " was turned off",
			Body: "Every delivery to " + e.Name + " (" + e.URL + ") has failed for three days, so PlayerSense " +
				"turned it off and stopped retrying.\n\nFix the endpoint, turn it back on, and resend what it missed:\n" + link + "\n"})
	}
	return nil
}
