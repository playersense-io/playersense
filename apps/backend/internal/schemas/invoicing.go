package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// Contracted accounts, billed by monthly invoice (ADR 0045).

// SetContractIn puts an organization on new terms, from now.
//
// `invoiced` moves it to monthly invoicing: usage credit does not cover is billed on the month's
// invoice with the contract's net terms, and the clock that suspends it starts at a missed due date
// rather than at the first dollar owed. `prepaid` moves it back, and issues a final invoice for
// everything not yet billed.
type SetContractIn struct {
	OrgSlug string `path:"org"`

	Collection string `json:"collection" doc:"invoiced or prepaid"`

	// Pointers, so absent means the deployment's default rather than zero days.
	TermsDays        *int `json:"terms_days,omitzero" doc:"Days an invoice is due after it is issued. Defaults to 30"`
	OverdueGraceDays *int `json:"overdue_grace_days,omitzero" doc:"Days after a missed due date before ingestion is suspended. Defaults to 14"`

	PlatformFeeMicros       int64 `json:"platform_fee_micros,omitzero" doc:"Billed every month, prorated in a part month. $500 is 500000000"`
	MinimumCommitmentMicros int64 `json:"minimum_commitment_micros,omitzero" doc:"The least a month bills for usage; the difference is a commitment line"`

	// CreditLimitMicros is the contract's cap on exposure — unbilled usage and unpaid invoices —
	// and is required for an invoiced contract: an account allowed to owe without a bound is one
	// nobody notices has stopped paying until the second invoice.
	CreditLimitMicros *int64 `json:"credit_limit_micros,omitzero" doc:"Required for invoiced. Owing this much, billed or not, suspends ingestion"`

	// BillingEmail is where Stripe sends the invoices. Required for an invoiced contract unless the
	// organization already has one: Stripe will not issue an invoice it is to send to nobody.
	BillingEmail string `json:"billing_email,omitzero" doc:"Where invoices are sent"`

	PONumber string `json:"po_number,omitzero" doc:"Printed on every invoice"`
	Memo     string `json:"memo,omitzero" doc:"The invoice footer"`

	Operator string `json:"operator" doc:"Your own address. Recorded in the customer's audit log as self-asserted"`
}

func (in *SetContractIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Collection).Trim().Lower().Required().OneOf("invoiced", "prepaid")
	v.Number(&in.TermsDays).Min(0).Max(120)
	v.Number(&in.OverdueGraceDays).Min(0).Max(90)
	v.Number(&in.PlatformFeeMicros).Min(0).Max(float64(MaxGrant))
	v.Number(&in.MinimumCommitmentMicros).Min(0).Max(float64(MaxGrant))
	v.Number(&in.CreditLimitMicros).Min(0).Max(float64(MaxGrant))
	v.String(&in.BillingEmail).Trim().Lower().Email().MaxLen(254)
	v.String(&in.PONumber).Trim().MaxLen(140)
	v.String(&in.Memo).Trim().MaxLen(500)
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
}

// ContractOut is the terms an organization is on.
type ContractOut struct {
	Collection              string    `json:"collection" doc:"invoiced or prepaid"`
	EffectiveFrom           time.Time `json:"effective_from"`
	TermsDays               int       `json:"terms_days"`
	OverdueGraceDays        int       `json:"overdue_grace_days"`
	PlatformFeeMicros       int64     `json:"platform_fee_micros"`
	MinimumCommitmentMicros int64     `json:"minimum_commitment_micros"`
	CreditLimitMicros       *int64    `json:"credit_limit_micros,omitzero"`
	PONumber                string    `json:"po_number,omitzero"`
	Memo                    string    `json:"memo,omitzero"`
}

// InvoiceParams names one of an organization's invoices.
type InvoiceParams struct {
	OrgSlug   string    `path:"org"`
	InvoiceID uuid.UUID `path:"invoice_id"`
}

func (in *InvoiceParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
}

// InvoicesOut is an organization's invoices, newest first.
type InvoicesOut struct {
	Invoices []InvoiceOut `json:"invoices"`
}

// InvoiceOut is one invoice, as the customer reads it.
type InvoiceOut struct {
	ID     uuid.UUID `json:"id"`
	Number string    `json:"number,omitzero" doc:"Stripe's invoice number, once issued"`
	Status string    `json:"status" doc:"draft (being issued), open, paid, void, uncollectible, or empty (nothing to bill; never sent)"`

	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end" doc:"Exclusive"`
	Final       bool      `json:"final,omitzero" doc:"Issued when the organization left invoicing"`

	Currency              string `json:"currency"`
	TotalMicros           int64  `json:"total_micros"`
	Total                 string `json:"total"`
	AmountPaidMicros      int64  `json:"amount_paid_micros"`
	AmountRemainingMicros int64  `json:"amount_remaining_micros"`

	IssuedAt *time.Time `json:"issued_at,omitzero"`
	DueAt    *time.Time `json:"due_at,omitzero"`
	PaidAt   *time.Time `json:"paid_at,omitzero"`
	Overdue  bool       `json:"overdue" doc:"Open and past its due date"`

	HostedURL string `json:"hosted_url,omitzero" doc:"Stripe's page to read and pay it"`
	PDFURL    string `json:"pdf_url,omitzero"`
	PONumber  string `json:"po_number,omitzero"`

	Lines []InvoiceLineOut `json:"lines"`
}

// InvoiceLineOut is one line.
type InvoiceLineOut struct {
	Kind         string     `json:"kind" doc:"platform_fee, usage, prior_usage, credits_applied, commitment or adjustment"`
	Description  string     `json:"description"`
	AmountMicros int64      `json:"amount_micros" doc:"The ledger's exact amount"`
	AmountCents  int64      `json:"amount_cents" doc:"What the invoice bills for it"`
	PeriodStart  *time.Time `json:"period_start,omitzero"`
}

// OperatorInvoicesOut is an organization's invoices as staff see them.
type OperatorInvoicesOut struct {
	Invoices []OperatorInvoiceOut `json:"invoices"`
}

// OperatorInvoiceOut is an invoice with what staff need to reconcile it.
type OperatorInvoiceOut struct {
	InvoiceOut

	StripeInvoiceID   string `json:"stripe_invoice_id,omitzero"`
	LastError         string `json:"last_error,omitzero" doc:"Why the last attempt to issue it did not"`
	DiscrepancyMicros int64  `json:"discrepancy_micros" doc:"Usage billed for the month minus what the dashboard said it cost; zero when they agree"`
	CarryInMicros     int64  `json:"carry_in_micros"`
	CarryOutMicros    int64  `json:"carry_out_micros"`
	LedgerFromID      int    `json:"ledger_from_id"`
	LedgerToID        int    `json:"ledger_to_id"`
}

// MarkInvoicePaidIn records a payment Stripe did not see: a wire, a cheque.
type MarkInvoicePaidIn struct {
	OrgSlug   string    `path:"org"`
	InvoiceID uuid.UUID `path:"invoice_id"`

	// Reference is the idempotency key: the bank's reference for the transfer.
	Reference string `json:"reference" doc:"The transfer's reference; the same one twice marks it paid once"`
	Operator  string `json:"operator"`
}

func (in *MarkInvoicePaidIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Reference).Trim().Required().MinLen(4).MaxLen(200)
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
}

// CreditInvoiceIn lowers what an open invoice asks for.
type CreditInvoiceIn struct {
	OrgSlug   string    `path:"org"`
	InvoiceID uuid.UUID `path:"invoice_id"`

	AmountMicros int64  `json:"amount_micros" doc:"Whole cents, at most what is still owed on it"`
	Reason       string `json:"reason" doc:"Printed on the credit note"`
	Reference    string `json:"reference" doc:"An idempotency key: the same one twice credits once"`
	Operator     string `json:"operator"`
}

func (in *CreditInvoiceIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.AmountMicros).Min(1).Max(float64(MaxGrant))
	v.String(&in.Reason).Trim().Required().MinLen(3).MaxLen(500)
	v.String(&in.Reference).Trim().Required().MinLen(4).MaxLen(200)
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
}
