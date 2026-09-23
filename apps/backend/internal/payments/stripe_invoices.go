package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v85"
)

// Usage invoices (ADR 0045): PlayerSense writes the invoice, Stripe issues and collects it.

// IssueInvoice issues a usage invoice, or continues the one an earlier attempt began.
//
// The steps an automatic top-up's invoice takes (ChargeOffSession), each keyed off the claim, with
// `send_invoice` in place of a charge: created with net terms, given its lines, finalized, sent. But a
// claim can outlive Stripe's memory of its keys — a long outage, a crash left until morning — and a
// key Stripe has forgotten creates a second invoice. So before anything is created the customer's
// invoices since the claim are searched for this one's uid, and a found invoice is continued from
// wherever it stopped: a draft is given only the lines it lacks, then finalized and sent; an open one
// is sent; a settled one is returned as it is.
func (s *Stripe) IssueInvoice(ctx context.Context, in InvoiceRequest) (IssuedInvoice, error) {
	meta := map[string]string{MetaAccount: in.AccountUID, MetaKind: KindUsageInvoice, MetaInvoice: in.InvoiceUID}

	invoice, err := s.findInvoice(ctx, in.Customer, in.InvoiceUID, in.Since)
	if err != nil {
		return IssuedInvoice{}, err
	}
	if invoice == nil {
		create := &stripe.InvoiceCreateParams{
			Customer:                    stripe.String(in.Customer),
			Currency:                    stripe.String(in.Currency),
			CollectionMethod:            stripe.String(string(stripe.InvoiceCollectionMethodSendInvoice)),
			DaysUntilDue:                stripe.Int64(in.DaysUntilDue),
			AutoAdvance:                 stripe.Bool(false), // finalized and sent by us, once
			PendingInvoiceItemsBehavior: stripe.String("exclude"),
			Description:                 stripe.String(in.Description),
			Metadata:                    meta,
		}
		if in.PONumber != "" {
			create.CustomFields = []*stripe.InvoiceCreateCustomFieldParams{{
				Name: stripe.String("PO number"), Value: stripe.String(in.PONumber),
			}}
		}
		if in.Memo != "" {
			create.Footer = stripe.String(in.Memo)
		}
		create.SetIdempotencyKey(in.Key + ":invoice")
		if invoice, err = s.client.V1Invoices.Create(ctx, create); err != nil {
			return IssuedInvoice{}, stripeFailure("create the invoice", err)
		}
	}

	if invoice.Status == stripe.InvoiceStatusDraft {
		if err := s.addLines(ctx, in, invoice.ID, meta); err != nil {
			return IssuedInvoice{}, err
		}
		finalize := &stripe.InvoiceFinalizeInvoiceParams{AutoAdvance: stripe.Bool(false)}
		finalize.SetIdempotencyKey(in.Key + ":finalize")
		if invoice, err = s.client.V1Invoices.FinalizeInvoice(ctx, invoice.ID, finalize); err != nil {
			return IssuedInvoice{}, stripeFailure("finalize the invoice", err)
		}
	}
	if invoice.Status == stripe.InvoiceStatusOpen {
		// Emails the customer the hosted invoice. Keyed, so a retry within Stripe's memory does not
		// send it twice; past it, a second copy of the same invoice is the worst case.
		send := &stripe.InvoiceSendInvoiceParams{}
		send.SetIdempotencyKey(in.Key + ":send")
		if invoice, err = s.client.V1Invoices.SendInvoice(ctx, invoice.ID, send); err != nil {
			return IssuedInvoice{}, stripeFailure("send the invoice", err)
		}
	}
	return issuedOf(invoice), nil
}

// addLines gives a draft the lines it does not have yet, found by position.
func (s *Stripe) addLines(ctx context.Context, in InvoiceRequest, invoice string, meta map[string]string) error {
	have := map[string]bool{}
	list := &stripe.InvoiceItemListParams{Invoice: stripe.String(invoice)}
	list.Limit = stripe.Int64(100)
	for item, err := range s.client.V1InvoiceItems.List(ctx, list).All(ctx) {
		if err != nil {
			return fmt.Errorf("list the invoice's lines: %w", err)
		}
		have[item.Metadata[MetaLine]] = true
	}
	for _, line := range in.Lines {
		position := strconv.Itoa(line.Position)
		if have[position] {
			continue
		}
		lineMeta := make(map[string]string, len(meta)+1)
		for k, v := range meta {
			lineMeta[k] = v
		}
		lineMeta[MetaLine] = position
		item := &stripe.InvoiceItemCreateParams{
			Customer:    stripe.String(in.Customer),
			Invoice:     stripe.String(invoice),
			Amount:      stripe.Int64(line.AmountCents),
			Currency:    stripe.String(in.Currency),
			Description: stripe.String(line.Description),
			Metadata:    lineMeta,
		}
		item.SetIdempotencyKey(in.Key + ":item:" + position)
		if _, err := s.client.V1InvoiceItems.Create(ctx, item); err != nil {
			return stripeFailure("add invoice line "+position, err)
		}
	}
	return nil
}

// findInvoice is the customer's invoice for a usage invoice uid, created since a moment, or nil.
func (s *Stripe) findInvoice(ctx context.Context, customer, uid string, since time.Time) (*stripe.Invoice, error) {
	params := &stripe.InvoiceListParams{
		Customer:     stripe.String(customer),
		CreatedRange: &stripe.RangeQueryParams{GreaterThanOrEqual: since.Add(-time.Hour).Unix()},
	}
	params.Limit = stripe.Int64(100)
	for invoice, err := range s.client.V1Invoices.List(ctx, params).All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("list the customer's invoices: %w", err)
		}
		if invoice.Metadata[MetaInvoice] == uid && invoice.Status != stripe.InvoiceStatusVoid {
			return invoice, nil
		}
	}
	return nil, nil
}

// SetBillingEmail sets the address Stripe sends the customer's invoices to.
func (s *Stripe) SetBillingEmail(ctx context.Context, customer, email string) error {
	if _, err := s.client.V1Customers.Update(ctx, customer, &stripe.CustomerUpdateParams{
		Email: stripe.String(email),
	}); err != nil {
		return fmt.Errorf("set the customer's billing address: %w", err)
	}
	return nil
}

// PayInvoiceOutOfBand marks an invoice paid by money Stripe did not see arrive: a wire, a cheque.
func (s *Stripe) PayInvoiceOutOfBand(ctx context.Context, invoice, key string) (IssuedInvoice, error) {
	params := &stripe.InvoicePayParams{PaidOutOfBand: stripe.Bool(true)}
	params.SetIdempotencyKey(key)
	paid, err := s.client.V1Invoices.Pay(ctx, invoice, params)
	if err != nil {
		return IssuedInvoice{}, stripeFailure("mark the invoice paid", err)
	}
	return issuedOf(paid), nil
}

// CreditInvoice issues a credit note lowering what an open invoice asks for.
func (s *Stripe) CreditInvoice(ctx context.Context, in CreditNoteRequest) (CreditNote, error) {
	params := &stripe.CreditNoteCreateParams{
		Invoice: stripe.String(in.Invoice),
		Amount:  stripe.Int64(in.AmountCents),
		Memo:    stripe.String(in.Memo),
		Metadata: map[string]string{
			MetaAccount: in.AccountUID, MetaKind: KindUsageInvoice, MetaInvoice: in.InvoiceUID,
		},
	}
	params.SetIdempotencyKey(in.Key)
	note, err := s.client.V1CreditNotes.Create(ctx, params)
	if err != nil {
		return CreditNote{}, stripeFailure("issue the credit note", err)
	}
	return CreditNote{ID: note.ID, AmountCents: note.Amount}, nil
}

// InvoiceOfPayment asks Stripe which invoice a PaymentIntent paid.
func (s *Stripe) InvoiceOfPayment(ctx context.Context, paymentIntent string) (string, error) {
	params := &stripe.InvoicePaymentListParams{Payment: &stripe.InvoicePaymentListPaymentParams{
		Type: stripe.String("payment_intent"), PaymentIntent: stripe.String(paymentIntent),
	}}
	params.Limit = stripe.Int64(1)
	for payment, err := range s.client.V1InvoicePayments.List(ctx, params).All(ctx) {
		if err != nil {
			return "", fmt.Errorf("find the invoice the payment paid: %w", err)
		}
		if payment.Invoice != nil {
			return payment.Invoice.ID, nil
		}
	}
	return "", nil
}

// issuedOf reads a Stripe invoice as PlayerSense needs it — from a response or from an event.
func issuedOf(invoice *stripe.Invoice) IssuedInvoice {
	out := IssuedInvoice{
		ID:         invoice.ID,
		Number:     invoice.Number,
		Status:     string(invoice.Status),
		Currency:   strings.ToLower(string(invoice.Currency)),
		HostedURL:  invoice.HostedInvoiceURL,
		PDFURL:     invoice.InvoicePDF,
		Total:      invoice.Total,
		AmountDue:  invoice.AmountDue,
		AmountPaid: invoice.AmountPaid,
		Metadata:   invoice.Metadata,
	}
	if invoice.Customer != nil {
		out.Customer = invoice.Customer.ID
	}
	if invoice.DueDate > 0 {
		out.DueAt = time.Unix(invoice.DueDate, 0).UTC()
	}
	return out
}
