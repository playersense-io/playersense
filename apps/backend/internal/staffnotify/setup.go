package staffnotify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/scheduler"
	"playersense.dev/backend/internal/valkey"
)

// checked is a Slack webhook that has passed the same validation a studio's does: https, on
// hooks.slack.com. A mistyped URL fails the process at start-up rather than every alert failing
// quietly later, which is when nobody is looking.
func checked(name, url string, allowPrivate bool) error {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	err := notify.NewSender(nil, allowPrivate).Validate(notify.Channel{Type: notify.Slack, URL: url})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// ForProcess builds the alerts channel and the logger that feeds it, for a process named
// `component`. With no STAFF_SLACK_ALERTS_URL both are nil, and a nil logger leaves the
// application to build its own from opts, so an unconfigured process is untouched.
//
// When it does build one it also makes it the process's default logger, and so it has to be called
// before anything captures slog.Default(): most packages here are handed the default rather than
// the application's logger, and their ERRORs are most of what is worth hearing about.
func ForProcess(s config.Settings, component string, opts muzak.LoggerOptions) (*Alerts, *slog.Logger, error) {
	if err := checked("STAFF_SLACK_ALERTS_URL", s.StaffSlackAlertsURL, s.NotifyAllowPrivate); err != nil {
		return nil, nil, err
	}
	alerts := NewAlerts(notify.NewSender(nil, s.NotifyAllowPrivate), s.StaffSlackAlertsURL)
	if alerts == nil {
		return nil, nil, nil
	}
	// The unwrapped logger, so what fails here is written and never raised.
	base := muzak.NewLogger(opts)
	alerts.Logger = base
	logger := slog.New(NewHandler(base.Handler(), alerts, component))
	slog.SetDefault(logger)
	return alerts, logger, nil
}

// GrowthFor builds the daily digest, or nil when there is no STAFF_SLACK_GROWTH_URL.
func GrowthFor(s config.Settings, db *pgdb.DB, cache *valkey.Client) (*Growth, error) {
	if err := checked("STAFF_SLACK_GROWTH_URL", s.StaffSlackGrowthURL, s.NotifyAllowPrivate); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.StaffSlackGrowthURL) == "" {
		return nil, nil
	}
	at, err := scheduler.ParseTimeOfDay(s.StaffGrowthAt)
	if err != nil {
		return nil, fmt.Errorf("STAFF_GROWTH_AT: %w", err)
	}
	g := NewGrowth(db, cache, notify.NewSender(nil, s.NotifyAllowPrivate), s.StaffSlackGrowthURL,
		time.Duration(at.Hour)*time.Hour+time.Duration(at.Minute)*time.Minute, s.DashboardURL)
	g.Logger = slog.Default()
	return g, nil
}

// Running is what a binary registers as a singleton so the staff channels start and stop with it.
// Either field may be nil, and a Running of two nils does nothing, so a binary registers it
// unconditionally.
type Running struct {
	Alerts *Alerts
	Growth *Growth
}

// Name identifies the component in start-up and shutdown logs.
func (r *Running) Name() string { return "staff-notifications" }

// Start opens the alerts first, so anything the digest's start-up logs is heard.
func (r *Running) Start(ctx context.Context) error {
	if err := r.Alerts.Start(ctx); err != nil {
		return err
	}
	return r.Growth.Start(ctx)
}

// Stop ends the digest first, then the alerts, so the last thing either says is still sent.
func (r *Running) Stop(ctx context.Context) error {
	err := r.Growth.Stop(ctx)
	if aerr := r.Alerts.Stop(ctx); err == nil {
		err = aerr
	}
	return err
}
