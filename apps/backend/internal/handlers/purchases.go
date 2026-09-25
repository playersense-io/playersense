package handlers

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/purchaseattempt"
	"playersense.dev/backend/ent/purchaseprovider"
	"playersense.dev/backend/ent/verifiedpurchase"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/purchases"
	"playersense.dev/backend/internal/ratelimit"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/stores"
)

// Purchase verification (project.md §46-48): the game and its server send receipts, the stores
// send notifications, the dashboard connects the stores and reads the log, and staff debug a
// receipt that would not verify.

// ---- documents -------------------------------------------------------------------------------

func traceOut(steps []stores.Step) []schemas.TraceStep {
	out := make([]schemas.TraceStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, schemas.TraceStep{Name: s.Name, OK: s.OK, Detail: s.Detail, Ms: s.Ms})
	}
	return out
}

func requestOf(in schemas.VerifyPurchaseIn) purchases.Request {
	platform, _ := stores.PlatformOfStore(in.Store)
	return purchases.Request{Platform: platform, PlayerID: in.PlayerID, InstallID: in.InstallID,
		Receipt: stores.Receipt{SignedTransaction: in.SignedTransaction, TransactionID: in.TransactionID,
			PurchaseToken: in.PurchaseToken, OrderID: in.OrderID, ProductID: in.ProductID,
			PriceMicros: in.PriceMicros, Currency: in.Currency}}
}

// verifyFailed turns what stopped a verification into an answer the caller can act on.
func verifyFailed(err error) error {
	var down *stores.Unavailable
	switch {
	case errors.Is(err, purchases.ErrNotConfigured):
		return muzak.Conflict("this project has not connected that store: connect it under Settings, Stores").
			WithCode("store_not_connected")
	case errors.As(err, &down):
		return muzak.ServiceUnavailable("the store could not answer; keep the receipt and retry").
			WithCode("store_unavailable")
	case errors.Is(err, stores.ErrNeedsKey):
		return muzak.UnprocessableEntity(err.Error()).WithCode("store_key_needed")
	case errors.Is(err, stores.ErrCredentials):
		return muzak.ServiceUnavailable("the store refused this project's credentials; the studio must fix them under " +
			"Settings, Stores; keep the receipt and retry").WithCode("store_credentials")
	}
	return core.AsHTTPError(err)
}

// ---- the game and its server -----------------------------------------------------------------

// VerifyPurchase checks a receipt with its store and records it. The answer's status says whether
// to grant: verified or sandbox. A retry answers the same purchase with duplicate set.
func VerifyPurchase(ctx *muzak.Context, in schemas.VerifyPurchaseIn) (schemas.PurchaseDoc, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	svc := muzak.From[*purchases.Service](ctx)
	who := in.PlayerID
	if who == "" {
		who = "install:" + in.InstallID
	}
	if limiter, found := muzak.TryFrom[*ratelimit.Limiter](ctx); found {
		if err := limiter.Allow(ctx, core.VerifyBudgets(tenant, who)...); err != nil {
			return schemas.PurchaseDoc{}, err
		}
	}
	projectUID, err := uuid.Parse(tenant.ProjectID)
	if err != nil {
		return schemas.PurchaseDoc{}, core.AsHTTPError(err)
	}
	req := requestOf(in)
	res, err := svc.Verify(ctx.Context(), purchases.Caller{ProjectID: int(tenant.ProjectSeq), ProjectUID: projectUID,
		EnvironmentID: int(tenant.EnvironmentSeq), Environment: tenant.Environment, Via: tenant.KeyKind}, req)
	if err != nil {
		if errors.As(err, new(*stores.Unavailable)) {
			ctx.SetHeader(muzak.HeaderRetryAfter, "30")
		}
		return schemas.PurchaseDoc{}, verifyFailed(err)
	}
	if res.Rejection != nil {
		return purchases.RejectionDocument(req, res), nil
	}
	return purchases.Document(res.Purchase, res.Duplicate), nil
}

// LookupPurchase reads a purchase by the store's transaction id: what a game server checks before
// granting, and again later to see whether it was refunded.
func LookupPurchase(ctx *muzak.Context, in schemas.PurchaseLookupParams) (schemas.PurchaseDoc, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	svc := muzak.From[*purchases.Service](ctx)
	projectUID, _ := uuid.Parse(tenant.ProjectID)
	platform, _ := stores.PlatformOfStore(in.Store)
	p, err := svc.Lookup(ctx.Context(), projectUID, platform, in.TransactionID)
	if err != nil {
		return schemas.PurchaseDoc{}, core.AsHTTPError(err)
	}
	if p == nil {
		return schemas.PurchaseDoc{}, muzak.NotFound("no verified purchase has that transaction id in this project")
	}
	return purchases.Document(p, false), nil
}

// PlayerPurchases lists one player's verified purchases, newest first.
func PlayerPurchases(ctx *muzak.Context, in schemas.PlayerPurchasesParams) (schemas.PurchaseDocsOut, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	db := muzak.From[*core.DB](ctx)
	projectUID, _ := uuid.Parse(tenant.ProjectID)
	rows, err := db.Client.VerifiedPurchase.Query().
		Where(verifiedpurchase.ProjectUID(projectUID), verifiedpurchase.PlayerID(in.PlayerID)).
		Order(ent.Desc(verifiedpurchase.FieldPurchasedAt)).Limit(in.Limit).All(ctx.Context())
	if err != nil {
		return schemas.PurchaseDocsOut{}, core.AsHTTPError(err)
	}
	out := schemas.PurchaseDocsOut{Purchases: make([]schemas.PurchaseDoc, 0, len(rows))}
	for _, p := range rows {
		out.Purchases = append(out.Purchases, purchases.Document(p, false))
	}
	return out, nil
}

// ---- store notifications ---------------------------------------------------------------------

func notificationFailed(err error) error {
	var rej *stores.Rejection
	switch {
	case errors.Is(err, purchases.ErrUnknownProject):
		return muzak.NotFound("no project listens at this notification URL")
	case errors.As(err, &rej):
		return muzak.BadRequest("the notification does not check out: " + rej.Detail).WithCode(string(rej.Code))
	case errors.Is(err, purchases.ErrNotConfigured):
		return muzak.NotFound("no project listens at this notification URL")
	}
	return core.AsHTTPError(err)
}

// AppleStoreNotification takes an App Store Server Notification V2. A refund, or a refund reversed,
// changes the purchase it names; everything else is acknowledged.
func AppleStoreNotification(ctx *muzak.Context, in schemas.AppleNotificationIn) (schemas.StoreNotificationOut, error) {
	svc := muzak.From[*purchases.Service](ctx)
	n, err := svc.AppleNotification(ctx.Context(), in.ProjectID, in.Token, in.SignedPayload)
	if err != nil {
		return schemas.StoreNotificationOut{}, notificationFailed(err)
	}
	out := schemas.StoreNotificationOut{Type: n.Type, Refund: n.IsRefund()}
	if !out.Refund {
		out.Ignored = "only refunds change a purchase"
	}
	return out, nil
}

// GoogleStoreNotification takes a Real-time Developer Notification from a Pub/Sub push
// subscription. A voided purchase is recorded as refunded.
func GoogleStoreNotification(ctx *muzak.Context, in schemas.GoogleNotificationIn) (schemas.StoreNotificationOut, error) {
	svc := muzak.From[*purchases.Service](ctx)
	body, _ := json.Marshal(map[string]any{"message": in.Message, "subscription": in.Subscription})
	n, err := svc.GoogleNotification(ctx.Context(), in.ProjectID, in.Token, body)
	if err != nil {
		if errors.Is(err, purchases.ErrUnknownProject) {
			return schemas.StoreNotificationOut{}, notificationFailed(err)
		}
		// Anything else is acknowledged: Pub/Sub redelivers a failure for a week, and a message
		// that cannot be read will not read better on the thousandth try. The sweep catches up.
		return schemas.StoreNotificationOut{Ignored: err.Error()}, nil
	}
	out := schemas.StoreNotificationOut{Refund: n.Voided != nil}
	switch {
	case n.Test:
		out.Type, out.Ignored = "test", "a test notification"
	case n.Voided != nil:
		out.Type = "voided_purchase"
	default:
		out.Type, out.Ignored = "other", "only voided purchases change a purchase"
	}
	return out, nil
}

// ---- the dashboard: stores -------------------------------------------------------------------

// notificationURL is where a store posts for a provider; Steam has none.
func notificationURL(ctx *muzak.Context, proj *ent.Project, p *ent.PurchaseProvider) string {
	if p.Platform == purchaseprovider.PlatformSteam {
		return ""
	}
	svc := muzak.From[*purchases.Service](ctx)
	return strings.TrimRight(svc.PublicURL, "/") + "/v1/store-notifications/" + string(p.Platform) + "/" +
		proj.UID.String() + "/" + p.NotifyToken
}

func storeOut(ctx *muzak.Context, proj *ent.Project, platform stores.Platform, p *ent.PurchaseProvider, day schemas.StoreOutcomes) schemas.StoreOut {
	out := schemas.StoreOut{Store: platform.StoreName(), Status: "not_connected", Day: day}
	if p == nil {
		return out
	}
	c := purchases.CredentialsOf(p)
	since, updated := p.VerifiedSince.UTC(), p.UpdatedAt.UTC()
	out.Connected, out.Enabled = true, p.Enabled
	out.BundleID, out.AppAppleID, out.IssuerID, out.KeyID = c.BundleID, c.AppAppleID, c.IssuerID, c.KeyID
	out.PackageName, out.AppID = c.PackageName, c.AppID
	out.NotificationURL = notificationURL(ctx, proj, p)
	out.VerifiedSince, out.UpdatedAt = &since, &updated
	out.LastVerifiedAt, out.LastNotificationAt, out.SweptAt = p.LastVerifiedAt, p.LastNotificationAt, p.SweptAt
	out.LastError, out.LastErrorAt = p.LastError, p.LastErrorAt
	out.CreatedBy, out.UpdatedBy = p.CreatedBy, p.UpdatedBy
	switch platform {
	case stores.Apple:
		out.HasKey = c.PrivateKey != ""
		if !out.HasKey {
			out.Missing = []string{"private_key"}
		}
	case stores.Google:
		out.HasKey = c.ServiceAccount != ""
		var key struct {
			ClientEmail string `json:"client_email"`
		}
		_ = json.Unmarshal([]byte(c.ServiceAccount), &key)
		out.ServiceAccountEmail = key.ClientEmail
	case stores.Steam:
		out.HasKey = c.PublisherKey != ""
	}
	if p.LastTestAt != nil {
		out.LastTest = &schemas.StoreTestOut{At: *p.LastTestAt, OK: p.LastTestOk != nil && *p.LastTestOk, Detail: p.LastTestDetail}
	}
	recentError := p.LastErrorAt != nil && time.Since(*p.LastErrorAt) < 24*time.Hour &&
		(p.LastVerifiedAt == nil || p.LastErrorAt.After(*p.LastVerifiedAt))
	switch {
	case !p.Enabled:
		out.Status = "off"
	case recentError || (out.LastTest != nil && !out.LastTest.OK):
		out.Status = "failing"
	case out.LastTest == nil && p.LastVerifiedAt == nil:
		out.Status = "untested"
	default:
		out.Status = "working"
	}
	return out
}

// dayOutcomes counts each store's verifications over the last day: one grouped read on the
// attempts index.
func dayOutcomes(ctx *muzak.Context, projectID int) (map[stores.Platform]schemas.StoreOutcomes, error) {
	db := muzak.From[*core.DB](ctx)
	var rows []struct {
		Platform string `json:"platform"`
		Outcome  string `json:"outcome"`
		Count    int    `json:"count"`
	}
	q := db.Client.PurchaseAttempt.Query().Where(purchaseattempt.CreatedAtGT(time.Now().Add(-24 * time.Hour)))
	if projectID != 0 {
		q = q.Where(purchaseattempt.ProjectID(projectID))
	}
	if err := q.GroupBy(purchaseattempt.FieldPlatform, purchaseattempt.FieldOutcome).
		Aggregate(ent.Count()).Scan(ctx.Context(), &rows); err != nil {
		return nil, err
	}
	out := map[stores.Platform]schemas.StoreOutcomes{}
	for _, r := range rows {
		o := out[stores.Platform(r.Platform)]
		addOutcome(&o, r.Outcome, r.Count)
		out[stores.Platform(r.Platform)] = o
	}
	return out, nil
}

func addOutcome(o *schemas.StoreOutcomes, outcome string, n int) {
	switch outcome {
	case "verified":
		o.Verified += n
	case "duplicate":
		o.Duplicate += n
	case "rejected":
		o.Rejected += n
	case "unavailable":
		o.Unavailable += n
	case "credentials":
		o.Credentials += n
	case "not_configured":
		o.NotConnected += n
	}
}

// ListStores is the project's three stores, connected or not.
func ListStores(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.StoreListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	rows, err := db.Client.PurchaseProvider.Query().Where(purchaseprovider.ProjectID(proj.ID)).All(ctx.Context())
	if err != nil {
		return schemas.StoreListOut{}, core.AsHTTPError(err)
	}
	day, err := dayOutcomes(ctx, proj.ID)
	if err != nil {
		return schemas.StoreListOut{}, core.AsHTTPError(err)
	}
	byPlatform := map[stores.Platform]*ent.PurchaseProvider{}
	for _, r := range rows {
		byPlatform[stores.Platform(r.Platform)] = r
	}
	out := schemas.StoreListOut{Stores: make([]schemas.StoreOut, 0, len(stores.Platforms))}
	for _, p := range stores.Platforms {
		out.Stores = append(out.Stores, storeOut(ctx, proj, p, byPlatform[p], day[p]))
	}
	return out, nil
}

func storeOf(ctx *muzak.Context, store string) (stores.Platform, *ent.PurchaseProvider, error) {
	platform, _ := stores.PlatformOfStore(store)
	proj := muzak.From[*core.Principal](ctx).Project
	p, err := muzak.From[*purchases.Service](ctx).Provider(ctx.Context(), proj.ID, platform)
	if err != nil {
		return platform, nil, core.AsHTTPError(err)
	}
	return platform, p, nil
}

// storeWritten is a provider's config and secrets from what was sent, over what it had.
func storeWritten(platform stores.Platform, in schemas.StoreIn, had *ent.PurchaseProvider) (map[string]any, map[string]string, error) {
	config := map[string]any{}
	secret := map[string]string{}
	if had != nil {
		for k, v := range had.Config {
			config[k] = v
		}
		for k, v := range had.Secret {
			secret[k] = v
		}
	}
	set := func(k, v string) {
		if v != "" {
			config[k] = v
		}
	}
	keep := func(k, v string) {
		if v != "" {
			secret[k] = v
		}
	}
	switch platform {
	case stores.Apple:
		set(purchases.KeyBundleID, in.BundleID)
		set(purchases.KeyIssuerID, in.IssuerID)
		set(purchases.KeyKeyID, in.KeyID)
		if in.AppAppleID > 0 {
			config[purchases.KeyAppAppleID] = strconv.FormatInt(in.AppAppleID, 10)
		}
		keep(purchases.SecretPrivateKey, in.PrivateKey)
		if config[purchases.KeyBundleID] == nil {
			return nil, nil, muzak.UnprocessableEntity("the App Store needs the app's bundle_id").WithCode("invalid_store")
		}
		if secret[purchases.SecretPrivateKey] != "" && (config[purchases.KeyIssuerID] == nil || config[purchases.KeyKeyID] == nil) {
			return nil, nil, muzak.UnprocessableEntity("an In-App Purchase key needs its issuer_id and key_id").WithCode("invalid_store")
		}
	case stores.Google:
		set(purchases.KeyPackageName, in.PackageName)
		if in.ServiceAccount != "" {
			var key struct {
				Type        string `json:"type"`
				ClientEmail string `json:"client_email"`
				PrivateKey  string `json:"private_key"`
			}
			if err := json.Unmarshal([]byte(in.ServiceAccount), &key); err != nil || key.Type != "service_account" ||
				key.ClientEmail == "" || key.PrivateKey == "" {
				return nil, nil, muzak.UnprocessableEntity("service_account is not a Google service account's JSON key").
					WithCode("invalid_store")
			}
		}
		keep(purchases.SecretServiceAccount, in.ServiceAccount)
		if config[purchases.KeyPackageName] == nil || secret[purchases.SecretServiceAccount] == "" {
			return nil, nil, muzak.UnprocessableEntity("Google Play needs the package_name and a service_account key").
				WithCode("invalid_store")
		}
	case stores.Steam:
		set(purchases.KeyAppID, in.AppID)
		keep(purchases.SecretPublisherKey, in.PublisherKey)
		if config[purchases.KeyAppID] == nil || secret[purchases.SecretPublisherKey] == "" {
			return nil, nil, muzak.UnprocessableEntity("Steam needs the app_id and a publisher_key").WithCode("invalid_store")
		}
	}
	return config, secret, nil
}

// ConnectStore connects a store, or changes how it is reached. From the moment it is connected,
// the store's money is its verified purchases.
func ConnectStore(ctx *muzak.Context, in schemas.StoreIn) (schemas.StoreOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	platform, had, err := storeOf(ctx, in.Store)
	if err != nil {
		return schemas.StoreOut{}, err
	}
	config, secret, err := storeWritten(platform, in, had)
	if err != nil {
		return schemas.StoreOut{}, err
	}
	who := actorOf(ctx).Label
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.PurchaseProvider, error) {
		var row *ent.PurchaseProvider
		var err error
		if had == nil {
			token, terr := purchases.NotifyToken()
			if terr != nil {
				return nil, core.AsHTTPError(terr)
			}
			create := tx.PurchaseProvider.Create().SetProjectID(proj.ID).SetPlatform(purchaseprovider.Platform(platform)).
				SetConfig(config).SetSecret(secret).SetNotifyToken(token).SetVerifiedSince(time.Now().UTC()).
				SetCreatedBy(who).SetUpdatedBy(who)
			if in.Enabled != nil {
				create = create.SetEnabled(*in.Enabled)
			}
			row, err = create.Save(ctx.Context())
		} else {
			update := tx.PurchaseProvider.UpdateOne(had).SetConfig(config).SetSecret(secret).SetUpdatedBy(who).
				ClearLastTestAt().ClearLastTestOk().SetLastTestDetail("").SetLastError("").ClearLastErrorAt()
			if in.Enabled != nil {
				update = update.SetEnabled(*in.Enabled)
			}
			row, err = update.Save(ctx.Context())
		}
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		changed := []string{}
		for _, k := range []string{"private_key", "service_account", "publisher_key"} {
			if (k == "private_key" && in.PrivateKey != "") || (k == "service_account" && in.ServiceAccount != "") ||
				(k == "publisher_key" && in.PublisherKey != "") {
				changed = append(changed, k)
			}
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.StoreConnected, "store", platform.StoreName(),
			map[string]any{"project": proj.Slug, "store": platform.StoreName(), "created": had == nil,
				"enabled": row.Enabled, "secrets_changed": changed})
	})
	if err != nil {
		return schemas.StoreOut{}, err
	}
	if had == nil {
		ctx.SetStatus(201)
	}
	day, _ := dayOutcomes(ctx, proj.ID)
	return storeOut(ctx, proj, platform, row, day[platform]), nil
}

// DisconnectStore removes a store's credentials. Its purchases stay; its money is what the game
// reports again.
func DisconnectStore(ctx *muzak.Context, in schemas.StoreParams) (muzak.Empty, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	platform, had, err := storeOf(ctx, in.Store)
	if err != nil {
		return muzak.Empty{}, err
	}
	if had == nil {
		return muzak.Empty{}, muzak.NotFound("this project has not connected that store")
	}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		if err := tx.PurchaseProvider.DeleteOne(had).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.StoreDisconnected, "store", platform.StoreName(),
			map[string]any{"project": proj.Slug, "store": platform.StoreName()})
	})
	if err != nil {
		return muzak.Empty{}, err
	}
	ctx.SetStatus(204)
	return muzak.Empty{}, nil
}

// TestStore proves a store's credentials without a purchase, kept as its last test.
func TestStore(ctx *muzak.Context, in schemas.StoreParams) (schemas.StoreTestOut, error) {
	svc := muzak.From[*purchases.Service](ctx)
	platform, had, err := storeOf(ctx, in.Store)
	if err != nil {
		return schemas.StoreTestOut{}, err
	}
	if had == nil {
		return schemas.StoreTestOut{}, muzak.NotFound("this project has not connected that store")
	}
	adapter := svc.Stores[platform]
	if adapter == nil {
		return schemas.StoreTestOut{}, muzak.ServiceUnavailable("this deployment cannot reach " + platform.StoreName())
	}
	detail, terr := adapter.Test(ctx.Context(), purchases.CredentialsOf(had))
	out := schemas.StoreTestOut{At: nowUTC(), OK: terr == nil, Detail: detail}
	if terr != nil {
		out.Detail = terr.Error()
	}
	_ = had.Update().SetLastTestAt(out.At).SetLastTestOk(out.OK).SetLastTestDetail(clipText(out.Detail, 500)).Exec(ctx.Context())
	return out, nil
}

// RotateStoreURL gives a store a new notification URL; the old one stops at once.
func RotateStoreURL(ctx *muzak.Context, in schemas.StoreParams) (schemas.StoreOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	platform, had, err := storeOf(ctx, in.Store)
	if err != nil {
		return schemas.StoreOut{}, err
	}
	if had == nil {
		return schemas.StoreOut{}, muzak.NotFound("this project has not connected that store")
	}
	token, err := purchases.NotifyToken()
	if err != nil {
		return schemas.StoreOut{}, core.AsHTTPError(err)
	}
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.PurchaseProvider, error) {
		row, err := tx.PurchaseProvider.UpdateOne(had).SetNotifyToken(token).SetUpdatedBy(actorOf(ctx).Label).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.StoreURLRotated, "store", platform.StoreName(),
			map[string]any{"project": proj.Slug, "store": platform.StoreName()})
	})
	if err != nil {
		return schemas.StoreOut{}, err
	}
	day, _ := dayOutcomes(ctx, proj.ID)
	return storeOut(ctx, proj, platform, row, day[platform]), nil
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// ---- the verification log --------------------------------------------------------------------

func attemptOut(a *ent.PurchaseAttempt, purchaseUIDs map[int]uuid.UUID, withTrace bool) schemas.PurchaseAttemptOut {
	out := schemas.PurchaseAttemptOut{
		ID: a.UID, At: a.CreatedAt.UTC(), Store: stores.Platform(a.Platform).StoreName(), Via: a.Via,
		PlayerID: a.PlayerID, InstallID: a.InstallID, ProductID: a.ProductID, StoreTransactionID: a.StoreTransactionID,
		Outcome: string(a.Outcome), Code: a.Code, Detail: a.Detail, DurationMs: a.DurationMs, Sandbox: a.Sandbox,
	}
	if a.PurchaseID != nil {
		if uid, ok := purchaseUIDs[*a.PurchaseID]; ok {
			out.PurchaseID = &uid
		}
	}
	if withTrace {
		out.Trace = traceOut(a.Trace)
	}
	return out
}

// attemptsPage reads one page of attempts matching a filter.
func attemptsPage(ctx *muzak.Context, q *ent.PurchaseAttemptQuery, f schemas.PurchaseAttemptFilter, withTrace bool) ([]*ent.PurchaseAttempt, schemas.PurchaseAttemptsOut, error) {
	db := muzak.From[*core.DB](ctx)
	if f.Store != "" {
		platform, _ := stores.PlatformOfStore(f.Store)
		q = q.Where(purchaseattempt.PlatformEQ(purchaseattempt.Platform(platform)))
	}
	if f.Outcome != "" {
		q = q.Where(purchaseattempt.OutcomeEQ(purchaseattempt.Outcome(f.Outcome)))
	}
	if f.Code != "" {
		q = q.Where(purchaseattempt.Code(f.Code))
	}
	if f.Player != "" {
		q = q.Where(purchaseattempt.Or(purchaseattempt.PlayerID(f.Player), purchaseattempt.InstallID(f.Player)))
	}
	if f.Before != uuid.Nil {
		cursor, err := db.Client.PurchaseAttempt.Query().Where(purchaseattempt.UID(f.Before)).Only(ctx.Context())
		if err != nil {
			return nil, schemas.PurchaseAttemptsOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(purchaseattempt.IDLT(cursor.ID))
	}
	rows, err := q.Order(ent.Desc(purchaseattempt.FieldID)).Limit(f.Limit + 1).All(ctx.Context())
	if err != nil {
		return nil, schemas.PurchaseAttemptsOut{}, core.AsHTTPError(err)
	}
	out := schemas.PurchaseAttemptsOut{Attempts: []schemas.PurchaseAttemptOut{}}
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
		next := rows[len(rows)-1].UID
		out.Next = &next
	}
	var ids []int
	for _, a := range rows {
		if a.PurchaseID != nil {
			ids = append(ids, *a.PurchaseID)
		}
	}
	uids := map[int]uuid.UUID{}
	if len(ids) > 0 {
		found, err := db.Client.VerifiedPurchase.Query().Where(verifiedpurchase.IDIn(ids...)).
			Select(verifiedpurchase.FieldID, verifiedpurchase.FieldUID).All(ctx.Context())
		if err != nil {
			return nil, out, core.AsHTTPError(err)
		}
		for _, p := range found {
			uids[p.ID] = p.UID
		}
	}
	for _, a := range rows {
		out.Attempts = append(out.Attempts, attemptOut(a, uids, withTrace))
	}
	return rows, out, nil
}

// ListPurchaseAttempts is the project's verification log, newest first: every receipt sent, what
// the store said, and step by step how it was checked. Kept thirty days.
func ListPurchaseAttempts(ctx *muzak.Context, in schemas.PurchaseAttemptsParams) (schemas.PurchaseAttemptsOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	_, out, err := attemptsPage(ctx, db.Client.PurchaseAttempt.Query().Where(purchaseattempt.ProjectID(proj.ID)),
		in.PurchaseAttemptFilter, true)
	return out, err
}

// ReadVerifiedPurchase is one verified purchase and every verification that named it.
func ReadVerifiedPurchase(ctx *muzak.Context, in schemas.VerifiedPurchaseParams) (schemas.VerifiedPurchaseOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	p, err := db.Client.VerifiedPurchase.Query().Where(verifiedpurchase.UID(in.PurchaseID),
		verifiedpurchase.ProjectUID(proj.UID)).Only(ctx.Context())
	if ent.IsNotFound(err) {
		return schemas.VerifiedPurchaseOut{}, muzak.NotFound("this project has no such verified purchase")
	}
	if err != nil {
		return schemas.VerifiedPurchaseOut{}, core.AsHTTPError(err)
	}
	attempts, err := db.Client.PurchaseAttempt.Query().Where(purchaseattempt.ProjectID(proj.ID),
		purchaseattempt.PurchaseID(p.ID)).Order(ent.Desc(purchaseattempt.FieldID)).Limit(50).All(ctx.Context())
	if err != nil {
		return schemas.VerifiedPurchaseOut{}, core.AsHTTPError(err)
	}
	out := schemas.VerifiedPurchaseOut{Purchase: purchases.Document(p, false), Attempts: []schemas.PurchaseAttemptOut{}}
	for _, a := range attempts {
		out.Attempts = append(out.Attempts, attemptOut(a, map[int]uuid.UUID{p.ID: p.UID}, true))
	}
	return out, nil
}
