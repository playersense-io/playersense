package main

import (
	"net/http"
	"playersense.dev/backend/internal/staff"
	"strings"
	"testing"
	"time"

	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/wallet"
)

// The wallet and the usage page, through HTTP.
//
// The ordering rules themselves are proved in internal/wallet against values. What is proved here is
// that the surface a person uses reaches them: an operator grants, the order is honoured, a customer
// reads their own balance and cannot touch anybody's credit.

// money is an organization with a billing account, its owner, and the operator's client.
type money struct {
	*stack
	owner    fbtest.Person
	slug     string
	operator *testclient.Client
}

func newWallet(t *testing.T) *money {
	t.Helper()
	return newWalletWith(t, paymentstest.New())
}

// newWalletWith is a wallet whose Stripe is the given fake; nil is a deployment with no Stripe key.
func newWalletWith(t *testing.T, stripe *paymentstest.Gateway) *money {
	t.Helper()
	var gateway payments.Gateway = payments.Disabled{}
	if stripe != nil {
		gateway = stripe
	}
	s := newStackWith(t, muzak.WithSingleton(gateway))
	owner := person(t)
	slug := s.createOrg(t, owner)

	org := s.organization(t, slug)
	testsupport.SeedBillingAccount(t, s.db, org.ID)

	return &money{
		stack: s, owner: owner, slug: slug,
		operator: staffOnly(s.staffClient(t, staff.Admin)),
	}
}

func (w *money) base() string  { return "/v1/organizations/" + w.slug }
func (w *money) admin() string { return "/v1/operator/organizations/" + w.slug }

func (w *money) client(t *testing.T) *testclient.Client {
	t.Helper()
	return w.as(t, w.owner)
}

// grant is the operator's action: add credit to this organization.
func (w *money) grant(t *testing.T, body map[string]any) creditGrantedOut {
	t.Helper()
	if _, named := body["reference"]; !named {
		body["reference"] = unique("ref")
	}
	return testclient.Decoded[creditGrantedOut](
		w.operator.Post(w.admin()+"/credits", testclient.JSON(body)).
			AssertStatus(http.StatusCreated))
}

func (w *money) read(t *testing.T) billingOut {
	t.Helper()
	return testclient.Decoded[billingOut](
		w.client(t).Get(w.base() + "/billing").AssertStatus(http.StatusOK))
}

func dollars(n int64) int64 { return n * billing.Micros }

// TestCreditIsSpentPromoFirstThenOldestPurchase is the rule, end to end.
//
// The same ordering internal/wallet proves against values, now through two HTTP surfaces and the
// database between them — because an ordering that is right in a sort function and wrong in the query
// feeding it is an ordering that is wrong.
func TestCreditIsSpentPromoFirstThenOldestPurchase(t *testing.T) {
	w := newWallet(t)
	now := time.Now().UTC()

	// A purchase from a month ago, a newer one, and a promo granted today. Chronologically the
	// promo is last, which is the case that fails if the order is simply by date.
	older := now.AddDate(0, 0, -30)
	newer := now.AddDate(0, 0, -2)
	w.grant(t, map[string]any{
		"kind": wallet.Purchase, "amount_micros": dollars(40),
		"granted_at": older, "description": "Invoice 1001",
	})
	w.grant(t, map[string]any{
		"kind": wallet.Purchase, "amount_micros": dollars(40),
		"granted_at": newer, "description": "Invoice 1002",
	})
	w.grant(t, map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(100),
		"description": "Launch credit",
	})

	balance := w.read(t)
	if balance.AvailableMicros != dollars(180) {
		t.Fatalf("available = %s, want $180", billing.USD(balance.AvailableMicros))
	}
	if balance.PromoMicros != dollars(100) || balance.PurchasedMicros != dollars(80) {
		t.Errorf("split = promo %s, purchased %s",
			billing.USD(balance.PromoMicros), billing.USD(balance.PurchasedMicros))
	}
	// The list is in the order it will be spent, so a customer reading it top to bottom reads
	// what their statement will consume.
	if len(balance.Lots) != 3 ||
		balance.Lots[0].Kind != wallet.Promo ||
		balance.Lots[1].Description != "Invoice 1001" ||
		balance.Lots[2].Description != "Invoice 1002" {
		t.Fatalf("lots = %+v", balance.Lots)
	}

	// $150: the whole promo, then the whole older purchase, then $10 of the newer one. Chosen to
	// need all three, because two draws would not show whether the purchases were ordered.
	charged := testclient.Decoded[creditChargedOut](
		w.operator.Post(w.admin()+"/charges", testclient.JSON(map[string]any{
			"amount_micros": dollars(150), "description": "March usage",
			"kind": "usage", "reference": unique("charge"),
		})).AssertStatus(http.StatusOK))

	if len(charged.Draws) != 3 {
		t.Fatalf("draws = %+v, want three", charged.Draws)
	}
	if charged.Draws[0].Kind != wallet.Promo || charged.Draws[0].AmountMicros != dollars(100) {
		t.Errorf("the first draw was %+v, want the whole promo", charged.Draws[0])
	}
	if charged.Draws[1].AmountMicros != dollars(40) || charged.Draws[2].AmountMicros != dollars(10) {
		t.Errorf("draws = %+v, want $40 then $10 from the purchases", charged.Draws[1:])
	}
	if charged.AvailableMicros != dollars(30) {
		t.Errorf("available after = %s, want $30", billing.USD(charged.AvailableMicros))
	}

	// One entry on the statement for a charge that consumed three grants: the customer was
	// charged once.
	statement := testclient.Decoded[ledgerOut](
		w.client(t).Get(w.base() + "/billing/ledger").AssertStatus(http.StatusOK))
	usage := 0
	for _, entry := range statement.Entries {
		if entry.Kind == "usage" {
			usage++
			if entry.AmountMicros != -dollars(150) {
				t.Errorf("the charge entry is %s", billing.USD(entry.AmountMicros))
			}
		}
	}
	if usage != 1 {
		t.Errorf("the statement has %d usage entries, want one", usage)
	}
}

// TestAPromoExpiresInNinetyDaysUnlessAnotherDateIsNamed is the requirement, stated as a test.
func TestAPromoExpiresInNinetyDaysUnlessAnotherDateIsNamed(t *testing.T) {
	w := newWallet(t)
	now := time.Now().UTC()

	// The default.
	granted := w.grant(t, map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(100), "description": "Default term",
	})
	if granted.Lot.ExpiresAt == nil {
		t.Fatal("a promo was granted with no expiry")
	}
	if days := granted.Lot.ExpiresAt.Sub(now).Hours() / 24; days < 89 || days > 91 {
		t.Errorf("the promo expires in %.1f days, want ninety", days)
	}

	// An operator's own date, which is the whole of "ninety days unless we agreed otherwise".
	chosen := now.AddDate(0, 6, 0).Truncate(time.Second)
	named := w.grant(t, map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(50),
		"expires_at": chosen, "description": "Six months, as agreed",
	})
	if named.Lot.ExpiresAt == nil || !named.Lot.ExpiresAt.Equal(chosen) {
		t.Errorf("expires_at = %v, want %v", named.Lot.ExpiresAt, chosen)
	}

	// Purchased credit does not expire, and naming a date for it is refused rather than ignored:
	// money a customer paid for is not ours to put a clock on.
	bought := w.grant(t, map[string]any{
		"kind": wallet.Purchase, "amount_micros": dollars(25), "description": "Top-up",
	})
	if bought.Lot.ExpiresAt != nil {
		t.Errorf("purchased credit expires at %v", bought.Lot.ExpiresAt)
	}
	refused := w.operator.Post(w.admin()+"/credits", testclient.JSON(map[string]any{
		"kind": wallet.Purchase, "amount_micros": dollars(25),
		"expires_at": chosen, "reference": unique("ref"),
	})).AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "does not expire") {
		t.Errorf("the refusal does not say why: %s", refused)
	}

	// A date already in the past is refused too: it would create a grant nobody can spend and
	// that the next sweep writes off, which looks like the money was taken away.
	w.operator.Post(w.admin()+"/credits", testclient.JSON(map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(10),
		"expires_at": now.AddDate(0, 0, -1), "reference": unique("ref"),
	})).AssertStatus(http.StatusUnprocessableEntity)
}

// TestWhatExpiresSoonIsOnTheBalance is the number a balance is dangerous without.
//
// A studio plans against $100 and loses it on Friday. The warning window is what makes the balance
// something they can act on rather than something they discover.
func TestWhatExpiresSoonIsOnTheBalance(t *testing.T) {
	w := newWallet(t)
	now := time.Now().UTC()

	soon := now.AddDate(0, 0, 5)
	w.grant(t, map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(100),
		"expires_at": soon, "description": "Trial credit",
	})
	w.grant(t, map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(60),
		"expires_at": now.AddDate(0, 3, 0), "description": "Later",
	})
	w.grant(t, map[string]any{
		"kind": wallet.Purchase, "amount_micros": dollars(20), "description": "Top-up",
	})

	balance := w.read(t)
	if len(balance.ExpiringSoon) != 1 {
		t.Fatalf("expiring soon = %+v, want just the one inside the window", balance.ExpiringSoon)
	}
	if balance.ExpiringSoon[0].Description != "Trial credit" {
		t.Errorf("expiring = %q", balance.ExpiringSoon[0].Description)
	}
	// Purchased credit never appears there, because it cannot expire.
	for _, lot := range balance.ExpiringSoon {
		if lot.Kind == wallet.Purchase {
			t.Error("purchased credit was reported as expiring")
		}
	}
}

// TestLapsedCreditLeavesTheBalanceAndStaysOnTheStatement is the two-numbers design, observed.
//
// Between a lot lapsing and the sweep reaching it, the ledger's running total is higher than what can
// be spent. Both are returned, because a number that looks wrong should be explainable — and the
// spendable one is the one every decision is made against.
func TestLapsedCreditLeavesTheBalanceAndStaysOnTheStatement(t *testing.T) {
	w := newWallet(t)
	now := time.Now().UTC()

	// Backdated so it has already lapsed, which is the state the sweep has not reached yet.
	w.grant(t, map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(100),
		"granted_at": now.AddDate(0, 0, -100),
		"expires_at": now.AddDate(0, 0, -1), "description": "Expired trial",
	})
	w.grant(t, map[string]any{
		"kind": wallet.Purchase, "amount_micros": dollars(30), "description": "Top-up",
	})

	balance := w.read(t)
	if balance.AvailableMicros != dollars(30) {
		t.Errorf("available = %s, want $30 — the promo lapsed", billing.USD(balance.AvailableMicros))
	}
	if balance.LedgerBalanceMicros != dollars(130) {
		t.Errorf("ledger balance = %s, want $130 until the sweep runs",
			billing.USD(balance.LedgerBalanceMicros))
	}
	// The lapsed lot is still listed, and marked. A customer looking for their $100 needs to see
	// where it went; an absent row answers nothing.
	found := false
	for _, lot := range balance.Lots {
		if lot.Description == "Expired trial" {
			found = true
			if !lot.Expired {
				t.Error("the lapsed lot is not marked expired")
			}
		}
	}
	if !found {
		t.Error("the lapsed lot is missing from the list")
	}

	// And a charge it could have covered is refused, which is what makes the overstated column
	// safe: the spend path checks the lots rather than the number.
	refused := w.operator.Post(w.admin()+"/charges", testclient.JSON(map[string]any{
		"amount_micros": dollars(100), "description": "Usage", "reference": unique("charge"),
	})).AssertStatus(http.StatusConflict)
	for _, want := range []string{"$30.00", "$100.00"} {
		if !strings.Contains(refused.String(), want) {
			t.Errorf("the refusal does not name %s: %s", want, refused)
		}
	}
}

// TestGrantingTwiceWithOneReferenceGrantsOnce is the protection an operator pressing a button twice
// needs.
//
// The reference is required rather than generated for exactly this: a key the server invented would
// be different on the retry, which is how an account ends up with $200.
func TestGrantingTwiceWithOneReferenceGrantsOnce(t *testing.T) {
	w := newWallet(t)
	body := map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(100),
		"description": "Launch credit", "reference": unique("ref"),
	}

	first := w.grant(t, body)
	// The second is a 200 rather than a 201, and says it granted nothing.
	second := testclient.Decoded[creditGrantedOut](
		w.operator.Post(w.admin()+"/credits", testclient.JSON(body)).
			AssertStatus(http.StatusOK))

	if !first.Granted || second.Granted {
		t.Errorf("granted = %v then %v, want true then false", first.Granted, second.Granted)
	}
	if first.Lot.ID != second.Lot.ID {
		t.Errorf("two lots: %s and %s", first.Lot.ID, second.Lot.ID)
	}
	if balance := w.read(t); balance.AvailableMicros != dollars(100) {
		t.Errorf("available = %s, want $100 once", billing.USD(balance.AvailableMicros))
	}
}

// TestACustomerCannotGrantThemselvesCredit is the permission boundary that matters commercially.
//
// A studio that could grant itself promotional credit would be a studio on an unlimited plan. There
// is no permission for it — the operator surface is a different credential entirely — so an owner,
// who holds everything, still cannot reach it.
func TestACustomerCannotGrantThemselvesCredit(t *testing.T) {
	w := newWallet(t)

	// The owner holds every permission in the vocabulary.
	w.client(t).Post(w.admin()+"/credits", testclient.JSON(map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(1000), "reference": unique("ref"),
	})).AssertStatus(http.StatusForbidden)

	// Nor charge anybody, including themselves.
	w.client(t).Post(w.admin()+"/charges", testclient.JSON(map[string]any{
		"amount_micros": dollars(1), "description": "x", "reference": unique("c"),
	})).AssertStatus(http.StatusForbidden)

	// And no credential at all is refused before anything is read.
	newAnonClient(t).Post(w.admin()+"/credits", testclient.JSON(map[string]any{
		"kind": wallet.Promo, "amount_micros": dollars(1), "reference": unique("ref"),
	})).AssertStatus(http.StatusUnauthorized)

	if balance := w.read(t); balance.AvailableMicros != 0 {
		t.Errorf("available = %s, want nothing", billing.USD(balance.AvailableMicros))
	}
}

// TestBillingNeedsItsOwnPermissions is why the BILLING role exists.
//
// The person who pays is often not the person who should read a player's data (§08), so reading the
// bill and reading the analytics are different privileges. A DEVELOPER holds `analytics.read` and
// neither billing permission.
func TestBillingNeedsItsOwnPermissions(t *testing.T) {
	w := newWallet(t)

	biller := person(t)
	w.join(t, w.slug, biller, authz.RoleBilling, true)
	developer := person(t)
	w.join(t, w.slug, developer, authz.RoleDeveloper, true)

	// The billing role reads the bill and the usage page.
	w.as(t, biller).Get(w.base() + "/billing").AssertStatus(http.StatusOK)
	w.as(t, biller).Get(w.base() + "/usage").AssertStatus(http.StatusOK)

	// The developer does not, and the refusal names what is missing.
	refused := w.as(t, developer).Get(w.base() + "/billing").AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), string(authz.BillingRead)) {
		t.Errorf("the refusal does not name the missing permission: %s", refused)
	}

	// Configuring automatic charging is the wider of the two, and the developer has neither.
	w.as(t, developer).Put(w.base()+"/billing/auto-topup",
		testclient.JSON(map[string]any{"enabled": false})).
		AssertStatus(http.StatusForbidden)
	// A billing manager can, and the domain rule is what refuses a configuration that would
	// charge in a loop.
	w.as(t, biller).Put(w.base()+"/billing/auto-topup",
		testclient.JSON(map[string]any{"enabled": false})).
		AssertStatus(http.StatusOK)
}

// TestAutoTopUpRefusesAConfigurationThatWouldChargeInALoop gives the orphaned validator its caller.
//
// A threshold at or above the amount tops up into a balance still under the threshold, so the next
// check charges again, and the next — against a real card, all night.
func TestAutoTopUpRefusesAConfigurationThatWouldChargeInALoop(t *testing.T) {
	w := newWallet(t)
	client := w.client(t)
	path := w.base() + "/billing/auto-topup"

	// Enabled with no saved card: refused, because it would do nothing and the account would
	// find out by being suspended. Whether there is a card is Stripe's answer, not a field.
	refused := client.Put(path, testclient.JSON(map[string]any{
		"enabled": true, "threshold_micros": dollars(10), "amount_micros": dollars(50),
	})).AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "payment method") {
		t.Errorf("the refusal does not mention the card: %s", refused)
	}

	// A card saved at Stripe, by a top-up. From here on the card exists.
	client.Post(w.base()+"/billing/top-ups", testclient.JSON(map[string]any{
		"amount_micros": dollars(20),
	})).AssertStatus(http.StatusOK)

	// A threshold above the amount: refused with the arithmetic named.
	refused = client.Put(path, testclient.JSON(map[string]any{
		"enabled": true, "threshold_micros": dollars(100), "amount_micros": dollars(50),
	})).AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "immediately") {
		t.Errorf("the refusal does not explain the loop: %s", refused)
	}

	// Below the processing floor: mostly fee.
	client.Put(path, testclient.JSON(map[string]any{
		"enabled": true, "threshold_micros": dollars(1), "amount_micros": dollars(2),
	})).AssertStatus(http.StatusUnprocessableEntity)

	// And one that works.
	ok := testclient.Decoded[billingOut](client.Put(path, testclient.JSON(map[string]any{
		"enabled": true, "threshold_micros": dollars(10), "amount_micros": dollars(50),
	})).AssertStatus(http.StatusOK))
	if !ok.AutoTopUp.Enabled || ok.AutoTopUp.AmountMicros != dollars(50) {
		t.Errorf("auto top-up = %+v", ok.AutoTopUp)
	}

	// Turning it off keeps the amounts, so turning it back on restores what was configured
	// rather than presenting an empty form.
	off := testclient.Decoded[billingOut](client.Put(path,
		testclient.JSON(map[string]any{"enabled": false})).AssertStatus(http.StatusOK))
	if off.AutoTopUp.Enabled || off.AutoTopUp.AmountMicros != dollars(50) {
		t.Errorf("after disabling = %+v, want the amounts kept", off.AutoTopUp)
	}
}

// TestTheUsagePageSaysWhatThePlanAllows is why both halves are on one response.
//
// 41,207 tracked players means nothing without the 10,000 the plan includes, and the allowance means
// nothing without the number beside it.
func TestTheUsagePageSaysWhatThePlanAllows(t *testing.T) {
	w := newWallet(t)

	usage := testclient.Decoded[usageOut](
		w.client(t).Get(w.base() + "/usage").AssertStatus(http.StatusOK))

	// The default range is the current calendar month, which is what a billing page opens on.
	if usage.From.Day() != 1 {
		t.Errorf("the default range starts on the %d, want the first", usage.From.Day())
	}
	if len(usage.Included) == 0 {
		t.Error("the usage page does not say what the plan includes")
	}
	if _, named := usage.Included[string(billing.IncludedMAU)]; !named {
		t.Errorf("included = %v, want the MAU allowance", usage.Included)
	}
	// Nothing has been metered, so there are no days rather than days of zeroes.
	if len(usage.Days) != 0 {
		t.Errorf("days = %+v, want none before anything is metered", usage.Days)
	}

	// A range that ends before it starts, and one longer than the bound.
	client := w.client(t)
	client.Get(w.base() + "/usage?from=2026-03-10&to=2026-03-01").
		AssertStatus(http.StatusUnprocessableEntity)
	client.Get(w.base() + "/usage?from=2020-01-01&to=2026-01-01").
		AssertStatus(http.StatusUnprocessableEntity)
	client.Get(w.base() + "/usage?from=not-a-date").
		AssertStatus(http.StatusUnprocessableEntity)
}

// TestAnotherStudiosWalletIsInvisible is the tenancy check on money.
func TestAnotherStudiosWalletIsInvisible(t *testing.T) {
	w := newWallet(t)
	stranger := person(t)

	w.as(t, stranger).Get(w.base() + "/billing").AssertStatus(http.StatusNotFound)
	w.as(t, stranger).Get(w.base() + "/usage").AssertStatus(http.StatusNotFound)
	w.as(t, stranger).Get(w.base() + "/billing/ledger").AssertStatus(http.StatusNotFound)
}

// Local decoding types, so these tests assert on the wire format.

type creditLotOut struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	AmountMicros    int64      `json:"amount_micros"`
	RemainingMicros int64      `json:"remaining_micros"`
	ExpiresAt       *time.Time `json:"expires_at"`
	GrantedAt       time.Time  `json:"granted_at"`
	Description     string     `json:"description"`
	Expired         bool       `json:"expired"`
}

type billingOut struct {
	State               string         `json:"state"`
	Currency            string         `json:"currency"`
	AvailableMicros     int64          `json:"available_micros"`
	Available           string         `json:"available"`
	PromoMicros         int64          `json:"promo_micros"`
	PurchasedMicros     int64          `json:"purchased_micros"`
	LedgerBalanceMicros int64          `json:"ledger_balance_micros"`
	Lots                []creditLotOut `json:"lots"`
	ExpiringSoon        []creditLotOut `json:"expiring_soon"`
	Plan                struct {
		Key          string           `json:"key"`
		Entitlements map[string]int64 `json:"entitlements"`
	} `json:"plan"`
	AutoTopUp struct {
		Enabled         bool   `json:"enabled"`
		ThresholdMicros int64  `json:"threshold_micros"`
		AmountMicros    int64  `json:"amount_micros"`
		Pending         bool   `json:"pending"`
		LastError       string `json:"last_error"`
	} `json:"auto_topup"`
}

type creditGrantedOut struct {
	Lot             creditLotOut `json:"lot"`
	AvailableMicros int64        `json:"available_micros"`
	Granted         bool         `json:"granted"`
}

type creditChargedOut struct {
	AmountMicros int64 `json:"amount_micros"`
	Draws        []struct {
		Lot          string `json:"lot"`
		Kind         string `json:"kind"`
		AmountMicros int64  `json:"amount_micros"`
	} `json:"draws"`
	AvailableMicros int64 `json:"available_micros"`
	Charged         bool  `json:"charged"`
}

type ledgerOut struct {
	Entries []struct {
		Kind               string `json:"kind"`
		AmountMicros       int64  `json:"amount_micros"`
		BalanceAfterMicros int64  `json:"balance_after_micros"`
		Description        string `json:"description"`
	} `json:"entries"`
}

type usageOut struct {
	From     time.Time        `json:"from"`
	To       time.Time        `json:"to"`
	Included map[string]int64 `json:"included"`
	Final    bool             `json:"final"`
	Totals   struct {
		PeakDailyPlayers uint64 `json:"peak_daily_players"`
		Events           uint64 `json:"events"`
	} `json:"totals"`
	Days []struct {
		Day            time.Time `json:"day"`
		TrackedPlayers uint64    `json:"tracked_players"`
		Events         uint64    `json:"events"`
	} `json:"days"`
	Projects []struct {
		Project struct {
			Slug string `json:"slug"`
		} `json:"project"`
	} `json:"projects"`
}
