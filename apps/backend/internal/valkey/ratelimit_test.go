package valkey_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/valkey"
)

func testClient(t *testing.T) *valkey.Client {
	t.Helper()
	dsn := os.Getenv("VALKEY_DSN")
	if dsn == "" {
		dsn = dsnFromEnvFile(t)
	}
	if dsn == "" {
		t.Skip("no VALKEY_DSN")
	}
	c := valkey.New(dsn)
	if err := c.Start(t.Context()); err != nil {
		t.Skipf("no valkey: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	return c
}

func dsnFromEnvFile(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for range 6 {
		raw, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "VALKEY_DSN="); ok {
					return v
				}
			}
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// TestIncrementCountsFromOne — the framework requires the count to include the request
// being counted, so the first request in a window returns one rather than zero.
func TestIncrementCountsFromOne(t *testing.T) {
	storage := valkey.NewRateLimitStorage(testClient(t))
	key := "test-" + time.Now().Format("150405.000000000")

	for want := 1; want <= 3; want++ {
		got, reset, err := storage.Increment(t.Context(), "unit", key, time.Minute)
		if err != nil {
			t.Fatalf("Increment = %v", err)
		}
		if got != want {
			t.Fatalf("count = %d, want %d", got, want)
		}
		if reset <= 0 || reset > time.Minute {
			t.Errorf("reset = %v, want something inside the window", reset)
		}
	}
}

// TestWindowIsNotExtended is the mistake the framework warns about explicitly.
//
// A limit whose window restarts on every request is a limit that never resets, and a
// client that keeps knocking is never let back in. PEXPIRE must be set only when the
// counter is created.
func TestWindowIsNotExtended(t *testing.T) {
	storage := valkey.NewRateLimitStorage(testClient(t))
	key := "test-window-" + time.Now().Format("150405.000000000")

	_, first, err := storage.Increment(t.Context(), "unit", key, 2*time.Second)
	if err != nil {
		t.Fatalf("Increment = %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	_, second, err := storage.Increment(t.Context(), "unit", key, 2*time.Second)
	if err != nil {
		t.Fatalf("Increment = %v", err)
	}
	if second >= first {
		t.Fatalf("the window was extended: %v then %v; it must keep counting down", first, second)
	}
}

// TestCountersAreSharedAcrossInstances is why this exists at all.
//
// The in-process default the framework ships is right for a single process and wrong for
// several: a limit of a hundred a minute silently becomes a hundred a minute per
// instance. Two storages over the same Valkey must see one budget.
func TestCountersAreSharedAcrossInstances(t *testing.T) {
	instanceA := valkey.NewRateLimitStorage(testClient(t))
	instanceB := valkey.NewRateLimitStorage(testClient(t))
	key := "test-shared-" + time.Now().Format("150405.000000000")

	if got, _, err := instanceA.Increment(t.Context(), "unit", key, time.Minute); err != nil || got != 1 {
		t.Fatalf("instance A first request: count=%d err=%v", got, err)
	}
	got, _, err := instanceB.Increment(t.Context(), "unit", key, time.Minute)
	if err != nil {
		t.Fatalf("instance B: %v", err)
	}
	if got != 2 {
		t.Fatalf("instance B saw count=%d; the budget is not shared", got)
	}
}

// TestUnavailableStorageReportsAnError — the limiter failing closed depends on this. An
// implementation that silently returned a count would let an attacker who can reach the
// storage choose the moment limits stop applying.
func TestUnavailableStorageReportsAnError(t *testing.T) {
	storage := valkey.NewRateLimitStorage(valkey.New("redis://127.0.0.1:1/0"))

	if _, _, err := storage.Increment(t.Context(), "unit", "k", time.Minute); err == nil {
		t.Fatal("an unavailable storage reported success")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	c := testClient(t)
	key := "test-cache-" + time.Now().Format("150405.000000000")

	if got, err := c.Get(t.Context(), key); err != nil || got != nil {
		t.Fatalf("a miss must be (nil, nil), got (%v, %v)", got, err)
	}
	if err := c.Set(t.Context(), key, []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set = %v", err)
	}
	got, err := c.Get(t.Context(), key)
	if err != nil || string(got) != "value" {
		t.Fatalf("Get = (%q, %v)", got, err)
	}
	if err := c.Del(t.Context(), key); err != nil {
		t.Fatalf("Del = %v", err)
	}
	if got, _ := c.Get(t.Context(), key); got != nil {
		t.Error("the key survived deletion")
	}
}
