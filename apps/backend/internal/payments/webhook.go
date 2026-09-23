package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v85"
	"github.com/stripe/stripe-go/v85/webhook"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/pgdb"
)

// Webhooks receives Stripe's events.
//
// Every event is recorded before it is acted on (StripeEvent), which makes three things true:
//
//   - **A redelivery does nothing.** The event id is unique; an event already processed or ignored
//     is answered 200 and left alone.
//   - **A failure is retried by Stripe.** An event that could not be handled — the database was
//     down — is answered 500, marked failed with the reason, and handled on Stripe's next attempt.
//   - **Nothing is lost.** The body is kept, so a handler fixed tomorrow can replay today.
//
// Most events PlayerSense is sent are recorded and ignored: cards are managed at Stripe and never
// stored here, so a payment_method.* event changes nothing on our side. What moves money is a
// payment succeeding; what moves the automatic top-up is a payment failing.
type Webhooks struct {
	DB     *pgdb.DB
	Secret string
	Log    *slog.Logger

	// RetryAfter is how long an automatic top-up waits after a decline.
	RetryAfter time.Duration

	Now func() time.Time
}

// ErrBadSignature is a request Stripe did not sign, or signed too long ago to trust.
var ErrBadSignature = errors.New("the webhook signature does not verify")

// Outcome is what receiving an event did.
type Outcome struct {
	EventID  string
	Type     string
	Status   string // processed, ignored, or duplicate
	Credited bool
	Reason   string
}

// Receive verifies, records and handles one delivery.
//
// The API version is not required to match the SDK's. The endpoint's version is set in Stripe's
// dashboard and can lag a deploy; refusing on a mismatch would answer 400 to every event until
// somebody noticed, and the few fields read here are stable across versions. The version is kept on
// the row, so a mismatch is visible rather than fatal.
func (w *Webhooks) Receive(ctx context.Context, payload []byte, signature string) (Outcome, error) {
	if w.Secret == "" {
		return Outcome{}, ErrNotConfigured
	}
	event, err := webhook.ConstructEventWithOptions(payload, signature, w.Secret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	out := Outcome{EventID: event.ID, Type: string(event.Type)}

	row, err := w.record(ctx, event, payload)
	if err != nil {
		return out, err
	}
	if row.Status == stripeevent.StatusProcessed || row.Status == stripeevent.StatusIgnored {
		out.Status = "duplicate"
		return out, nil
	}

	tx, err := w.DB.Client.Tx(ctx)
	if err != nil {
		return out, err
	}
	result, err := w.handle(ctx, tx, event)
	if err != nil {
		_ = tx.Rollback()
		w.fail(ctx, row.ID, err)
		return out, err
	}

	update := tx.StripeEvent.UpdateOneID(row.ID).
		SetStatus(result.status).
		SetProcessedAt(w.now()).
		SetError(truncate(result.reason, 500)).
		AddAttempts(1)
	if result.accountID != 0 {
		update = update.SetAccountID(result.accountID)
	}
	if err := update.Exec(ctx); err != nil {
		_ = tx.Rollback()
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	out.Status, out.Credited, out.Reason = string(result.status), result.credited, result.reason
	return out, nil
}

// record stores the event, or finds the row an earlier delivery stored.
func (w *Webhooks) record(ctx context.Context, event stripe.Event, payload []byte) (*ent.StripeEvent, error) {
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, fmt.Errorf("decode the event body: %w", err)
	}
	err := w.DB.Client.StripeEvent.Create().
		SetStripeEventID(event.ID).
		SetType(string(event.Type)).
		SetAPIVersion(event.APIVersion).
		SetStripeCreatedAt(time.Unix(event.Created, 0).UTC()).
		SetPayload(body).
		OnConflictColumns(stripeevent.FieldStripeEventID).
		DoNothing().
		Exec(ctx)
	if err != nil && !errors.Is(err, sqlNoRows) {
		return nil, fmt.Errorf("record the event: %w", err)
	}
	return w.DB.Client.StripeEvent.Query().Where(stripeevent.StripeEventID(event.ID)).Only(ctx)
}

// fail marks an event whose handling raised, so the reason is on the row when Stripe retries.
func (w *Webhooks) fail(ctx context.Context, id int, cause error) {
	if err := w.DB.Client.StripeEvent.UpdateOneID(id).
		SetStatus(stripeevent.StatusFailed).
		SetError(truncate(cause.Error(), 500)).
		AddAttempts(1).
		Exec(ctx); err != nil {
		w.log().Error("could not record a failed stripe event", "error", err, "cause", cause)
	}
}

type handled struct {
	status    stripeevent.Status
	reason    string
	credited  bool
	accountID int
}

func ignored(reason string) handled {
	return handled{status: stripeevent.StatusIgnored, reason: reason}
}

// handle acts on one event inside the transaction that marks it handled.
func (w *Webhooks) handle(ctx context.Context, tx *ent.Tx, event stripe.Event) (handled, error) {
	// Money going back — refunds and disputes (returns.go).
	if result, matched, err := w.returns(ctx, tx, event); matched {
		return result, err
	}
	switch event.Type {
	case "payment_intent.succeeded":
		var pi stripe.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
			return handled{}, fmt.Errorf("decode the payment intent: %w", err)
		}
		return w.credit(ctx, tx, paymentOf(&pi))

	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		var session stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
			return handled{}, fmt.Errorf("decode the checkout session: %w", err)
		}
		// A bank debit completes the session before the money arrives. Nothing is credited
		// until it does, which is async_payment_succeeded or the PaymentIntent's own event.
		if session.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
			return ignored("the session is complete and not yet paid"), nil
		}
		if session.PaymentIntent == nil {
			return ignored("the session has no payment intent"), nil
		}
		p := Payment{
			ID: session.PaymentIntent.ID, Status: "succeeded",
			Currency: string(session.Currency), AmountReceived: session.AmountTotal,
			Metadata: session.Metadata,
		}
		if session.Customer != nil {
			p.Customer = session.Customer.ID
		}
		return w.credit(ctx, tx, p)

	case "payment_intent.payment_failed", "payment_intent.canceled", "payment_intent.requires_action":
		var pi stripe.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
			return handled{}, fmt.Errorf("decode the payment intent: %w", err)
		}
		p := paymentOf(&pi)
		if p.Metadata[MetaKind] != KindAutoTopUp {
			// A failed Checkout attempt: the customer is on Stripe's page and can try another
			// card. Nothing on our side is waiting for it.
			return ignored("not an automatic top-up"), nil
		}
		reason := p.FailureMessage
		if reason == "" {
			reason = "the automatic top-up was " + p.Status
		}
		if err := Release(ctx, tx, p, w.now().Add(w.retryAfter()), reason); err != nil {
			if isPermanent(err) {
				return ignored(err.Error()), nil
			}
			return handled{}, err
		}
		return handled{status: stripeevent.StatusProcessed, reason: reason, accountID: accountIDOf(ctx, tx, p)}, nil

	default:
		// checkout.session.expired, async_payment_failed, payment_intent.created/processing,
		// payment_method.*, account.updated, and every invoice.* and invoiceitem.* event:
		// recorded, and nothing here depends on them. Invoices in particular never credit a
		// wallet — a payment is credited by its PaymentIntent, once, and an invoice describing
		// the same payment must not be a second report of it.
		return ignored(""), nil
	}
}

func (w *Webhooks) credit(ctx context.Context, tx *ent.Tx, p Payment) (handled, error) {
	if p.Metadata[MetaAccount] == "" {
		// Made by something other than PlayerSense — a dashboard charge, `stripe trigger` —
		// and nothing to do with any wallet.
		return ignored("not a PlayerSense payment"), nil
	}
	credited, err := Credit(ctx, tx, p, w.now())
	if err != nil {
		if isPermanent(err) {
			// Recorded as ignored with the reason, and loudly: a payment Stripe took that no
			// wallet received is money somebody has to look at.
			w.log().Error("a stripe payment could not be credited", "payment_intent", p.ID, "error", err)
			return ignored(err.Error()), nil
		}
		return handled{}, err
	}
	return handled{status: stripeevent.StatusProcessed, credited: credited, accountID: accountIDOf(ctx, tx, p)}, nil
}

func isPermanent(err error) bool {
	return errors.Is(err, ErrNotOurs) || errors.Is(err, ErrCustomerMismatch) || errors.Is(err, ErrWrongCurrency)
}

// accountIDOf links the event row to its account, best effort.
func accountIDOf(ctx context.Context, tx *ent.Tx, p Payment) int {
	uid, err := uuid.Parse(p.Metadata[MetaAccount])
	if err != nil {
		return 0
	}
	id, err := tx.BillingAccount.Query().Where(billingaccount.UID(uid)).OnlyID(ctx)
	if err != nil {
		return 0
	}
	return id
}

func (w *Webhooks) retryAfter() time.Duration {
	if w.RetryAfter > 0 {
		return w.RetryAfter
	}
	return 6 * time.Hour
}

func (w *Webhooks) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *Webhooks) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}
