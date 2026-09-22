package payments_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v85/webhook"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/wallet"
)

// The rules around Stripe, against the real database: every credit happens once however a payment
// is reported, every automatic charge happens once however its response is lost, and a decline is
// waited out rather than hammered.

const secret = "whsec_payments_test"

type fixture struct {
	t        *testing.T
	db       *pgdb.DB
	account  *ent.BillingAccount
	customer string
	gateway  *paymentstest.Gateway
	hooks    *payments.Webhooks
	now      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	ctx := context.Background()

	id := uuid.NewString()[:12]
	person, err := identity.EnsureUser(ctx, db.Client, identity.Claims{
		UID: "firebase-" + id, Email: "payments-" + id + "@studio.example",
		EmailVerified: true, Name: "Ada", Provider: "google.com",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	orgs, err := identity.Organizations(ctx, db.Client, person.ID)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no organization: %v", err)
	}
	account := testsupport.SeedBillingAccount(t, db, orgs[0].ID)
	customer := "cus_" + id
	account, err = account.Update().SetStripeCustomerID(customer).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: db, account: account, customer: customer,
		gateway: paymentstest.New(), now: time.Now().UTC()}
	f.gateway.Only = customer
	// Off again afterwards, so a later fixture's pass over the shared database does not find it.
	t.Cleanup(func() {
		_, _ = db.Client.BillingAccount.UpdateOneID(account.ID).SetAutoTopupEnabled(false).Save(context.Background())
	})
	f.hooks = &payments.Webhooks{DB: db, Secret: secret, RetryAfter: time.Hour, Now: f.clock}
	return f
}

func (f *fixture) clock() time.Time { return f.now }

func (f *fixture) autoTopUp() *payments.AutoTopUp {
	return &payments.AutoTopUp{DB: f.db, Gateway: f.gateway, Now: f.clock,
		StaleAfter: 10 * time.Minute, RetryAfter: time.Hour}
}

// enable turns automatic top-up on: $50 whenever the balance is below $10.
func (f *fixture) enable() {
	f.t.Helper()
	if _, err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
		SetAutoTopupEnabled(true).
		SetAutoTopupThresholdMicros(10 * billing.Micros).
		SetAutoTopupAmountMicros(50 * billing.Micros).
		Save(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) row() *ent.BillingAccount {
	f.t.Helper()
	row, err := f.db.Client.BillingAccount.Get(context.Background(), f.account.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

func (f *fixture) lots() []*ent.CreditLot {
	f.t.Helper()
	rows, err := f.db.Client.CreditLot.Query().Where(creditlot.AccountID(f.account.ID)).All(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func (f *fixture) run(a *payments.AutoTopUp) {
	f.t.Helper()
	if _, err := a.Run(context.Background()); err != nil {
		f.t.Logf("pass: %v", err)
	}
}

// owe puts the account in debt, the way usage does.
func (f *fixture) owe(amount int64) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := wallet.ChargeUsage(context.Background(), tx, wallet.UsageRequest{
		AccountID: f.account.ID, Amount: amount, At: time.Now(), Key: "usage:" + uuid.NewString(),
	}); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

// deliver signs an event the way Stripe does and hands it to the webhook.
func (f *fixture) deliver(eventID, kind string, object map[string]any) (payments.Outcome, error) {
	f.t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id": eventID, "object": "event", "type": kind, "api_version": "2026-04-22.dahlia",
		"created": f.now.Unix(), "data": map[string]any{"object": object},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret})
	return f.hooks.Receive(context.Background(), signed.Payload, signed.Header)
}

func (f *fixture) paymentIntent(id string, cents int64, meta map[string]string) map[string]any {
	return map[string]any{
		"id": id, "object": "payment_intent", "status": "succeeded", "currency": "usd",
		"amount": cents, "amount_received": cents, "customer": f.customer, "metadata": meta,
	}
}

func (f *fixture) ours(kind string) map[string]string {
	return map[string]string{payments.MetaAccount: f.account.UID.String(), payments.MetaKind: kind}
}

// ---- the webhook ------------------------------------------------------------

// A Checkout payment is reported twice — by the session and by its PaymentIntent — and each can be
// delivered more than once. However many arrive, in whatever order, the wallet is credited once.
func TestAPaymentReportedManyTimesIsCreditedOnce(t *testing.T) {
	f := newFixture(t)
	pi := "pi_" + uuid.NewString()[:12]
	session := map[string]any{
		"id": "cs_" + uuid.NewString()[:8], "object": "checkout.session", "payment_status": "paid",
		"payment_intent": pi, "customer": f.customer, "currency": "usd", "amount_total": 10_000,
		"metadata": f.ours(payments.KindCheckout),
	}
	intent := f.paymentIntent(pi, 10_000, f.ours(payments.KindCheckout))

	for i, delivery := range []struct {
		id, kind string
		object   map[string]any
	}{
		{"evt_session_" + pi, "checkout.session.completed", session},
		{"evt_intent_" + pi, "payment_intent.succeeded", intent},
		{"evt_session_" + pi, "checkout.session.completed", session}, // redelivered
		{"evt_intent_" + pi, "payment_intent.succeeded", intent},     // redelivered
	} {
		out, err := f.deliver(delivery.id, delivery.kind, delivery.object)
		if err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
		if i == 0 && !out.Credited {
			t.Error("the first report did not credit")
		}
		if i > 0 && out.Credited {
			t.Errorf("delivery %d credited again (%s)", i, out.Status)
		}
	}

	lots := f.lots()
	if len(lots) != 1 || lots[0].AmountMicros != 100*billing.Micros || lots[0].Kind != creditlot.KindPurchase {
		t.Fatalf("lots = %d; want one $100 purchase", len(lots))
	}
	if lots[0].ExpiresAt != nil {
		t.Error("a top-up expires; purchased credit must not")
	}
}

// A top-up pays what usage owed before any of it is spendable.
func TestATopUpPaysArrearsFirst(t *testing.T) {
	f := newFixture(t)
	f.owe(30 * billing.Micros)
	if _, err := f.deliver("evt_"+uuid.NewString(), "payment_intent.succeeded",
		f.paymentIntent("pi_"+uuid.NewString()[:12], 10_000, f.ours(payments.KindCheckout))); err != nil {
		t.Fatal(err)
	}
	row := f.row()
	if row.ArrearsMicros != 0 || row.BalanceMicros != 70*billing.Micros || row.State != "active" {
		t.Errorf("arrears %d, balance %d, state %s; want 0, $70, active", row.ArrearsMicros, row.BalanceMicros, row.State)
	}
}

// Only Stripe can say a payment happened.
func TestAnUnsignedOrForgedWebhookIsRefused(t *testing.T) {
	f := newFixture(t)
	payload := []byte(`{"id":"evt_forged","type":"payment_intent.succeeded","data":{"object":{}}}`)

	for name, header := range map[string]string{
		"no signature":    "",
		"wrong secret":    webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: "whsec_other"}).Header,
		"a stale signing": webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: time.Now().Add(-time.Hour)}).Header,
	} {
		if _, err := f.hooks.Receive(context.Background(), payload, header); !errors.Is(err, payments.ErrBadSignature) {
			t.Errorf("%s: %v, want a refused signature", name, err)
		}
	}
	if len(f.lots()) != 0 {
		t.Error("an unverified event credited a wallet")
	}
}

// A payment naming this account but made by somebody else's customer is not credited, whatever its
// metadata says; nor is one in another currency, nor one PlayerSense did not make.
func TestAPaymentThatIsNotThisAccountsIsNotCredited(t *testing.T) {
	f := newFixture(t)

	stranger := f.paymentIntent("pi_"+uuid.NewString()[:12], 10_000, f.ours(payments.KindCheckout))
	stranger["customer"] = "cus_somebody_else"
	euros := f.paymentIntent("pi_"+uuid.NewString()[:12], 10_000, f.ours(payments.KindCheckout))
	euros["currency"] = "eur"
	foreign := f.paymentIntent("pi_"+uuid.NewString()[:12], 10_000, map[string]string{})

	for name, object := range map[string]map[string]any{
		"another customer": stranger, "another currency": euros, "not ours": foreign,
	} {
		out, err := f.deliver("evt_"+uuid.NewString(), "payment_intent.succeeded", object)
		if err != nil {
			t.Fatalf("%s: %v — a payment that will never be creditable must not be retried", name, err)
		}
		if out.Status != "ignored" || out.Credited {
			t.Errorf("%s: %+v, want ignored", name, out)
		}
	}
	if len(f.lots()) != 0 {
		t.Error("a payment that was not this account's was credited")
	}
}

// A session paid by bank debit completes before the money arrives; nothing is credited until it does.
func TestAnUnpaidSessionCreditsNothing(t *testing.T) {
	f := newFixture(t)
	out, err := f.deliver("evt_"+uuid.NewString(), "checkout.session.completed", map[string]any{
		"id": "cs_x", "object": "checkout.session", "payment_status": "unpaid",
		"payment_intent": "pi_later", "customer": f.customer, "currency": "usd", "amount_total": 10_000,
		"metadata": f.ours(payments.KindCheckout),
	})
	if err != nil || out.Status != "ignored" || len(f.lots()) != 0 {
		t.Errorf("an unpaid session: %+v, %v, %d lots", out, err, len(f.lots()))
	}
}

// Every event Stripe is configured to send is recorded, and the ones nothing depends on are
// answered 200 and left alone.
func TestEveryConfiguredEventIsRecordedAndAcknowledged(t *testing.T) {
	f := newFixture(t)
	for _, kind := range []string{
		"account.updated", "checkout.session.async_payment_failed", "checkout.session.expired",
		"payment_intent.amount_capturable_updated", "payment_intent.created",
		"payment_intent.partially_funded", "payment_intent.processing",
		"payment_method.attached", "payment_method.automatically_updated",
		"payment_method.detached", "payment_method.updated",
	} {
		id := "evt_" + uuid.NewString()
		out, err := f.deliver(id, kind, map[string]any{"id": "obj_1", "object": "unknown"})
		if err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		if out.Status != "ignored" {
			t.Errorf("%s: %s, want ignored", kind, out.Status)
		}
		row, err := f.db.Client.StripeEvent.Query().Where(stripeevent.StripeEventID(id)).Only(context.Background())
		if err != nil || row.Type != kind || len(row.Payload) == 0 {
			t.Errorf("%s was not recorded with its body: %v", kind, err)
		}
	}
}

// ---- automatic top-up -------------------------------------------------------

// Below the threshold, one charge; above it, none.
func TestALowBalanceIsToppedUpOnce(t *testing.T) {
	f := newFixture(t)
	f.enable()
	a := f.autoTopUp()

	f.run(a)
	f.run(a)
	f.run(a)

	if n := f.gateway.Charged(); n != 1 {
		t.Fatalf("charged %d times, want once", n)
	}
	row := f.row()
	if row.BalanceMicros != 50*billing.Micros || row.AutoTopupPendingKey != nil {
		t.Errorf("balance %d, pending %v; want $50 and no claim", row.BalanceMicros, row.AutoTopupPendingKey)
	}
}

// The case idempotency exists for: the card was charged and the answer never came back. The claim
// is kept, retried once stale with the same key, and the one payment is credited once.
func TestALostResponseIsRetriedAsTheSameCharge(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.LoseResponses = 1
	a := f.autoTopUp()

	f.run(a) // charged at "Stripe", no answer here
	if row := f.row(); row.AutoTopupPendingKey == nil || len(f.lots()) != 0 {
		t.Fatal("a charge with no answer released its claim or credited")
	}
	f.run(a) // not stale yet: left alone
	if f.gateway.Calls != 1 {
		t.Fatalf("a fresh claim was retried: %d calls", f.gateway.Calls)
	}

	f.now = f.now.Add(11 * time.Minute)
	f.run(a) // stale: retried with its own key, and Stripe answers with the first payment

	if f.gateway.Charged() != 1 || f.gateway.Calls != 2 {
		t.Errorf("charged %d times in %d calls; want once in two", f.gateway.Charged(), f.gateway.Calls)
	}
	if lots := f.lots(); len(lots) != 1 {
		t.Errorf("lots = %d, want one", len(lots))
	}
	if f.row().AutoTopupPendingKey != nil {
		t.Error("the claim was not released once the payment was credited")
	}
}

// A claim older than Stripe's memory of keys is looked for before anything is sent again.
func TestAClaimOlderThanStripesMemoryIsFoundNotRepeated(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.LoseResponses = 1
	a := f.autoTopUp()
	f.run(a)

	f.now = f.now.Add(30 * time.Hour)
	f.gateway.Down = false
	calls := f.gateway.Calls
	f.run(a)

	if f.gateway.Calls != calls {
		t.Errorf("a charge was sent for a claim that should have been found (%d calls)", f.gateway.Calls-calls)
	}
	if f.gateway.Charged() != 1 || len(f.lots()) != 1 {
		t.Errorf("charged %d, lots %d; want one of each", f.gateway.Charged(), len(f.lots()))
	}
}

// The webhook for an automatic top-up the loop already credited changes nothing.
func TestTheWebhookForAnAutoTopUpTheLoopCreditedIsANoOp(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.run(f.autoTopUp())

	var charged payments.Payment
	for _, p := range f.gateway.Charges {
		charged = p
	}
	intent := f.paymentIntent(charged.ID, charged.AmountReceived, charged.Metadata)
	out, err := f.deliver("evt_"+uuid.NewString(), "payment_intent.succeeded", intent)
	if err != nil || out.Credited {
		t.Errorf("the webhook credited again: %+v, %v", out, err)
	}
	if len(f.lots()) != 1 {
		t.Errorf("lots = %d, want one", len(f.lots()))
	}
}

// A decline is waited out, not hammered, and says why.
func TestADeclinedCardIsNotRetriedUntilLater(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.Outcome = "declined"
	a := f.autoTopUp()

	f.run(a)
	f.run(a)
	if f.gateway.Calls != 1 {
		t.Fatalf("a declined card was tried %d times at once", f.gateway.Calls)
	}
	row := f.row()
	if row.AutoTopupPendingKey != nil || row.AutoTopupRetryAfter == nil || row.AutoTopupLastError == "" {
		t.Errorf("after a decline: pending %v, retry %v, error %q", row.AutoTopupPendingKey, row.AutoTopupRetryAfter, row.AutoTopupLastError)
	}

	f.gateway.Outcome = ""
	f.now = f.now.Add(2 * time.Hour)
	f.run(a)
	if f.gateway.Charged() != 1 || f.row().AutoTopupLastError != "" {
		t.Errorf("after the wait: charged %d, error %q", f.gateway.Charged(), f.row().AutoTopupLastError)
	}
}

// A declined automatic top-up reported by webhook — the path a slow bank takes — releases the same way.
func TestAFailedAutoTopUpWebhookReleasesTheClaim(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.Outcome = "processing"
	f.run(f.autoTopUp())
	row := f.row()
	if row.AutoTopupPendingKey == nil {
		t.Fatal("a processing charge released its claim")
	}
	meta := f.ours(payments.KindAutoTopUp)
	meta[payments.MetaKey] = *row.AutoTopupPendingKey
	failed := f.paymentIntent("pi_"+uuid.NewString()[:12], 5_000, meta)
	failed["status"] = "requires_payment_method"
	failed["amount_received"] = 0
	failed["last_payment_error"] = map[string]any{"message": "Insufficient funds."}

	if _, err := f.deliver("evt_"+uuid.NewString(), "payment_intent.payment_failed", failed); err != nil {
		t.Fatal(err)
	}
	row = f.row()
	if row.AutoTopupPendingKey != nil || row.AutoTopupLastError != "Insufficient funds." {
		t.Errorf("pending %v, error %q", row.AutoTopupPendingKey, row.AutoTopupLastError)
	}
}

// No saved card: nothing is sent, and the account is told why.
func TestNoSavedCardSendsNothing(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.Card = ""
	f.run(f.autoTopUp())
	if f.gateway.Calls != 0 || f.row().AutoTopupLastError == "" {
		t.Errorf("calls %d, error %q", f.gateway.Calls, f.row().AutoTopupLastError)
	}
}

// Stripe unreachable before anything was sent: the claim goes stale and is retried, never lost.
func TestStripeDownLosesNothing(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.Down = true
	a := f.autoTopUp()
	f.run(a)
	if f.row().AutoTopupPendingKey == nil {
		t.Fatal("the claim was dropped while Stripe was down")
	}
	f.gateway.Down = false
	f.now = f.now.Add(11 * time.Minute)
	f.run(a)
	if f.gateway.Charged() != 1 || len(f.lots()) != 1 {
		t.Errorf("after Stripe came back: charged %d, lots %d", f.gateway.Charged(), len(f.lots()))
	}
}

// Many billing writers at once top up one account once.
func TestConcurrentPassesChargeOnce(t *testing.T) {
	f := newFixture(t)
	f.enable()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.autoTopUp().Run(context.Background())
		}()
	}
	wg.Wait()
	if n := f.gateway.Charged(); n != 1 {
		t.Errorf("eight concurrent passes charged %d times", n)
	}
	if len(f.lots()) != 1 {
		t.Errorf("lots = %d", len(f.lots()))
	}
}

// Debt is covered in one charge of whole top-ups, not one top-up per pass; and never beyond the cap.
func TestTheAmountCoversTheDebtInOneCharge(t *testing.T) {
	account := func(balance int64) *ent.BillingAccount {
		return &ent.BillingAccount{BalanceMicros: balance,
			AutoTopupThresholdMicros: 10 * billing.Micros, AutoTopupAmountMicros: 50 * billing.Micros}
	}
	cases := []struct {
		balance int64
		want    int64
		needed  bool
	}{
		{20 * billing.Micros, 0, false},
		{10 * billing.Micros, 0, false},
		{5 * billing.Micros, 50 * billing.Micros, true},
		{-200 * billing.Micros, 250 * billing.Micros, true},
		{-1_000_000 * billing.Micros, billing.MaxTopUp, true},
	}
	for _, c := range cases {
		got, needed := payments.Amount(account(c.balance))
		if got != c.want || needed != c.needed {
			t.Errorf("balance %s: %s (%v), want %s (%v)", billing.USD(c.balance),
				billing.USD(got), needed, billing.USD(c.want), c.needed)
		}
	}
}
