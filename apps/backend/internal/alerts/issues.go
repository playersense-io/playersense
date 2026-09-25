package alerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/logissue"
	"playersense.dev/backend/ent/logissuealert"
	"playersense.dev/backend/ent/notificationrule"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/webhookendpoint"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/webhooks"
)

// The issue events (ADR 0051).
const (
	IssueNew       = "issue.new"
	IssueRegressed = "issue.regressed"
	IssueSpiking   = "issue.spiking"
)

// IsIssueEvent reports whether a type is one of the issue events: grouped when many arrive at
// once, and cooled down per issue rather than per type.
func IsIssueEvent(t string) bool {
	return t == IssueNew || t == IssueRegressed || t == IssueSpiking
}

// IssueDetector turns what the game's logs are doing into the three things worth telling somebody
// (ADR 0051), and never into more:
//
//	issue.new        an issue first seen within the last two hours, announced once, ever
//	issue.regressed  a resolved issue seen again, once per resolution, and unresolved again
//	issue.spiking    an issue far above its usual rate, once per episode
//
// It reads the ClickHouse rollups (log_issue_hours, log_issues), never the logs, and only for the
// projects something listens to: a notification rule or a webhook endpoint that hears issue events.
// A crash that happens ten thousand times is one issue, one row of log_issue_alerts, and one message.
// Its events go through the webhook outbox, so endpoints hear them and rules route them.
type IssueDetector struct {
	DB    *pgdb.DB
	CH    *chdb.Conn
	Every time.Duration
	// Rules is how the decisions are made; DefaultIssueRules when zero.
	Rules  IssueRules
	Logger *slog.Logger

	lastPrune time.Time
	cancel    context.CancelFunc
	done      chan struct{}
}

// IssueRules are the thresholds the detector decides by.
type IssueRules struct {
	// NewWithin: an issue first seen longer ago than this, when the detector first meets it, is not
	// new. So turning a rule on does not announce every issue the project already had.
	NewWithin time.Duration
	// A spike is an hour at SpikeFactor times the issue's usual hourly rate, and at least SpikeMin
	// events: ten times one event an hour is not a spike anybody needs to hear about.
	SpikeFactor float64
	SpikeMin    uint64
	// SpikeAfter: an issue younger than this has no usual rate to spike from; its arrival was news
	// enough.
	SpikeAfter time.Duration
}

// DefaultIssueRules are the thresholds a detector uses unless told otherwise.
var DefaultIssueRules = IssueRules{NewWithin: 2 * time.Hour, SpikeFactor: 10, SpikeMin: 50, SpikeAfter: 24 * time.Hour}

// IssueFacts is what the rollups say about one issue.
type IssueFacts struct {
	ProjectID     int
	Environment   string
	Fingerprint   uint64
	Kind          string
	Level         string
	ExceptionType string
	Title         string
	Culprit       string
	Release       string
	FirstSeen     time.Time
	LastSeen      time.Time
	Events        uint64
	Players       uint64
	// Events this hour so far, in the full hour before it, and in the 24 hours before this one.
	ThisHour, PrevHour, Day uint64
}

// IssueState is what was already said about an issue.
type IssueState struct {
	Known        bool
	AnnouncedAt  *time.Time
	RegressedFor *time.Time
	Spiking      bool
	SpikingSince *time.Time
}

// IssueDecision is what to say now, and what to remember.
type IssueDecision struct {
	New, Regressed, Spiking bool
	// Unresolve: a regression puts the issue back among the unresolved, as it is again.
	Unresolve bool
	Next      IssueState
}

// Changed reports whether the decision says or remembers anything new.
func (d IssueDecision) Changed(before IssueState) bool {
	return d.New || d.Regressed || d.Spiking || d.Unresolve || !before.Known || d.Next.Spiking != before.Spiking
}

// DecideIssue is the whole of the detector's judgement, apart from any database: given what the
// rollups say, what the studio decided (unresolved, resolved, ignored) and what was already said,
// what to say now. Each announcement is gated on the state it leaves behind, which is what makes
// every one of them happen once.
func DecideIssue(f IssueFacts, st IssueState, status string, resolvedAt *time.Time, now time.Time, r IssueRules) IssueDecision {
	d := IssueDecision{Next: st}
	d.Next.Known = true
	if status == string(logissue.StatusIgnored) {
		// Ignored is ignored: nothing is said, and a spike in progress is forgotten.
		d.Next.Spiking, d.Next.SpikingSince = false, nil
		return d
	}
	if !st.Known && !f.FirstSeen.Before(now.Add(-r.NewWithin)) {
		d.New = true
		at := now
		d.Next.AnnouncedAt = &at
	}
	if status == string(logissue.StatusResolved) && resolvedAt != nil && f.LastSeen.After(*resolvedAt) &&
		(st.RegressedFor == nil || !st.RegressedFor.Equal(*resolvedAt)) {
		d.Regressed, d.Unresolve = true, true
		at := *resolvedAt
		d.Next.RegressedFor = &at
	}

	usual := float64(f.Day) / 24
	threshold := max(float64(r.SpikeMin), r.SpikeFactor*usual)
	switch {
	case !st.Spiking && !d.New && f.FirstSeen.Before(now.Add(-r.SpikeAfter)) && float64(f.ThisHour) >= threshold:
		d.Spiking = true
		at := now
		d.Next.Spiking, d.Next.SpikingSince = true, &at
	case st.Spiking && st.SpikingSince != nil && now.Sub(*st.SpikingSince) >= time.Hour &&
		float64(f.ThisHour) < threshold/2 && float64(f.PrevHour) < threshold/2:
		// Over only after a whole hour back near normal, so a spike that dips as the hour
		// turns is still the one episode, and still the one message.
		d.Next.Spiking, d.Next.SpikingSince = false, nil
	}
	return d
}

// Name identifies the component in start-up and shutdown logs.
func (d *IssueDetector) Name() string { return "issue-detector" }

// Start runs a pass every minute, in the background.
func (d *IssueDetector) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done = cancel, make(chan struct{})
	every := d.Every
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		defer close(d.done)
		for {
			if _, err := d.Pass(ctx, time.Now()); err != nil && ctx.Err() == nil {
				d.log().Error("an issue detector pass failed; tried again next minute", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(every):
			}
		}
	}()
	return nil
}

// Stop ends the detector, waiting for the pass in flight.
func (d *IssueDetector) Stop(ctx context.Context) error {
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

// Pass looks at every issue active in the last two hours of the projects something listens to,
// and announces what changed. It answers how many announcements it made.
func (d *IssueDetector) Pass(ctx context.Context, now time.Time) (int, error) {
	now = now.UTC()
	rules := d.Rules
	if rules.SpikeFactor == 0 {
		rules = DefaultIssueRules
	}
	projects, err := d.listening(ctx)
	if err != nil || len(projects) == 0 {
		return 0, err
	}
	conn, err := d.CH.DB()
	if err != nil {
		return 0, err
	}
	facts, err := activeIssues(ctx, conn, projects, now)
	if err != nil || len(facts) == 0 {
		return 0, err
	}
	envs, err := d.environments(ctx, facts)
	if err != nil {
		return 0, err
	}
	announced := 0
	for _, f := range facts {
		kind, ok := envs[f.environmentSeq]
		if !ok {
			continue
		}
		f.Environment = kind
		n, err := d.judge(ctx, f.IssueFacts, projects[f.ProjectID], now, rules)
		if err != nil {
			return announced, err
		}
		announced += n
	}
	if now.Sub(d.lastPrune) > time.Hour {
		d.lastPrune = now
		// An issue quiet for ninety days has fallen out of every rollup; so does what was said of it.
		if _, err := d.DB.Client.LogIssueAlert.Delete().
			Where(logissuealert.LastSeenLT(now.AddDate(0, 0, -90))).Exec(ctx); err != nil {
			return announced, err
		}
	}
	return announced, nil
}

type activeFacts struct {
	IssueFacts
	environmentSeq uint16
}

// listening is the projects with an enabled rule or webhook endpoint that hears issue events, and
// each project's public id for the events' envelope.
func (d *IssueDetector) listening(ctx context.Context) (map[int]*ent.Project, error) {
	ids := map[int]bool{}
	rules, err := d.DB.Client.NotificationRule.Query().
		Where(notificationrule.Enabled(true), notificationrule.ProjectIDNotNil()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: read rules: %w", err)
	}
	for _, r := range rules {
		if Hears(r.Events, IssueNew) || Hears(r.Events, IssueRegressed) || Hears(r.Events, IssueSpiking) {
			ids[*r.ProjectID] = true
		}
	}
	endpoints, err := d.DB.Client.WebhookEndpoint.Query().Where(webhookendpoint.Enabled(true)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: read webhook endpoints: %w", err)
	}
	for _, e := range endpoints {
		if webhooks.Hears(e.Events, IssueNew) || webhooks.Hears(e.Events, IssueRegressed) || webhooks.Hears(e.Events, IssueSpiking) {
			ids[e.ProjectID] = true
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	list := make([]int, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	rows, err := d.DB.Client.Project.Query().Where(project.IDIn(list...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: read projects: %w", err)
	}
	out := make(map[int]*ent.Project, len(rows))
	for _, p := range rows {
		out[p.ID] = p
	}
	return out, nil
}

// activeIssues is every issue with events in the last two hours of these projects, with its hourly
// counts and what log_issues says of it. Two reads of the rollups, whatever the event volume.
func activeIssues(ctx context.Context, conn driver.Conn, projects map[int]*ent.Project, now time.Time) ([]activeFacts, error) {
	seqs := make([]uint32, 0, len(projects))
	for id := range projects {
		seqs = append(seqs, uint32(id))
	}
	hour := now.Truncate(time.Hour)
	rows, err := conn.Query(ctx, `
		SELECT project_seq, environment_seq, fingerprint,
			sumIf(events, hour = ?) AS this_hour,
			sumIf(events, hour = ?) AS prev_hour,
			sumIf(events, hour < ?) AS day
		FROM log_issue_hours
		WHERE project_seq IN ? AND hour >= ?
		GROUP BY project_seq, environment_seq, fingerprint
		HAVING this_hour + prev_hour > 0`,
		hour, hour.Add(-time.Hour), hour, seqs, hour.Add(-24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("alerts: read active issues: %w", err)
	}
	type key struct {
		project uint32
		env     uint16
	}
	byScope := map[key]map[uint64]*activeFacts{}
	var order []*activeFacts
	for rows.Next() {
		var projectSeq uint32
		var envSeq uint16
		f := &activeFacts{}
		if err := rows.Scan(&projectSeq, &envSeq, &f.Fingerprint, &f.ThisHour, &f.PrevHour, &f.Day); err != nil {
			rows.Close()
			return nil, err
		}
		f.ProjectID, f.environmentSeq = int(projectSeq), envSeq
		k := key{projectSeq, envSeq}
		if byScope[k] == nil {
			byScope[k] = map[uint64]*activeFacts{}
		}
		byScope[k][f.Fingerprint] = f
		order = append(order, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for k, issues := range byScope {
		fps := make([]uint64, 0, len(issues))
		for fp := range issues {
			fps = append(fps, fp)
		}
		for start := 0; start < len(fps); start += 500 {
			chunk := fps[start:min(start+500, len(fps))]
			rows, err := conn.Query(ctx, `
				SELECT fingerprint, max(kind), max(severity), min(first_seen), max(last_seen), sum(events),
					argMaxMerge(exception_type), argMaxMerge(title), argMaxMerge(culprit),
					argMaxMerge(last_release), uniqCombinedMerge(installs)
				FROM log_issues
				WHERE project_seq = ? AND environment_seq = ? AND fingerprint IN ?
				GROUP BY fingerprint`, k.project, k.env, chunk)
			if err != nil {
				return nil, fmt.Errorf("alerts: describe issues: %w", err)
			}
			for rows.Next() {
				var fp uint64
				var kind, severity uint8
				var d IssueFacts
				if err := rows.Scan(&fp, &kind, &severity, &d.FirstSeen, &d.LastSeen, &d.Events,
					&d.ExceptionType, &d.Title, &d.Culprit, &d.Release, &d.Players); err != nil {
					rows.Close()
					return nil, err
				}
				f := issues[fp]
				f.Kind, f.Level = issueKinds[min(int(kind), len(issueKinds)-1)], issueLevels[min(int(severity), len(issueLevels)-1)]
				f.FirstSeen, f.LastSeen, f.Events, f.Players = d.FirstSeen.UTC(), d.LastSeen.UTC(), d.Events, d.Players
				f.ExceptionType, f.Title, f.Culprit, f.Release = d.ExceptionType, d.Title, d.Culprit, d.Release
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	out := make([]activeFacts, 0, len(order))
	for _, f := range order {
		if !f.FirstSeen.IsZero() {
			out = append(out, *f)
		}
	}
	return out, nil
}

var (
	issueKinds  = []string{"", "log", "exception", "crash"}
	issueLevels = []string{"", "trace", "debug", "info", "warn", "error", "fatal"}
)

func (d *IssueDetector) environments(ctx context.Context, facts []activeFacts) (map[uint16]string, error) {
	seen := map[int]bool{}
	var ids []int
	for _, f := range facts {
		if id := int(f.environmentSeq); !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	rows, err := d.DB.Client.Environment.Query().Where(environment.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: read environments: %w", err)
	}
	out := make(map[uint16]string, len(rows))
	for _, e := range rows {
		out[uint16(e.ID)] = string(e.Kind)
	}
	return out, nil
}

// judge decides one issue, and writes what it decided with the events it announces, in one
// transaction: a state saved without its event, or an event without its state, would be a message
// lost or a message sent twice. Nothing is written for an issue with nothing new to say.
func (d *IssueDetector) judge(ctx context.Context, f IssueFacts, p *ent.Project, now time.Time, r IssueRules) (int, error) {
	fp := strconv.FormatUint(f.Fingerprint, 10)
	env := logissue.Environment(f.Environment)
	row, err := d.DB.Client.LogIssueAlert.Query().Where(logissuealert.ProjectID(f.ProjectID),
		logissuealert.Environment(f.Environment), logissuealert.Fingerprint(fp)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return 0, err
	}
	st := IssueState{}
	if row != nil {
		st = IssueState{Known: true, AnnouncedAt: row.AnnouncedAt, RegressedFor: row.RegressedFor,
			Spiking: row.Spiking, SpikingSince: row.SpikingSince}
	}
	decided, err := d.DB.Client.LogIssue.Query().Where(logissue.ProjectID(f.ProjectID),
		logissue.EnvironmentEQ(env), logissue.Fingerprint(fp)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return 0, err
	}
	status := string(logissue.StatusUnresolved)
	var resolvedAt *time.Time
	if decided != nil {
		status, resolvedAt = string(decided.Status), decided.ResolvedAt
	}

	dec := DecideIssue(f, st, status, resolvedAt, now, r)
	// Nothing to say and nothing new to remember: no write at all, bar keeping last_seen roughly
	// right for the prune, once an hour.
	if !dec.Changed(st) && row != nil && now.Sub(row.LastSeen) < time.Hour {
		return 0, nil
	}

	tx, err := d.DB.Client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if row == nil {
		err = tx.LogIssueAlert.Create().SetProjectID(f.ProjectID).SetEnvironment(f.Environment).SetFingerprint(fp).
			SetNillableAnnouncedAt(dec.Next.AnnouncedAt).SetNillableRegressedFor(dec.Next.RegressedFor).
			SetSpiking(dec.Next.Spiking).SetNillableSpikingSince(dec.Next.SpikingSince).SetLastSeen(f.LastSeen).
			OnConflictColumns(logissuealert.FieldProjectID, logissuealert.FieldEnvironment, logissuealert.FieldFingerprint).
			DoNothing().Exec(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			// Another detector took this issue first: it has said what there was to say.
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
	} else {
		update := tx.LogIssueAlert.UpdateOne(row).SetSpiking(dec.Next.Spiking).SetLastSeen(f.LastSeen).
			SetNillableAnnouncedAt(dec.Next.AnnouncedAt).SetNillableRegressedFor(dec.Next.RegressedFor)
		if dec.Next.SpikingSince != nil {
			update = update.SetSpikingSince(*dec.Next.SpikingSince)
		} else {
			update = update.ClearSpikingSince()
		}
		if err := update.Exec(ctx); err != nil {
			return 0, err
		}
	}
	if dec.Unresolve && decided != nil {
		if err := tx.LogIssue.UpdateOne(decided).SetStatus(logissue.StatusUnresolved).ClearResolvedAt().
			SetUpdatedBy("PlayerSense (regressed)").Exec(ctx); err != nil {
			return 0, err
		}
	}
	announced := 0
	for _, a := range []struct {
		on   bool
		kind string
	}{{dec.New, IssueNew}, {dec.Regressed, IssueRegressed}, {dec.Spiking, IssueSpiking}} {
		if !a.on {
			continue
		}
		if err := webhooks.Emit(ctx, tx.Client(), webhooks.Event{ProjectID: p.ID, ProjectUID: p.UID,
			Environment: f.Environment, Type: a.kind, Data: issueData(f, a.kind, r), At: now}); err != nil {
			return 0, err
		}
		announced++
	}
	return announced, tx.Commit()
}

// issueData is what an issue's event carries: enough to read in a notification and to act on in a
// webhook, the same fields for all three.
func issueData(f IssueFacts, kind string, r IssueRules) map[string]any {
	data := map[string]any{
		"fingerprint":    strconv.FormatUint(f.Fingerprint, 10),
		"issue_title":    f.Title,
		"kind":           f.Kind,
		"level":          f.Level,
		"exception_type": f.ExceptionType,
		"culprit":        f.Culprit,
		"release":        f.Release,
		"events":         f.Events,
		"players":        f.Players,
		"first_seen":     f.FirstSeen.Format(time.RFC3339),
	}
	if kind == IssueSpiking {
		data["this_hour"] = f.ThisHour
		data["usual_per_hour"] = float64(f.Day) / 24
	}
	return data
}

func (d *IssueDetector) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
