package purchases

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/purchasechange"
	"playersense.dev/backend/ent/purchaseprovider"
	"playersense.dev/backend/ent/verifiedpurchase"
	"playersense.dev/backend/internal/stores"
)

// Where a refund was heard.
const (
	SourceNotification = "notification"
	SourceSweep        = "sweep"
)

// ErrUnknownProject is a notification URL naming no project, or a token that does not match.
var ErrUnknownProject = errors.New("no project listens at this notification URL")

// ApplyRefund records what a store said was given back (or, reversed, restored), on the purchase it
// names within one project. It answers whether anything changed: a refund heard twice, from a
// notification and then a sweep, changes the purchase once.
func (s *Service) ApplyRefund(ctx context.Context, projectUID uuid.UUID, r stores.Refund, source string) (bool, error) {
	q := s.client().VerifiedPurchase.Query().Where(verifiedpurchase.ProjectUID(projectUID),
		verifiedpurchase.PlatformEQ(verifiedpurchase.Platform(r.Platform)))
	switch {
	case r.TransactionID != "":
		// Google names a test purchase by its token alone; its record's id is the digest.
		if r.TokenDigest != "" {
			q = q.Where(verifiedpurchase.Or(verifiedpurchase.StoreTransactionID(r.TransactionID),
				verifiedpurchase.TokenDigest(r.TokenDigest)))
		} else {
			q = q.Where(verifiedpurchase.StoreTransactionID(r.TransactionID))
		}
	case r.TokenDigest != "":
		q = q.Where(verifiedpurchase.TokenDigest(r.TokenDigest))
	case r.OrderID != "":
		q = q.Where(verifiedpurchase.OrderID(r.OrderID))
	default:
		return false, nil
	}
	p, err := q.First(ctx)
	if ent.IsNotFound(err) {
		// Never verified here: nothing of ours to give back.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	at := r.At
	if at.IsZero() {
		at = time.Now().UTC()
	}

	changed := false
	err = withTx(ctx, s.client(), func(tx *ent.Tx) error {
		// Read again under a lock: a notification and a sweep may be applying the same refund.
		p, err := tx.VerifiedPurchase.Query().Where(verifiedpurchase.ID(p.ID)).ForUpdate().Only(ctx)
		if err != nil {
			return err
		}
		switch {
		case r.Reversed && p.Status == verifiedpurchase.StatusRefunded:
			if err := tx.VerifiedPurchase.UpdateOne(p).SetStatus(verifiedpurchase.StatusVerified).
				ClearRefundedAt().SetRefundReason("").SetRefundSource("").Exec(ctx); err != nil {
				return err
			}
			changed = true
			return tx.PurchaseChange.Create().SetPurchaseID(p.ID).SetKind(purchasechange.KindReinstate).SetAt(at).Exec(ctx)
		case !r.Reversed && p.Status == verifiedpurchase.StatusVerified:
			if err := tx.VerifiedPurchase.UpdateOne(p).SetStatus(verifiedpurchase.StatusRefunded).
				SetRefundedAt(at).SetRefundReason(string(r.Reason)).SetRefundSource(source).Exec(ctx); err != nil {
				return err
			}
			changed = true
			return tx.PurchaseChange.Create().SetPurchaseID(p.ID).SetKind(purchasechange.KindRefund).SetAt(at).Exec(ctx)
		case !r.Reversed && p.RefundReason == "" && r.Reason != "":
			// Heard first from a notification that gave no reason; the sweep knows it.
			return tx.VerifiedPurchase.UpdateOne(p).SetRefundReason(string(r.Reason)).Exec(ctx)
		}
		return nil
	})
	return changed, err
}

// providerAt finds the provider a notification URL names, and checks its token.
func (s *Service) providerAt(ctx context.Context, projectUID uuid.UUID, p stores.Platform, token string) (*ent.PurchaseProvider, *ent.Project, error) {
	proj, err := s.client().Project.Query().Where(project.UID(projectUID), project.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, ErrUnknownProject
	}
	if err != nil {
		return nil, nil, err
	}
	prov, err := s.Provider(ctx, proj.ID, p)
	if err != nil {
		return nil, nil, err
	}
	if prov == nil || subtle.ConstantTimeCompare([]byte(prov.NotifyToken), []byte(token)) != 1 {
		return nil, nil, ErrUnknownProject
	}
	return prov, proj, nil
}

// AppleNotification handles an App Store Server Notification V2. Apple signs it, so it is believed
// once the signature checks out; the token in the URL only says which project it is for.
func (s *Service) AppleNotification(ctx context.Context, projectUID uuid.UUID, token, signedPayload string) (stores.AppleNotification, error) {
	prov, _, err := s.providerAt(ctx, projectUID, stores.Apple, token)
	if err != nil {
		return stores.AppleNotification{}, err
	}
	apple, ok := s.Stores[stores.Apple].(interface {
		Notification(stores.Credentials, string) (stores.AppleNotification, error)
	})
	if !ok {
		return stores.AppleNotification{}, ErrNotConfigured
	}
	n, err := apple.Notification(CredentialsOf(prov), signedPayload)
	if err != nil {
		return n, err
	}
	_ = prov.Update().SetLastNotificationAt(time.Now()).Exec(ctx)
	if n.IsRefund() {
		if _, err := s.ApplyRefund(ctx, projectUID, n.Refund(), SourceNotification); err != nil {
			return n, err
		}
	}
	return n, nil
}

// GoogleNotification handles a Real-time Developer Notification pushed by Pub/Sub. The token in
// the push URL is its credential; a voided purchase in it is recorded as refunded, with the reason
// left for the sweep, which reads it from the voided purchases list.
func (s *Service) GoogleNotification(ctx context.Context, projectUID uuid.UUID, token string, body []byte) (stores.GoogleNotification, error) {
	prov, _, err := s.providerAt(ctx, projectUID, stores.Google, token)
	if err != nil {
		return stores.GoogleNotification{}, err
	}
	n, err := stores.DecodeNotification(body)
	if err != nil {
		return n, err
	}
	creds := CredentialsOf(prov)
	if n.PackageName != "" && n.PackageName != creds.PackageName {
		return n, fmt.Errorf("a notification for %s, and this project's app is %s", n.PackageName, creds.PackageName)
	}
	_ = prov.Update().SetLastNotificationAt(time.Now()).Exec(ctx)
	if n.Voided != nil {
		if _, err := s.ApplyRefund(ctx, projectUID, *n.Voided, SourceNotification); err != nil {
			return n, err
		}
	}
	return n, nil
}

// SweepEvery is how often each provider's refunds are swept.
const SweepEvery = 6 * time.Hour

// SweepResult is what one sweep pass did.
type SweepResult struct {
	Providers, Refunds, Failed int
}

// Sweep reads each connected store's refunds since it last looked: what a notification missed,
// and the reasons notifications leave out. A few providers at a time; each is due every six hours.
func (s *Service) Sweep(ctx context.Context, now time.Time) (SweepResult, error) {
	var out SweepResult
	due, err := s.client().PurchaseProvider.Query().Where(purchaseprovider.Enabled(true),
		purchaseprovider.Or(purchaseprovider.SweptAtIsNil(), purchaseprovider.SweptAtLT(now.Add(-SweepEvery)))).
		Limit(100).All(ctx)
	if err != nil {
		return out, err
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, prov := range due {
		adapter := s.Stores[stores.Platform(prov.Platform)]
		if adapter == nil {
			continue
		}
		proj, err := s.client().Project.Get(ctx, prov.ProjectID)
		if err != nil || proj.DeletedAt != nil {
			continue
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(prov *ent.PurchaseProvider, projectUID uuid.UUID) {
			defer wg.Done()
			defer func() { <-slots }()
			since := prov.VerifiedSince
			if prov.SweepCursor != nil {
				since = *prov.SweepCursor
			}
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			refunds, next, err := adapter.Refunds(callCtx, CredentialsOf(prov), since)
			cancel()
			applied := 0
			for _, r := range refunds {
				if changed, aerr := s.ApplyRefund(ctx, projectUID, r, SourceSweep); aerr == nil && changed {
					applied++
				}
			}
			update := prov.Update().SetSweptAt(now)
			if err != nil {
				update = update.SetLastError(clip("the refund sweep: "+err.Error(), 500)).SetLastErrorAt(now)
			} else if next.After(since) {
				update = update.SetSweepCursor(next)
			}
			_ = update.Exec(ctx)
			mu.Lock()
			out.Providers++
			out.Refunds += applied
			if err != nil {
				out.Failed++
			}
			mu.Unlock()
		}(prov, proj.UID)
	}
	wg.Wait()
	return out, nil
}
