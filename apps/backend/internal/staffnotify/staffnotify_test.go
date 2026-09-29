package staffnotify

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/testsupport"
)

// recorder is a Sender that keeps what it was asked to send.
type recorder struct {
	mu   sync.Mutex
	sent []notify.Message
	fail bool
}

func (r *recorder) Send(_ context.Context, _ notify.Channel, m notify.Message) notify.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return notify.Result{Detail: "answered 500"}
	}
	r.sent = append(r.sent, m)
	return notify.Result{OK: true}
}

func (r *recorder) messages() []notify.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Message(nil), r.sent...)
}

func TestARepeatIsCountedNotSentAndTheCountRidesOnTheNext(t *testing.T) {
	a := NewAlerts(&recorder{}, "https://hooks.slack.com/services/T/B/x")
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

	if _, ok := a.admit("api|boom", now); !ok {
		t.Fatal("the first of a kind was held back")
	}
	for i := 1; i <= 3; i++ {
		if _, ok := a.admit("api|boom", now.Add(time.Duration(i)*time.Minute)); ok {
			t.Fatalf("repeat %d inside the window was sent", i)
		}
	}
	if _, ok := a.admit("api|other", now.Add(time.Minute)); !ok {
		t.Fatal("a different kind was held back by another's window")
	}

	extra, ok := a.admit("api|boom", now.Add(16*time.Minute))
	if !ok {
		t.Fatal("a repeat after the window was still held back")
	}
	if !strings.Contains(extra, "3 more") {
		t.Fatalf("the count of what was held back: %q", extra)
	}
}

func TestNoMoreThanAnHourlyAllowanceIsSentWhateverTheKind(t *testing.T) {
	a := NewAlerts(&recorder{}, "https://hooks.slack.com/services/T/B/x")
	a.PerHour = 3
	now := time.Date(2026, 9, 29, 10, 5, 0, 0, time.UTC)

	sent := 0
	for i := range 10 {
		if _, ok := a.admit("kind-"+string(rune('a'+i)), now); ok {
			sent++
		}
	}
	if sent != 3 {
		t.Fatalf("%d sent in one hour, want 3", sent)
	}
	if _, ok := a.admit("kind-z", now.Add(time.Hour)); !ok {
		t.Fatal("the next hour's allowance did not start")
	}
	if held, _ := a.Held(); held != 7 {
		t.Fatalf("%d held back, want 7", held)
	}
}

func TestAnErrorIsRaisedAndNothingBelowIt(t *testing.T) {
	rec := &recorder{}
	alerts := NewAlerts(rec, "https://hooks.slack.com/services/T/B/x")
	if err := alerts.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(NewHandler(slog.NewJSONHandler(io.Discard, nil), alerts, "api")).
		With("request_id", "req-1", "password", "hunter2")

	logger.Info("nothing to see")
	logger.Warn("not this either", "error", "warn")
	logger.Error("muzak: recovered from a panic",
		"panic", "index out of range", "path", "/v1/thing", "stack", "goroutine 1 [running]:\n...",
		"token", "sk_live_secret", "error", "boom")

	if err := alerts.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := rec.messages()
	if len(got) != 1 {
		t.Fatalf("%d alerts, want 1: %+v", len(got), got)
	}
	m := got[0]
	if m.Title != "[critical] api: muzak: recovered from a panic" || m.Severity != "critical" {
		t.Fatalf("title and severity: %q %q", m.Title, m.Severity)
	}
	all := strings.Join(m.Lines, "\n")
	for _, want := range []string{"panic: index out of range", "path: /v1/thing", "request_id: req-1", "error: boom"} {
		if !strings.Contains(all, want) {
			t.Errorf("the alert lacks %q: %s", want, all)
		}
	}
	for _, leaked := range []string{"hunter2", "sk_live_secret", "goroutine"} {
		if strings.Contains(all, leaked) {
			t.Errorf("the alert carries %q: %s", leaked, all)
		}
	}
}

// The whole way to a webhook: a logged error, through the real sender, as the JSON Slack takes.
func TestAnErrorReachesAWebhookAsSlackText(t *testing.T) {
	bodies := make(chan string, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	defer hook.Close()

	alerts := NewAlerts(notify.NewSender(nil, true), hook.URL)
	if err := alerts.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	slog.New(NewHandler(slog.NewJSONHandler(io.Discard, nil), alerts, "worker")).
		Error("an invoice's usage does not match the dashboard's month", "invoice", "inv-1")
	if err := alerts.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	select {
	case body := <-bodies:
		for _, want := range []string{`"text"`, "[critical] worker: an invoice", "invoice: inv-1"} {
			if !strings.Contains(body, want) {
				t.Errorf("the posted body lacks %q: %s", want, body)
			}
		}
	default:
		t.Fatal("nothing reached the webhook")
	}
}

func TestNoAlertsChannelLeavesTheHandlerAlone(t *testing.T) {
	inner := slog.NewJSONHandler(io.Discard, nil)
	if got := NewHandler(inner, nil, "api"); got != slog.Handler(inner) {
		t.Fatal("an unconfigured process's handler was wrapped")
	}
	if NewAlerts(&recorder{}, "  ") != nil {
		t.Fatal("an empty URL made an alerts channel")
	}
	var none *Alerts
	none.Raise("k", "s", "t", "") // must not panic
	if err := none.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMoneyIsRenderedToTheCent(t *testing.T) {
	for micros, want := range map[int64]string{
		0: "$0.00", 999_999: "$1.00", 1_000_000: "$1.00", 1_234_567_890: "$1,234.57",
		-5_000: "-$0.01", 1_000_000_000_000: "$1,000,000.00",
	} {
		if got := dollars(micros); got != want {
			t.Errorf("dollars(%d) = %q, want %q", micros, got, want)
		}
	}
}

func TestTheDigestNamesTheDayAndAlignsItsRows(t *testing.T) {
	m := Digest{
		Day:          time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		TopupsMicros: 1_000_000_000, InvoicesMicros: 240_000_000, RefundedMicros: 10_000_000, UsageMicros: 310_420_000,
		NewOrganizations: 4, TotalOrganizations: 132, NewUsers: 9, TotalUsers: 415, NewProjects: 6, TotalProjects: 201,
		Owing: 2,
	}.Message("")
	if m.Title != "Growth, Mon 28 Sep 2026 (UTC)" {
		t.Fatalf("title: %q", m.Title)
	}
	all := strings.Join(m.Lines, "\n")
	for _, want := range []string{
		"Money in           $1,240.00  (top-ups $1,000.00, invoices $240.00)",
		"Refunded           $10.00",
		"Net                $1,230.00",
		"Usage charged      $310.42",
		"New organizations  4  (132 in all)",
		"New users          9  (415 in all)",
		"Accounts owing     2",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the digest lacks %q:\n%s", want, all)
		}
	}
}

// memory is a Marker in a map.
type memory struct {
	mu   sync.Mutex
	held map[string]bool
}

func (m *memory) Ready() bool                { return true }
func (m *memory) Key(parts ...string) string { return strings.Join(parts, ":") }
func (m *memory) SetNX(_ context.Context, key string, _ []byte, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held[key] {
		return false, nil
	}
	m.held[key] = true
	return true, nil
}
func (m *memory) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.held, k)
	}
	return nil
}

func TestCollectReadsMoneyAndCountsAndLeavesPromoOut(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	ctx := t.Context()
	today := time.Now().UTC()

	before, err := Collect(ctx, db.Client, today)
	if err != nil {
		t.Fatal(err)
	}

	p := testsupport.SeedProject(t, db, "standard")
	acct := testsupport.SeedBillingAccount(t, db, p.OrgSeq)
	entry := func(kind ledgerentry.Kind, micros int64) {
		t.Helper()
		if err := db.Client.LedgerEntry.Create().SetAccountID(acct.ID).SetKind(kind).SetAmountMicros(micros).
			SetBalanceAfterMicros(0).SetCurrency("usd").SetIdempotencyKey(uuid.NewString()).Exec(ctx); err != nil {
			t.Fatalf("write a %s entry: %v", kind, err)
		}
	}
	entry(ledgerentry.KindTopup, 50_000_000)
	entry(ledgerentry.KindInvoicePayment, 20_000_000)
	entry(ledgerentry.KindRefund, -5_000_000)
	entry(ledgerentry.KindUsage, -7_000_000)
	entry(ledgerentry.KindPromo, 99_000_000) // credit given, not money

	after, err := Collect(ctx, db.Client, today)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.TopupsMicros - before.TopupsMicros; got != 50_000_000 {
		t.Errorf("top-ups moved by %d", got)
	}
	if got := after.InvoicesMicros - before.InvoicesMicros; got != 20_000_000 {
		t.Errorf("invoices moved by %d", got)
	}
	if got := after.RefundedMicros - before.RefundedMicros; got != 5_000_000 {
		t.Errorf("refunds moved by %d, want a positive 5,000,000", got)
	}
	if got := after.UsageMicros - before.UsageMicros; got != 7_000_000 {
		t.Errorf("usage moved by %d, want a positive 7,000,000", got)
	}
	if got := after.NetMicros() - before.NetMicros(); got != 65_000_000 {
		t.Errorf("net moved by %d: promo credit must not be revenue", got)
	}
	if after.NewOrganizations-before.NewOrganizations != 1 || after.TotalOrganizations-before.TotalOrganizations != 1 {
		t.Errorf("organizations: %+v then %+v", before, after)
	}
	if after.NewProjects-before.NewProjects < 1 {
		t.Errorf("projects: %d then %d", before.NewProjects, after.NewProjects)
	}
}

func TestTheDigestIsPostedOnceInsideItsWindowAndRetriedAfterAFailure(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	rec := &recorder{}
	clock := time.Date(2026, 9, 29, 8, 59, 0, 0, time.UTC)
	g := NewGrowth(db, &memory{held: map[string]bool{}}, rec, "https://hooks.slack.com/services/T/B/x", 9*time.Hour, "")
	g.Now = func() time.Time { return clock }
	pass := func() bool {
		t.Helper()
		sent, err := g.Pass(t.Context())
		if err != nil && !strings.Contains(err.Error(), "post to Slack") {
			t.Fatal(err)
		}
		return sent
	}

	if pass() {
		t.Fatal("posted a minute before it is due")
	}

	clock = clock.Add(2 * time.Minute) // 09:01
	rec.fail = true
	if pass() {
		t.Fatal("reported a post that Slack refused")
	}
	rec.fail = false
	if !pass() {
		t.Fatal("did not retry after a failed post")
	}
	if pass() {
		t.Fatal("posted a second time the same day")
	}
	got := rec.messages()
	if len(got) != 1 || got[0].Title != "Growth, Mon 28 Sep 2026 (UTC)" {
		t.Fatalf("what was posted: %+v", got)
	}

	// A worker that comes up after the window does not post the morning's numbers at supper.
	g2 := NewGrowth(db, &memory{held: map[string]bool{}}, rec, "https://hooks.slack.com/services/T/B/x", 9*time.Hour, "")
	g2.Now = func() time.Time { return time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC) }
	if sent, _ := g2.Pass(t.Context()); sent {
		t.Fatal("posted long after the window closed")
	}
}
