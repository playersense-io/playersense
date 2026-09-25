package schemas

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// ---- verification (the SDK and the game's server) -------------------------------------------

// VerifyPurchaseIn is one receipt, as the game or its server was handed it.
type VerifyPurchaseIn struct {
	Store     string `json:"store" enum:"app_store,google_play,steam" doc:"The store that sold it"`
	PlayerID  string `json:"player_id,omitzero" doc:"The game's own id for the player; send it once they have one"`
	InstallID string `json:"install_id,omitzero" doc:"The SDK's install id; a purchase before sign-in belongs to the install"`
	ProductID string `json:"product_id,omitzero" doc:"The product the game sold. Required for Google Play; checked against the store's answer elsewhere"`

	SignedTransaction string `json:"signed_transaction,omitzero" doc:"App Store: the StoreKit 2 transaction's jwsRepresentation. Checked against Apple's certificates with no call to Apple"`
	TransactionID     string `json:"transaction_id,omitzero" doc:"App Store: a transaction id, looked up with the project's In-App Purchase key"`
	PurchaseToken     string `json:"purchase_token,omitzero" doc:"Google Play: the purchase's token"`
	OrderID           string `json:"order_id,omitzero" doc:"Steam: the order id the game gave InitTxn"`

	PriceMicros int64  `json:"price_micros,omitzero" doc:"The price the game showed, in millionths. Google's answer has none, so for Google this is what is recorded"`
	Currency    string `json:"currency,omitzero" doc:"ISO 4217, with price_micros"`
}

func (in *VerifyPurchaseIn) Validate(v *muzak.Validation) {
	v.String(&in.Store).Trim().Lower().Required().OneOf("app_store", "google_play", "steam")
	v.String(&in.PlayerID).Trim().MaxLen(128)
	v.String(&in.InstallID).Trim().MaxLen(64)
	v.String(&in.ProductID).Trim().MaxLen(255)
	v.String(&in.SignedTransaction).Trim().MaxLen(16 << 10)
	v.String(&in.TransactionID).Trim().MaxLen(64)
	v.String(&in.PurchaseToken).Trim().MaxLen(1024)
	v.String(&in.OrderID).Trim().MaxLen(64)
	v.String(&in.Currency).Trim().Upper().MaxLen(3)
	v.Number(&in.PriceMicros).Min(0)
	if in.PlayerID == "" && in.InstallID == "" {
		v.Reject(&in.PlayerID, "send the player_id, the install_id, or both: a purchase belongs to somebody")
	}
	switch in.Store {
	case "app_store":
		if in.SignedTransaction == "" && in.TransactionID == "" {
			v.Reject(&in.SignedTransaction, "an App Store purchase needs its signed_transaction (or its transaction_id)")
		}
	case "google_play":
		if in.PurchaseToken == "" || in.ProductID == "" {
			v.Reject(&in.PurchaseToken, "a Google Play purchase needs its purchase_token and product_id")
		}
	case "steam":
		if in.OrderID == "" {
			v.Reject(&in.OrderID, "a Steam purchase needs its order_id")
		}
	}
}

// PurchaseLookupParams names a purchase by its store and the store's transaction id.
type PurchaseLookupParams struct {
	Store         string `path:"store" enum:"app_store,google_play,steam"`
	TransactionID string `path:"transaction" doc:"The store's transaction id: Apple's transactionId, Google's orderId, Steam's transid"`
}

func (in *PurchaseLookupParams) Validate(v *muzak.Validation) {
	v.String(&in.Store).Trim().Lower().Required().OneOf("app_store", "google_play", "steam")
	v.String(&in.TransactionID).Trim().Required().MaxLen(255)
}

// PlayerPurchasesParams asks for one player's purchases.
type PlayerPurchasesParams struct {
	PlayerID string `path:"player"`
	Limit    int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *PlayerPurchasesParams) Validate(v *muzak.Validation) {
	v.String(&in.PlayerID).Trim().Required().MaxLen(128)
	v.Number(&in.Limit).Min(1).Max(200)
}

// PurchaseDoc is a purchase as the API hands it to the game and its server: the answer to a
// verification, and what the purchase webhook will carry. Grant the product when status is verified
// or sandbox, keyed by id so a retry grants it once.
type PurchaseDoc struct {
	ID uuid.UUID `json:"id,omitzero" doc:"PlayerSense's id for the purchase; absent when rejected"`
	// Status: verified and counted; refunded, verified then given back; sandbox, a test purchase,
	// verified and kept out of revenue; rejected, the receipt did not check out.
	Status string `json:"status" enum:"verified,refunded,sandbox,rejected"`
	// Duplicate is a purchase PlayerSense answered before for this player: the same id again.
	Duplicate bool `json:"duplicate"`

	Player      PurchasePlayer   `json:"player"`
	Product     PurchaseProduct  `json:"product"`
	Price       *PurchasePrice   `json:"price,omitzero"`
	Store       PurchaseStore    `json:"store"`
	Environment string           `json:"environment" enum:"production,sandbox"`
	Country     string           `json:"country,omitzero"`
	First       bool             `json:"first_purchase" doc:"The player's first counted purchase in the project"`
	PurchasedAt *time.Time       `json:"purchased_at,omitzero"`
	VerifiedAt  *time.Time       `json:"verified_at,omitzero"`
	Refund      *PurchaseRefund  `json:"refund,omitzero"`
	Rejection   *PurchaseProblem `json:"rejection,omitzero"`
}

type PurchasePlayer struct {
	ID        string `json:"id,omitzero"`
	InstallID string `json:"install_id,omitzero"`
}

type PurchaseProduct struct {
	ID       string `json:"id,omitzero"`
	Kind     string `json:"kind,omitzero" enum:"consumable,non_consumable,subscription,unknown"`
	Quantity int    `json:"quantity,omitzero"`
}

type PurchasePrice struct {
	AmountMicros int64  `json:"amount_micros"`
	Currency     string `json:"currency"`
	// FromStore is false where the price is the one the game reported (Google Play's answer has
	// none).
	FromStore bool `json:"from_store"`
}

type PurchaseStore struct {
	Name                  string `json:"name" enum:"app_store,google_play,steam"`
	TransactionID         string `json:"transaction_id,omitzero"`
	OriginalTransactionID string `json:"original_transaction_id,omitzero"`
	OrderID               string `json:"order_id,omitzero"`
	VerifiedInMs          int    `json:"verified_in_ms,omitzero"`
}

type PurchaseRefund struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitzero" enum:"customer_request,accidental_purchase,not_received,defective,chargeback,fraud,other"`
	Source string    `json:"source,omitzero" enum:"verification,notification,sweep"`
}

type PurchaseProblem struct {
	Code    string `json:"code" enum:"invalid_receipt,signature_mismatch,wrong_app,product_mismatch,not_purchased,replayed"`
	Message string `json:"message"`
}

// PurchaseDocsOut is a list of purchases.
type PurchaseDocsOut struct {
	Purchases []PurchaseDoc `json:"purchases"`
}

// ---- store notifications ---------------------------------------------------------------------

// StoreNotificationParams is where a store posts: the project and the URL's token.
type StoreNotificationParams struct {
	ProjectID uuid.UUID `path:"project"`
	Token     string    `path:"token"`
}

func (in *StoreNotificationParams) Validate(v *muzak.Validation) {
	v.String(&in.Token).Trim().Required().MaxLen(64)
}

// AppleNotificationIn is an App Store Server Notification V2.
type AppleNotificationIn struct {
	StoreNotificationParams
	SignedPayload string `json:"signedPayload" doc:"The notification, signed by Apple"`
}

func (in *AppleNotificationIn) Validate(v *muzak.Validation) {
	in.StoreNotificationParams.Validate(v)
	v.String(&in.SignedPayload).Required().MaxLen(256 << 10)
}

// GoogleNotificationIn is a Pub/Sub push carrying a Real-time Developer Notification.
type GoogleNotificationIn struct {
	StoreNotificationParams
	Message      json.RawMessage `json:"message" doc:"The Pub/Sub message; its data is the notification"`
	Subscription string          `json:"subscription,omitzero"`
}

func (in *GoogleNotificationIn) Validate(v *muzak.Validation) {
	in.StoreNotificationParams.Validate(v)
	if len(in.Message) == 0 {
		v.Reject(&in.Message, "a Pub/Sub push carries a message")
	}
}

// StoreNotificationOut is what a notification changed.
type StoreNotificationOut struct {
	Type    string `json:"type,omitzero"`
	Refund  bool   `json:"refund"`
	Ignored string `json:"ignored,omitzero"`
}

// ---- the dashboard: stores -------------------------------------------------------------------

// StoreParams names one of the project's stores.
type StoreParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Store       string `path:"store" enum:"app_store,google_play,steam"`
}

func (in *StoreParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Store).Trim().Lower().Required().OneOf("app_store", "google_play", "steam")
}

// StoreIn connects a store, or changes how it is reached. A secret not sent is kept.
type StoreIn struct {
	StoreParams
	Enabled *bool `json:"enabled,omitzero"`

	// App Store.
	BundleID   string `json:"bundle_id,omitzero" doc:"App Store: the app's bundle id"`
	AppAppleID int64  `json:"app_apple_id,omitzero" doc:"App Store: the app's Apple id, from App Store Connect"`
	IssuerID   string `json:"issuer_id,omitzero" doc:"App Store: the In-App Purchase key's issuer id"`
	KeyID      string `json:"key_id,omitzero" doc:"App Store: the In-App Purchase key's id"`
	PrivateKey string `json:"private_key,omitzero" doc:"App Store: the In-App Purchase key's .p8 file. Write-only"`

	// Google Play.
	PackageName    string `json:"package_name,omitzero" doc:"Google Play: the app's package name"`
	ServiceAccount string `json:"service_account,omitzero" doc:"Google Play: a service account's JSON key with access to the app. Write-only"`

	// Steam.
	AppID        string `json:"app_id,omitzero" doc:"Steam: the app id"`
	PublisherKey string `json:"publisher_key,omitzero" doc:"Steam: a publisher Web API key. Write-only"`
}

func (in *StoreIn) Validate(v *muzak.Validation) {
	in.StoreParams.Validate(v)
	v.String(&in.BundleID).Trim().MaxLen(255)
	v.String(&in.IssuerID).Trim().MaxLen(64)
	v.String(&in.KeyID).Trim().MaxLen(32)
	v.String(&in.PrivateKey).Trim().MaxLen(4096)
	v.String(&in.PackageName).Trim().MaxLen(255)
	v.String(&in.ServiceAccount).Trim().MaxLen(16 << 10)
	v.String(&in.AppID).Trim().MaxLen(20)
	v.String(&in.PublisherKey).Trim().MaxLen(128)
	v.Number(&in.AppAppleID).Min(0)
	if in.PrivateKey != "" && !strings.Contains(in.PrivateKey, "PRIVATE KEY") {
		v.Reject(&in.PrivateKey, "paste the whole .p8 file, from BEGIN PRIVATE KEY to END PRIVATE KEY")
	}
}

// StoreOut is one store as the project reaches it. Secrets are never returned; has_* says whether
// one is set.
type StoreOut struct {
	Store     string `json:"store" enum:"app_store,google_play,steam"`
	Connected bool   `json:"connected"`
	Enabled   bool   `json:"enabled"`

	BundleID    string `json:"bundle_id,omitzero"`
	AppAppleID  int64  `json:"app_apple_id,omitzero"`
	IssuerID    string `json:"issuer_id,omitzero"`
	KeyID       string `json:"key_id,omitzero"`
	HasKey      bool   `json:"has_key"`
	PackageName string `json:"package_name,omitzero"`
	// ServiceAccountEmail is the service account's address, read from its key.
	ServiceAccountEmail string `json:"service_account_email,omitzero"`
	AppID               string `json:"app_id,omitzero"`

	// NotificationURL is where to point App Store Server Notifications V2, or the Pub/Sub push
	// subscription for Google Play's Real-time Developer Notifications. Steam sends none; its
	// refunds are swept.
	NotificationURL string `json:"notification_url,omitzero"`

	// VerifiedSince is when this store's revenue became its verified purchases.
	VerifiedSince      *time.Time    `json:"verified_since,omitzero"`
	LastVerifiedAt     *time.Time    `json:"last_verified_at,omitzero"`
	LastNotificationAt *time.Time    `json:"last_notification_at,omitzero"`
	SweptAt            *time.Time    `json:"swept_at,omitzero"`
	LastError          string        `json:"last_error,omitzero"`
	LastErrorAt        *time.Time    `json:"last_error_at,omitzero"`
	LastTest           *StoreTestOut `json:"last_test,omitzero"`
	Day                StoreOutcomes `json:"last_day" doc:"Verifications in the last 24 hours, by what came of them"`
	CreatedBy          string        `json:"created_by,omitzero"`
	UpdatedBy          string        `json:"updated_by,omitzero"`
	UpdatedAt          *time.Time    `json:"updated_at,omitzero"`
	Status             string        `json:"status" enum:"not_connected,off,untested,working,failing"`
	Missing            []string      `json:"missing,omitzero" doc:"What is not set that some checks need"`
}

// StoreOutcomes counts verifications by outcome.
type StoreOutcomes struct {
	Verified     int `json:"verified"`
	Duplicate    int `json:"duplicate"`
	Rejected     int `json:"rejected"`
	Unavailable  int `json:"unavailable"`
	Credentials  int `json:"credentials"`
	NotConnected int `json:"not_connected"`
}

// StoreListOut is the project's three stores.
type StoreListOut struct {
	Stores []StoreOut `json:"stores"`
}

// StoreTestOut is how a store answered a test.
type StoreTestOut struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Detail string    `json:"detail"`
}

// ---- the dashboard and staff: the verification log -------------------------------------------

// PurchaseAttemptsParams filters the verification log.
type PurchaseAttemptsParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	PurchaseAttemptFilter
}

func (in *PurchaseAttemptsParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	in.PurchaseAttemptFilter.validate(v)
}

// PurchaseAttemptFilter narrows the log.
type PurchaseAttemptFilter struct {
	Store   string    `query:"store,omitzero" enum:"app_store,google_play,steam"`
	Outcome string    `query:"outcome,omitzero" enum:"verified,duplicate,rejected,unavailable,credentials,not_configured"`
	Code    string    `query:"code,omitzero" doc:"A rejection's code"`
	Player  string    `query:"player,omitzero" doc:"A player id or install id"`
	Before  uuid.UUID `query:"before,omitzero" doc:"The next cursor of the previous page"`
	Limit   int       `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (f *PurchaseAttemptFilter) validate(v *muzak.Validation) {
	v.String(&f.Store).Trim().Lower().OneOf("", "app_store", "google_play", "steam")
	v.String(&f.Outcome).Trim().Lower().OneOf("", "verified", "duplicate", "rejected", "unavailable", "credentials", "not_configured")
	v.String(&f.Code).Trim().Lower().MaxLen(40)
	v.String(&f.Player).Trim().MaxLen(128)
	v.Number(&f.Limit).Min(1).Max(200)
}

// PurchaseAttemptOut is one verification.
type PurchaseAttemptOut struct {
	ID                 uuid.UUID     `json:"id"`
	At                 time.Time     `json:"at"`
	Store              string        `json:"store"`
	Via                string        `json:"via" enum:"project,server"`
	PlayerID           string        `json:"player_id,omitzero"`
	InstallID          string        `json:"install_id,omitzero"`
	ProductID          string        `json:"product_id,omitzero"`
	StoreTransactionID string        `json:"store_transaction_id,omitzero"`
	Outcome            string        `json:"outcome" enum:"verified,duplicate,rejected,unavailable,credentials,not_configured"`
	Code               string        `json:"code,omitzero"`
	Detail             string        `json:"detail,omitzero"`
	DurationMs         int           `json:"duration_ms"`
	Sandbox            bool          `json:"sandbox"`
	PurchaseID         *uuid.UUID    `json:"purchase_id,omitzero"`
	Trace              []TraceStep   `json:"trace,omitzero"`
	Project            *ProjectBrief `json:"project,omitzero" doc:"Staff reads only: whose attempt"`
}

// TraceStep is one thing a verification did.
type TraceStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitzero"`
	Ms     int    `json:"ms"`
}

// ProjectBrief names a project and its organization.
type ProjectBrief struct {
	ID           uuid.UUID `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Organization string    `json:"organization"`
}

// PurchaseAttemptsOut is one page of the log.
type PurchaseAttemptsOut struct {
	Attempts []PurchaseAttemptOut `json:"attempts"`
	Next     *uuid.UUID           `json:"next,omitzero"`
}

// VerifiedPurchaseParams names one verified purchase.
type VerifiedPurchaseParams struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	PurchaseID  uuid.UUID `path:"purchase"`
}

func (in *VerifiedPurchaseParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// VerifiedPurchaseOut is a purchase and every verification that named it.
type VerifiedPurchaseOut struct {
	Purchase PurchaseDoc          `json:"purchase"`
	Attempts []PurchaseAttemptOut `json:"attempts"`
}

// ---- staff -----------------------------------------------------------------------------------

// OperatorPurchaseAttemptsParams searches every project's log.
type OperatorPurchaseAttemptsParams struct {
	StaffTicket
	Project uuid.UUID `query:"project,omitzero" doc:"One project's attempts"`
	PurchaseAttemptFilter
}

func (in *OperatorPurchaseAttemptsParams) Validate(v *muzak.Validation) {
	in.PurchaseAttemptFilter.validate(v)
}

// OperatorPurchaseAttemptParams names one attempt.
type OperatorPurchaseAttemptParams struct {
	StaffTicket
	AttemptID uuid.UUID `path:"attempt"`
}

// PurchaseVerificationParams asks for verification across the platform.
type PurchaseVerificationParams struct {
	StaffTicket
}

// PurchaseVerificationOut is verification across the platform over the last day: what came of it,
// why receipts were turned away, and the stores failing.
type PurchaseVerificationOut struct {
	Since      time.Time                `json:"since"`
	ByStore    map[string]StoreOutcomes `json:"by_store"`
	Rejections map[string]int           `json:"rejections" doc:"Rejected receipts by code"`
	Failing    []FailingStoreOut        `json:"failing" doc:"Connected stores whose last error is recent"`
}

// FailingStoreOut is one project's store that is failing.
type FailingStoreOut struct {
	Project     ProjectBrief `json:"project"`
	Store       string       `json:"store"`
	LastError   string       `json:"last_error"`
	LastErrorAt time.Time    `json:"last_error_at"`
}

// PurchaseCheckIn is a receipt staff check against a project's store, recording nothing.
type PurchaseCheckIn struct {
	StaffTicket
	Project string `path:"project" doc:"The project's id"`
	VerifyPurchaseIn
}

func (in *PurchaseCheckIn) Validate(v *muzak.Validation) {
	if in.PlayerID == "" && in.InstallID == "" {
		in.InstallID = "staff-check"
	}
	in.VerifyPurchaseIn.Validate(v)
}

// PurchaseCheckOut is what the store said, step by step.
type PurchaseCheckOut struct {
	Outcome     string              `json:"outcome" enum:"verified,rejected,unavailable,credentials,not_configured"`
	Detail      string              `json:"detail,omitzero"`
	Rejection   *PurchaseProblem    `json:"rejection,omitzero"`
	Transaction *CheckedTransaction `json:"transaction,omitzero"`
	// Recorded is the purchase already on record for this transaction, if any.
	Recorded   *PurchaseDoc `json:"recorded,omitzero"`
	Trace      []TraceStep  `json:"trace"`
	DurationMs int          `json:"duration_ms"`
}

// CheckedTransaction is the store's answer, normalized.
type CheckedTransaction struct {
	TransactionID         string     `json:"transaction_id"`
	OriginalTransactionID string     `json:"original_transaction_id,omitzero"`
	OrderID               string     `json:"order_id,omitzero"`
	AppID                 string     `json:"app_id"`
	ProductID             string     `json:"product_id"`
	Kind                  string     `json:"kind"`
	Quantity              int        `json:"quantity"`
	PurchasedAt           time.Time  `json:"purchased_at"`
	AmountMicros          int64      `json:"amount_micros"`
	Currency              string     `json:"currency"`
	PriceFromStore        bool       `json:"price_from_store"`
	Country               string     `json:"country,omitzero"`
	Sandbox               bool       `json:"sandbox"`
	RefundedAt            *time.Time `json:"refunded_at,omitzero"`
	RefundReason          string     `json:"refund_reason,omitzero"`
}
