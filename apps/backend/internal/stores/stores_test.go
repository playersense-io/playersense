package stores

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/stores/storetest"
)

func appleTx(id string) map[string]any {
	return map[string]any{
		"transactionId": id, "originalTransactionId": id, "bundleId": "com.example.game",
		"productId": "gems_500", "purchaseDate": time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).UnixMilli(),
		"quantity": 1, "type": "Consumable", "environment": "Production", "price": 4990, "currency": "usd",
		"storefront": "JPN", "signedDate": time.Now().UnixMilli(),
	}
}

func rejectionCode(err error) Code {
	var r *Rejection
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

func TestAStoreKitTransactionIsCheckedAgainstApplesChain(t *testing.T) {
	ca := storetest.NewAppleCA(t, true)
	a := &AppleStore{Roots: ca.Roots}
	c := Credentials{Platform: Apple, BundleID: "com.example.game"}
	ctx := context.Background()

	trace := NewTrace()
	tx, err := a.Verify(ctx, c, Receipt{SignedTransaction: ca.Sign(t, appleTx("2000000111")), ProductID: "gems_500"}, trace)
	if err != nil {
		t.Fatalf("verify: %v (%+v)", err, trace.Steps)
	}
	if tx.TransactionID != "2000000111" || tx.AmountMicros != 4_990_000 || tx.Currency != "USD" ||
		tx.Country != "JP" || tx.Kind != Consumable || tx.Sandbox || !tx.PriceFromStore {
		t.Fatalf("read as %+v", tx)
	}
	if len(trace.Steps) < 4 {
		t.Fatalf("the trace has %d steps", len(trace.Steps))
	}

	// Changed after signing: the signature no longer matches.
	signed := ca.Sign(t, appleTx("2000000112"))
	parts := strings.Split(signed, ".")
	forged := appleTx("2000000112")
	forged["price"] = 1
	body, _ := json.Marshal(forged)
	parts[1] = base64.RawURLEncoding.EncodeToString(body)
	if _, err := a.Verify(ctx, c, Receipt{SignedTransaction: strings.Join(parts, ".")}, nil); rejectionCode(err) != SignatureMismatch {
		t.Fatalf("a changed payload: %v", err)
	}

	// Another app's transaction.
	other := appleTx("2000000113")
	other["bundleId"] = "com.other.game"
	if _, err := a.Verify(ctx, c, Receipt{SignedTransaction: ca.Sign(t, other)}, nil); rejectionCode(err) != WrongApp {
		t.Fatalf("another app: %v", err)
	}

	// Another product than the game said.
	if _, err := a.Verify(ctx, c, Receipt{SignedTransaction: ca.Sign(t, appleTx("2000000114")), ProductID: "gems_100"}, nil); rejectionCode(err) != ProductMismatch {
		t.Fatalf("another product: %v", err)
	}

	// Xcode's local testing.
	local := appleTx("2000000115")
	local["environment"] = "Xcode"
	if _, err := a.Verify(ctx, c, Receipt{SignedTransaction: ca.Sign(t, local)}, nil); rejectionCode(err) != InvalidReceipt {
		t.Fatalf("an Xcode transaction: %v", err)
	}

	// A chain from the right root without the App Store extensions.
	plain := storetest.NewAppleCA(t, false)
	if _, err := (&AppleStore{Roots: plain.Roots}).Verify(ctx, c, Receipt{SignedTransaction: plain.Sign(t, appleTx("2000000116"))}, nil); rejectionCode(err) != SignatureMismatch {
		t.Fatalf("no extensions: %v", err)
	}

	// Signed by a chain that is not Apple's at all.
	if _, err := (&AppleStore{}).Verify(ctx, c, Receipt{SignedTransaction: ca.Sign(t, appleTx("2000000117"))}, nil); rejectionCode(err) != SignatureMismatch {
		t.Fatalf("not Apple's root: %v", err)
	}

	if _, err := a.Verify(ctx, c, Receipt{SignedTransaction: "not.a.jws!"}, nil); rejectionCode(err) != InvalidReceipt {
		t.Fatalf("garbage: %v", err)
	}
}

func TestTheEmbeddedRootIsApplesG3(t *testing.T) {
	cert, err := x509.ParseCertificate(appleRootG3)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.Raw)
	if got := hex.EncodeToString(sum[:]); got != "63343abfb89a6a03ebb57e9b3f5fa7be7c4f5c756f3017b3a8c488c3653e9179" {
		t.Fatalf("fingerprint %s", got)
	}
}

func p8(t *testing.T) (string, *ecdsa.PrivateKey) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), key
}

func TestATransactionIDIsLookedUpInProductionThenTheSandbox(t *testing.T) {
	ca := storetest.NewAppleCA(t, true)
	pemKey, key := p8(t)
	sandboxTx := appleTx("2000000200")
	sandboxTx["environment"] = "Sandbox"
	refund := appleTx("2000000200")
	refund["environment"] = "Sandbox"
	refund["revocationDate"] = time.Now().UnixMilli()
	refund["revocationReason"] = 1

	checkToken := func(t *testing.T, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Errorf("no bearer JWT")
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if len(sig) != 64 || !ecdsa.Verify(&key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			t.Errorf("the JWT is not signed with the project's key")
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		if !strings.Contains(string(claims), `"aud":"appstoreconnect-v1"`) || !strings.Contains(string(claims), `"bid":"com.example.game"`) {
			t.Errorf("claims %s", claims)
		}
	}
	production := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkToken(t, r)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errorCode":4040010,"errorMessage":"Transaction id not found."}`))
	}))
	defer production.Close()
	sandbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkToken(t, r)
		switch {
		case r.URL.Path == "/inApps/v1/transactions/2000000200":
			_ = json.NewEncoder(w).Encode(map[string]string{"signedTransactionInfo": ca.Sign(t, sandboxTx)})
		case r.URL.Path == "/inApps/v1/notifications/history":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["notificationType"] != "REFUND" {
				_ = json.NewEncoder(w).Encode(map[string]any{"notificationHistory": []any{}})
				return
			}
			n := ca.Sign(t, map[string]any{"notificationType": "REFUND", "notificationUUID": "n-1",
				"data": map[string]any{"bundleId": "com.example.game", "environment": "Sandbox",
					"signedTransactionInfo": ca.Sign(t, refund)}})
			_ = json.NewEncoder(w).Encode(map[string]any{"notificationHistory": []any{map[string]string{"signedPayload": n}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer sandbox.Close()

	a := &AppleStore{HTTP: http.DefaultClient, Roots: ca.Roots, Production: production.URL, Sandbox: sandbox.URL}
	c := Credentials{Platform: Apple, BundleID: "com.example.game", IssuerID: "issuer", KeyID: "KEY123", PrivateKey: pemKey}
	tx, err := a.Verify(context.Background(), c, Receipt{TransactionID: "2000000200"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !tx.Sandbox || tx.TransactionID != "2000000200" {
		t.Fatalf("read as %+v", tx)
	}

	// Without a key, a transaction id cannot be looked up.
	if _, err := a.Verify(context.Background(), Credentials{BundleID: "com.example.game"}, Receipt{TransactionID: "1"}, nil); !errors.Is(err, ErrNeedsKey) {
		t.Fatalf("no key: %v", err)
	}

	refunds, _, err := a.Refunds(context.Background(), c, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(refunds) != 1 || refunds[0].TransactionID != "2000000200" || refunds[0].Reason != RefundDefective || refunds[0].Reversed {
		t.Fatalf("refunds %+v", refunds)
	}
}

func serviceAccount(t *testing.T, tokenURL string) string {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "verifier@example.iam.gserviceaccount.com",
		"private_key_id": "k1", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri": tokenURL,
	})
	return string(raw)
}

func TestAPlayTokenIsLookedUp(t *testing.T) {
	tokens := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokens++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"at-1","token_type":"Bearer","expires_in":3600}`))
			return
		case r.Header.Get("Authorization") != "Bearer at-1":
			w.WriteHeader(http.StatusUnauthorized)
			return
		case strings.HasSuffix(r.URL.Path, "/tokens/good"):
			_, _ = w.Write([]byte(`{"purchaseTimeMillis":"1788000000000","purchaseState":0,"orderId":"GPA.3312-1234-5678-12345","regionCode":"br","quantity":1}`))
		case strings.HasSuffix(r.URL.Path, "/tokens/tester"):
			_, _ = w.Write([]byte(`{"purchaseTimeMillis":"1788000000000","purchaseState":0,"purchaseType":0,"regionCode":"US"}`))
		case strings.HasSuffix(r.URL.Path, "/tokens/pending"):
			_, _ = w.Write([]byte(`{"purchaseTimeMillis":"1788000000000","purchaseState":2}`))
		case strings.HasSuffix(r.URL.Path, "/voidedpurchases"):
			_, _ = w.Write([]byte(`{"voidedPurchases":[{"purchaseToken":"good","orderId":"GPA.3312-1234-5678-12345","voidedTimeMillis":"1788100000000","voidedReason":7}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	g := &GoogleStore{HTTP: srv.Client(), API: srv.URL}
	c := Credentials{Platform: Google, PackageName: "com.example.game", ServiceAccount: serviceAccount(t, srv.URL+"/token")}
	ctx := context.Background()
	tx, err := g.Verify(ctx, c, Receipt{PurchaseToken: "good", ProductID: "gems_500", PriceMicros: 25_900_000, Currency: "brl"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tx.TransactionID != "GPA.3312-1234-5678-12345" || tx.Country != "BR" || tx.AmountMicros != 25_900_000 ||
		tx.Currency != "BRL" || tx.PriceFromStore || tx.Sandbox || tx.TokenDigest != Digest("good") {
		t.Fatalf("read as %+v", tx)
	}
	tester, err := g.Verify(ctx, c, Receipt{PurchaseToken: "tester", ProductID: "gems_500"}, nil)
	if err != nil || !tester.Sandbox || tester.TransactionID != "token:"+Digest("tester") {
		t.Fatalf("a tester's purchase: %+v %v", tester, err)
	}
	if _, err := g.Verify(ctx, c, Receipt{PurchaseToken: "pending", ProductID: "gems_500"}, nil); rejectionCode(err) != NotPurchased {
		t.Fatalf("pending: %v", err)
	}
	if _, err := g.Verify(ctx, c, Receipt{PurchaseToken: "unknown", ProductID: "gems_500"}, nil); rejectionCode(err) != InvalidReceipt {
		t.Fatalf("unknown: %v", err)
	}
	if tokens != 1 {
		t.Fatalf("%d token exchanges for four lookups", tokens)
	}
	refunds, _, err := g.Refunds(ctx, c, time.Now().Add(-time.Hour))
	if err != nil || len(refunds) != 1 || refunds[0].Reason != RefundChargeback || refunds[0].TokenDigest != Digest("good") {
		t.Fatalf("refunds %+v %v", refunds, err)
	}

	// A service account Google does not honour.
	bad := &GoogleStore{HTTP: srv.Client(), API: srv.URL + "/nowhere"}
	badToken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer badToken.Close()
	if _, err := bad.Verify(ctx, Credentials{PackageName: "p", ServiceAccount: serviceAccount(t, badToken.URL)},
		Receipt{PurchaseToken: "good", ProductID: "gems_500"}, nil); !errors.Is(err, ErrCredentials) {
		t.Fatalf("a refused service account: %v", err)
	}
}

func TestADeveloperNotificationIsRead(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte(`{"version":"1.0","packageName":"com.example.game","eventTimeMillis":"1788100000000","voidedPurchaseNotification":{"purchaseToken":"good","orderId":"GPA.1","productType":2,"refundType":1}}`))
	n, err := DecodeNotification([]byte(`{"message":{"data":"` + data + `","messageId":"m-1"},"subscription":"projects/p/subscriptions/s"}`))
	if err != nil {
		t.Fatal(err)
	}
	if n.PackageName != "com.example.game" || n.Voided == nil || n.Voided.TransactionID != "GPA.1" || n.Voided.TokenDigest != Digest("good") {
		t.Fatalf("read as %+v", n)
	}
}

func TestASteamOrderIsQueriedThenTheSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "pub-key" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/ISteamMicroTxn/QueryTxn"):
			_, _ = w.Write([]byte(`{"response":{"result":"Failure","error":{"errorcode":100,"errordesc":"Order not found"}}}`))
		case strings.HasPrefix(r.URL.Path, "/ISteamMicroTxnSandbox/QueryTxn"):
			_, _ = w.Write([]byte(`{"response":{"result":"OK","params":{"orderid":"938473","transid":"374839","steamid":"7656","status":"Succeeded","currency":"EUR","time":"2026-09-01T10:00:00Z","country":"UK","items":[{"itemid":"sword","qty":1,"amount":"499"},{"itemid":"shield","qty":1,"amount":100}]}}}`))
		case strings.HasPrefix(r.URL.Path, "/ISteamMicroTxn/GetReport"):
			_, _ = w.Write([]byte(`{"response":{"result":"OK","params":{"count":1,"orders":[{"orderid":"938473","transid":"374839","status":"Chargedback","time":"2026-09-02T10:00:00Z","items":[]}]}}}`))
		}
	}))
	defer srv.Close()

	s := &SteamStore{HTTP: srv.Client(), API: srv.URL}
	c := Credentials{Platform: Steam, AppID: "480", PublisherKey: "pub-key"}
	tx, err := s.Verify(context.Background(), c, Receipt{OrderID: "938473", ProductID: "shield"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tx.TransactionID != "374839" || tx.OrderID != "938473" || tx.AmountMicros != 5_990_000 || tx.Country != "GB" ||
		!tx.Sandbox || tx.Quantity != 2 || tx.ProductID != "sword" {
		t.Fatalf("read as %+v", tx)
	}
	refunds, _, err := s.Refunds(context.Background(), c, time.Now().Add(-time.Hour))
	if err != nil || len(refunds) != 1 || refunds[0].Reason != RefundChargeback || refunds[0].TransactionID != "374839" {
		t.Fatalf("refunds %+v %v", refunds, err)
	}
	if _, err := s.Test(context.Background(), Credentials{AppID: "480", PublisherKey: "wrong"}); !errors.Is(err, ErrCredentials) {
		t.Fatalf("a wrong key: %v", err)
	}
}

func TestCountryCodes(t *testing.T) {
	for in, want := range map[string]string{"USA": "US", "jpn": "JP", "GBR": "GB", "DE": "DE", "XYZ": "", "": ""} {
		if got := Alpha2(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if n := len(alpha2); n < 245 {
		t.Fatalf("%d countries", n)
	}
}
