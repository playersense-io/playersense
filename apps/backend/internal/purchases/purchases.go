// Package purchases verifies game players' purchases with their stores and keeps the record
// (project.md §46-48): one row per store transaction, refunds as the stores report them, every
// attempt logged, and each change handed to ClickHouse through an outbox so revenue counts it
// exactly once.
//
// The flow for one receipt:
//
//	game or game server -> Verify -> the store's adapter (internal/stores)
//	  -> verified_purchases + purchase_changes, in one transaction
//	  -> purchase_attempts (always)
//	  -> the worker ships purchase_changes to ClickHouse (ship.go)
//
// Idempotency is the database's, as §48 asks: (platform, store_transaction_id) is unique, so a
// retry, a second device and a race between two requests all find the one row. A retry by the
// same player answers the purchase again; the same transaction from anybody else is a replay and
// is turned away.
package purchases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/purchaseattempt"
	"playersense.dev/backend/ent/purchasechange"
	"playersense.dev/backend/ent/purchaseprovider"
	"playersense.dev/backend/ent/verifiedpurchase"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/stores"
)

// Service verifies purchases and records what the stores say about them.
type Service struct {
	DB     *pgdb.DB
	Stores stores.Set
	Log    *slog.Logger
	// Timeout bounds one store call; 15 seconds when zero.
	Timeout time.Duration
	// PublicURL is the API's public address, which a store's notification URL is under.
	PublicURL string
}

// ErrNotConfigured is a store the project has not connected, or has turned off.
var ErrNotConfigured = errors.New("the project has not connected this store")

// Caller is who is asking: the project and environment a key names, and its class.
type Caller struct {
	ProjectID     int
	ProjectUID    uuid.UUID
	EnvironmentID int
	// Via is the key class, "project" or "server", or "debug" for staff checking a receipt, which
	// records nothing but the attempt.
	Via string
}

// Request is one receipt to verify, and who it is for.
type Request struct {
	Platform  stores.Platform
	PlayerID  string
	InstallID string
	Receipt   stores.Receipt
}

// Result is what came of it.
type Result struct {
	// Purchase is the recorded purchase: new, or the one a retry found. Nil when rejected.
	Purchase *ent.VerifiedPurchase
	// Duplicate is a purchase this player was already granted: the game must not grant it again.
	Duplicate bool
	// Rejection is why the receipt was turned away.
	Rejection *stores.Rejection
	// Transaction is the store's answer, for a debug check that records nothing.
	Transaction *stores.Transaction
	Trace       []stores.Step
	DurationMs  int
	Attempt     *ent.PurchaseAttempt
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) client() *ent.Client { return s.DB.Client }

// Provider reads a project's connection to a store; nil when there is none.
func (s *Service) Provider(ctx context.Context, projectID int, p stores.Platform) (*ent.PurchaseProvider, error) {
	prov, err := s.client().PurchaseProvider.Query().Where(purchaseprovider.ProjectID(projectID),
		purchaseprovider.PlatformEQ(purchaseprovider.Platform(p))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return prov, err
}

// Verify checks a receipt with its store and records what the store said.
func (s *Service) Verify(ctx context.Context, c Caller, req Request) (Result, error) {
	var res Result
	started := time.Now()
	attempt := &attemptRow{caller: c, req: req}
	finish := func(outcome purchaseattempt.Outcome, code stores.Code, detail string) {
		res.DurationMs = int(time.Since(started).Milliseconds())
		attempt.outcome, attempt.code, attempt.detail = outcome, code, detail
		attempt.ms, attempt.trace = res.DurationMs, res.Trace
		if res.Purchase != nil {
			id := res.Purchase.ID
			attempt.purchaseID, attempt.transactionID, attempt.sandbox = &id, res.Purchase.StoreTransactionID, res.Purchase.Sandbox
		} else if res.Transaction != nil {
			attempt.transactionID, attempt.sandbox = res.Transaction.TransactionID, res.Transaction.Sandbox
		}
	}

	prov, err := s.Provider(ctx, c.ProjectID, req.Platform)
	if err != nil {
		return res, err
	}
	adapter := s.Stores[req.Platform]
	if prov == nil || !prov.Enabled || adapter == nil {
		finish(purchaseattempt.OutcomeNotConfigured, "", "the project has not connected "+req.Platform.StoreName())
		res.Attempt, _ = attempt.save(ctx, s.client())
		return res, ErrNotConfigured
	}

	// A retry answered from the record, without asking the store again, where the receipt
	// names its transaction before the store is asked. A StoreKit 2 transaction is checked
	// locally, which costs less than finding out, so it always is.
	if c.Via != "debug" {
		if found, err := s.recorded(ctx, c, req); err != nil {
			return res, err
		} else if found != nil {
			res.Trace = []stores.Step{{Name: "record", OK: true, Detail: "already verified as " + found.StoreTransactionID}}
			return s.answerExisting(ctx, c, req, found, attempt, finish, &res)
		}
	}

	trace := stores.NewTrace()
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	tx, verr := adapter.Verify(callCtx, CredentialsOf(prov), req.Receipt, trace)
	cancel()
	res.Trace = trace.Steps

	var rej *stores.Rejection
	var down *stores.Unavailable
	switch {
	case verr == nil:
	case errors.As(verr, &rej):
		res.Rejection = rej
		finish(purchaseattempt.OutcomeRejected, rej.Code, rej.Detail)
		if c.Via == "debug" {
			return res, nil
		}
		return res, s.saveRejection(ctx, attempt, &res)
	case errors.As(verr, &down):
		finish(purchaseattempt.OutcomeUnavailable, "", down.Error())
		res.Attempt, _ = attempt.save(ctx, s.client())
		return res, verr
	case errors.Is(verr, stores.ErrCredentials), errors.Is(verr, stores.ErrNeedsKey):
		finish(purchaseattempt.OutcomeCredentials, "", verr.Error())
		res.Attempt, _ = attempt.save(ctx, s.client())
		if c.Via != "debug" {
			now := time.Now()
			_ = prov.Update().SetLastError(clip(verr.Error(), 500)).SetLastErrorAt(now).Exec(ctx)
		}
		return res, verr
	default:
		finish(purchaseattempt.OutcomeUnavailable, "", verr.Error())
		res.Attempt, _ = attempt.save(ctx, s.client())
		return res, &stores.Unavailable{Detail: verr.Error()}
	}

	res.Transaction = &tx
	if c.Via == "debug" {
		finish(purchaseattempt.OutcomeVerified, "", "checked by staff; nothing recorded")
		res.Attempt, _ = attempt.save(ctx, s.client())
		return res, nil
	}

	purchase, existing, err := s.record(ctx, c, req, tx, int(time.Since(started).Milliseconds()))
	if err != nil {
		return res, err
	}
	if existing != nil {
		return s.answerExisting(ctx, c, req, existing, attempt, finish, &res)
	}
	res.Purchase = purchase
	finish(purchaseattempt.OutcomeVerified, "", "")
	res.Attempt, err = attempt.save(ctx, s.client())
	if err != nil {
		s.log().Warn("a verification's attempt was not logged", "error", err)
	}
	if prov.LastVerifiedAt == nil || time.Since(*prov.LastVerifiedAt) > time.Minute {
		_ = prov.Update().SetLastVerifiedAt(time.Now()).Exec(ctx)
	}
	return res, nil
}

// recorded finds a purchase a receipt already names, before the store is asked.
func (s *Service) recorded(ctx context.Context, c Caller, req Request) (*ent.VerifiedPurchase, error) {
	q := s.client().VerifiedPurchase.Query().Where(verifiedpurchase.PlatformEQ(verifiedpurchase.Platform(req.Platform)))
	switch {
	case req.Platform == stores.Apple && req.Receipt.TransactionID != "" && req.Receipt.SignedTransaction == "":
		q = q.Where(verifiedpurchase.StoreTransactionID(req.Receipt.TransactionID))
	case req.Platform == stores.Google && req.Receipt.PurchaseToken != "":
		q = q.Where(verifiedpurchase.TokenDigest(stores.Digest(req.Receipt.PurchaseToken)))
	case req.Platform == stores.Steam && req.Receipt.OrderID != "":
		q = q.Where(verifiedpurchase.ProjectUID(c.ProjectUID), verifiedpurchase.OrderID(req.Receipt.OrderID))
	default:
		return nil, nil
	}
	found, err := q.First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return found, err
}

// owns reports whether a request speaks for the player (or install) a purchase belongs to.
func owns(c Caller, req Request, p *ent.VerifiedPurchase) bool {
	if p.ProjectUID != c.ProjectUID {
		return false
	}
	if req.PlayerID != "" && p.PlayerID == req.PlayerID {
		return true
	}
	return req.InstallID != "" && p.InstallID == req.InstallID &&
		(p.PlayerID == "" || req.PlayerID == "" || p.PlayerID == req.PlayerID)
}

// answerExisting answers a receipt whose purchase is already on record: the same purchase to its
// owner, and a replay to anybody else.
func (s *Service) answerExisting(ctx context.Context, c Caller, req Request, found *ent.VerifiedPurchase,
	attempt *attemptRow, finish func(purchaseattempt.Outcome, stores.Code, string), res *Result) (Result, error) {
	if !owns(c, req, found) {
		res.Rejection = &stores.Rejection{Code: stores.Replayed,
			Detail: "the transaction was already granted to another player"}
		res.Transaction = &stores.Transaction{TransactionID: found.StoreTransactionID, Sandbox: found.Sandbox,
			ProductID: found.ProductID}
		finish(purchaseattempt.OutcomeRejected, stores.Replayed, res.Rejection.Detail)
		return *res, s.saveRejection(ctx, attempt, res)
	}
	// The install bought it before the player signed in: the purchase is the player's now.
	if found.PlayerID == "" && req.PlayerID != "" {
		if updated, err := found.Update().SetPlayerID(req.PlayerID).Save(ctx); err == nil {
			found = updated
		}
	}
	res.Purchase, res.Duplicate = found, true
	finish(purchaseattempt.OutcomeDuplicate, "", "")
	res.Attempt, _ = attempt.save(ctx, s.client())
	return *res, nil
}

// saveRejection logs the attempt and, in the same transaction, the rejection for analytics.
func (s *Service) saveRejection(ctx context.Context, attempt *attemptRow, res *Result) error {
	return withTx(ctx, s.client(), func(tx *ent.Tx) error {
		saved, err := attempt.save(ctx, tx.Client())
		if err != nil {
			return err
		}
		res.Attempt = saved
		return tx.PurchaseChange.Create().SetAttemptID(saved.ID).SetKind(purchasechange.KindRejection).
			SetAt(saved.CreatedAt).Exec(ctx)
	})
}

// record writes a verified purchase and its changes, or answers the purchase already there.
func (s *Service) record(ctx context.Context, c Caller, req Request, t stores.Transaction, ms int) (*ent.VerifiedPurchase, *ent.VerifiedPurchase, error) {
	platform := verifiedpurchase.Platform(req.Platform)
	if t.Kind == "" {
		t.Kind = stores.UnknownKind
	}
	t.Quantity = max(t.Quantity, 1)
	var created *ent.VerifiedPurchase
	err := withTx(ctx, s.client(), func(tx *ent.Tx) error {
		first := false
		if !t.Sandbox {
			owner := verifiedpurchase.PlayerID(req.PlayerID)
			if req.PlayerID == "" {
				owner = verifiedpurchase.InstallID(req.InstallID)
			}
			n, err := tx.VerifiedPurchase.Query().Where(verifiedpurchase.ProjectUID(c.ProjectUID), owner,
				verifiedpurchase.Sandbox(false)).Limit(1).Count(ctx)
			if err != nil {
				return err
			}
			first = n == 0
		}
		create := tx.VerifiedPurchase.Create().
			SetProjectUID(c.ProjectUID).SetEnvironmentID(c.EnvironmentID).
			SetPlayerID(req.PlayerID).SetInstallID(req.InstallID).
			SetPlatform(platform).SetStoreTransactionID(t.TransactionID).
			SetOriginalTransactionID(t.OriginalTransactionID).SetOrderID(t.OrderID).SetTokenDigest(t.TokenDigest).
			SetProductID(t.ProductID).SetKind(verifiedpurchase.Kind(t.Kind)).SetQuantity(t.Quantity).
			SetAmountMicros(t.AmountMicros).SetCurrency(t.Currency).SetPriceFromStore(t.PriceFromStore).
			SetCountry(t.Country).SetSandbox(t.Sandbox).SetPurchasedAt(t.PurchasedAt).
			SetFirstPurchase(first).SetVerifyMs(ms).SetVerifiedVia(c.Via)
		if t.RefundedAt != nil {
			create = create.SetStatus(verifiedpurchase.StatusRefunded).SetRefundedAt(*t.RefundedAt).
				SetRefundReason(string(t.RefundReason)).SetRefundSource("verification")
		}
		p, err := create.Save(ctx)
		if err != nil {
			return err
		}
		if err := tx.PurchaseChange.Create().SetPurchaseID(p.ID).SetKind(purchasechange.KindPurchase).
			SetAt(p.PurchasedAt).Exec(ctx); err != nil {
			return err
		}
		if t.RefundedAt != nil {
			if err := tx.PurchaseChange.Create().SetPurchaseID(p.ID).SetKind(purchasechange.KindRefund).
				SetAt(*t.RefundedAt).Exec(ctx); err != nil {
				return err
			}
		}
		created = p
		return nil
	})
	if ent.IsConstraintError(err) {
		// Another request recorded it first: theirs is the purchase.
		existing, qerr := s.client().VerifiedPurchase.Query().Where(verifiedpurchase.PlatformEQ(platform),
			verifiedpurchase.StoreTransactionID(t.TransactionID)).Only(ctx)
		if qerr != nil {
			return nil, nil, fmt.Errorf("a purchase that conflicted and then could not be read: %w", qerr)
		}
		return nil, existing, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return created, nil, nil
}

// Lookup reads a purchase by its store transaction, for the server surface.
func (s *Service) Lookup(ctx context.Context, projectUID uuid.UUID, p stores.Platform, transactionID string) (*ent.VerifiedPurchase, error) {
	found, err := s.client().VerifiedPurchase.Query().Where(verifiedpurchase.ProjectUID(projectUID),
		verifiedpurchase.PlatformEQ(verifiedpurchase.Platform(p)), verifiedpurchase.StoreTransactionID(transactionID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return found, err
}

// attemptRow is an attempt as it is being decided, saved once the outcome is known.
type attemptRow struct {
	caller        Caller
	req           Request
	outcome       purchaseattempt.Outcome
	code          stores.Code
	detail        string
	ms            int
	trace         []stores.Step
	purchaseID    *int
	transactionID string
	sandbox       bool
}

// save logs the attempt. A staff member's check is not one of the project's attempts, and is
// not logged.
func (a *attemptRow) save(ctx context.Context, client *ent.Client) (*ent.PurchaseAttempt, error) {
	if a.caller.Via == "debug" {
		return nil, nil
	}
	return client.PurchaseAttempt.Create().
		SetProjectID(a.caller.ProjectID).SetEnvironmentID(a.caller.EnvironmentID).
		SetPlatform(purchaseattempt.Platform(a.req.Platform)).SetVia(a.caller.Via).
		SetPlayerID(a.req.PlayerID).SetInstallID(a.req.InstallID).SetProductID(clip(a.req.Receipt.ProductID, 255)).
		SetStoreTransactionID(clip(a.transactionID, 255)).SetSandbox(a.sandbox).
		SetOutcome(a.outcome).SetCode(string(a.code)).SetDetail(clip(a.detail, 500)).
		SetDurationMs(a.ms).SetTrace(a.trace).SetNillablePurchaseID(a.purchaseID).
		Save(ctx)
}

// withTx runs fn in a transaction, committed when it returns nil and rolled back otherwise.
func withTx(ctx context.Context, client *ent.Client, fn func(*ent.Tx) error) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
