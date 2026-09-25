package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/purchaseattempt"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/purchases"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/staff"
	"playersense.dev/backend/internal/stores"
	"playersense.dev/backend/internal/stores/storetest"
)

// purchaseRig is a stack whose Apple checks run against a test CA, and whose Google and Steam are
// fakes, with a project that has a project key and a server key.
type purchaseRig struct {
	*stack
	svc                *purchases.Service
	ca                 *storetest.AppleCA
	google, steam      *stores.Fake
	owner              *testclient.Client
	org, proj          string
	projectUID         uuid.UUID
	projectID, envID   int
	sdk, server        *testclient.Client
	storesBase, bundle string
}

func newPurchaseRig(t *testing.T) *purchaseRig {
	t.Helper()
	ca := storetest.NewAppleCA(t, true)
	google, steam := stores.NewFake(), stores.NewFake()
	svc := &purchases.Service{Stores: stores.Set{stores.Apple: &stores.AppleStore{Roots: ca.Roots},
		stores.Google: google, stores.Steam: steam}, PublicURL: "https://api.playersense.test"}
	s := newStackWith(t, muzak.WithSingleton(svc))
	svc.DB = s.db

	person := person(t)
	org := s.createOrg(t, person)
	proj := s.createProject(t, person, org)
	owner := s.as(t, person)
	p, err := s.db.Client.Project.Query().Where(project.Slug(proj)).Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.db.Client.Environment.Query().Where(environment.ProjectID(p.ID),
		environment.KindEQ(environment.KindProduction)).Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	keys := "/v1/organizations/" + org + "/projects/" + proj + "/keys"
	mint := func(kind string) *testclient.Client {
		k := testclient.Decoded[keyCreatedOut](owner.Post(keys, testclient.JSON(map[string]string{
			"kind": kind, "environment": "production", "name": kind})).AssertStatus(http.StatusCreated))
		return testclient.New(t, s.app, testclient.WithHeader("Authorization", "Bearer "+k.Secret))
	}
	return &purchaseRig{stack: s, svc: svc, ca: ca, google: google, steam: steam, owner: owner, org: org, proj: proj,
		projectUID: p.UID, projectID: p.ID, envID: env.ID, sdk: mint("project"), server: mint("server"),
		storesBase: "/v1/organizations/" + org + "/projects/" + proj + "/stores",
		bundle:     "com.example." + strings.ReplaceAll(proj, "-", "")}
}

func (r *purchaseRig) appleTx(id string, at time.Time) map[string]any {
	return storetest.Transaction(r.bundle, id, "gems_500", at)
}

func (r *purchaseRig) verify(body map[string]any) *testclient.Response {
	return r.sdk.Post("/v1/purchases/verify", testclient.JSON(body))
}

func TestPurchasesAreVerifiedRecordedOnceAndRefunded(t *testing.T) {
	r := newPurchaseRig(t)
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	// Apple's transaction ids are unique across the store, and so across this table: one run's.
	run := strconv.FormatInt(time.Now().UnixNano(), 10)
	tx1, tx2, tx3 := run+"1", run+"2", run+"3"

	// ---- connecting the stores ----
	list := testclient.Decoded[schemas.StoreListOut](r.owner.Get(r.storesBase).AssertStatus(http.StatusOK))
	if len(list.Stores) != 3 || list.Stores[0].Status != "not_connected" {
		t.Fatalf("stores before any is connected: %+v", list.Stores)
	}
	r.verify(map[string]any{"store": "steam", "player_id": "p1", "order_id": "1"}).AssertStatus(http.StatusConflict)

	r.owner.Put(r.storesBase+"/app_store", testclient.JSON(map[string]any{})).AssertStatus(http.StatusUnprocessableEntity)
	apple := testclient.Decoded[schemas.StoreOut](r.owner.Put(r.storesBase+"/app_store", testclient.JSON(map[string]any{
		"bundle_id": r.bundle})).AssertStatus(http.StatusCreated))
	if apple.Status != "untested" || apple.HasKey || !strings.HasPrefix(apple.NotificationURL,
		"https://api.playersense.test/v1/store-notifications/apple/"+r.projectUID.String()+"/") {
		t.Fatalf("the App Store once connected: %+v", apple)
	}
	r.owner.Put(r.storesBase+"/google_play", testclient.JSON(map[string]any{"package_name": "com.example.game",
		"service_account": `{"type":"nope"}`})).AssertStatus(http.StatusUnprocessableEntity)
	play := testclient.Decoded[schemas.StoreOut](r.owner.Put(r.storesBase+"/google_play", testclient.JSON(map[string]any{
		"package_name": "com.example.game", "service_account": `{"type":"service_account","client_email":"v@p.iam.gserviceaccount.com","private_key":"k"}`})).
		AssertStatus(http.StatusCreated))
	if play.ServiceAccountEmail != "v@p.iam.gserviceaccount.com" || !play.HasKey {
		t.Fatalf("Google Play once connected: %+v", play)
	}
	if body := r.owner.Get(r.storesBase).String(); strings.Contains(body, `service_account\"`) || strings.Contains(body, `private_key\":\"k`) {
		t.Fatalf("the stores list disclosed a secret: %s", body)
	}
	testclient.Decoded[schemas.StoreOut](r.owner.Put(r.storesBase+"/steam", testclient.JSON(map[string]any{
		"app_id": "480", "publisher_key": "pub"})).AssertStatus(http.StatusCreated))
	tested := testclient.Decoded[schemas.StoreTestOut](r.owner.Post(r.storesBase+"/steam/test", testclient.JSON(map[string]any{})).AssertStatus(http.StatusOK))
	if !tested.OK {
		t.Fatalf("the steam test: %+v", tested)
	}

	// ---- the App Store, from the game with its project key ----
	first := testclient.Decoded[schemas.PurchaseDoc](r.verify(map[string]any{"store": "app_store", "player_id": "p1",
		"signed_transaction": r.ca.Sign(t, r.appleTx(tx1, now))}).AssertStatus(http.StatusOK))
	if first.Status != "verified" || first.Duplicate || !first.First || first.Price == nil ||
		first.Price.AmountMicros != 4_990_000 || first.Country != "US" || first.Store.TransactionID != tx1 {
		t.Fatalf("a first purchase: %+v", first)
	}
	again := testclient.Decoded[schemas.PurchaseDoc](r.verify(map[string]any{"store": "app_store", "player_id": "p1",
		"signed_transaction": r.ca.Sign(t, r.appleTx(tx1, now))}).AssertStatus(http.StatusOK))
	if !again.Duplicate || again.ID != first.ID {
		t.Fatalf("a retry is the same purchase: %+v", again)
	}
	replay := testclient.Decoded[schemas.PurchaseDoc](r.verify(map[string]any{"store": "app_store", "player_id": "p2",
		"signed_transaction": r.ca.Sign(t, r.appleTx(tx1, now))}).AssertStatus(http.StatusOK))
	if replay.Status != "rejected" || replay.Rejection == nil || replay.Rejection.Code != "replayed" {
		t.Fatalf("somebody else's transaction: %+v", replay)
	}
	signed := r.ca.Sign(t, r.appleTx(tx2, now))
	parts := strings.Split(signed, ".")
	forged := r.appleTx(tx2, now)
	forged["price"] = 99_990
	raw, _ := json.Marshal(forged)
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	bad := testclient.Decoded[schemas.PurchaseDoc](r.verify(map[string]any{"store": "app_store", "player_id": "p1",
		"signed_transaction": strings.Join(parts, ".")}).AssertStatus(http.StatusOK))
	if bad.Rejection == nil || bad.Rejection.Code != "signature_mismatch" {
		t.Fatalf("a forged transaction: %+v", bad)
	}
	test := r.appleTx(tx3, now)
	test["environment"] = "Sandbox"
	sandbox := testclient.Decoded[schemas.PurchaseDoc](r.verify(map[string]any{"store": "app_store", "install_id": uuid.NewString(),
		"signed_transaction": r.ca.Sign(t, test)}).AssertStatus(http.StatusOK))
	if sandbox.Status != "sandbox" || sandbox.Environment != "sandbox" || sandbox.First {
		t.Fatalf("a TestFlight purchase: %+v", sandbox)
	}
	r.verify(map[string]any{"store": "app_store", "signed_transaction": "x"}).AssertStatus(http.StatusUnprocessableEntity)

	// ---- Google Play, from the game's server ----
	r.google.Sells("tok-1-"+run, stores.Transaction{TransactionID: "GPA.1-" + run, TokenDigest: stores.Digest("tok-1-" + run),
		ProductID: "gems_500", Quantity: 1, PurchasedAt: now, Country: "BR"})
	r.google.Sells("tok-2-"+run, stores.Transaction{TransactionID: "GPA.2-" + run, TokenDigest: stores.Digest("tok-2-" + run),
		ProductID: "gems_500", Quantity: 1, PurchasedAt: now, Country: "BR"})
	gp := testclient.Decoded[schemas.PurchaseDoc](r.server.Post("/v1/purchases/verify", testclient.JSON(map[string]any{
		"store": "google_play", "player_id": "p3", "purchase_token": "tok-1-" + run, "product_id": "gems_500",
		"price_micros": 25_900_000, "currency": "brl"})).AssertStatus(http.StatusOK))
	if gp.Status != "verified" || gp.Price.AmountMicros != 25_900_000 || gp.Price.Currency != "BRL" || gp.Price.FromStore {
		t.Fatalf("a Play purchase: %+v", gp)
	}
	calls := r.google.Calls()
	r.server.Post("/v1/purchases/verify", testclient.JSON(map[string]any{"store": "google_play", "player_id": "p3",
		"purchase_token": "tok-1-" + run, "product_id": "gems_500"})).AssertStatus(http.StatusOK)
	if r.google.Calls() != calls {
		t.Fatal("a retry of a known token asked Google again")
	}
	testclient.Decoded[schemas.PurchaseDoc](r.server.Post("/v1/purchases/verify", testclient.JSON(map[string]any{
		"store": "google_play", "player_id": "p4", "purchase_token": "tok-2-" + run, "product_id": "gems_500",
		"price_micros": 9_990_000, "currency": "USD"})).AssertStatus(http.StatusOK))

	// ---- Steam, when Steam cannot answer ----
	r.steam.Down(true)
	res := r.verify(map[string]any{"store": "steam", "player_id": "p1", "order_id": "42"}).AssertStatus(http.StatusServiceUnavailable)
	if res.Header.Get("Retry-After") == "" {
		t.Fatal("a store that could not answer carries no Retry-After")
	}
	r.steam.Down(false)

	// ---- reading back, with the server key only ----
	found := testclient.Decoded[schemas.PurchaseDoc](r.server.Get("/v1/purchases/app_store/" + tx1).AssertStatus(http.StatusOK))
	if found.ID != first.ID {
		t.Fatalf("lookup %+v", found)
	}
	r.sdk.Get("/v1/purchases/app_store/" + tx1).AssertStatus(http.StatusForbidden)
	r.server.Get("/v1/purchases/app_store/nope").AssertStatus(http.StatusNotFound)

	// ---- refunds: Apple's notification, Google's notification, Google's sweep ----
	token := apple.NotificationURL[strings.LastIndex(apple.NotificationURL, "/")+1:]
	refunded := r.appleTx(tx1, now)
	refunded["revocationDate"] = time.Now().UnixMilli()
	refunded["revocationReason"] = 0
	notice := r.ca.Sign(t, r.ca.Notification(t, "REFUND", r.bundle, refunded))
	anon := testclient.New(t, r.app)
	anon.Post("/v1/store-notifications/apple/"+r.projectUID.String()+"/wrong", testclient.JSON(map[string]any{
		"signedPayload": notice})).AssertStatus(http.StatusNotFound)
	anon.Post("/v1/store-notifications/apple/"+r.projectUID.String()+"/"+token, testclient.JSON(map[string]any{
		"signedPayload": storetest.NewAppleCA(t, true).Sign(t, map[string]any{"notificationType": "REFUND"})})).
		AssertStatus(http.StatusBadRequest)
	heard := testclient.Decoded[schemas.StoreNotificationOut](anon.Post("/v1/store-notifications/apple/"+r.projectUID.String()+"/"+token,
		testclient.JSON(map[string]any{"signedPayload": notice})).AssertStatus(http.StatusOK))
	if !heard.Refund {
		t.Fatalf("a refund notification: %+v", heard)
	}
	after := testclient.Decoded[schemas.PurchaseDoc](r.server.Get("/v1/purchases/app_store/" + tx1).AssertStatus(http.StatusOK))
	if after.Status != "refunded" || after.Refund == nil || after.Refund.Source != "notification" || after.Refund.Reason != "customer_request" {
		t.Fatalf("after Apple's refund: %+v", after)
	}

	playToken := play.NotificationURL[strings.LastIndex(play.NotificationURL, "/")+1:]
	data := base64.StdEncoding.EncodeToString([]byte(`{"packageName":"com.example.game","eventTimeMillis":"` +
		strconv.FormatInt(time.Now().UnixMilli(), 10) + `","voidedPurchaseNotification":{"purchaseToken":"tok-1-` + run + `","orderId":"GPA.1-` + run + `"}}`))
	anon.Post("/v1/store-notifications/google/"+r.projectUID.String()+"/"+playToken, testclient.JSON(map[string]any{
		"message": map[string]any{"data": data, "messageId": "m-1"}, "subscription": "s"})).AssertStatus(http.StatusOK)
	voided := testclient.Decoded[schemas.PurchaseDoc](r.server.Get("/v1/purchases/google_play/GPA.1-" + run).AssertStatus(http.StatusOK))
	if voided.Status != "refunded" || voided.Refund.Source != "notification" {
		t.Fatalf("after Google's notification: %+v", voided)
	}

	r.google.Refunded(stores.Refund{Platform: stores.Google, TransactionID: "GPA.2-" + run, At: time.Now().UTC(),
		Reason: stores.RefundChargeback})
	if _, err := r.svc.Sweep(t.Context(), time.Now().UTC().Add(purchases.SweepEvery*2)); err != nil {
		t.Fatal(err)
	}
	swept := testclient.Decoded[schemas.PurchaseDoc](r.server.Get("/v1/purchases/google_play/GPA.2-" + run).AssertStatus(http.StatusOK))
	if swept.Status != "refunded" || swept.Refund.Source != "sweep" || swept.Refund.Reason != "chargeback" {
		t.Fatalf("after the sweep: %+v", swept)
	}

	// ---- the verification log ----
	attempts := testclient.Decoded[schemas.PurchaseAttemptsOut](r.owner.Get("/v1/organizations/" + r.org + "/projects/" + r.proj +
		"/purchase-attempts?outcome=rejected").AssertStatus(http.StatusOK))
	codes := map[string]bool{}
	for _, a := range attempts.Attempts {
		codes[a.Code] = true
		if len(a.Trace) == 0 {
			t.Fatalf("an attempt without its trace: %+v", a)
		}
	}
	if !codes["replayed"] || !codes["signature_mismatch"] {
		t.Fatalf("rejections logged: %v", codes)
	}
	one := testclient.Decoded[schemas.VerifiedPurchaseOut](r.owner.Get("/v1/organizations/" + r.org + "/projects/" + r.proj +
		"/verified-purchases/" + first.ID.String()).AssertStatus(http.StatusOK))
	if len(one.Attempts) != 2 || one.Purchase.Status != "refunded" {
		t.Fatalf("one purchase and its attempts: %+v", one)
	}
	list = testclient.Decoded[schemas.StoreListOut](r.owner.Get(r.storesBase).AssertStatus(http.StatusOK))
	if list.Stores[0].Day.Verified < 2 || list.Stores[0].Day.Rejected < 2 || list.Stores[0].Status != "working" {
		t.Fatalf("the App Store's day: %+v", list.Stores[0])
	}

	// ---- analytics: shipped to ClickHouse, counted by "verified wins" ----
	conn := chdb.New(r.settings.ClickHouseDSN)
	if err := conn.Start(t.Context()); err != nil {
		t.Skipf("no ClickHouse: %v", err)
	}
	defer conn.Stop(t.Context()) //nolint:errcheck
	// A purchase the game reported through the App Store, which verification now outranks, and one
	// through a web shop nobody verifies, which still counts.
	sqlConn, _ := conn.DB()
	for _, row := range []struct{ store, id string }{{"app_store", uuid.NewString()}, {"web_shop", uuid.NewString()}} {
		if err := sqlConn.Exec(t.Context(), `INSERT INTO purchases (project_seq, environment_seq, retention_tier,
			purchase_date, event_time, event_id, install_id, session_id, event_name, product_id, store, revenue_micros,
			currency, platform, country_code, app_version) VALUES (?, ?, 90, ?, ?, ?, ?, '', 'purchase', 'gems_500', ?,
			1990000, 'USD', 'ios', 'US', '1.0')`, uint32(r.projectID), uint16(r.envID), now, now, row.id,
			uuid.NewString(), row.store); err != nil {
			t.Fatal(err)
		}
	}
	shipper := &purchases.Shipper{DB: r.db, ClickHouse: conn}
	for {
		n, err := shipper.Ship(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	analytics := "/v1/organizations/" + r.org + "/projects/" + r.proj + "/analytics/production"
	day := now.Format("2006-01-02")
	ledger := testclient.Decoded[schemas.PurchasesOut](r.owner.Get(analytics + "/purchases?from=" + day + "&to=" + day + "&limit=50").
		AssertStatus(http.StatusOK))
	statuses := map[string]int{}
	for _, p := range ledger.Purchases {
		statuses[p.Store+"/"+p.Source+"/"+p.Status]++
	}
	want := map[string]int{"app_store/verified/refunded": 1, "app_store/verified/sandbox": 1, "app_store/verified/rejected": 2,
		"google_play/verified/refunded": 2, "web_shop/reported/reported": 1}
	for k, n := range want {
		if statuses[k] != n {
			t.Fatalf("the ledger has %d %s, want %d: %v", statuses[k], k, n, statuses)
		}
	}
	if statuses["app_store/reported/reported"] != 0 || !ledger.Verified {
		t.Fatalf("a reported App Store purchase was counted beside the verified ones: %v", statuses)
	}
	rev := testclient.Decoded[schemas.RevenueOut](r.owner.Get(analytics + "/revenue?from=" + day + "&to=" + day).AssertStatus(http.StatusOK))
	if !rev.Totals.Verified || rev.Totals.Sandbox != 1 || rev.Totals.Rejected != 2 {
		t.Fatalf("revenue totals: %+v", rev.Totals)
	}

	// Shipping again sends nothing.
	if n, err := shipper.Ship(t.Context()); err != nil || n != 0 {
		t.Fatalf("a second ship: %d, %v", n, err)
	}

	// ---- staff debugging a receipt ----
	desk := staffOnly(r.staffClient(t, staff.Support))
	overview := testclient.Decoded[schemas.PurchaseVerificationOut](desk.Get("/v1/operator/purchase-verification").AssertStatus(http.StatusOK))
	if overview.ByStore["app_store"].Rejected < 2 || overview.Rejections["replayed"] < 1 {
		t.Fatalf("the overview: %+v", overview)
	}
	theirs := testclient.Decoded[schemas.PurchaseAttemptsOut](desk.Get("/v1/operator/purchase-attempts?project=" +
		r.projectUID.String() + "&outcome=rejected").AssertStatus(http.StatusOK))
	if len(theirs.Attempts) < 2 || theirs.Attempts[0].Project == nil || theirs.Attempts[0].Project.Slug != r.proj {
		t.Fatalf("the project's attempts for staff: %+v", theirs)
	}
	detail := testclient.Decoded[schemas.PurchaseAttemptOut](desk.Get("/v1/operator/purchase-attempts/" +
		theirs.Attempts[0].ID.String()).AssertStatus(http.StatusOK))
	if len(detail.Trace) == 0 {
		t.Fatalf("an attempt for staff without its trace: %+v", detail)
	}
	before, _ := r.db.Client.PurchaseAttempt.Query().Where(purchaseattempt.ProjectID(r.projectID)).Count(t.Context())
	check := testclient.Decoded[schemas.PurchaseCheckOut](desk.Post("/v1/operator/projects/"+r.projectUID.String()+"/purchase-check",
		testclient.JSON(map[string]any{"store": "app_store", "signed_transaction": strings.Join(parts, ".")})).AssertStatus(http.StatusOK))
	if check.Outcome != "rejected" || check.Rejection.Code != "signature_mismatch" || len(check.Trace) < 2 {
		t.Fatalf("a staff check of a forged receipt: %+v", check)
	}
	check = testclient.Decoded[schemas.PurchaseCheckOut](desk.Post("/v1/operator/projects/"+r.projectUID.String()+"/purchase-check",
		testclient.JSON(map[string]any{"store": "app_store", "signed_transaction": r.ca.Sign(t, r.appleTx(tx1, now))})).
		AssertStatus(http.StatusOK))
	if check.Outcome != "verified" || check.Recorded == nil || check.Recorded.Status != "refunded" || check.Transaction.AmountMicros != 4_990_000 {
		t.Fatalf("a staff check of a recorded receipt: %+v", check)
	}
	if after, _ := r.db.Client.PurchaseAttempt.Query().Where(purchaseattempt.ProjectID(r.projectID)).Count(t.Context()); after != before {
		t.Fatalf("a staff check was logged as the project's attempt: %d then %d", before, after)
	}

	// Disconnected, the store verifies nothing more and its money is what the game reports.
	r.owner.Delete(r.storesBase + "/steam").AssertStatus(http.StatusNoContent)
	r.verify(map[string]any{"store": "steam", "player_id": "p1", "order_id": "42"}).AssertStatus(http.StatusConflict)
}
