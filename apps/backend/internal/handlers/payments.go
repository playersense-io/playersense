package handlers

import (
	"context"
	"errors"
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/schemas"
)

// Paying for credit through Stripe (ADR 0040).
//
// PlayerSense hosts no payment form and stores no billing information. A top-up is a redirect to
// Stripe Checkout; managing cards and reading receipts is a redirect to Stripe's customer portal; and
// the money arrives by webhook, which is the only thing that puts credit in a wallet. The browser
// coming back from Checkout proves nothing — anybody can load a success URL — so the dashboard
// shows the balance the webhook produced rather than trusting the redirect.

// CreateTopUp opens a Stripe Checkout page for one top-up.
//
// Nothing is credited here. The session is a page the person may close; the wallet moves only when
// Stripe reports the payment succeeded.
func CreateTopUp(ctx *muzak.Context, in schemas.TopUpIn) (schemas.CheckoutOut, error) {
	db := muzak.From[*core.DB](ctx)
	settings := muzak.From[config.Settings](ctx)
	gateway := muzak.From[payments.Gateway](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.CheckoutOut{}, err
	}
	customer, err := ensureCustomer(ctx, account)
	if err != nil {
		return schemas.CheckoutOut{}, err
	}
	checkout, err := gateway.CreateCheckout(ctx.Context(), payments.CheckoutRequest{
		AccountUID:  account.UID.String(),
		Customer:    customer,
		AmountCents: in.AmountMicros / 10_000,
		Currency:    account.Currency,
		SuccessURL:  settings.StripeCheckoutSuccessURL,
		CancelURL:   settings.StripeCheckoutCancelURL,
	})
	if err != nil {
		return schemas.CheckoutOut{}, paymentsError(err)
	}
	return schemas.CheckoutOut{URL: checkout.URL, ID: checkout.ID, ExpiresAt: checkout.ExpiresAt}, nil
}

// OpenBillingPortal opens Stripe's customer portal: add or remove cards, choose the default, read
// receipts. Opening it before the first top-up is allowed — adding a card first is a reasonable
// order to do things in — so it opens the Stripe customer if there is none yet.
func OpenBillingPortal(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.PortalOut, error) {
	db := muzak.From[*core.DB](ctx)
	settings := muzak.From[config.Settings](ctx)
	gateway := muzak.From[payments.Gateway](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.PortalOut{}, err
	}
	customer, err := ensureCustomer(ctx, account)
	if err != nil {
		return schemas.PortalOut{}, err
	}
	url, err := gateway.CreatePortal(ctx.Context(), customer, settings.StripePortalReturnURL)
	if err != nil {
		return schemas.PortalOut{}, paymentsError(err)
	}
	return schemas.PortalOut{URL: url}, nil
}

// ReceiveStripeWebhook is where Stripe reports what happened.
//
// The status is Stripe's instruction as much as ours: 2xx means "done, do not send again", anything
// else means "send again later". So a bad signature is 400 (never going to verify), and a failure to
// handle a verified event is 500 (Stripe retries, and the event row says why).
func ReceiveStripeWebhook(ctx *muzak.Context, _ muzak.Empty) (schemas.WebhookOut, error) {
	hooks := muzak.From[*payments.Webhooks](ctx)
	body, ok := ctx.RawBody()
	if !ok {
		return schemas.WebhookOut{}, muzak.NewHTTPError(http.StatusInternalServerError,
			"the webhook route does not capture its body")
	}
	// Copied: the captured buffer is released with the request, and the event row keeps it.
	payload := append([]byte(nil), body...)

	outcome, err := hooks.Receive(ctx.Context(), payload, ctx.Header("Stripe-Signature"))
	switch {
	case errors.Is(err, payments.ErrBadSignature):
		return schemas.WebhookOut{}, muzak.BadRequest("the signature does not verify").WithCode("bad_signature")
	case errors.Is(err, payments.ErrNotConfigured):
		return schemas.WebhookOut{}, muzak.ServiceUnavailable("payments are not configured").
			WithCode("payments_not_configured")
	case err != nil:
		return schemas.WebhookOut{}, err
	}
	return schemas.WebhookOut{Received: true, Status: outcome.Status}, nil
}

// ensureCustomer returns the account's Stripe customer, opening one the first time it is needed.
//
// Keyed on the account at Stripe, so two tabs opening Checkout at once open one customer; and stored
// only where none is stored yet, so the second tab does not overwrite the first with the same id.
func ensureCustomer(ctx *muzak.Context, account *ent.BillingAccount) (string, error) {
	if account.StripeCustomerID != nil {
		return *account.StripeCustomerID, nil
	}
	db := muzak.From[*core.DB](ctx)
	gateway := muzak.From[payments.Gateway](ctx)
	principal := muzak.From[*core.Principal](ctx)

	email := ""
	if account.BillingEmail != nil {
		email = *account.BillingEmail
	} else if principal.User != nil {
		email = principal.User.Email
	}
	customer, err := gateway.CreateCustomer(ctx.Context(), payments.CustomerRequest{
		AccountUID: account.UID.String(),
		Name:       principal.Organization.Name,
		Email:      email,
		OrgSlug:    principal.Organization.Slug,
	})
	if err != nil {
		return "", paymentsError(err)
	}
	if err := db.Client.BillingAccount.Update().
		Where(billingaccount.ID(account.ID), billingaccount.StripeCustomerIDIsNil()).
		SetStripeCustomerID(customer).
		Exec(ctx.Context()); err != nil {
		return "", core.AsHTTPError(err)
	}
	return stripeCustomerOf(ctx.Context(), db, account.ID)
}

func stripeCustomerOf(ctx context.Context, db *core.DB, accountID int) (string, error) {
	fresh, err := db.Client.BillingAccount.Get(ctx, accountID)
	if err != nil {
		return "", core.AsHTTPError(err)
	}
	if fresh.StripeCustomerID == nil {
		return "", muzak.ServiceUnavailable("the payment account could not be opened; try again")
	}
	return *fresh.StripeCustomerID, nil
}

// chargeable asks Stripe whether the account has a card an automatic top-up could use.
func chargeable(ctx *muzak.Context, account *ent.BillingAccount) (bool, error) {
	if account.StripeCustomerID == nil {
		return false, nil
	}
	card, err := muzak.From[payments.Gateway](ctx).ChargeableCard(ctx.Context(), *account.StripeCustomerID)
	if err != nil {
		return false, paymentsError(err)
	}
	return card != "", nil
}

// paymentsError says "Stripe is unreachable" as a 503 — try again — and never as a 500, which would
// read as our bug.
func paymentsError(err error) error {
	if errors.Is(err, payments.ErrNotConfigured) {
		return muzak.ServiceUnavailable("payments are not configured on this deployment").
			WithCode("payments_not_configured")
	}
	return muzak.ServiceUnavailable("Stripe could not be reached; try again").
		WithCode("payments_unavailable").Wrap(err)
}
