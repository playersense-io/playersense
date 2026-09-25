// Package stores checks a game player's purchase with the store that sold it (project.md §46,
// §47): Apple's App Store, Google Play and Steam. Each adapter takes what the game was handed at
// checkout, asks the store (or, for a StoreKit 2 transaction, checks Apple's signature, which
// needs no call at all), and answers with one normalized Transaction or a reason it was turned
// away.
//
// Nothing here writes anything. Recording a purchase, refusing a replay and counting revenue are
// internal/purchases' job; this package only knows how each store says yes, no, and refunded.
package stores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Platform is a store. The values are the ones verified_purchases stores.
type Platform string

const (
	Apple  Platform = "apple"
	Google Platform = "google"
	Steam  Platform = "steam"
)

// Platforms is every store purchases are verified with.
var Platforms = []Platform{Apple, Google, Steam}

// Valid reports whether p is a store this build verifies with.
func (p Platform) Valid() bool { return p == Apple || p == Google || p == Steam }

// StoreName is the store as the analytics tables and the dashboard write it.
func (p Platform) StoreName() string {
	switch p {
	case Apple:
		return "app_store"
	case Google:
		return "google_play"
	}
	return string(p)
}

// DevicePlatform is the platform a purchase from this store was made on, for a verified purchase
// the game's own events did not describe.
func (p Platform) DevicePlatform() string {
	switch p {
	case Apple:
		return "ios"
	case Google:
		return "android"
	}
	return string(p)
}

// PlatformOfStore reads a store name back: app_store to apple, and so on.
func PlatformOfStore(store string) (Platform, bool) {
	switch store {
	case "app_store", "apple":
		return Apple, true
	case "google_play", "google":
		return Google, true
	case "steam":
		return Steam, true
	}
	return "", false
}

// Receipt is what the game hands over: which fields matter depends on the store.
type Receipt struct {
	// SignedTransaction is a StoreKit 2 transaction's jwsRepresentation. Checked against Apple's
	// certificate chain here, with no call to Apple.
	SignedTransaction string
	// TransactionID is an App Store transaction id, looked up with the App Store Server API; it
	// needs the project's In-App Purchase key.
	TransactionID string
	// PurchaseToken is Google Play's token for the purchase, with ProductID.
	PurchaseToken string
	// OrderID is the order id the game gave Steam's InitTxn.
	OrderID string
	// ProductID is the product the game believes was bought. Required for Google, whose lookup
	// is by product; checked against the store's answer for the others.
	ProductID string
	// PriceMicros and Currency are the price the game showed. Google's answer carries no price,
	// so for Google these are what is recorded; for Apple and Steam the store's own price wins.
	PriceMicros int64
	Currency    string
}

// Kind is what sort of product was bought.
type Kind string

const (
	Consumable    Kind = "consumable"
	NonConsumable Kind = "non_consumable"
	Subscription  Kind = "subscription"
	UnknownKind   Kind = "unknown"
)

// Transaction is one purchase as the store described it, in one shape for every store.
type Transaction struct {
	// TransactionID is the store's own id for the purchase, unique across the store: Apple's
	// transactionId, Google's orderId (or a digest of the token for a test purchase, which has
	// none), Steam's transid.
	TransactionID         string
	OriginalTransactionID string
	// OrderID is Steam's order id, the one the game chose; empty elsewhere.
	OrderID string
	// TokenDigest is a digest of Google's purchase token, which is how a notification that names
	// only the token finds the purchase.
	TokenDigest string

	AppID       string
	ProductID   string
	Kind        Kind
	Quantity    int
	PurchasedAt time.Time

	AmountMicros int64
	Currency     string
	// PriceFromStore is false where the price is the one the game reported (Google).
	PriceFromStore bool
	// Country is ISO 3166 alpha-2, empty when the store did not say.
	Country string
	// Sandbox is a test purchase: TestFlight or the sandbox, a Play license tester, Steam's
	// sandbox. Verified, and kept out of revenue.
	Sandbox bool

	// RefundedAt is set when the store already says the purchase was given back.
	RefundedAt   *time.Time
	RefundReason RefundReason
}

// RefundReason is why a purchase was given back, in one vocabulary for every store.
type RefundReason string

const (
	RefundCustomerRequest RefundReason = "customer_request"
	RefundAccidental      RefundReason = "accidental_purchase"
	RefundNotReceived     RefundReason = "not_received"
	RefundDefective       RefundReason = "defective"
	RefundChargeback      RefundReason = "chargeback"
	RefundFraud           RefundReason = "fraud"
	RefundOther           RefundReason = "other"
)

// Refund is one purchase a store says was given back (or, Reversed, whose refund was undone).
type Refund struct {
	Platform      Platform
	TransactionID string
	OrderID       string
	TokenDigest   string
	At            time.Time
	Reason        RefundReason
	Reversed      bool
	// Transaction is the store's full description where it came with the refund (Apple).
	Transaction *Transaction
}

// Code is why a receipt was turned away. Each is a statement about the receipt, never about us:
// a store that could not be reached is Unavailable, not a rejection.
type Code string

const (
	// InvalidReceipt: malformed, or the store does not know it.
	InvalidReceipt Code = "invalid_receipt"
	// SignatureMismatch: the signature does not check out against the store's certificates.
	SignatureMismatch Code = "signature_mismatch"
	// WrongApp: a real receipt, for another app.
	WrongApp Code = "wrong_app"
	// ProductMismatch: a real receipt, for another product than the game said.
	ProductMismatch Code = "product_mismatch"
	// NotPurchased: known to the store but not paid: pending, canceled or never finalized.
	NotPurchased Code = "not_purchased"
	// Replayed: a transaction already granted to another player, sent again.
	Replayed Code = "replayed"
)

// Rejection is a receipt the store (or its signature) says is not a purchase.
type Rejection struct {
	Code   Code
	Detail string
}

func (r *Rejection) Error() string { return string(r.Code) + ": " + r.Detail }

func reject(code Code, format string, args ...any) error {
	return &Rejection{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Unavailable is a store that could not answer: unreachable, slow, rate limited or failing. The
// receipt is neither good nor bad yet, and the caller should retry.
type Unavailable struct {
	Status int
	Detail string
}

func (u *Unavailable) Error() string { return "the store could not answer: " + u.Detail }

// ErrCredentials is a store refusing the project's credentials: a key revoked, a service account
// without access. Nothing a retry fixes; somebody has to fix the store settings.
var ErrCredentials = errors.New("the store refused the project's credentials")

// ErrNeedsKey is a receipt that can only be checked with credentials the project has not given.
var ErrNeedsKey = errors.New("this receipt needs store credentials the project has not added")

// Credentials is how one project reaches one store: its identifiers and its secrets.
type Credentials struct {
	Platform Platform

	// Apple: the bundle id is enough for StoreKit 2 transactions. The App Store Server API (a
	// transaction id lookup, refund sweeps, test notifications) needs the rest.
	BundleID   string
	AppAppleID int64
	IssuerID   string
	KeyID      string
	PrivateKey string // the .p8 file's PEM

	// Google: the package name, and a service account's JSON key with access to the app.
	PackageName    string
	ServiceAccount string

	// Steam: the app id and a publisher Web API key.
	AppID        string
	PublisherKey string
}

// Step is one thing an adapter did while checking a receipt: what the debugging page shows.
type Step struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Ms     int    `json:"ms"`
}

// Trace collects the steps. A nil Trace records nothing.
type Trace struct {
	Steps []Step
	last  time.Time
}

// NewTrace starts a trace now.
func NewTrace() *Trace { return &Trace{last: time.Now()} }

func (t *Trace) step(name string, ok bool, format string, args ...any) {
	if t == nil {
		return
	}
	now := time.Now()
	if t.last.IsZero() {
		t.last = now
	}
	t.Steps = append(t.Steps, Step{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...),
		Ms: int(now.Sub(t.last).Milliseconds())})
	t.last = now
}

// Adapter is one store.
type Adapter interface {
	// Verify checks a receipt with the store.
	Verify(ctx context.Context, c Credentials, r Receipt, t *Trace) (Transaction, error)
	// Refunds reads what the store gave back since a moment, and the moment to read from next.
	Refunds(ctx context.Context, c Credentials, since time.Time) ([]Refund, time.Time, error)
	// Test proves the credentials work, without a purchase.
	Test(ctx context.Context, c Credentials) (string, error)
}

// Set is the adapters by store.
type Set map[Platform]Adapter

// Live is the adapters that call the real stores.
func Live(client *http.Client) Set {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return Set{
		Apple:  &AppleStore{HTTP: client},
		Google: &GoogleStore{HTTP: client},
		Steam:  &SteamStore{HTTP: client},
	}
}

// Digest is a stable short digest of a secret-ish identifier, such as Google's purchase token.
func Digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// statusError reads an HTTP status as the error it means: a refusal of the credentials, the store
// being unavailable, or nil for anything else, which the adapter reads itself.
func statusError(status int, body []byte) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d: %s)", ErrCredentials, status, snippet(body))
	case status == http.StatusTooManyRequests || status >= 500:
		return &Unavailable{Status: status, Detail: fmt.Sprintf("HTTP %d: %s", status, snippet(body))}
	}
	return nil
}

func snippet(body []byte) string {
	const max = 200
	if len(body) > max {
		return string(body[:max]) + "..."
	}
	return string(body)
}
