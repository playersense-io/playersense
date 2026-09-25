package handlers

import (
	"errors"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/purchaseattempt"
	"playersense.dev/backend/ent/purchaseprovider"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/purchases"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/stores"
)

// Purchase verification as staff see it: every project's log, a day's outcomes across the
// platform with the stores that are failing, and a receipt checked against a project's store step
// by step, recording nothing.

// projectBriefs names the projects a page of rows belongs to, with their organizations.
func projectBriefs(ctx *muzak.Context, ids []int) (map[int]*schemas.ProjectBrief, error) {
	db := muzak.From[*core.DB](ctx)
	out := map[int]*schemas.ProjectBrief{}
	if len(ids) == 0 {
		return out, nil
	}
	projs, err := db.Client.Project.Query().Where(project.IDIn(ids...)).All(ctx.Context())
	if err != nil {
		return nil, err
	}
	var orgIDs []int
	for _, p := range projs {
		orgIDs = append(orgIDs, p.OrganizationID)
	}
	orgs, err := db.Client.Organization.Query().Where(organization.IDIn(orgIDs...)).All(ctx.Context())
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	for _, o := range orgs {
		names[o.ID] = o.Slug
	}
	for _, p := range projs {
		out[p.ID] = &schemas.ProjectBrief{ID: p.UID, Slug: p.Slug, Name: p.Name, Organization: names[p.OrganizationID]}
	}
	return out, nil
}

// OperatorPurchaseAttempts searches every project's verification log.
func OperatorPurchaseAttempts(ctx *muzak.Context, in schemas.OperatorPurchaseAttemptsParams) (schemas.PurchaseAttemptsOut, error) {
	db := muzak.From[*core.DB](ctx)
	q := db.Client.PurchaseAttempt.Query()
	if in.Project != uuid.Nil {
		p, err := db.Client.Project.Query().Where(project.UID(in.Project)).Only(ctx.Context())
		if err != nil {
			return schemas.PurchaseAttemptsOut{}, muzak.NotFound("no project has that id")
		}
		q = q.Where(purchaseattempt.ProjectID(p.ID))
	}
	rows, out, err := attemptsPage(ctx, q, in.PurchaseAttemptFilter, false)
	if err != nil {
		return out, err
	}
	ids := make([]int, 0, len(rows))
	for _, a := range rows {
		ids = append(ids, a.ProjectID)
	}
	briefs, err := projectBriefs(ctx, ids)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	for i, a := range rows {
		out.Attempts[i].Project = briefs[a.ProjectID]
	}
	return out, nil
}

// OperatorPurchaseAttempt is one attempt, with how it was checked.
func OperatorPurchaseAttempt(ctx *muzak.Context, in schemas.OperatorPurchaseAttemptParams) (schemas.PurchaseAttemptOut, error) {
	db := muzak.From[*core.DB](ctx)
	a, err := db.Client.PurchaseAttempt.Query().Where(purchaseattempt.UID(in.AttemptID)).Only(ctx.Context())
	if ent.IsNotFound(err) {
		return schemas.PurchaseAttemptOut{}, muzak.NotFound("no verification attempt has that id")
	}
	if err != nil {
		return schemas.PurchaseAttemptOut{}, core.AsHTTPError(err)
	}
	out := attemptOut(a, nil, true)
	briefs, err := projectBriefs(ctx, []int{a.ProjectID})
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	out.Project = briefs[a.ProjectID]
	return out, nil
}

// PurchaseVerification is the last day of verification across the platform.
func PurchaseVerification(ctx *muzak.Context, _ schemas.PurchaseVerificationParams) (schemas.PurchaseVerificationOut, error) {
	db := muzak.From[*core.DB](ctx)
	since := time.Now().Add(-24 * time.Hour).UTC()
	out := schemas.PurchaseVerificationOut{Since: since, ByStore: map[string]schemas.StoreOutcomes{},
		Rejections: map[string]int{}, Failing: []schemas.FailingStoreOut{}}

	day, err := dayOutcomes(ctx, 0)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	for _, p := range stores.Platforms {
		out.ByStore[p.StoreName()] = day[p]
	}

	var codes []struct {
		Code  string `json:"code"`
		Count int    `json:"count"`
	}
	if err := db.Client.PurchaseAttempt.Query().Where(purchaseattempt.CreatedAtGT(since),
		purchaseattempt.OutcomeEQ(purchaseattempt.OutcomeRejected)).
		GroupBy(purchaseattempt.FieldCode).Aggregate(ent.Count()).Scan(ctx.Context(), &codes); err != nil {
		return out, core.AsHTTPError(err)
	}
	for _, c := range codes {
		out.Rejections[c.Code] = c.Count
	}

	failing, err := db.Client.PurchaseProvider.Query().Where(purchaseprovider.Enabled(true),
		purchaseprovider.LastErrorAtGT(since)).Order(ent.Desc(purchaseprovider.FieldLastErrorAt)).Limit(100).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	ids := make([]int, 0, len(failing))
	for _, f := range failing {
		ids = append(ids, f.ProjectID)
	}
	briefs, err := projectBriefs(ctx, ids)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	for _, f := range failing {
		if f.LastVerifiedAt != nil && f.LastVerifiedAt.After(*f.LastErrorAt) {
			continue // it has worked since
		}
		b := briefs[f.ProjectID]
		if b == nil {
			continue
		}
		out.Failing = append(out.Failing, schemas.FailingStoreOut{Project: *b,
			Store: stores.Platform(f.Platform).StoreName(), LastError: f.LastError, LastErrorAt: *f.LastErrorAt})
	}
	return out, nil
}

// CheckPurchase checks a receipt against a project's store as staff debugging it: every step the
// check took, what the store answered, and the purchase on record for it. Nothing is recorded.
func CheckPurchase(ctx *muzak.Context, in schemas.PurchaseCheckIn) (schemas.PurchaseCheckOut, error) {
	db := muzak.From[*core.DB](ctx)
	svc := muzak.From[*purchases.Service](ctx)
	proj := muzak.From[core.StaffAccessRecord](ctx).Project
	env, err := db.Client.Environment.Query().Where(environment.ProjectID(proj.ID),
		environment.KindEQ(environment.KindProduction)).First(ctx.Context())
	if err != nil {
		return schemas.PurchaseCheckOut{}, core.AsHTTPError(err)
	}
	req := requestOf(in.VerifyPurchaseIn)
	res, verr := svc.Verify(ctx.Context(), purchases.Caller{ProjectID: proj.ID, ProjectUID: proj.UID,
		EnvironmentID: env.ID, Via: "debug"}, req)
	out := schemas.PurchaseCheckOut{Trace: traceOut(res.Trace), DurationMs: res.DurationMs}
	var down *stores.Unavailable
	switch {
	case errors.Is(verr, purchases.ErrNotConfigured):
		out.Outcome, out.Detail = "not_configured", "the project has not connected "+req.Platform.StoreName()
		return out, nil
	case errors.As(verr, &down):
		out.Outcome, out.Detail = "unavailable", down.Error()
		return out, nil
	case errors.Is(verr, stores.ErrCredentials), errors.Is(verr, stores.ErrNeedsKey):
		out.Outcome, out.Detail = "credentials", verr.Error()
		return out, nil
	case verr != nil:
		return out, core.AsHTTPError(verr)
	case res.Rejection != nil:
		out.Outcome = "rejected"
		out.Rejection = &schemas.PurchaseProblem{Code: string(res.Rejection.Code), Message: res.Rejection.Detail}
		return out, nil
	}
	out.Outcome = "verified"
	if t := res.Transaction; t != nil {
		out.Transaction = &schemas.CheckedTransaction{TransactionID: t.TransactionID,
			OriginalTransactionID: t.OriginalTransactionID, OrderID: t.OrderID, AppID: t.AppID, ProductID: t.ProductID,
			Kind: string(t.Kind), Quantity: t.Quantity, PurchasedAt: t.PurchasedAt, AmountMicros: t.AmountMicros,
			Currency: t.Currency, PriceFromStore: t.PriceFromStore, Country: t.Country, Sandbox: t.Sandbox,
			RefundedAt: t.RefundedAt, RefundReason: string(t.RefundReason)}
		if p, err := svc.Lookup(ctx.Context(), proj.UID, req.Platform, t.TransactionID); err == nil && p != nil {
			doc := purchases.Document(p, false)
			out.Recorded = &doc
		}
	}
	return out, nil
}
