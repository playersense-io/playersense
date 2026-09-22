package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// Errors that make a payment one this wallet will not credit. None of them is retried: a payment for
// an account that does not exist, or in the wrong currency, will not become creditable by waiting.
var (
	ErrNotOurs          = errors.New("the payment was not made by PlayerSense for a known account")
	ErrCustomerMismatch = errors.New("the payment's customer is not the account's customer")
	ErrWrongCurrency    = errors.New("the payment's currency is not the account's")
)

// Credit puts a succeeded payment into its account's wallet, once.
//
// Called from three places — a Checkout Session completing, a PaymentIntent succeeding, and an
// automatic top-up's own response — and all three land here with the same PaymentIntent id, which is
// the lot's idempotency key. So the first to arrive credits and the other two find the lot and do
// nothing. It reports whether this call was the one that credited.
//
// A purchase lot: money the customer paid, which does not expire, and which pays any arrears before
// it becomes spendable (ADR 0039).
func Credit(ctx context.Context, tx *ent.Tx, p Payment, at time.Time) (bool, error) {
	if !p.Succeeded() || p.ID == "" || p.AmountReceived <= 0 {
		return false, fmt.Errorf("a payment that has not succeeded cannot be credited (%s)", p.Status)
	}
	account, err := accountFor(ctx, tx, p)
	if err != nil {
		return false, err
	}
	if strings.ToLower(p.Currency) != strings.ToLower(account.Currency) {
		return false, fmt.Errorf("%w: %s, not %s", ErrWrongCurrency, p.Currency, account.Currency)
	}

	description := "Top-up"
	if p.Metadata[MetaKind] == KindAutoTopUp {
		description = "Automatic top-up"
	}
	_, granted, err := wallet.Grant(ctx, tx, wallet.GrantRequest{
		AccountID:       account.ID,
		Kind:            wallet.Purchase,
		Amount:          p.AmountReceived * billing.CentMicros,
		GrantedAt:       at,
		Description:     description,
		StripeReference: p.ID,
		Key:             "stripe:" + p.ID,
	})
	if err != nil {
		return false, err
	}

	// An automatic top-up that has landed releases its claim, so the next one can be made when
	// the balance runs low again. Only the claim this payment was made under: a key that has
	// since moved on belongs to a later attempt.
	if key := p.Metadata[MetaKey]; key != "" {
		if err := tx.BillingAccount.Update().
			Where(billingaccount.ID(account.ID), billingaccount.AutoTopupPendingKey(key)).
			ClearAutoTopupPendingKey().ClearAutoTopupPendingAt().ClearAutoTopupPendingAmountMicros().
			ClearAutoTopupRetryAfter().SetAutoTopupLastError("").
			Exec(ctx); err != nil {
			return false, fmt.Errorf("release the automatic top-up: %w", err)
		}
	}
	return granted, nil
}

// Release ends an automatic top-up that did not charge, and says when to try again.
func Release(ctx context.Context, tx *ent.Tx, p Payment, retryAt time.Time, reason string) error {
	key := p.Metadata[MetaKey]
	if key == "" {
		return nil
	}
	account, err := accountFor(ctx, tx, p)
	if err != nil {
		return err
	}
	return tx.BillingAccount.Update().
		Where(billingaccount.ID(account.ID), billingaccount.AutoTopupPendingKey(key)).
		ClearAutoTopupPendingKey().ClearAutoTopupPendingAt().ClearAutoTopupPendingAmountMicros().
		SetAutoTopupRetryAfter(retryAt).
		SetAutoTopupLastError(truncate(reason, 500)).
		Exec(ctx)
}

// accountFor resolves the account a payment names, and checks the payment is really its.
//
// The metadata is ours — PlayerSense put it there when it created the payment — but a webhook is
// only as trustworthy as its signature, and the signature proves Stripe sent it, not that this
// account's customer paid. So the customer is checked too: a payment naming account A made by
// customer B is refused, whatever its metadata says.
func accountFor(ctx context.Context, tx *ent.Tx, p Payment) (*ent.BillingAccount, error) {
	uid, err := uuid.Parse(p.Metadata[MetaAccount])
	if err != nil {
		return nil, ErrNotOurs
	}
	account, err := tx.BillingAccount.Query().Where(billingaccount.UID(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotOurs
	}
	if err != nil {
		return nil, fmt.Errorf("find the billing account: %w", err)
	}
	if account.StripeCustomerID == nil || p.Customer == "" || *account.StripeCustomerID != p.Customer {
		return nil, ErrCustomerMismatch
	}
	return account, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
