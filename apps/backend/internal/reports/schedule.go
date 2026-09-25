package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/channeldelivery"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/report"
	"playersense.dev/backend/ent/reportexport"
	"playersense.dev/backend/ent/reportrun"
	"playersense.dev/backend/ent/reportrunevent"
	"playersense.dev/backend/internal/purchases"
	"playersense.dev/backend/internal/query"
)

// Scheduler runs reports on their schedules, in the worker. A pass claims every report whose next
// run is due by moving its next_run_at forward in one conditional update, so two workers never run
// one schedule twice, then runs what it claimed a few at a time. It also tidies: a run left
// running by a process that died is marked failed, files past their week are deleted, and logs
// past their term are dropped.
type Scheduler struct {
	Runner   *Runner
	Engine   *query.Engine
	Every    time.Duration
	Parallel int
	Log      *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

// Housekeeping terms.
const (
	// StaleRun is how long a run may say it is running before it is taken to have died with
	// its process. A run is bounded at ten minutes.
	StaleRun = 30 * time.Minute
	// DeliveryLogDays is how long the channel delivery log is kept.
	DeliveryLogDays = 90
	// RunEventDays is how long a run's events are kept; the run itself stays.
	RunEventDays = 30
)

// PassResult is what one pass did.
type PassResult struct {
	Ran, Stale, Expired, Pruned int
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Name identifies the component in start-up and shutdown logs.
func (s *Scheduler) Name() string { return "report-scheduler" }

// Start begins the loop in the background.
func (s *Scheduler) Start(context.Context) error {
	if s.Every <= 0 {
		s.Every = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.Every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.Run(ctx, time.Now().UTC()); err != nil {
					s.log().Error("a report scheduling pass failed", "error", err)
				}
			}
		}
	}()
	return nil
}

// Stop ends the loop, waiting for a pass in flight.
func (s *Scheduler) Stop(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
	case <-ctx.Done():
	}
	return nil
}

// Run is one pass.
func (s *Scheduler) Run(ctx context.Context, now time.Time) (PassResult, error) {
	var out PassResult
	db := s.Runner.DB.Client
	due, err := db.Report.Query().Where(report.NextRunAtLTE(now)).WithProject().Limit(50).All(ctx)
	if err != nil {
		return out, err
	}
	parallel := s.Parallel
	if parallel <= 0 {
		parallel = 4
	}
	slots := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, r := range due {
		raw, _ := json.Marshal(r.Graph)
		g, gerr := Decode(raw)
		next := time.Time{}
		if gerr == nil {
			next = g.NextRun(now)
		}
		claim := db.Report.Update().Where(report.ID(r.ID), report.NextRunAt(*r.NextRunAt))
		if next.IsZero() {
			claim = claim.ClearNextRunAt()
		} else {
			claim = claim.SetNextRunAt(next)
		}
		n, err := claim.Save(ctx)
		if err != nil || n == 0 {
			continue // another worker claimed it, or it changed under us
		}
		if gerr != nil || r.Edges.Project == nil || r.Edges.Project.DeletedAt != nil {
			continue
		}
		scope, err := ScopeOf(ctx, db, r.Edges.Project, string(r.Environment))
		if err != nil {
			s.log().Warn("a scheduled report could not be scoped", "report", r.UID, "error", err)
			continue
		}
		run, err := db.ReportRun.Create().SetReportID(r.ID).SetTrigger(reportrun.TriggerSchedule).
			SetStartedAt(now).SetRequestedBy("schedule").Save(ctx)
		if err != nil {
			return out, err
		}
		out.Ran++
		slots <- struct{}{}
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			defer func() { <-slots }()
			runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			s.Runner.Execute(runCtx, t)
		}(Target{Report: r, Run: run, Graph: g, Project: r.Edges.Project,
			Sources: EngineSources{Engine: s.Engine, Scope: scope}})
	}
	wg.Wait()

	// A run its process never finished.
	stale, err := db.ReportRun.Query().Where(reportrun.StatusEQ(reportrun.StatusRunning),
		reportrun.StartedAtLT(now.Add(-StaleRun))).All(ctx)
	if err != nil {
		return out, err
	}
	for _, run := range stale {
		last, _ := db.ReportRunEvent.Query().Where(reportrunevent.RunID(run.ID)).
			Order(ent.Desc(reportrunevent.FieldSeq)).First(ctx)
		seq := 1
		if last != nil {
			seq = last.Seq + 1
		}
		_ = db.ReportRunEvent.Create().SetRunID(run.ID).SetSeq(seq).SetType("run_finished").
			SetPayload(map[string]any{"type": "run_finished", "run": run.UID.String(), "at": now.UnixMilli(),
				"status": "failed"}).Exec(ctx)
		if err := db.ReportRun.UpdateOne(run).SetStatus(reportrun.StatusFailed).SetFinishedAt(now).
			SetError("the process running it stopped before it finished").Exec(ctx); err != nil {
			return out, err
		}
		out.Stale++
	}

	// Files past their week.
	expired, err := db.ReportExport.Query().Where(reportexport.ExpiresAtLT(now)).Limit(500).All(ctx)
	if err != nil {
		return out, err
	}
	for _, e := range expired {
		if s.Runner.Store != nil {
			if err := s.Runner.Store.Delete(ctx, e.ObjectPath); err != nil {
				s.log().Warn("an expired report file could not be deleted; tried again next pass", "path", e.ObjectPath, "error", err)
				continue
			}
		}
		if err := db.ReportExport.DeleteOne(e).Exec(ctx); err != nil {
			return out, err
		}
		out.Expired++
	}

	// Logs past their term.
	n, err := db.ChannelDelivery.Delete().Where(channeldelivery.CreatedAtLT(now.AddDate(0, 0, -DeliveryLogDays))).Exec(ctx)
	if err != nil {
		return out, err
	}
	out.Pruned += n
	old, err := db.ReportRun.Query().Where(reportrun.StartedAtLT(now.AddDate(0, 0, -RunEventDays))).IDs(ctx)
	if err != nil {
		return out, err
	}
	if len(old) > 0 {
		n, err = db.ReportRunEvent.Delete().Where(reportrunevent.RunIDIn(old...)).Exec(ctx)
		if err != nil {
			return out, err
		}
		out.Pruned += n
	}
	return out, nil
}

// ScopeOf is where a report reads: its project's environment, as the query engine names it.
func ScopeOf(ctx context.Context, db *ent.Client, p *ent.Project, env string) (query.Scope, error) {
	e, err := db.Environment.Query().Where(environment.ProjectID(p.ID),
		environment.KindEQ(environment.Kind(env))).Only(ctx)
	if err != nil {
		return query.Scope{}, fmt.Errorf("the project's %s environment: %w", env, err)
	}
	if p.OrganizationID == 0 {
		if p, err = db.Project.Query().Where(project.ID(p.ID)).Only(ctx); err != nil {
			return query.Scope{}, err
		}
	}
	verified, err := purchases.VerifiedStores(ctx, db, p.ID)
	if err != nil {
		return query.Scope{}, err
	}
	return query.Scope{OrgSeq: uint32(p.OrganizationID), ProjectSeq: uint32(p.ID), EnvironmentSeq: uint16(e.ID),
		PolicyTier: string(p.PolicyTier), Verified: verified}, nil
}
