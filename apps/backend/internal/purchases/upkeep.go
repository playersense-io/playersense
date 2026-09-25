package purchases

import (
	"context"
	"log/slog"
	"time"
)

// Upkeep is the worker's slow loop for purchases: the refund sweep, each store every six hours,
// and the pruning of shipped changes and old attempts.
type Upkeep struct {
	Service *Service
	Shipper *Shipper
	Every   time.Duration
	Log     *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

func (u *Upkeep) log() *slog.Logger {
	if u.Log != nil {
		return u.Log
	}
	return slog.Default()
}

// Name identifies the component in start-up and shutdown logs.
func (u *Upkeep) Name() string { return "purchase-upkeep" }

// Run is one pass.
func (u *Upkeep) Run(ctx context.Context, now time.Time) error {
	swept, err := u.Service.Sweep(ctx, now)
	if err != nil {
		return err
	}
	if swept.Providers > 0 {
		u.log().Info("swept the stores for refunds", "stores", swept.Providers, "refunds", swept.Refunds, "failed", swept.Failed)
	}
	_, err = u.Shipper.Prune(ctx, now)
	return err
}

// Start begins the loop in the background.
func (u *Upkeep) Start(context.Context) error {
	if u.Every <= 0 {
		u.Every = 10 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel, u.done = cancel, make(chan struct{})
	go func() {
		defer close(u.done)
		ticker := time.NewTicker(u.Every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := u.Run(ctx, time.Now().UTC()); err != nil {
					u.log().Error("a purchase upkeep pass failed", "error", err)
				}
			}
		}
	}()
	return nil
}

// Stop ends the loop, waiting for a pass in flight.
func (u *Upkeep) Stop(ctx context.Context) error {
	if u.cancel == nil {
		return nil
	}
	u.cancel()
	select {
	case <-u.done:
	case <-ctx.Done():
	}
	return nil
}
