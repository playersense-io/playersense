package fx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"playersense.dev/backend/ent/fxrate"
	"playersense.dev/backend/internal/fx"
	"playersense.dev/backend/internal/testsupport"
)

// However many processes ask, the source is called once while Valkey holds its answer: the free
// plan's thousand calls a month allow one an hour, not one a request.
func TestRatesAreFetchedOnceAnHourWhoeverAsks(t *testing.T) {
	settings := testsupport.Settings(t)
	cache := testsupport.Cache(t, settings)
	db := testsupport.Postgres(t, settings)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("app_id") != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"timestamp":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"base":"USD","rates":{"EUR":0.9,"JPY":150,"TRY":34}}`))
	}))
	defer srv.Close()

	// This test's made-up rates must not outlive it: the cache and today's kept rates are shared
	// with the local stack, which would otherwise convert at them for an hour.
	clear := func() {
		_ = cache.Del(context.Background(), cache.Key("fx:latest:usd"), cache.Key("fx:fetching"))
		_, _ = db.Client.FxRate.Delete().Where(fxrate.DayGTE(time.Now().UTC().Truncate(24 * time.Hour))).Exec(context.Background())
	}
	clear()
	t.Cleanup(clear)
	svc := &fx.Service{Source: &fx.Source{AppID: "key", BaseURL: srv.URL}, Cache: cache, DB: db}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := svc.Latest(t.Context())
			if err != nil || r.PerUSD["EUR"] != 0.9 || r.PerUSD["USD"] != 1 {
				t.Errorf("latest: %+v %v", r, err)
			}
		}()
	}
	wg.Wait()
	if _, err := svc.Latest(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("the source was called %d times, want once", n)
	}

	// With the source gone, the rates kept in PostgreSQL still answer.
	_ = cache.Del(context.Background(), cache.Key("fx:latest:usd"))
	srv.Close()
	r, err := svc.Latest(t.Context())
	if err != nil || r.PerUSD["JPY"] != 150 {
		t.Fatalf("with the source down: %+v %v", r, err)
	}
}

func TestADayWithoutRatesBorrowsTheNearest(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	svc := &fx.Service{DB: db}
	table, err := svc.Window(t.Context(), time.Now().AddDate(-5, 0, 0), time.Now().AddDate(-5, 0, 7))
	if err != nil {
		t.Skipf("no rates kept yet: %v", err)
	}
	usd, ok := table.USD(9_000_000, "EUR", time.Now().AddDate(-5, 0, 1))
	if !ok || usd <= 0 {
		t.Fatalf("five years ago, borrowing the nearest day: %v %v", usd, ok)
	}
	if table.Exact {
		t.Fatal("a borrowed rate is marked exact")
	}
	if usd, ok := table.USD(5_000_000, "USD", time.Now()); !ok || usd != 5 {
		t.Fatalf("dollars: %v", usd)
	}
}
