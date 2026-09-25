// Package fx is exchange rates: how many of each currency a US dollar buys, so revenue paid in
// forty currencies can be read as one figure at the day's rate.
//
// Rates come from Open Exchange Rates' latest.json, base USD, which the free plan allows a
// thousand times a month. So they are fetched at most once an hour, whoever asks: the answer is
// kept in Valkey for an hour, and a lock there means one process fetches while the others wait
// for its answer. Each fetch is also kept as that day's rates in PostgreSQL (fx_rates), which is
// how a day long past is still converted at its own rate: history builds up from the day this
// runs, with no call to the paid historical endpoint. A day before the first kept is converted at
// the nearest day there is, and the answer says so.
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/fxrate"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/valkey"
)

// ErrNotConfigured is a deployment with no Open Exchange Rates key: money stays in the
// currencies paid.
var ErrNotConfigured = errors.New("fx: no exchange rate source is configured")

// Rates is one moment's rates: units of each currency per US dollar.
type Rates struct {
	At     time.Time          `json:"at"`
	PerUSD map[string]float64 `json:"per_usd"`
}

// Source is Open Exchange Rates.
type Source struct {
	AppID string
	// BaseURL overrides https://openexchangerates.org, for tests.
	BaseURL string
	HTTP    *http.Client
}

// Latest fetches the latest rates. One call against the month's allowance.
func (s *Source) Latest(ctx context.Context) (Rates, error) {
	if s == nil || s.AppID == "" {
		return Rates{}, ErrNotConfigured
	}
	base := s.BaseURL
	if base == "" {
		base = "https://openexchangerates.org"
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/latest.json?app_id="+url.QueryEscape(s.AppID)+"&base=USD", nil)
	if err != nil {
		return Rates{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Rates{}, fmt.Errorf("fx: open exchange rates: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Rates{}, fmt.Errorf("fx: open exchange rates answered %d: %.200s", resp.StatusCode, raw)
	}
	var body struct {
		Timestamp int64              `json:"timestamp"`
		Base      string             `json:"base"`
		Rates     map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return Rates{}, fmt.Errorf("fx: open exchange rates: %w", err)
	}
	if body.Base != "" && body.Base != "USD" {
		return Rates{}, fmt.Errorf("fx: rates are based on %s, not USD", body.Base)
	}
	out := Rates{At: time.Unix(body.Timestamp, 0).UTC(), PerUSD: map[string]float64{"USD": 1}}
	for c, r := range body.Rates {
		if r > 0 && len(c) == 3 {
			out.PerUSD[strings.ToUpper(c)] = r
		}
	}
	return out, nil
}

// Service hands out rates: the latest from Valkey or, at most once an hour, from the source; a
// window's from PostgreSQL.
type Service struct {
	Source *Source
	Cache  *valkey.Client
	DB     *pgdb.DB
	// TTL is how long fetched rates are kept in Valkey; an hour when zero.
	TTL time.Duration
	Log *slog.Logger
}

const cacheKey = "fx:latest:usd"
const lockKey = "fx:fetching"

func (s *Service) ttl() time.Duration {
	if s.TTL <= 0 {
		return time.Hour
	}
	return s.TTL
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Configured reports whether there is a source to fetch from.
func (s *Service) Configured() bool { return s != nil && s.Source != nil && s.Source.AppID != "" }

func (s *Service) cached(ctx context.Context) (Rates, bool) {
	if s.Cache == nil || !s.Cache.Ready() {
		return Rates{}, false
	}
	raw, err := s.Cache.Get(ctx, s.Cache.Key(cacheKey))
	if err != nil || raw == nil {
		return Rates{}, false
	}
	var r Rates
	if json.Unmarshal(raw, &r) != nil || len(r.PerUSD) == 0 {
		return Rates{}, false
	}
	return r, true
}

// Latest is the latest rates: Valkey's copy while it lasts; otherwise one process fetches them,
// keeps them an hour in Valkey and as the day's rates in PostgreSQL, and the rest wait a moment
// for its answer. When the source cannot be reached, the newest day kept is used.
func (s *Service) Latest(ctx context.Context) (Rates, error) {
	if r, ok := s.cached(ctx); ok {
		return r, nil
	}
	if !s.Configured() {
		return s.newestKept(ctx)
	}
	if s.Cache != nil && s.Cache.Ready() {
		got, err := s.Cache.SetNX(ctx, s.Cache.Key(lockKey), []byte("1"), 30*time.Second)
		if err == nil && !got {
			// Another process is fetching: its answer lands in Valkey shortly.
			for i := 0; i < 20; i++ {
				select {
				case <-ctx.Done():
					return Rates{}, ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
				if r, ok := s.cached(ctx); ok {
					return r, nil
				}
			}
			return s.newestKept(ctx)
		}
		defer func() { _ = s.Cache.Del(context.Background(), s.Cache.Key(lockKey)) }()
	}
	r, err := s.Source.Latest(ctx)
	if err != nil {
		s.log().Warn("exchange rates could not be fetched; the newest kept are used", "error", err)
		return s.newestKept(ctx)
	}
	if s.Cache != nil && s.Cache.Ready() {
		if raw, err := json.Marshal(r); err == nil {
			_ = s.Cache.Set(ctx, s.Cache.Key(cacheKey), raw, s.ttl())
		}
	}
	if err := s.keep(ctx, r); err != nil {
		s.log().Warn("the day's exchange rates could not be kept", "error", err)
	}
	return r, nil
}

// keep writes rates as their day's, over any kept earlier that day: the last fetch of a day is
// the day's rate.
func (s *Service) keep(ctx context.Context, r Rates) error {
	if s.DB == nil {
		return nil
	}
	day := r.At.UTC().Truncate(24 * time.Hour)
	creates := make([]*ent.FxRateCreate, 0, len(r.PerUSD))
	for c, v := range r.PerUSD {
		creates = append(creates, s.DB.Client.FxRate.Create().SetDay(day).SetCurrency(c).SetPerUsd(v))
	}
	return s.DB.Client.FxRate.CreateBulk(creates...).OnConflictColumns(fxrate.FieldDay, fxrate.FieldCurrency).
		UpdateNewValues().Exec(ctx)
}

// newestKept is the newest day's rates in PostgreSQL.
func (s *Service) newestKept(ctx context.Context) (Rates, error) {
	if s.DB == nil {
		return Rates{}, ErrNotConfigured
	}
	last, err := s.DB.Client.FxRate.Query().Order(ent.Desc(fxrate.FieldDay)).First(ctx)
	if ent.IsNotFound(err) {
		return Rates{}, ErrNotConfigured
	}
	if err != nil {
		return Rates{}, err
	}
	rows, err := s.DB.Client.FxRate.Query().Where(fxrate.Day(last.Day)).All(ctx)
	if err != nil {
		return Rates{}, err
	}
	out := Rates{At: last.Day, PerUSD: map[string]float64{}}
	for _, r := range rows {
		out.PerUSD[r.Currency] = r.PerUsd
	}
	return out, nil
}

// Table is a window's rates by day, for converting money paid on each.
type Table struct {
	days []time.Time
	by   map[time.Time]map[string]float64
	// Exact is false when some day asked about had no rates of its own and borrowed the nearest.
	Exact bool
}

// Window is the rates for every day from `from` to `to`, and today's from Latest when `to` is
// today. A day with no rates of its own borrows the nearest day's.
func (s *Service) Window(ctx context.Context, from, to time.Time) (*Table, error) {
	t := &Table{by: map[time.Time]map[string]float64{}, Exact: true}
	if s == nil || s.DB == nil {
		return t, ErrNotConfigured
	}
	from, to = from.UTC().Truncate(24*time.Hour), to.UTC().Truncate(24*time.Hour)
	rows, err := s.DB.Client.FxRate.Query().Where(fxrate.DayGTE(from.AddDate(0, 0, -14)), fxrate.DayLTE(to)).All(ctx)
	if err != nil {
		return t, err
	}
	for _, r := range rows {
		d := r.Day.UTC()
		if t.by[d] == nil {
			t.by[d] = map[string]float64{}
		}
		t.by[d][r.Currency] = r.PerUsd
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if !to.Before(today) {
		if latest, err := s.Latest(ctx); err == nil {
			t.by[today] = latest.PerUSD
		}
	}
	if len(t.by) == 0 {
		// Nothing in or near the window: the newest kept, for every day.
		latest, err := s.Latest(ctx)
		if err != nil {
			return t, err
		}
		t.by[latest.At.UTC().Truncate(24*time.Hour)] = latest.PerUSD
	}
	for d := range t.by {
		t.days = append(t.days, d)
	}
	sort.Slice(t.days, func(i, j int) bool { return t.days[i].Before(t.days[j]) })
	return t, nil
}

// IsExact reports whether every day converted so far had rates of its own.
func (t *Table) IsExact() bool { return t != nil && t.Exact }

// Empty reports whether the table has no rates at all.
func (t *Table) Empty() bool { return t == nil || len(t.days) == 0 }

// PerUSD is how many of a currency a dollar bought on a day: that day's rate, or the nearest
// earlier day's, or the nearest later day's.
func (t *Table) PerUSD(day time.Time, currency string) (float64, bool) {
	if t.Empty() {
		return 0, false
	}
	currency = strings.ToUpper(currency)
	if currency == "USD" {
		return 1, true
	}
	day = day.UTC().Truncate(24 * time.Hour)
	if r, ok := t.by[day][currency]; ok {
		return r, true
	}
	i := sort.Search(len(t.days), func(i int) bool { return t.days[i].After(day) })
	for j := i - 1; j >= 0; j-- {
		if r, ok := t.by[t.days[j]][currency]; ok {
			t.Exact = false
			return r, true
		}
	}
	for j := i; j < len(t.days); j++ {
		if r, ok := t.by[t.days[j]][currency]; ok {
			t.Exact = false
			return r, true
		}
	}
	return 0, false
}

// USD converts an amount in millionths of a currency, paid on a day, to US dollars.
func (t *Table) USD(micros int64, currency string, day time.Time) (float64, bool) {
	if micros == 0 {
		return 0, true
	}
	r, ok := t.PerUSD(day, currency)
	if !ok || r <= 0 {
		return 0, false
	}
	return float64(micros) / 1e6 / r, true
}

// Refresher fetches the rates once an hour in the worker, so a day with no dashboard traffic still
// has its rates kept. With Valkey's copy fresh it costs nothing and calls nobody.
type Refresher struct {
	Service *Service
	Every   time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// Name identifies the component in start-up and shutdown logs.
func (r *Refresher) Name() string { return "fx-refresher" }

// Start refreshes now and then every hour.
func (r *Refresher) Start(context.Context) error {
	if !r.Service.Configured() {
		return nil
	}
	if r.Every <= 0 {
		r.Every = time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan struct{})
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(r.Every)
		defer ticker.Stop()
		for {
			_, _ = r.Service.Latest(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

// Stop ends the loop.
func (r *Refresher) Stop(ctx context.Context) error {
	if r.cancel == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
	case <-ctx.Done():
	}
	return nil
}
