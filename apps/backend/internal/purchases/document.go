package purchases

import (
	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/verifiedpurchase"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/stores"
)

// Document is a recorded purchase as the API hands it to the game and its server, and as the
// purchase webhooks carry it.
func Document(p *ent.VerifiedPurchase, duplicate bool) schemas.PurchaseDoc {
	platform := stores.Platform(p.Platform)
	status := "verified"
	switch {
	case p.Status == verifiedpurchase.StatusRefunded:
		status = "refunded"
	case p.Sandbox:
		status = "sandbox"
	}
	env := "production"
	if p.Sandbox {
		env = "sandbox"
	}
	purchased, verified := p.PurchasedAt.UTC(), p.CreatedAt.UTC()
	doc := schemas.PurchaseDoc{
		ID: p.UID, Status: status, Duplicate: duplicate,
		Player:  schemas.PurchasePlayer{ID: p.PlayerID, InstallID: p.InstallID},
		Product: schemas.PurchaseProduct{ID: p.ProductID, Kind: string(p.Kind), Quantity: p.Quantity},
		Price:   &schemas.PurchasePrice{AmountMicros: p.AmountMicros, Currency: p.Currency, FromStore: p.PriceFromStore},
		Store: schemas.PurchaseStore{Name: platform.StoreName(), TransactionID: p.StoreTransactionID,
			OriginalTransactionID: p.OriginalTransactionID, OrderID: p.OrderID, VerifiedInMs: p.VerifyMs},
		Environment: env, Country: p.Country, First: p.FirstPurchase,
		PurchasedAt: &purchased, VerifiedAt: &verified,
	}
	if p.RefundedAt != nil {
		doc.Refund = &schemas.PurchaseRefund{At: p.RefundedAt.UTC(), Reason: p.RefundReason, Source: p.RefundSource}
	}
	return doc
}

// RejectionDocument is a receipt turned away, in the same shape.
func RejectionDocument(req Request, res Result) schemas.PurchaseDoc {
	doc := schemas.PurchaseDoc{
		Status:      "rejected",
		Player:      schemas.PurchasePlayer{ID: req.PlayerID, InstallID: req.InstallID},
		Product:     schemas.PurchaseProduct{ID: req.Receipt.ProductID},
		Store:       schemas.PurchaseStore{Name: req.Platform.StoreName()},
		Environment: "production",
		Rejection:   &schemas.PurchaseProblem{Code: string(res.Rejection.Code), Message: res.Rejection.Detail},
	}
	if t := res.Transaction; t != nil {
		doc.Store.TransactionID = t.TransactionID
		if t.ProductID != "" {
			doc.Product.ID = t.ProductID
		}
		if t.Sandbox {
			doc.Environment = "sandbox"
		}
	}
	return doc
}
