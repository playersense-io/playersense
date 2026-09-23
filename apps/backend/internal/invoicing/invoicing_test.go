package invoicing_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/billingcontract"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/invoiceline"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/invoicing"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/wallet"
)

// The month close and the issue (ADR 0045), against the real database and a Stripe in memory.
//
// Every test drives its own account through CloseAccount and Issue rather than a whole pass: a pass
// works every invoiced account in the shared database, and would close other tests' months under them.

var (
	may  = time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	june = time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	july = time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
)

func dollars(n int64) int64 { return n * billing.Micros }

type fixture struct {
	t       *testing.T
	db      *pgdb.DB
	account *ent.BillingAccount
	gateway *paymentstest.Gateway
	now     time.Time
	run     string
}

// newFixture is an invoiced account from `from`, on a contract with this fee and commitment.
func newFixture(t *testing.T, from time.Time, fee, commitment int64) *fixture {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	ctx := context.Background()
	id := uuid.NewString()[:12]
	person, err := identity.EnsureUser(ctx, db.Client, identity.Claims{
		UID: "firebase-" + id, Email: "invoicing-" + id + "@studio.example", EmailVerified: true,
		Name: "Grace", Provider: "google.com",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	orgs, err := identity.Organizations(ctx, db.Client, person.ID)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no organization: %v", err)
	}
	account := testsupport.SeedBillingAccount(t, db, orgs[0].ID)
	account, err = account.Update().
		SetStripeCustomerID("cus_" + id).
		SetCollection(billingaccount.CollectionInvoiced).
		SetInvoicedThrough(from).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Client.BillingContract.Create().
		SetAccountID(account.ID).SetCollection(billingcontract.CollectionInvoiced).
		SetEffectiveFrom(from).SetTermsDays(30).SetOverdueGraceDays(14).
		SetPlatformFeeMicros(fee).SetMinimumCommitmentMicros(commitment).
		SetPoNumber("PO-" + id).SetOperator("ada@playersense.dev").
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	// Back to prepaid afterwards, so no pass over the shared database finds it.
	t.Cleanup(func() {
		_ = db.Client.BillingAccount.UpdateOneID(account.ID).
			SetCollection(billingaccount.CollectionPrepaid).Exec(context.Background())
	})
	// Three hours into July: May and June are due, July is not.
	return &fixture{t: t, db: db, account: account, gateway: paymentstest.New(),
		now: july.Add(3 * time.Hour), run: id}
}

func (f *fixture) job() *invoicing.Job {
	return &invoicing.Job{DB: f.db, Gateway: f.gateway, CloseAfter: 2 * time.Hour,
		StaleAfter: 10 * time.Minute, Now: func() time.Time { return f.now }}
}

// charge is usage for a month, as the drain settles it.
func (f *fixture) charge(month time.Time, amount int64) {
	f.t.Helper()
	f.tx(func(ctx context.Context, tx *ent.Tx) error {
		_, err := wallet.ChargeUsage(ctx, tx, wallet.UsageRequest{AccountID: f.account.ID, Amount: amount,
			At: time.Now(), PeriodStart: month, Key: "usage:" + uuid.NewString()})
		return err
	})
}

func (f *fixture) promo(amount int64) {
	f.t.Helper()
	f.tx(func(ctx context.Context, tx *ent.Tx) error {
		_, _, err := wallet.Grant(ctx, tx, wallet.GrantRequest{AccountID: f.account.ID, Kind: wallet.Promo,
			Amount: amount, GrantedAt: time.Now(), Key: "promo:" + uuid.NewString()})
		return err
	})
}

func (f *fixture) tx(fn func(context.Context, *ent.Tx) error) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if err := fn(context.Background(), tx); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

// close runs the month close for this account and returns what it wrote, oldest first.
func (f *fixture) close() []*ent.Invoice {
	f.t.Helper()
	if _, err := f.job().CloseAccount(context.Background(), f.account.ID); err != nil {
		f.t.Fatalf("close: %v", err)
	}
	return f.invoices()
}

func (f *fixture) invoices() []*ent.Invoice {
	f.t.Helper()
	rows, err := f.db.Client.Invoice.Query().
		Where(invoice.AccountID(f.account.ID)).
		WithLines(func(q *ent.InvoiceLineQuery) { q.Order(ent.Asc(invoiceline.FieldPosition)) }).
		Order(ent.Asc(invoice.FieldPeriodStart)).
		All(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func (f *fixture) row() *ent.BillingAccount {
	f.t.Helper()
	row, err := f.db.Client.BillingAccount.Get(context.Background(), f.account.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

// cents is an invoice's lines, by kind, as Stripe bills them.
func cents(row *ent.Invoice) map[invoiceline.Kind]int64 {
	out := map[invoiceline.Kind]int64{}
	for _, l := range row.Edges.Lines {
		out[l.Kind] += l.AmountCents
	}
	return out
}

func sum(row *ent.Invoice) int64 {
	var total int64
	for _, l := range row.Edges.Lines {
		total += l.AmountCents
	}
	return total
}

// A month's invoice is its fee and its players, less what credit paid for — and the lines add up to
// exactly what it bills, with the part below a cent carried rather than rounded.
func TestAMonthIsBilledFromItsLedger(t *testing.T) {
	f := newFixture(t, may, dollars(500), 0)
	f.promo(dollars(5))
	f.charge(may, 12_345_678) // $12.345678, $5 of it paid by the promo

	invoices := f.close()

	if len(invoices) != 2 {
		t.Fatalf("%d invoices, want May and June", len(invoices))
	}
	inv := invoices[0]
	if inv.Status != invoice.StatusDraft || !inv.PeriodStart.Equal(may) || !inv.PeriodEnd.Equal(june) {
		t.Errorf("invoice %s [%s, %s)", inv.Status, inv.PeriodStart, inv.PeriodEnd)
	}
	lines := cents(inv)
	if lines[invoiceline.KindPlatformFee] != 50_000 || lines[invoiceline.KindUsage] != 1_234 ||
		lines[invoiceline.KindCreditsApplied] != -500 {
		t.Errorf("lines %v; want fee 50000, usage 1234, credits −500", lines)
	}
	if inv.TotalMicros != 507_340_000 || sum(inv) != 50_734 || inv.CarryOutMicros != 5_678 {
		t.Errorf("total %s, lines %d¢, carry %d; want $507.34 billed and 5678 micros carried",
			billing.USD(inv.TotalMicros), sum(inv), inv.CarryOutMicros)
	}
	if inv.PoNumber != "PO-"+f.run || inv.TermsDays != 30 {
		t.Errorf("po %q, terms %d", inv.PoNumber, inv.TermsDays)
	}

	// June had no players: its fee, and the carry — still below a cent — carried again.
	if june := invoices[1]; june.CarryInMicros != 5_678 || june.TotalMicros != dollars(500) ||
		june.CarryOutMicros != 5_678 {
		t.Errorf("June: carry in %d, total %s, carry out %d", june.CarryInMicros,
			billing.USD(june.TotalMicros), june.CarryOutMicros)
	}
	if row := f.row(); !row.InvoicedThrough.Equal(july) {
		t.Errorf("invoiced through %s, want July", row.InvoicedThrough)
	}
}

// Closing again writes nothing: the account's starting point moved with the invoice.
func TestAClosedMonthIsNotBilledTwice(t *testing.T) {
	f := newFixture(t, may, dollars(100), 0)
	f.close()
	again := f.close()
	if len(again) != 2 {
		t.Errorf("%d invoices after closing twice, want 2", len(again))
	}
}

// Usage for May that settles after May was invoiced is on June's invoice, as May's — never on neither.
func TestALateChargeIsOnTheNextInvoice(t *testing.T) {
	f := newFixture(t, may, 0, 0)
	f.now = june.Add(3 * time.Hour)
	f.charge(may, dollars(40))
	first := f.close()

	f.charge(may, dollars(7)) // settled late
	f.charge(june, dollars(20))
	f.now = july.Add(3 * time.Hour)
	all := f.close()

	if len(first) != 1 || len(all) != 2 {
		t.Fatalf("%d then %d invoices", len(first), len(all))
	}
	if lines := cents(all[0]); lines[invoiceline.KindUsage] != 4_000 {
		t.Errorf("May: %v", lines)
	}
	lines := cents(all[1])
	if lines[invoiceline.KindUsage] != 2_000 || lines[invoiceline.KindPriorUsage] != 700 {
		t.Errorf("June: %v; want June's $20 and May's late $7", lines)
	}
}

// A contract that starts on the 16th pays for sixteen of May's thirty-one days.
func TestTheFeeIsProratedInThePartMonth(t *testing.T) {
	f := newFixture(t, may.AddDate(0, 0, 15), dollars(310), 0)
	f.now = june.Add(3 * time.Hour)

	invoices := f.close()

	if len(invoices) != 1 {
		t.Fatalf("%d invoices", len(invoices))
	}
	if lines := cents(invoices[0]); lines[invoiceline.KindPlatformFee] != 16_000 {
		t.Errorf("fee %d¢, want $160.00 for 16 of 31 days", lines[invoiceline.KindPlatformFee])
	}
}

// Usage below the minimum is topped up to it; usage above it is billed as it is. And June's usage,
// settled before May closed, is on June's invoice — not May's.
func TestTheCommitmentTopsUpUsageBelowIt(t *testing.T) {
	f := newFixture(t, may, 0, dollars(1_000))
	f.charge(may, dollars(300))
	f.charge(june, dollars(1_500))

	invoices := f.close()

	if len(invoices) != 2 {
		t.Fatalf("%d invoices", len(invoices))
	}
	if lines := cents(invoices[0]); lines[invoiceline.KindCommitment] != 70_000 ||
		lines[invoiceline.KindPriorUsage] != 0 || invoices[0].TotalMicros != dollars(1_000) {
		t.Errorf("May: %v, total %s; want $700 of commitment and $1,000 billed", lines, billing.USD(invoices[0].TotalMicros))
	}
	if lines := cents(invoices[1]); lines[invoiceline.KindCommitment] != 0 || invoices[1].TotalMicros != dollars(1_500) {
		t.Errorf("June: %v; usage over the minimum needs no top-up", lines)
	}
}

// A month waits while its usage is still queued, and closes once it has settled.
func TestAMonthWaitsForItsUsageToSettle(t *testing.T) {
	f := newFixture(t, may, dollars(100), 0)
	f.now = june.Add(3 * time.Hour)
	ctx := context.Background()
	plan, err := f.db.Client.Plan.Create().SetKey("invoicing-" + f.run).SetName("Invoicing test").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	card, err := f.db.Client.RateCard.Create().SetPlanID(plan.ID).SetMeter("tracked_players").
		SetTiers(billing.Tiers{{UnitPriceMicros: 500}}).SetEffectiveFrom(may).SetCreatedBy("test@playersense.dev").
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := f.db.Client.UsageCharge.Create().SetAccountID(f.account.ID).SetPeriodStart(may).
		SetRateCardID(card.ID).SetFirstUnit(1).SetLastUnit(10).SetAmountMicros(5_000).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if n := len(f.close()); n != 0 {
		t.Fatalf("%d invoices while May's usage is queued", n)
	}
	if err := queued.Update().SetSettledAt(time.Now()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(f.close()); n != 1 {
		t.Errorf("%d invoices once it settled, want 1", n)
	}
}

// The close waits CloseAfter past the month's end.
func TestAMonthClosesOnlyAfterItEnds(t *testing.T) {
	f := newFixture(t, may, dollars(100), 0)
	f.now = june.Add(time.Hour)
	if n := len(f.close()); n != 0 {
		t.Errorf("%d invoices an hour after May ended, want none until two", n)
	}
	f.now = june.Add(3 * time.Hour)
	if n := len(f.close()); n != 1 {
		t.Errorf("%d invoices three hours after, want May's", n)
	}
}

// Issued once, however the response is lost: the retry after StaleAfter is the same invoice.
func TestALostResponseIsResumedAsTheSameInvoice(t *testing.T) {
	f := newFixture(t, may, dollars(250), 0)
	f.now = june.Add(3 * time.Hour)
	inv := f.close()[0]
	f.gateway.LoseInvoiceResponses = 1
	ctx := context.Background()

	if issued, err := f.job().Issue(ctx, inv.ID); err == nil || issued {
		t.Fatalf("issued %v, %v; want the lost response reported", issued, err)
	}
	if issued, _ := f.job().Issue(ctx, inv.ID); issued {
		t.Fatal("retried before StaleAfter: two passes would ask at once")
	}
	f.now = f.now.Add(11 * time.Minute)
	if issued, err := f.job().Issue(ctx, inv.ID); err != nil || !issued {
		t.Fatalf("issued %v, %v", issued, err)
	}

	if f.gateway.Issued() != 1 || f.gateway.IssueCalls != 2 {
		t.Errorf("%d invoices from %d requests; want one", f.gateway.Issued(), f.gateway.IssueCalls)
	}
	row, _ := f.db.Client.Invoice.Get(ctx, inv.ID)
	if row.Status != invoice.StatusOpen || row.StripeInvoiceID == nil || row.DueAt == nil || row.HostedURL == "" {
		t.Errorf("after issuing: %+v", row)
	}
	request := f.gateway.IssueRequests[0]
	if request.Key != inv.IssueKey || request.DaysUntilDue != 30 || request.PONumber != "PO-"+f.run ||
		len(request.Lines) != 1 || request.Lines[0].AmountCents != 25_000 {
		t.Errorf("request %+v", request)
	}
}

// Nothing to bill is recorded and never sent.
func TestAnEmptyInvoiceIsNotSent(t *testing.T) {
	f := newFixture(t, may, 0, 0)
	f.now = june.Add(3 * time.Hour)
	inv := f.close()[0]
	if inv.Status != invoice.StatusEmpty {
		t.Fatalf("status %s, want empty", inv.Status)
	}
	if issued, err := f.job().Issue(context.Background(), inv.ID); issued || err != nil || f.gateway.IssueCalls != 0 {
		t.Errorf("issued %v, %v, %d calls; want nothing sent", issued, err, f.gateway.IssueCalls)
	}
}

// Where the ledger disagrees with the dashboard's month, the invoice says by how much.
func TestADifferenceFromTheDashboardIsRecorded(t *testing.T) {
	f := newFixture(t, may, 0, 0)
	f.now = june.Add(3 * time.Hour)
	f.charge(may, dollars(30))
	if _, err := f.db.Client.MonthlyUsage.Create().SetAccountID(f.account.ID).SetPeriodStart(may).
		SetTrackedPlayers(100).SetChargedMicros(dollars(25)).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inv := f.close()[0]; inv.DiscrepancyMicros != dollars(5) {
		t.Errorf("discrepancy %s, want $5", billing.USD(inv.DiscrepancyMicros))
	}
}

// The final invoice, when an account leaves invoicing: everything unbilled, to now.
func TestAFinalInvoiceBillsToNow(t *testing.T) {
	f := newFixture(t, may, dollars(310), 0)
	f.charge(may, dollars(12))
	end := may.AddDate(0, 0, 10)
	var inv *ent.Invoice
	f.tx(func(ctx context.Context, tx *ent.Tx) error {
		var err error
		inv, err = invoicing.Close(ctx, tx, f.account.ID, end, true)
		return err
	})
	if inv == nil || !inv.Final || !inv.PeriodEnd.Equal(end) {
		t.Fatalf("final invoice %+v", inv)
	}
	f.tx(func(ctx context.Context, tx *ent.Tx) error {
		var err error
		inv, err = f.db.Client.Invoice.Query().Where(invoice.ID(inv.ID)).WithLines().Only(ctx)
		return err
	})
	if lines := cents(inv); lines[invoiceline.KindPlatformFee] != 10_000 || lines[invoiceline.KindUsage] != 1_200 {
		t.Errorf("lines %v; want 10 days of the fee and the $12 of players", lines)
	}
}
