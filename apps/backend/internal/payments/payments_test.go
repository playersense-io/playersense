package payments_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v85/webhook"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/ent/plan"
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

// enable turns automatic top-up on — $50 whenever the balance is below $10 — and, as the settings
// route does, queues a top-up if the balance is already below.
func (f *fixture) enable() {
	f.t.Helper()
	f.configure(true)
}

// enableUnqueued turns it on the way a hand edit in the database would: no signal. Only the sweep
// can find such an account.
func (f *fixture) enableUnqueued() {
	f.t.Helper()
	f.configure(false)
}

func (f *fixture) configure(queue bool) {
	f.t.Helper()
	update := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
		SetAutoTopupEnabled(true).
		SetAutoTopupThresholdMicros(10 * billing.Micros).
		SetAutoTopupAmountMicros(50 * billing.Micros)
	row := f.row()
	row.AutoTopupEnabled, row.AutoTopupThresholdMicros = true, 10*billing.Micros
	if queue && wallet.WantsTopUp(row, row.BalanceMicros) {
		update = update.SetAutoTopupRequestedAt(f.now)
	}
	if _, err := update.Save(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) topUp(amount int64) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, _, err := wallet.Grant(context.Background(), tx, wallet.GrantRequest{
		AccountID: f.account.ID, Kind: wallet.Purchase, Amount: amount,
		GrantedAt: time.Now(), Key: "topup:" + uuid.NewString(),
	}); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
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

// ---- the queue --------------------------------------------------------------

// The connection that did not exist: a usage charge that leaves the balance low queues a top-up in
// its own transaction, the next pass of the queue charges the card, and the account leaves the queue.
func TestAChargeThatLeavesTheBalanceLowQueuesATopUp(t *testing.T) {
	f := newFixture(t)
	f.topUp(20 * billing.Micros)
	f.enable()
	if f.row().AutoTopupRequestedAt != nil {
		t.Fatal("an account above its threshold is queued")
	}

	f.owe(15 * billing.Micros) // $20 → $5: below the $10 threshold
	if f.row().AutoTopupRequestedAt == nil {
		t.Fatal("the charge that left the balance low did not queue a top-up")
	}

	f.run(f.autoTopUp())
	row := f.row()
	if f.gateway.Charged() != 1 || row.BalanceMicros != 55*billing.Micros {
		t.Errorf("charged %d, balance %s; want one charge and $55", f.gateway.Charged(), billing.USD(row.BalanceMicros))
	}
	if row.AutoTopupRequestedAt != nil {
		t.Error("a topped-up account is still queued")
	}
}

// The queue only reads asking accounts; the sweep is the net for anything that never asked.
func TestTheSweepFindsWhatTheQueueNeverSaw(t *testing.T) {
	f := newFixture(t)
	f.enableUnqueued()
	a := f.autoTopUp()

	f.run(a)
	if f.gateway.Calls != 0 {
		t.Fatal("the queue charged an account that never asked")
	}
	if _, err := a.Sweep(context.Background()); err != nil {
		t.Logf("sweep: %v", err)
	}
	if f.gateway.Charged() != 1 {
		t.Errorf("the sweep charged %d times, want once", f.gateway.Charged())
	}
}

// Switching automatic top-up off takes the account out of the queue at its next pass, without a charge.
func TestADisabledAccountLeavesTheQueueUncharged(t *testing.T) {
	f := newFixture(t)
	f.enable()
	if _, err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
		SetAutoTopupEnabled(false).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.run(f.autoTopUp())
	if f.gateway.Calls != 0 || f.row().AutoTopupRequestedAt != nil {
		t.Errorf("calls %d, still queued %v", f.gateway.Calls, f.row().AutoTopupRequestedAt != nil)
	}
}

// ---- the daily limit --------------------------------------------------------

// Three charges in any 24 hours, declined or not; the fourth waits for the window, says why, and
// happens once the window reopens.
func TestAtMostThreeAutomaticChargesInADay(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.Outcome = "declined"
	a := f.autoTopUp() // RetryAfter is an hour

	for range 5 {
		f.run(a)
		f.now = f.now.Add(2 * time.Hour)
	}
	if f.gateway.Calls != 3 {
		t.Fatalf("%d charges sent in ten hours, want the limit of 3", f.gateway.Calls)
	}
	row := f.row()
	if !strings.Contains(row.AutoTopupLastError, "limit of 3") || row.AutoTopupRequestedAt == nil {
		t.Errorf("error %q, queued %v; want the limit named and the request kept", row.AutoTopupLastError, row.AutoTopupRequestedAt != nil)
	}

	f.gateway.Outcome = ""
	f.now = f.now.Add(15 * time.Hour) // past 24 hours from the first charge
	f.run(a)
	if f.gateway.Calls != 4 || f.gateway.Charged() != 1 {
		t.Errorf("after the window: %d calls, %d charged; want the fourth to go through", f.gateway.Calls, f.gateway.Charged())
	}
}

// A retry of a charge whose answer was lost is the same request to Stripe, and does not spend a slot.
func TestARetriedChargeDoesNotCountTowardTheLimit(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.gateway.LoseResponses = 1
	a := f.autoTopUp()
	f.run(a)
	f.now = f.now.Add(11 * time.Minute)
	f.run(a)
	if got := f.row().AutoTopupWindowCount; got != 1 {
		t.Errorf("window count %d after one charge and its retry, want 1", got)
	}
}

// Many workers at the limit's edge take one slot between them.
func TestConcurrentWorkersCannotExceedTheLimit(t *testing.T) {
	f := newFixture(t)
	f.enable()
	if _, err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
		SetAutoTopupWindowStartedAt(f.now).SetAutoTopupWindowCount(2).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.gateway.Outcome = "declined"

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.autoTopUp().Run(context.Background())
		}()
	}
	wg.Wait()
	if f.gateway.Calls != 1 || f.row().AutoTopupWindowCount != 3 {
		t.Errorf("%d charges sent with one slot left; window count %d", f.gateway.Calls, f.row().AutoTopupWindowCount)
	}
}

// ---- money going back -------------------------------------------------------

// credit is a Checkout top-up as Stripe reports it, credited through the webhook; returns its PI.
func (f *fixture) credit(cents int64) string {
	f.t.Helper()
	pi := "pi_" + uuid.NewString()[:12]
	if _, err := f.deliver("evt_"+uuid.NewString(), "payment_intent.succeeded",
		f.paymentIntent(pi, cents, f.ours(payments.KindCheckout))); err != nil {
		f.t.Fatal(err)
	}
	return pi
}

func (f *fixture) refund(pi, id, status string, cents int64) payments.Outcome {
	f.t.Helper()
	out, err := f.deliver("evt_"+uuid.NewString(), "refund.updated", map[string]any{
		"id": id, "object": "refund", "amount": cents, "currency": "usd", "status": status,
		"payment_intent": pi,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) dispute(kind, pi, id, status string, cents int64) payments.Outcome {
	f.t.Helper()
	out, err := f.deliver("evt_"+uuid.NewString(), kind, map[string]any{
		"id": id, "object": "dispute", "amount": cents, "currency": "usd", "status": status,
		"payment_intent": pi,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) balance() int64 {
	f.t.Helper()
	return f.row().BalanceMicros
}

func (f *fixture) refunds() int {
	f.t.Helper()
	n, err := f.db.Client.LedgerEntry.Query().
		Where(ledgerentry.AccountID(f.account.ID), ledgerentry.KindEQ(ledgerentry.KindRefund)).Count(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// A refund, reported by every event that reports one and delivered more than once, is taken back once
// — and automatic top-up is switched off, so the refunded card is not charged again seconds later.
func TestARefundIsTakenBackOnceAndStopsAutoTopUp(t *testing.T) {
	f := newFixture(t)
	f.enable()
	pi := f.credit(10_000)
	refund := "re_" + uuid.NewString()[:12]

	for _, kind := range []string{"refund.created", "refund.updated", "refund.updated"} {
		if _, err := f.deliver("evt_"+uuid.NewString(), kind, map[string]any{
			"id": refund, "object": "refund", "amount": 10_000, "currency": "usd",
			"status": "succeeded", "payment_intent": pi,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.deliver("evt_"+uuid.NewString(), "charge.refunded", map[string]any{
		"id": "ch_x", "object": "charge", "payment_intent": pi, "amount_refunded": 10_000,
	}); err != nil {
		t.Fatal(err)
	}

	row := f.row()
	if row.BalanceMicros != 0 || f.refunds() != 1 {
		t.Errorf("balance %s after one refund of the whole top-up, %d refund entries; want $0 and 1",
			billing.USD(row.BalanceMicros), f.refunds())
	}
	if row.AutoTopupEnabled || row.AutoTopupLastError == "" {
		t.Errorf("automatic top-up enabled=%v, error %q; want off with a reason", row.AutoTopupEnabled, row.AutoTopupLastError)
	}
	f.assertInvariant()
}

// Credit already spent is taken from the customer's other money, oldest first, then owed — and never
// from promotional credit, which was not paid for.
func TestARefundOfSpentCreditTakesPurchasesNotPromos(t *testing.T) {
	f := newFixture(t)
	first := f.credit(10_000)  // $100
	f.owe(70 * billing.Micros) // $70 of it spent on players
	f.credit(5_000)            // $50 more
	f.grantPromo(20 * billing.Micros)

	f.refund(first, "re_"+uuid.NewString()[:12], "succeeded", 10_000)

	// $30 left of the first, $50 of the second, $20 promo; refund $100 → first $30, second $50,
	// and $20 owed. The promo stays.
	row := f.row()
	if row.ArrearsMicros != 20*billing.Micros {
		t.Errorf("arrears %s, want $20", billing.USD(row.ArrearsMicros))
	}
	var promo, purchased int64
	for _, lot := range f.lots() {
		if lot.Kind == creditlot.KindPromo {
			promo += lot.RemainingMicros
		} else {
			purchased += lot.RemainingMicros
		}
	}
	if promo != 20*billing.Micros || purchased != 0 {
		t.Errorf("promo %s, purchased %s left; want $20 promo untouched and no purchase", billing.USD(promo), billing.USD(purchased))
	}
	f.assertInvariant()
}

// A pending refund takes nothing; the refund takes when it succeeds; and a refund that then fails puts
// back exactly what it took, once.
func TestARefundTakesOnlyWhenItSucceedsAndFailingPutsItBack(t *testing.T) {
	f := newFixture(t)
	pi := f.credit(10_000)
	refund := "re_" + uuid.NewString()[:12]

	if out := f.refund(pi, refund, "pending", 4_000); out.Status != "ignored" || f.balance() != 100*billing.Micros {
		t.Fatalf("a pending refund: %s, balance %s", out.Status, billing.USD(f.balance()))
	}
	f.refund(pi, refund, "succeeded", 4_000)
	if f.balance() != 60*billing.Micros {
		t.Fatalf("after the refund: %s, want $60", billing.USD(f.balance()))
	}
	f.refund(pi, refund, "failed", 4_000)
	f.refund(pi, refund, "failed", 4_000)
	if f.balance() != 100*billing.Micros {
		t.Errorf("after the refund failed: %s, want $100 restored once", billing.USD(f.balance()))
	}
	f.assertInvariant()
}

// A refund of something PlayerSense did not sell is nobody's wallet's business.
func TestARefundOfAnotherPaymentIsIgnored(t *testing.T) {
	f := newFixture(t)
	f.credit(10_000)
	if out := f.refund("pi_unknown", "re_x"+uuid.NewString()[:8], "succeeded", 10_000); out.Status != "ignored" {
		t.Errorf("status %s, want ignored", out.Status)
	}
	if f.balance() != 100*billing.Micros {
		t.Error("an unrelated refund moved the balance")
	}
}

// A dispute: taken when the funds are withdrawn (reported twice, taken once), put back once when won.
func TestADisputeIsTakenBackAndPutBackWhenWon(t *testing.T) {
	f := newFixture(t)
	f.enable()
	pi := f.credit(10_000)
	id := "dp_" + uuid.NewString()[:12]

	f.dispute("charge.dispute.created", pi, id, "needs_response", 10_000)
	f.dispute("charge.dispute.funds_withdrawn", pi, id, "needs_response", 10_000)
	f.dispute("charge.dispute.updated", pi, id, "under_review", 10_000)
	if f.balance() != 0 || f.refunds() != 1 || f.row().AutoTopupEnabled {
		t.Fatalf("after the dispute: balance %s, %d entries, auto %v", billing.USD(f.balance()), f.refunds(), f.row().AutoTopupEnabled)
	}

	f.dispute("charge.dispute.funds_reinstated", pi, id, "won", 10_000)
	f.dispute("charge.dispute.closed", pi, id, "won", 10_000)
	if f.balance() != 100*billing.Micros {
		t.Errorf("after winning: %s, want $100 restored once", billing.USD(f.balance()))
	}
	f.assertInvariant()
}

// An inquiry moves no money; a lost dispute leaves the credit taken.
func TestAnInquiryTakesNothingAndALostDisputeKeepsItTaken(t *testing.T) {
	f := newFixture(t)
	pi := f.credit(10_000)
	if out := f.dispute("charge.dispute.created", pi, "dp_i"+uuid.NewString()[:8], "warning_needs_response", 10_000); out.Status != "ignored" {
		t.Errorf("an inquiry: %s", out.Status)
	}
	lost := "dp_l" + uuid.NewString()[:8]
	f.dispute("charge.dispute.funds_withdrawn", pi, lost, "needs_response", 10_000)
	f.dispute("charge.dispute.closed", pi, lost, "lost", 10_000)
	if f.balance() != 0 {
		t.Errorf("after a lost dispute: %s, want $0", billing.USD(f.balance()))
	}
}

// Invoices, Issuing and Radar reviews never move money here — least of all an invoice describing a
// payment already credited, which would otherwise be a second report of it. The invoice events a
// receipt is kept from (ADR 0047) are processed, for the receipt, and still move nothing.
func TestInvoiceIssuingAndReviewEventsNeverMoveMoney(t *testing.T) {
	f := newFixture(t)
	pi := f.credit(10_000)
	for _, kind := range []string{
		"invoice.created", "invoice.deleted", "invoice.finalization_failed", "invoice.finalized",
		"invoice.marked_uncollectible", "invoice.overdue", "invoice.overpaid", "invoice.paid",
		"invoice.payment_action_required", "invoice.payment_attempt_required", "invoice.payment_failed",
		"invoice.payment_succeeded", "invoice.sent", "invoice.upcoming", "invoice.updated",
		"invoice.voided", "invoice.will_be_due", "invoice_payment.paid", "invoiceitem.created",
		"invoiceitem.deleted", "issuing_dispute.closed", "issuing_dispute.created",
		"issuing_dispute.funds_reinstated", "issuing_dispute.funds_rescinded",
		"issuing_dispute.submitted", "issuing_dispute.updated", "review.closed",
	} {
		out, err := f.deliver("evt_"+uuid.NewString(), kind, map[string]any{
			"id": "in_" + uuid.NewString()[:8], "object": "invoice", "payment_intent": pi,
			"amount_paid": 10_000, "currency": "usd", "customer": f.customer, "status": "paid",
			"metadata": f.ours(payments.KindCheckout),
		})
		want := "ignored"
		switch kind {
		case "invoice.finalized", "invoice.paid", "invoice.voided", "invoice.marked_uncollectible", "invoice.updated":
			want = "processed"
		}
		if err != nil || out.Status != want {
			t.Errorf("%s: %s, %v; want %s", kind, out.Status, err, want)
		}
	}
	if len(f.lots()) != 1 || f.balance() != 100*billing.Micros {
		t.Errorf("lots %d, balance %s; want the one top-up untouched", len(f.lots()), billing.USD(f.balance()))
	}
}

func (f *fixture) grantPromo(amount int64) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, _, err := wallet.Grant(context.Background(), tx, wallet.GrantRequest{
		AccountID: f.account.ID, Kind: wallet.Promo, Amount: amount,
		GrantedAt: time.Now(), Key: "promo:" + uuid.NewString(),
	}); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

// assertInvariant: the ledger's running total is what the lots hold minus what is owed — arrears, and
// an invoiced account's receivable.
func (f *fixture) assertInvariant() {
	f.t.Helper()
	row := f.row()
	var held int64
	for _, lot := range f.lots() {
		held += lot.RemainingMicros
	}
	if row.BalanceMicros != held-row.ArrearsMicros-row.ReceivableMicros {
		f.t.Errorf("balance %s ≠ lots %s − arrears %s − receivable %s", billing.USD(row.BalanceMicros),
			billing.USD(held), billing.USD(row.ArrearsMicros), billing.USD(row.ReceivableMicros))
	}
}

// ---- subscriptions (ADR 0042) -----------------------------------------------

// subscriptionPlans gives the fixture a free default plan and an enterprise plan with Stripe prices.
func (f *fixture) subscriptionPlans() (free, enterprise *ent.Plan) {
	f.t.Helper()
	ctx := context.Background()
	id := uuid.NewString()[:8]
	var err error
	free, err = f.db.Client.Plan.Create().SetKey("free-" + id).SetName("Free").SetIsDefault(true).Save(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _, _ = f.db.Client.Plan.UpdateOneID(free.ID).SetIsDefault(false).Save(context.Background()) })
	// Only one default at a time, as the catalogue route guarantees.
	if _, err := f.db.Client.Plan.Update().Where(plan.IsDefault(true), plan.IDNEQ(free.ID)).SetIsDefault(false).Save(ctx); err != nil {
		f.t.Fatal(err)
	}
	enterprise, err = f.db.Client.Plan.Create().SetKey("enterprise-" + id).SetName("Enterprise").
		SetMonthlyPriceMicros(99 * billing.Micros).SetYearlyPriceMicros(999 * billing.Micros).
		SetStripePriceMonthlyID("price_m_" + id).SetStripePriceYearlyID("price_y_" + id).
		SetFeatures([]string{string(billing.PremiumSupport), string(billing.SlackReports), string(billing.MCPAccess)}).
		Save(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return free, enterprise
}

func (f *fixture) subscriptionEvent(kind, id, status, price, interval string, at time.Time, cancelAtEnd bool) payments.Outcome {
	f.t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id": "evt_" + uuid.NewString(), "object": "event", "type": kind, "api_version": "2026-04-22.dahlia",
		"created": at.Unix(), "data": map[string]any{"object": map[string]any{
			"id": id, "object": "subscription", "status": status, "customer": f.customer,
			"cancel_at_period_end": cancelAtEnd,
			"items": map[string]any{"object": "list", "data": []any{map[string]any{
				"id": "si_1", "object": "subscription_item", "current_period_end": at.Add(30 * 24 * time.Hour).Unix(),
				"price": map[string]any{"id": price, "object": "price", "recurring": map[string]any{"interval": interval}},
			}}},
		}},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret})
	out, err := f.hooks.Receive(context.Background(), signed.Payload, signed.Header)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// Subscribing puts the account on the plan; canceling at period end keeps it there until the end; the
// end puts it back on the default plan.
func TestASubscriptionMovesTheAccountBetweenPlans(t *testing.T) {
	f := newFixture(t)
	free, enterprise := f.subscriptionPlans()
	sub := "sub_" + uuid.NewString()[:10]
	t0 := time.Now().Add(-time.Hour)

	f.subscriptionEvent("customer.subscription.created", sub, "active", *enterprise.StripePriceMonthlyID, "month", t0, false)
	row := f.row()
	if row.PlanID != enterprise.ID || *row.SubscriptionStatus != "active" || *row.SubscriptionInterval != "month" {
		t.Fatalf("after subscribing: plan %d, status %v, interval %v", row.PlanID, row.SubscriptionStatus, row.SubscriptionInterval)
	}

	f.subscriptionEvent("customer.subscription.updated", sub, "active", *enterprise.StripePriceMonthlyID, "month", t0.Add(time.Minute), true)
	if row := f.row(); row.PlanID != enterprise.ID || !row.SubscriptionCancelAtPeriodEnd {
		t.Errorf("canceling at period end: plan %d, cancel %v; want still enterprise, true", row.PlanID, row.SubscriptionCancelAtPeriodEnd)
	}

	f.subscriptionEvent("customer.subscription.deleted", sub, "canceled", *enterprise.StripePriceMonthlyID, "month", t0.Add(2*time.Minute), true)
	if row := f.row(); row.PlanID != free.ID || *row.SubscriptionStatus != "canceled" {
		t.Errorf("after it ended: plan %d, status %v; want the free plan", row.PlanID, *row.SubscriptionStatus)
	}
}

// An `updated` delivered after the `deleted` it preceded does not bring the subscription back.
func TestALateSubscriptionEventCannotUndoANewerOne(t *testing.T) {
	f := newFixture(t)
	free, enterprise := f.subscriptionPlans()
	sub := "sub_" + uuid.NewString()[:10]
	t0 := time.Now().Add(-time.Hour)

	f.subscriptionEvent("customer.subscription.created", sub, "active", *enterprise.StripePriceYearlyID, "year", t0, false)
	f.subscriptionEvent("customer.subscription.deleted", sub, "canceled", *enterprise.StripePriceYearlyID, "year", t0.Add(2*time.Minute), false)
	out := f.subscriptionEvent("customer.subscription.updated", sub, "active", *enterprise.StripePriceYearlyID, "year", t0.Add(time.Minute), false)
	if out.Status != "ignored" || f.row().PlanID != free.ID {
		t.Errorf("a stale update: %s, plan %d; want ignored and still free", out.Status, f.row().PlanID)
	}
}

// Stripe retrying a failed renewal (past_due) keeps the plan; giving up (unpaid) ends it.
func TestARenewalBeingRetriedKeepsThePlan(t *testing.T) {
	f := newFixture(t)
	free, enterprise := f.subscriptionPlans()
	sub := "sub_" + uuid.NewString()[:10]
	t0 := time.Now().Add(-time.Hour)
	price := *enterprise.StripePriceMonthlyID

	f.subscriptionEvent("customer.subscription.created", sub, "active", price, "month", t0, false)
	f.subscriptionEvent("customer.subscription.updated", sub, "past_due", price, "month", t0.Add(time.Minute), false)
	if f.row().PlanID != enterprise.ID {
		t.Fatal("a renewal being retried took the plan away")
	}
	f.subscriptionEvent("customer.subscription.updated", sub, "unpaid", price, "month", t0.Add(2*time.Minute), false)
	if f.row().PlanID != free.ID {
		t.Error("a renewal Stripe gave up on kept the plan")
	}
}

// A subscription that was never the account's cannot end the plan staff gave it by contract, and a
// price that belongs to no plan moves nothing.
func TestOnlyTheAccountsOwnSubscriptionMovesItsPlan(t *testing.T) {
	f := newFixture(t)
	_, enterprise := f.subscriptionPlans()
	if _, err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).SetPlanID(enterprise.ID).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-time.Hour)
	f.subscriptionEvent("customer.subscription.deleted", "sub_other", "incomplete_expired", *enterprise.StripePriceMonthlyID, "month", t0, false)
	if f.row().PlanID != enterprise.ID {
		t.Error("a subscription that was never the account's ended its contract plan")
	}
	if out := f.subscriptionEvent("customer.subscription.created", "sub_x", "active", "price_nobody", "month", t0.Add(time.Minute), false); out.Status != "ignored" {
		t.Errorf("an unknown price: %s, want ignored", out.Status)
	}
}
