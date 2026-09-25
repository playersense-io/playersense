package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v85"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/paymentcard"
	"playersense.dev/backend/ent/receipt"
	"playersense.dev/backend/ent/stripeevent"
)

// What the billing page shows of Stripe's side of an account: its saved cards, the details its
// invoices print, and its receipts. Stripe holds all three and edits them (Checkout, the customer
// portal); these are copies kept in step by webhooks, for showing, never for charging. A card
// number never reaches PlayerSense: brand, last four and expiry are all Stripe gives out.

// describing handles the events that describe a customer: its cards, its invoice details, and
// the invoices that are receipts for a top-up or a subscription. It reports whether the event was
// one of those.
func (w *Webhooks) describing(ctx context.Context, tx *ent.Tx, event stripe.Event) (handled, bool, error) {
	switch event.Type {
	case "payment_method.attached", "payment_method.updated", "payment_method.automatically_updated":
		var pm stripe.PaymentMethod
		if err := json.Unmarshal(event.Data.Raw, &pm); err != nil {
			return handled{}, true, fmt.Errorf("decode the payment method: %w", err)
		}
		card, ok := CardOf(&pm)
		if !ok || pm.Customer == nil {
			return ignored("not a customer's card"), true, nil
		}
		accountID, found, err := accountOfCustomer(ctx, tx, pm.Customer.ID)
		if err != nil || !found {
			return ignored("a customer PlayerSense did not open"), true, err
		}
		if err := saveCard(ctx, tx, accountID, card, false); err != nil {
			return handled{}, true, err
		}
		return handled{status: stripeevent.StatusProcessed, accountID: accountID}, true, nil

	case "payment_method.detached":
		var pm stripe.PaymentMethod
		if err := json.Unmarshal(event.Data.Raw, &pm); err != nil {
			return handled{}, true, fmt.Errorf("decode the payment method: %w", err)
		}
		row, err := tx.PaymentCard.Query().Where(paymentcard.StripePaymentMethodID(pm.ID)).Only(ctx)
		if ent.IsNotFound(err) {
			return ignored("a card PlayerSense never listed"), true, nil
		}
		if err != nil {
			return handled{}, true, err
		}
		if err := tx.PaymentCard.UpdateOne(row).SetDetachedAt(w.now()).SetIsDefault(false).Exec(ctx); err != nil {
			return handled{}, true, err
		}
		return handled{status: stripeevent.StatusProcessed, accountID: row.AccountID}, true, nil

	case "customer.updated":
		var c stripe.Customer
		if err := json.Unmarshal(event.Data.Raw, &c); err != nil {
			return handled{}, true, fmt.Errorf("decode the customer: %w", err)
		}
		accountID, found, err := accountOfCustomer(ctx, tx, c.ID)
		if err != nil || !found {
			return ignored("a customer PlayerSense did not open"), true, err
		}
		details := DetailsOf(&c)
		if err := saveDetails(ctx, tx, accountID, details, nil); err != nil {
			return handled{}, true, err
		}
		if err := setDefault(ctx, tx, accountID, details.DefaultCard); err != nil {
			return handled{}, true, err
		}
		return handled{status: stripeevent.StatusProcessed, accountID: accountID}, true, nil

	case "customer.tax_id.created", "customer.tax_id.updated", "customer.tax_id.deleted":
		var id stripe.TaxID
		if err := json.Unmarshal(event.Data.Raw, &id); err != nil {
			return handled{}, true, fmt.Errorf("decode the tax id: %w", err)
		}
		if id.Customer == nil {
			return ignored("not a customer's tax id"), true, nil
		}
		accountID, found, err := accountOfCustomer(ctx, tx, id.Customer.ID)
		if err != nil || !found {
			return ignored("a customer PlayerSense did not open"), true, err
		}
		text := string(id.Type) + " " + id.Value
		change := func(ids []string) []string {
			ids = slices.DeleteFunc(ids, func(s string) bool { return s == text })
			if event.Type != "customer.tax_id.deleted" {
				ids = append(ids, text)
			}
			return ids
		}
		if err := saveDetails(ctx, tx, accountID, CustomerDetails{}, change); err != nil {
			return handled{}, true, err
		}
		return handled{status: stripeevent.StatusProcessed, accountID: accountID}, true, nil

	case "invoice.finalized", "invoice.paid", "invoice.voided", "invoice.marked_uncollectible", "invoice.updated":
		var raw stripe.Invoice
		if err := json.Unmarshal(event.Data.Raw, &raw); err != nil {
			return handled{}, true, fmt.Errorf("decode the invoice: %w", err)
		}
		kind := receiptKind(&raw)
		if kind == "" || raw.Customer == nil || raw.Status == stripe.InvoiceStatusDraft {
			return handled{}, false, nil
		}
		accountID, found, err := accountOfCustomer(ctx, tx, raw.Customer.ID)
		if err != nil || !found {
			return ignored("a customer PlayerSense did not open"), true, err
		}
		if err := saveReceipt(ctx, tx, accountID, kind, &raw, time.Unix(event.Created, 0).UTC()); err != nil {
			return handled{}, true, err
		}
		return handled{status: stripeevent.StatusProcessed, accountID: accountID}, true, nil
	}
	return handled{}, false, nil
}

// receiptKind is what an invoice is a receipt for, or empty for one that is not a receipt: a usage
// invoice, which bills rather than describes, or an invoice made by hand.
func receiptKind(raw *stripe.Invoice) receipt.Kind {
	switch raw.Metadata[MetaKind] {
	case KindUsageInvoice:
		return ""
	case KindCheckout, KindAutoTopUp:
		return receipt.KindTopUp
	}
	if raw.Parent != nil && raw.Parent.SubscriptionDetails != nil {
		return receipt.KindSubscription
	}
	return ""
}

func accountOfCustomer(ctx context.Context, tx *ent.Tx, customer string) (int, bool, error) {
	id, err := tx.BillingAccount.Query().Where(billingaccount.StripeCustomerID(customer)).OnlyID(ctx)
	if ent.IsNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// saveCard writes one card as Stripe described it. A card saved to an account with no default
// becomes its default, as Stripe's own off-session charge would choose it (ChargeableCard).
func saveCard(ctx context.Context, tx *ent.Tx, accountID int, card Card, isDefault bool) error {
	if !isDefault {
		has, err := tx.PaymentCard.Query().Where(paymentcard.AccountID(accountID), paymentcard.IsDefault(true),
			paymentcard.DetachedAtIsNil(), paymentcard.StripePaymentMethodIDNEQ(card.ID)).Exist(ctx)
		if err != nil {
			return err
		}
		isDefault = !has
	}
	return tx.PaymentCard.Create().
		SetAccountID(accountID).SetStripePaymentMethodID(card.ID).
		SetBrand(card.Brand).SetLast4(card.Last4).SetExpMonth(card.ExpMonth).SetExpYear(card.ExpYear).
		SetFunding(card.Funding).SetCountry(card.Country).SetIsDefault(isDefault).
		OnConflictColumns(paymentcard.FieldStripePaymentMethodID).
		UpdateBrand().UpdateLast4().UpdateExpMonth().UpdateExpYear().UpdateFunding().UpdateCountry().
		ClearDetachedAt().
		Exec(ctx)
}

// setDefault makes one card the default, and every other not. Empty leaves the flags alone: a
// customer with no default at Stripe is charged on its newest card, which saveCard already marked.
func setDefault(ctx context.Context, tx *ent.Tx, accountID int, id string) error {
	if id == "" {
		return nil
	}
	if err := tx.PaymentCard.Update().Where(paymentcard.AccountID(accountID), paymentcard.StripePaymentMethodIDNEQ(id)).
		SetIsDefault(false).Exec(ctx); err != nil {
		return err
	}
	return tx.PaymentCard.Update().Where(paymentcard.AccountID(accountID), paymentcard.StripePaymentMethodID(id)).
		SetIsDefault(true).Exec(ctx)
}

// saveDetails writes the invoice details snapshot. With a change it edits the tax ids only, which
// is what a tax id event knows; without, it replaces the rest and keeps the tax ids.
func saveDetails(ctx context.Context, tx *ent.Tx, accountID int, d CustomerDetails, change func([]string) []string) error {
	account, err := tx.BillingAccount.Query().Where(billingaccount.ID(accountID)).ForUpdate().Only(ctx)
	if err != nil {
		return err
	}
	var current struct {
		Name    string   `json:"name"`
		Email   string   `json:"email"`
		Address []string `json:"address"`
		Country string   `json:"country"`
		TaxIDs  []string `json:"tax_ids"`
	}
	if raw, err := json.Marshal(account.InvoiceDetails); err == nil {
		_ = json.Unmarshal(raw, &current)
	}
	if change != nil {
		current.TaxIDs = change(current.TaxIDs)
	} else {
		current.Name, current.Email, current.Address, current.Country = d.Name, d.Email, d.Address, d.Country
		if d.TaxIDs != nil {
			current.TaxIDs = d.TaxIDs
		}
	}
	snapshot := map[string]any{"name": current.Name, "email": current.Email, "address": nonNilStrings(current.Address),
		"country": current.Country, "tax_ids": nonNilStrings(current.TaxIDs)}
	return tx.BillingAccount.UpdateOne(account).SetInvoiceDetails(snapshot).Exec(ctx)
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// saveReceipt writes a receipt, unless a newer event already did.
func saveReceipt(ctx context.Context, tx *ent.Tx, accountID int, kind receipt.Kind, raw *stripe.Invoice, at time.Time) error {
	existing, err := tx.Receipt.Query().Where(receipt.StripeInvoiceID(raw.ID)).Only(ctx)
	switch {
	case err == nil && existing.StripeUpdatedAt.After(at):
		return nil
	case err != nil && !ent.IsNotFound(err):
		return err
	}
	issued := time.Unix(raw.Created, 0).UTC()
	var paid *time.Time
	if raw.StatusTransitions != nil {
		if raw.StatusTransitions.FinalizedAt > 0 {
			issued = time.Unix(raw.StatusTransitions.FinalizedAt, 0).UTC()
		}
		if raw.StatusTransitions.PaidAt > 0 {
			t := time.Unix(raw.StatusTransitions.PaidAt, 0).UTC()
			paid = &t
		}
	}
	description := raw.Description
	if description == "" {
		description = map[receipt.Kind]string{receipt.KindTopUp: "Top-up", receipt.KindSubscription: "Subscription"}[kind]
		if raw.Metadata[MetaKind] == KindAutoTopUp {
			description = "Automatic top-up"
		}
	}
	return tx.Receipt.Create().
		SetAccountID(accountID).SetStripeInvoiceID(raw.ID).SetNumber(raw.Number).SetKind(kind).
		SetDescription(truncate(description, 255)).SetStatus(receipt.Status(raw.Status)).
		SetTotalMicros(raw.Total * 10_000).SetCurrency(strings.ToLower(string(raw.Currency))).
		SetIssuedAt(issued).SetNillablePaidAt(paid).
		SetHostedURL(truncate(raw.HostedInvoiceURL, 1024)).SetPdfURL(truncate(raw.InvoicePDF, 1024)).
		SetStripeUpdatedAt(at).
		OnConflictColumns(receipt.FieldStripeInvoiceID).
		UpdateNumber().UpdateStatus().UpdateTotalMicros().UpdateIssuedAt().UpdatePaidAt().
		UpdateHostedURL().UpdatePdfURL().UpdateStripeUpdatedAt().UpdateDescription().
		Exec(ctx)
}

// SyncCustomer writes what DescribeCustomer answered: the invoice details, every card Stripe
// lists, and any card no longer listed marked detached. For the first time the billing page asks,
// and for a refresh, when the webhooks may not have told the whole story.
func SyncCustomer(ctx context.Context, tx *ent.Tx, accountID int, d CustomerDetails, now time.Time) error {
	if err := saveDetails(ctx, tx, accountID, d, nil); err != nil {
		return err
	}
	listed := make([]string, 0, len(d.Cards))
	for _, card := range d.Cards {
		listed = append(listed, card.ID)
		if err := saveCard(ctx, tx, accountID, card, card.ID == d.DefaultCard); err != nil {
			return err
		}
	}
	if err := tx.PaymentCard.Update().Where(paymentcard.AccountID(accountID), paymentcard.DetachedAtIsNil(),
		paymentcard.StripePaymentMethodIDNotIn(listed...)).SetDetachedAt(now).SetIsDefault(false).Exec(ctx); err != nil {
		return err
	}
	if err := setDefault(ctx, tx, accountID, d.DefaultCard); err != nil {
		return err
	}
	return tx.BillingAccount.UpdateOneID(accountID).SetCardsSyncedAt(now).Exec(ctx)
}

// MarkDefault records a card as the account's default, as Stripe was just told.
func MarkDefault(ctx context.Context, tx *ent.Tx, accountID int, card string) error {
	return setDefault(ctx, tx, accountID, card)
}

// MarkDetached records a card as removed, as Stripe was just told. It answers the account's next
// default: the newest card left, or empty when none is.
func MarkDetached(ctx context.Context, tx *ent.Tx, accountID int, card string, now time.Time) (string, error) {
	if err := tx.PaymentCard.Update().Where(paymentcard.AccountID(accountID), paymentcard.StripePaymentMethodID(card)).
		SetDetachedAt(now).SetIsDefault(false).Exec(ctx); err != nil {
		return "", err
	}
	next, err := tx.PaymentCard.Query().Where(paymentcard.AccountID(accountID), paymentcard.DetachedAtIsNil()).
		Order(ent.Desc(paymentcard.FieldIsDefault), ent.Desc(paymentcard.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return next.StripePaymentMethodID, nil
}
