package payments_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v85"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/billingcontract"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/invoicing"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/wallet"
)

// Usage invoices (ADR 0045) with Stripe in the loop: real invoices issued in test mode, and every event
// that moves one — finalized, sent, credited, paid by card, voided, marked uncollectible, disputed and
// won — arriving as Stripe's own signed webhook at a running API through `stripe listen`.
//
// Opt-in, STRIPE_E2E=1, with the same two processes beside it as the top-up round trip:
//
//	stripe listen --forward-to localhost:21080/v1/stripe/webhook
//	STRIPE_WEBHOOK_SECRET=<the secret `stripe listen` printed> go run ./apps/backend/cmd/api
//
// Not covered: invoice.overdue, which Stripe sends on its own schedule some time after a due date has
// passed. The suspension job marks an invoice overdue from its due date without waiting for it.
func TestEndToEndUsageInvoicesWithStripesOwnWebhooks(t *testing.T) {
	if os.Getenv("STRIPE_E2E") != "1" {
		t.Skip("set STRIPE_E2E=1 with `stripe listen` and the API running")
	}
	gw, sc := liveGateway(t)
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	ctx := context.Background()

	// An invoiced account, as the contract route leaves one.
	id := uuid.NewString()[:12]
	person, err := identity.EnsureUser(ctx, db.Client, identity.Claims{
		UID: "firebase-" + id, Email: "e2e-invoiced-" + id + "@studio.example",
		EmailVerified: true, Name: "E2E", Provider: "google.com",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	orgs, _ := identity.Organizations(ctx, db.Client, person.ID)
	account := testsupport.SeedBillingAccount(t, db, orgs[0].ID)
	customer, err := gw.CreateCustomer(ctx, payments.CustomerRequest{
		AccountUID: account.UID.String(), Name: "E2E invoiced studio " + id, OrgSlug: orgs[0].Slug,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.SetBillingEmail(ctx, customer, "finance-"+id+"@studio.example"); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	account, err = account.Update().
		SetStripeCustomerID(customer).
		SetCollection(billingaccount.CollectionInvoiced).
		SetInvoicedThrough(start).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Client.BillingAccount.UpdateOneID(account.ID).
			SetCollection(billingaccount.CollectionPrepaid).Exec(context.Background())
	})
	if _, err := db.Client.BillingContract.Create().SetAccountID(account.ID).
		SetCollection(billingcontract.CollectionInvoiced).SetEffectiveFrom(start.Add(-time.Minute)).
		SetTermsDays(30).SetPoNumber("PO-E2E-" + id).SetOperator("e2e@playersense.dev").Save(ctx); err != nil {
		t.Fatal(err)
	}
	job := &invoicing.Job{DB: db, Gateway: gw}
	e2e := &invoiceRun{t: t, db: db, sc: sc, job: job, account: account.ID, end: start}

	// 1. Issued: finalized and sent arrive and record the number, the page and the due date.
	first := e2e.issue((120 * billing.Micros))
	e2e.await("invoice.finalized", byID(*first.StripeInvoiceID), stripeevent.StatusProcessed)
	e2e.await("invoice.sent", byID(*first.StripeInvoiceID), stripeevent.StatusProcessed)
	if row := e2e.invoice(first); row.Number == "" || row.HostedURL == "" || row.DueAt == nil {
		t.Errorf("after finalized and sent: number %q, page %q, due %v", row.Number, row.HostedURL, row.DueAt)
	}
	e2e.assertReceivable((120 * billing.Micros))

	// 2. A credit note made at Stripe — applied by its webhook alone.
	note, err := gw.CreditInvoice(ctx, payments.CreditNoteRequest{
		AccountUID: account.UID.String(), InvoiceUID: first.UID.String(), Invoice: *first.StripeInvoiceID,
		AmountCents: 2_000, Memo: "E2E credit", Key: "playersense-credit-e2e-" + id,
	})
	if err != nil {
		t.Fatal(err)
	}
	e2e.await("credit_note.created", byID(note.ID), stripeevent.StatusProcessed)
	e2e.assertReceivable((100 * billing.Micros))

	// 3. Paid by card on the invoice: invoice.paid settles it, invoice_payment.paid records the
	// PaymentIntent, and the PaymentIntent's own event — none of our metadata — credits nothing.
	card := savedCard(t, sc, customer, "pm_card_visa")
	if _, err := sc.V1Invoices.Pay(ctx, *first.StripeInvoiceID, &stripe.InvoicePayParams{
		PaymentMethod: stripe.String(card),
	}); err != nil {
		t.Fatalf("pay by card: %v", err)
	}
	paid := e2e.await("invoice.paid", byID(*first.StripeInvoiceID), stripeevent.StatusProcessed)
	e2e.await("invoice_payment.paid", byField("invoice", *first.StripeInvoiceID), stripeevent.StatusProcessed)
	e2e.assertReceivable(0)
	row := e2e.invoice(first)
	if row.Status != invoice.StatusPaid || row.PaymentIntent == nil || row.AmountPaidMicros != (100*billing.Micros) {
		t.Errorf("after paying: %s, payment %v, paid %s", row.Status, row.PaymentIntent, billing.USD(row.AmountPaidMicros))
	}
	t.Logf("invoice.paid %s (api %s) processed", paid.StripeEventID, paid.APIVersion)
	e2e.await("payment_intent.succeeded", byID(*row.PaymentIntent), stripeevent.StatusIgnored)
	e2e.assertNoLots()

	// 4. Voided: what it billed is written off.
	second := e2e.issue((40 * billing.Micros))
	if _, err := sc.V1Invoices.VoidInvoice(ctx, *second.StripeInvoiceID, nil); err != nil {
		t.Fatalf("void: %v", err)
	}
	e2e.await("invoice.voided", byID(*second.StripeInvoiceID), stripeevent.StatusProcessed)
	e2e.assertReceivable(0)
	if row := e2e.invoice(second); row.Status != invoice.StatusVoid {
		t.Errorf("after voiding: %s", row.Status)
	}

	// 5. Marked uncollectible: written off the same way.
	third := e2e.issue((25 * billing.Micros))
	if _, err := sc.V1Invoices.MarkUncollectible(ctx, *third.StripeInvoiceID, nil); err != nil {
		t.Fatalf("mark uncollectible: %v", err)
	}
	e2e.await("invoice.marked_uncollectible", byID(*third.StripeInvoiceID), stripeevent.StatusProcessed)
	e2e.assertReceivable(0)

	// 6. Paid with a card whose bank disputes it: the receivable is owed again and the invoice
	// reopened; winning the dispute settles it again.
	fourth := e2e.issue((60 * billing.Micros))
	disputing := savedCard(t, sc, customer, "pm_card_createDispute")
	if _, err := sc.V1Invoices.Pay(ctx, *fourth.StripeInvoiceID, &stripe.InvoicePayParams{
		PaymentMethod: stripe.String(disputing),
	}); err != nil {
		t.Fatalf("pay with the disputing card: %v", err)
	}
	e2e.await("invoice.paid", byID(*fourth.StripeInvoiceID), stripeevent.StatusProcessed)
	e2e.await("invoice_payment.paid", byField("invoice", *fourth.StripeInvoiceID), stripeevent.StatusProcessed)
	pi := *e2e.invoice(fourth).PaymentIntent
	opened := e2e.await("charge.dispute.created", byField("payment_intent", pi), stripeevent.StatusProcessed)
	e2e.assertReceivable((60 * billing.Micros))
	if row := e2e.invoice(fourth); row.Status != invoice.StatusOpen {
		t.Errorf("after the dispute: %s, want open again", row.Status)
	}
	dispute := objectOf(opened)["id"].(string)
	if _, err := sc.V1Disputes.Update(ctx, dispute, &stripe.DisputeUpdateParams{
		Evidence: &stripe.DisputeUpdateEvidenceParams{UncategorizedText: stripe.String("winning_evidence")},
		Submit:   stripe.Bool(true),
	}); err != nil {
		t.Fatalf("submit evidence: %v", err)
	}
	e2e.await("charge.dispute.closed", byID(dispute), stripeevent.StatusProcessed)
	e2e.assertReceivable(0)
	if row := e2e.invoice(fourth); row.Status != invoice.StatusPaid {
		t.Errorf("after winning: %s, want paid", row.Status)
	}

	// The customer events a contract causes arrive and are recorded, and change nothing.
	e2e.await("customer.created", byID(customer), stripeevent.StatusIgnored)
	e2e.await("customer.updated", byID(customer), stripeevent.StatusIgnored)
	e2e.assertInvariant()
}

// invoiceRun is one account's invoices, and the webhooks they cause.
type invoiceRun struct {
	t       *testing.T
	db      *pgdb.DB
	sc      *stripe.Client
	job     *invoicing.Job
	account int
	end     time.Time
}

// issue charges usage, closes a period with it, and issues the invoice at Stripe.
func (r *invoiceRun) issue(amount int64) *ent.Invoice {
	r.t.Helper()
	ctx := context.Background()
	r.end = r.end.Add(time.Minute)
	tx, err := r.db.Client.Tx(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := wallet.ChargeUsage(ctx, tx, wallet.UsageRequest{AccountID: r.account, Amount: amount,
		At: time.Now(), PeriodStart: invoicing.MonthOf(r.end), Key: "usage:" + uuid.NewString()}); err != nil {
		r.t.Fatal(err)
	}
	row, err := invoicing.Close(ctx, tx, r.account, r.end, false)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		r.t.Fatal(err)
	}
	if issued, err := r.job.Issue(ctx, row.ID); err != nil || !issued {
		r.t.Fatalf("issue: %v, %v", issued, err)
	}
	return r.invoice(row)
}

func (r *invoiceRun) invoice(row *ent.Invoice) *ent.Invoice {
	r.t.Helper()
	fresh, err := r.db.Client.Invoice.Get(context.Background(), row.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	return fresh
}

// await is Stripe's event of a kind about an object, once the API has handled it with this status.
func (r *invoiceRun) await(kind string, match func(map[string]any) bool, want stripeevent.Status) *ent.StripeEvent {
	r.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		events, err := r.db.Client.StripeEvent.Query().
			Where(stripeevent.Type(kind), stripeevent.StatusNEQ(stripeevent.StatusReceived)).
			Order(ent.Desc(stripeevent.FieldID)).Limit(200).All(context.Background())
		if err != nil {
			r.t.Fatal(err)
		}
		for _, e := range events {
			if match(objectOf(e)) {
				if e.Status != want {
					r.t.Fatalf("%s %s was %s (%s); want %s", kind, e.StripeEventID, e.Status, e.Error, want)
				}
				r.t.Logf("%-30s %s", kind, e.Status)
				return e
			}
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("no %s arrived; is `stripe listen` forwarding to the running API?", kind)
		}
		time.Sleep(time.Second)
	}
}

func (r *invoiceRun) row() *ent.BillingAccount {
	r.t.Helper()
	row, err := r.db.Client.BillingAccount.Get(context.Background(), r.account)
	if err != nil {
		r.t.Fatal(err)
	}
	return row
}

// assertReceivable: whole-dollar usage and no fee, so no invoice carries anything below a cent.
func (r *invoiceRun) assertReceivable(want int64) {
	r.t.Helper()
	if got := r.row().ReceivableMicros; got != want {
		r.t.Errorf("receivable %s, want %s", billing.USD(got), billing.USD(want))
	}
}

// assertNoLots: paying an invoice settles the receivable; it never becomes credit.
func (r *invoiceRun) assertNoLots() {
	r.t.Helper()
	lots, err := r.db.Client.BillingAccount.QueryCreditLots(r.row()).Count(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	if lots != 0 {
		r.t.Errorf("%d lots on the account; a card payment of an invoice became credit", lots)
	}
}

func (r *invoiceRun) assertInvariant() {
	r.t.Helper()
	row := r.row()
	lots, err := r.db.Client.BillingAccount.QueryCreditLots(row).All(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	var held int64
	for _, lot := range lots {
		held += lot.RemainingMicros
	}
	if row.BalanceMicros != held-row.ArrearsMicros-row.ReceivableMicros {
		r.t.Errorf("balance %s ≠ lots %s − arrears %s − receivable %s", billing.USD(row.BalanceMicros),
			billing.USD(held), billing.USD(row.ArrearsMicros), billing.USD(row.ReceivableMicros))
	}
}

func objectOf(e *ent.StripeEvent) map[string]any {
	data, _ := e.Payload["data"].(map[string]any)
	object, _ := data["object"].(map[string]any)
	return object
}

func byID(id string) func(map[string]any) bool {
	return byField("id", id)
}

func byField(name, value string) func(map[string]any) bool {
	return func(object map[string]any) bool { return object[name] == value }
}
