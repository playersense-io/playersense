package charging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/pgdb"
)

// Service runs the charger: the bus subscription, the drain and the prune, as one component.
//
// The three are independent loops on purpose. The subscription is paced by traffic, the drain by a
// clock, and the prune by the calendar; a slow drain must not hold back counting, and a burst of
// counting must not delay a drain.
type Service struct {
	Bus     eventbus.Bus
	Accruer *Accruer
	Drainer *Drainer
	Pruner  *Pruner
	Log     *slog.Logger

	// DrainEvery is how often queued charges are moved into wallets.
	DrainEvery time.Duration

	// PruneEvery is how often last-but-one month's player set is removed.
	PruneEvery time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Name identifies the component in start-up and shutdown logs.
func (s *Service) Name() string { return "billing-writer" }

// Start runs the three loops in the background and returns.
//
// Not derived from the start-up context, for the reason analytics-writer gives: that one is
// cancelled once start-up finishes.
func (s *Service) Start(context.Context) error {
	if s.DrainEvery <= 0 {
		return fmt.Errorf("the drain needs an interval")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	s.wg.Add(3)
	go func() {
		defer s.wg.Done()
		if err := s.Bus.Subscribe(ctx, eventbus.GroupBillingWriter, s.Accruer.Handle); err != nil && ctx.Err() == nil {
			s.log().Error("the subscription stopped", "error", err)
		}
	}()
	go func() {
		defer s.wg.Done()
		s.every(ctx, s.DrainEvery, func(ctx context.Context) {
			if _, err := s.Drainer.Run(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("a drain pass left charges queued", "error", err)
			}
		})
	}()
	go func() {
		defer s.wg.Done()
		if s.Pruner == nil || s.PruneEvery <= 0 {
			return
		}
		s.every(ctx, s.PruneEvery, func(ctx context.Context) {
			if _, err := s.Pruner.Run(ctx); err != nil && ctx.Err() == nil {
				s.log().Warn("the player-set prune failed; it runs again next interval", "error", err)
			}
		})
	}()
	return nil
}

// Stop ends the loops and waits for whatever is in flight.
//
// Waiting matters for the drain above all: a transaction that is cut off rolls back, which is safe,
// but one that is allowed to finish is one fewer pass of latency on somebody's balance.
func (s *Service) Stop(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("the billing writer did not stop within the shutdown timeout")
	}
	s.log().Info("billing writer stopped",
		"messages", s.Accruer.Stats.Messages.Load(),
		"counted", s.Accruer.Stats.Counted.Load(),
		"known_by_cache", s.Accruer.Stats.KnownByCache.Load(),
		"queued", s.Accruer.Stats.Queued.Load(),
		"drained_charges", s.Drainer.Stats.Charges.Load(),
		"drain_failures", s.Drainer.Stats.Failures.Load(),
		"unbilled", s.Accruer.Stats.Unbilled.Load())
	return nil
}

// every runs fn now and then on an interval until the context ends. A pass that overruns the
// interval delays the next rather than overlapping it.
func (s *Service) every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		fn(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Pruner removes player sets nobody can be charged against any more.
//
// §38 says the monthly actor set is computed and discarded. A real-time meter has to keep it while
// the month runs, and this is the "discarded": once a month is over and the one after it has begun
// too, its digests decide nothing and are deleted. The month before the current one is kept, so a
// late batch published at 23:59:59 on the last day and delivered after midnight still finds its
// month's set.
type Pruner struct {
	DB  *pgdb.DB
	Now func() time.Time

	// Chunk bounds one DELETE, so pruning a large month is many short statements rather than
	// one long lock.
	Chunk int
}

// Run deletes everything before the start of last month and reports how many rows went.
func (p *Pruner) Run(ctx context.Context) (int64, error) {
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	cutoff := PeriodOf(now).AddDate(0, -1, 0)
	chunk := p.Chunk
	if chunk <= 0 {
		chunk = 10_000
	}

	var total int64
	for {
		res, err := p.DB.Client.ExecContext(ctx,
			`DELETE FROM billable_player WHERE id IN (
			     SELECT id FROM billable_player WHERE period_start < $1 LIMIT $2)`,
			cutoff, chunk)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(chunk) || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}
