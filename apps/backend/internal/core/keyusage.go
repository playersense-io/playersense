package core

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"playersense.dev/backend/ent/apikey"
	"playersense.dev/backend/internal/valkey"
)

// KeyUsage counts requests per API key, for the dashboard's keys page: how many an hour, and when
// each was last used.
//
// The request path pays one atomic add. Counts gather in this process and a flusher writes them
// every few seconds: to Valkey as a hash per hour (field: the key's public id), kept two days, and
// the last use to PostgreSQL at most every few minutes per key. A process that dies loses at most
// one interval's counts, which is a figure on a chart, never data.
type KeyUsage struct {
	cache *valkey.Client
	db    *DB
	log   *slog.Logger
	every time.Duration

	keys sync.Map // public id -> *keyCount

	cancel context.CancelFunc
	done   chan struct{}
}

type keyCount struct {
	pending   atomic.Int64
	lastMs    atomic.Int64
	persisted atomic.Int64 // when last_used_at was last written, unix ms
}

// usageRetention is how long an hour's counts are kept: the page shows a day, and a day more
// covers a comparison with the day before.
const usageRetention = 49 * time.Hour

// lastUsedEvery is how often a key's last_used_at is written to PostgreSQL while it is in use.
const lastUsedEvery = 5 * time.Minute

// NewKeyUsage returns a tracker that flushes every interval.
func NewKeyUsage(cache *valkey.Client, db *DB, log *slog.Logger, every time.Duration) *KeyUsage {
	if every <= 0 {
		every = 15 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &KeyUsage{cache: cache, db: db, log: log, every: every, done: make(chan struct{})}
}

// Touch counts one request by a key.
func (u *KeyUsage) Touch(publicID string) {
	if u == nil || publicID == "" {
		return
	}
	entry, found := u.keys.Load(publicID)
	if !found {
		entry, _ = u.keys.LoadOrStore(publicID, &keyCount{})
	}
	c := entry.(*keyCount)
	c.pending.Add(1)
	c.lastMs.Store(time.Now().UnixMilli())
}

// Name identifies the component in start-up and shutdown logs.
func (u *KeyUsage) Name() string { return "key-usage" }

// Start begins flushing in the background.
func (u *KeyUsage) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	go func() {
		defer close(u.done)
		ticker := time.NewTicker(u.every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				u.Flush(ctx)
			}
		}
	}()
	return nil
}

// Stop flushes what is pending and ends the loop.
func (u *KeyUsage) Stop(ctx context.Context) error {
	if u.cancel == nil {
		return nil
	}
	u.cancel()
	select {
	case <-u.done:
	case <-ctx.Done():
	}
	u.Flush(ctx)
	return nil
}

// Flush writes what has gathered since the last flush.
func (u *KeyUsage) Flush(ctx context.Context) {
	now := time.Now().UTC()
	hour := UsageHourKey(u.cache, now)
	counts := map[string]int64{}
	last := map[string]string{}
	var persist []string
	u.keys.Range(func(k, v any) bool {
		c := v.(*keyCount)
		if n := c.pending.Swap(0); n > 0 {
			counts[k.(string)] = n
			ms := c.lastMs.Load()
			last[k.(string)] = strconv.FormatInt(ms, 10)
			if ms-c.persisted.Load() >= lastUsedEvery.Milliseconds() {
				c.persisted.Store(ms)
				persist = append(persist, k.(string))
			}
		}
		return true
	})
	if len(counts) == 0 {
		return
	}
	if u.cache != nil && u.cache.Ready() {
		if err := u.cache.AddHourlyCounts(ctx, hour, counts, usageRetention, u.cache.Key("keyuse", "last"), last); err != nil {
			u.log.Warn("could not write key usage", "error", err)
		}
	}
	if u.db != nil && u.db.Ready() {
		for _, publicID := range persist {
			ms, _ := strconv.ParseInt(last[publicID], 10, 64)
			at := time.UnixMilli(ms).UTC()
			if err := u.db.Client.APIKey.Update().
				Where(apikey.PublicID(publicID), apikey.Or(apikey.LastUsedAtIsNil(), apikey.LastUsedAtLT(at))).
				SetLastUsedAt(at).Exec(ctx); err != nil {
				u.log.Warn("could not record a key's last use", "error", err)
			}
		}
	}
}

// UsageHourKey is where one hour's counts live.
func UsageHourKey(cache *valkey.Client, at time.Time) string {
	return cache.Key("keyuse", at.UTC().Format("2006010215"))
}

// ReadKeyUsage reads, for each key, its counts over the last hours hours, oldest first, and when
// it was last used, in two round trips however many keys.
func ReadKeyUsage(ctx context.Context, cache *valkey.Client, publicIDs []string, hours int, now time.Time) (map[string][]int64, map[string]time.Time, error) {
	series := make(map[string][]int64, len(publicIDs))
	last := map[string]time.Time{}
	if len(publicIDs) == 0 || cache == nil || !cache.Ready() {
		return series, last, nil
	}
	keys := make([]string, hours)
	start := now.UTC().Truncate(time.Hour).Add(-time.Duration(hours-1) * time.Hour)
	for i := range hours {
		keys[i] = UsageHourKey(cache, start.Add(time.Duration(i)*time.Hour))
	}
	values, err := cache.HashFields(ctx, keys, publicIDs)
	if err != nil {
		return series, last, err
	}
	for _, id := range publicIDs {
		series[id] = make([]int64, hours)
	}
	for i := range hours {
		for j, id := range publicIDs {
			if v := values[i][j]; v != "" {
				series[id][i], _ = strconv.ParseInt(v, 10, 64)
			}
		}
	}
	lasts, err := cache.HashFields(ctx, []string{cache.Key("keyuse", "last")}, publicIDs)
	if err != nil {
		return series, last, err
	}
	for j, id := range publicIDs {
		if ms, err := strconv.ParseInt(lasts[0][j], 10, 64); err == nil && ms > 0 {
			last[id] = time.UnixMilli(ms).UTC()
		}
	}
	return series, last, nil
}
