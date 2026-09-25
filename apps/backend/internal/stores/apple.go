package stores

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// Apple's root, which signs every StoreKit 2 transaction and every App Store Server
// Notification: Apple Root CA - G3, SHA-256 63:34:3A:BF:...:91:79, valid until 2039. Embedded
// rather than read from the system store, because a system store trusts every public CA and a
// transaction must be signed by Apple's.
//
//go:embed certs/AppleRootCA-G3.cer
var appleRootG3 []byte

// The extensions Apple puts on the two certificates below its root, which is what tells an
// App Store signing certificate from any other certificate Apple's root has signed.
var (
	oidAppleLeaf         = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 11, 1}
	oidAppleIntermediate = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 1}
)

// AppleStore checks App Store purchases.
type AppleStore struct {
	HTTP *http.Client
	// Roots overrides Apple's root, for tests.
	Roots *x509.CertPool
	// Production and Sandbox override the App Store Server API's hosts, for tests.
	Production, Sandbox string
	// Now overrides the clock, for tests.
	Now func() time.Time
}

func (a *AppleStore) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *AppleStore) roots() (*x509.CertPool, error) {
	if a.Roots != nil {
		return a.Roots, nil
	}
	root, err := x509.ParseCertificate(appleRootG3)
	if err != nil {
		return nil, fmt.Errorf("apple root: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return pool, nil
}

func (a *AppleStore) host(sandbox bool) string {
	switch {
	case sandbox && a.Sandbox != "":
		return a.Sandbox
	case sandbox:
		return "https://api.storekit-sandbox.itunes.apple.com"
	case a.Production != "":
		return a.Production
	}
	return "https://api.storekit.itunes.apple.com"
}

// appleTransaction is JWSTransactionDecodedPayload, the fields of it anything here reads.
type appleTransaction struct {
	TransactionID         string `json:"transactionId"`
	OriginalTransactionID string `json:"originalTransactionId"`
	BundleID              string `json:"bundleId"`
	ProductID             string `json:"productId"`
	PurchaseDate          int64  `json:"purchaseDate"`
	Quantity              int    `json:"quantity"`
	Type                  string `json:"type"`
	Environment           string `json:"environment"`
	Price                 int64  `json:"price"`
	Currency              string `json:"currency"`
	Storefront            string `json:"storefront"`
	RevocationDate        int64  `json:"revocationDate"`
	RevocationReason      *int   `json:"revocationReason"`
	SignedDate            int64  `json:"signedDate"`
}

// Verify checks a StoreKit 2 transaction's signature, or looks a transaction id up with the App
// Store Server API.
func (a *AppleStore) Verify(ctx context.Context, c Credentials, r Receipt, t *Trace) (Transaction, error) {
	if c.BundleID == "" {
		return Transaction{}, fmt.Errorf("%w: the App Store bundle id is not set", ErrNeedsKey)
	}
	signed := r.SignedTransaction
	if signed == "" {
		if r.TransactionID == "" {
			return Transaction{}, reject(InvalidReceipt, "send the transaction's jwsRepresentation as signed_transaction, or its transaction_id")
		}
		found, err := a.lookup(ctx, c, r.TransactionID, t)
		if err != nil {
			return Transaction{}, err
		}
		signed = found
	}

	var p appleTransaction
	if err := a.verifyJWS(signed, &p, t); err != nil {
		return Transaction{}, err
	}
	tx, err := a.transaction(c, p, r, t)
	if err != nil {
		return Transaction{}, err
	}
	return tx, nil
}

func (a *AppleStore) transaction(c Credentials, p appleTransaction, r Receipt, t *Trace) (Transaction, error) {
	if p.BundleID != c.BundleID {
		t.step("bundle", false, "signed for %q, and this project's app is %q", p.BundleID, c.BundleID)
		return Transaction{}, reject(WrongApp, "the transaction is for %q, not this project's %q", p.BundleID, c.BundleID)
	}
	t.step("bundle", true, "for %s", p.BundleID)
	switch p.Environment {
	case "Production", "Sandbox":
	default:
		t.step("environment", false, "%q", p.Environment)
		return Transaction{}, reject(InvalidReceipt, "signed in the %q environment, which is Xcode's local StoreKit testing and cannot be verified by any server", p.Environment)
	}
	if r.ProductID != "" && r.ProductID != p.ProductID {
		t.step("product", false, "the game said %q, Apple says %q", r.ProductID, p.ProductID)
		return Transaction{}, reject(ProductMismatch, "the transaction bought %q, not %q", p.ProductID, r.ProductID)
	}
	if p.TransactionID == "" || p.PurchaseDate == 0 {
		return Transaction{}, reject(InvalidReceipt, "the transaction has no id or purchase date")
	}

	tx := Transaction{
		TransactionID:         p.TransactionID,
		OriginalTransactionID: p.OriginalTransactionID,
		AppID:                 p.BundleID,
		ProductID:             p.ProductID,
		Kind:                  appleKind(p.Type),
		Quantity:              max(p.Quantity, 1),
		PurchasedAt:           time.UnixMilli(p.PurchaseDate).UTC(),
		Country:               Alpha2(p.Storefront),
		Sandbox:               p.Environment == "Sandbox",
	}
	// price is in thousandths of the currency's unit, and only in transactions signed since 2023.
	if p.Price > 0 && p.Currency != "" {
		tx.AmountMicros, tx.Currency, tx.PriceFromStore = p.Price*1000, strings.ToUpper(p.Currency), true
	} else {
		tx.AmountMicros, tx.Currency = r.PriceMicros, strings.ToUpper(r.Currency)
	}
	if p.RevocationDate > 0 {
		at := time.UnixMilli(p.RevocationDate).UTC()
		tx.RefundedAt, tx.RefundReason = &at, appleRefundReason(p.RevocationReason)
	}
	t.step("transaction", true, "%s, %s, %d micros %s%s", tx.TransactionID, tx.ProductID, tx.AmountMicros, tx.Currency,
		map[bool]string{true: ", sandbox", false: ""}[tx.Sandbox])
	return tx, nil
}

func appleKind(t string) Kind {
	switch t {
	case "Consumable":
		return Consumable
	case "Non-Consumable":
		return NonConsumable
	case "Auto-Renewable Subscription", "Non-Renewing Subscription":
		return Subscription
	}
	return UnknownKind
}

// appleRefundReason reads revocationReason: 1 is "an actual or perceived issue within your app",
// 0 is anything else, which is the customer asking.
func appleRefundReason(reason *int) RefundReason {
	if reason != nil && *reason == 1 {
		return RefundDefective
	}
	return RefundCustomerRequest
}

// verifyJWS checks a JWS Apple signed: the certificate chain in its header up to Apple's root, the
// App Store extensions on the two certificates below it, and the ES256 signature over the rest.
// Only then is the payload read into `into`.
func (a *AppleStore) verifyJWS(token string, into any, t *Trace) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.step("decode", false, "not a JWS: %d parts", len(parts))
		return reject(InvalidReceipt, "not a signed transaction: expected three dot-separated parts")
	}
	rawHeader, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	payload, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	sig, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	if err := errors.Join(err1, err2, err3); err != nil {
		t.step("decode", false, "%v", err)
		return reject(InvalidReceipt, "not a signed transaction: a part is not base64url")
	}
	var header struct {
		Alg string   `json:"alg"`
		X5C []string `json:"x5c"`
	}
	if err := json.Unmarshal(rawHeader, &header); err != nil {
		t.step("decode", false, "header: %v", err)
		return reject(InvalidReceipt, "the signed transaction's header is not JSON")
	}
	if header.Alg != "ES256" || len(header.X5C) < 2 {
		t.step("decode", false, "alg %q with %d certificates", header.Alg, len(header.X5C))
		return reject(SignatureMismatch, "not signed the way Apple signs: ES256 with a certificate chain")
	}
	t.step("decode", true, "ES256, %d certificates", len(header.X5C))

	certs := make([]*x509.Certificate, 0, len(header.X5C))
	for i, enc := range header.X5C {
		der, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return reject(SignatureMismatch, "certificate %d is not base64", i)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return reject(SignatureMismatch, "certificate %d does not parse", i)
		}
		certs = append(certs, cert)
	}
	leaf, intermediate := certs[0], certs[1]

	// The chain is checked as of when Apple signed, which the payload says; that moment is only
	// trusted once the signature below checks out, and it is checked below. Checking as of now
	// would turn away an old transaction whose leaf has since expired, which Apple's own library
	// accepts too.
	var dated struct {
		SignedDate int64 `json:"signedDate"`
	}
	_ = json.Unmarshal(payload, &dated)
	at := a.now()
	if dated.SignedDate > 0 {
		at = time.UnixMilli(dated.SignedDate)
	}
	roots, err := a.roots()
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(intermediate)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: pool, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		t.step("chain", false, "%v", err)
		return reject(SignatureMismatch, "the certificate chain does not lead to Apple's root")
	}
	if !hasExtension(leaf, oidAppleLeaf) || !hasExtension(intermediate, oidAppleIntermediate) {
		t.step("chain", false, "the certificates lack the App Store extensions")
		return reject(SignatureMismatch, "signed by a certificate Apple issued for something other than the App Store")
	}
	t.step("chain", true, "%s, under Apple Root CA - G3", leaf.Subject.CommonName)

	key, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || len(sig) != 64 {
		t.step("signature", false, "not a P-256 signature")
		return reject(SignatureMismatch, "the signature is not ES256")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(key, digest[:], r, s) {
		t.step("signature", false, "does not match")
		return reject(SignatureMismatch, "the signature does not match the transaction: it was changed after Apple signed it")
	}
	t.step("signature", true, "matches")

	if err := json.Unmarshal(payload, into); err != nil {
		return reject(InvalidReceipt, "the signed payload is not JSON")
	}
	return nil
}

func hasExtension(c *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return true
		}
	}
	return false
}

func (c Credentials) hasAppleKey() bool {
	return c.IssuerID != "" && c.KeyID != "" && c.PrivateKey != ""
}

// appleToken is the App Store Server API's bearer token: an ES256 JWT signed with the project's
// In-App Purchase key, good for twenty minutes.
func (a *AppleStore) appleToken(c Credentials) (string, error) {
	block, _ := pem.Decode([]byte(c.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("%w: the In-App Purchase key is not a PEM (.p8) file", ErrCredentials)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("%w: the In-App Purchase key does not parse: %v", ErrCredentials, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("%w: the In-App Purchase key is not an EC key", ErrCredentials)
	}
	now := a.now().Unix()
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": c.KeyID, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": c.IssuerID, "iat": now, "exp": now + 20*60,
		"aud": "appstoreconnect-v1", "bid": c.BundleID})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// call sends one App Store Server API request, reading a 2xx body into out. A 404 comes back as
// errAppleNotFound so a lookup can try the sandbox.
func (a *AppleStore) call(ctx context.Context, c Credentials, sandbox bool, method, path string, body, out any) error {
	token, err := a.appleToken(c)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.host(sandbox)+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return &Unavailable{Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err := statusError(resp.StatusCode, raw); err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errAppleNotFound
	case resp.StatusCode == http.StatusBadRequest:
		return reject(InvalidReceipt, "Apple refused the request: %s", snippet(raw))
	case resp.StatusCode >= 300:
		return &Unavailable{Status: resp.StatusCode, Detail: snippet(raw)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return &Unavailable{Status: resp.StatusCode, Detail: "an answer that is not JSON"}
		}
	}
	return nil
}

var errAppleNotFound = errors.New("not found")

// lookup finds a transaction by id, in production and then in the sandbox, and answers its
// signed form.
func (a *AppleStore) lookup(ctx context.Context, c Credentials, id string, t *Trace) (string, error) {
	if !c.hasAppleKey() {
		t.step("lookup", false, "no In-App Purchase key")
		return "", fmt.Errorf("%w: looking a transaction id up needs the project's In-App Purchase key; send signed_transaction instead", ErrNeedsKey)
	}
	for _, sandbox := range []bool{false, true} {
		var out struct {
			SignedTransactionInfo string `json:"signedTransactionInfo"`
		}
		err := a.call(ctx, c, sandbox, http.MethodGet, "/inApps/v1/transactions/"+id, nil, &out)
		if errors.Is(err, errAppleNotFound) {
			continue
		}
		if err != nil {
			t.step("lookup", false, "%v", err)
			return "", err
		}
		t.step("lookup", true, "found in %s", map[bool]string{true: "the sandbox", false: "production"}[sandbox])
		return out.SignedTransactionInfo, nil
	}
	t.step("lookup", false, "neither production nor the sandbox knows %s", id)
	return "", reject(InvalidReceipt, "Apple knows no transaction %s", id)
}

// AppleNotification is an App Store Server Notification V2, checked and read.
type AppleNotification struct {
	UUID        string
	Type        string
	Subtype     string
	BundleID    string
	Environment string
	Transaction *Transaction
}

// Notification checks and reads the signedPayload Apple posts to the project's notification URL.
func (a *AppleStore) Notification(c Credentials, signedPayload string) (AppleNotification, error) {
	var p struct {
		NotificationType string `json:"notificationType"`
		Subtype          string `json:"subtype"`
		NotificationUUID string `json:"notificationUUID"`
		Data             struct {
			BundleID              string `json:"bundleId"`
			Environment           string `json:"environment"`
			SignedTransactionInfo string `json:"signedTransactionInfo"`
		} `json:"data"`
	}
	if err := a.verifyJWS(signedPayload, &p, nil); err != nil {
		return AppleNotification{}, err
	}
	n := AppleNotification{UUID: p.NotificationUUID, Type: p.NotificationType, Subtype: p.Subtype,
		BundleID: p.Data.BundleID, Environment: p.Data.Environment}
	if n.BundleID != "" && c.BundleID != "" && n.BundleID != c.BundleID {
		return n, reject(WrongApp, "a notification for %q, not this project's %q", n.BundleID, c.BundleID)
	}
	if p.Data.SignedTransactionInfo != "" {
		var tp appleTransaction
		if err := a.verifyJWS(p.Data.SignedTransactionInfo, &tp, nil); err != nil {
			return n, err
		}
		tx, err := a.transaction(c, tp, Receipt{}, nil)
		if err != nil {
			return n, err
		}
		n.Transaction = &tx
	}
	return n, nil
}

// Refunds reads the REFUND and REFUND_REVERSED notifications Apple sent since a moment, from its
// notification history: the sweep that catches a notification the project's URL missed. Apple
// keeps 180 days of it, and only for an app whose notification URL is set.
func (a *AppleStore) Refunds(ctx context.Context, c Credentials, since time.Time) ([]Refund, time.Time, error) {
	if !c.hasAppleKey() {
		return nil, since, nil
	}
	now := a.now()
	if floor := now.AddDate(0, 0, -179); since.Before(floor) {
		since = floor
	}
	var out []Refund
	for _, sandbox := range []bool{false, true} {
		for _, kind := range []string{"REFUND", "REFUND_REVERSED"} {
			pageToken := ""
			for page := 0; page < 50; page++ {
				var resp struct {
					NotificationHistory []struct {
						SignedPayload string `json:"signedPayload"`
					} `json:"notificationHistory"`
					HasMore         bool   `json:"hasMore"`
					PaginationToken string `json:"paginationToken"`
				}
				path := "/inApps/v1/notifications/history"
				if pageToken != "" {
					path += "?paginationToken=" + pageToken
				}
				err := a.call(ctx, c, sandbox, http.MethodPost, path, map[string]any{
					"startDate": since.UnixMilli(), "endDate": now.UnixMilli(), "notificationType": kind,
				}, &resp)
				if errors.Is(err, errAppleNotFound) {
					break
				}
				if err != nil {
					return out, since, err
				}
				for _, h := range resp.NotificationHistory {
					n, err := a.Notification(c, h.SignedPayload)
					if err != nil || n.Transaction == nil {
						continue
					}
					out = append(out, refundOf(n))
				}
				if !resp.HasMore || resp.PaginationToken == "" {
					break
				}
				pageToken = resp.PaginationToken
			}
		}
	}
	return out, now, nil
}

// refundOf reads a REFUND or REFUND_REVERSED notification as a Refund.
func refundOf(n AppleNotification) Refund {
	tx := n.Transaction
	r := Refund{Platform: Apple, TransactionID: tx.TransactionID, Transaction: tx,
		Reversed: n.Type == "REFUND_REVERSED", Reason: tx.RefundReason}
	if tx.RefundedAt != nil {
		r.At = *tx.RefundedAt
	}
	return r
}

// IsRefund reports whether a notification changes a purchase's refund state.
func (n AppleNotification) IsRefund() bool {
	return (n.Type == "REFUND" || n.Type == "REFUND_REVERSED") && n.Transaction != nil
}

// Refund reads a refund notification.
func (n AppleNotification) Refund() Refund { return refundOf(n) }

// Test asks Apple to send a test notification to the project's URL, which proves the key and,
// once it arrives, the URL. Without a key it says what a key would add.
func (a *AppleStore) Test(ctx context.Context, c Credentials) (string, error) {
	if c.BundleID == "" {
		return "", fmt.Errorf("%w: the bundle id is not set", ErrNeedsKey)
	}
	if !c.hasAppleKey() {
		return "StoreKit 2 transactions for " + c.BundleID + " can be verified. Add an In-App Purchase key to look transaction ids up and to sweep for refunds.", nil
	}
	var out struct {
		TestNotificationToken string `json:"testNotificationToken"`
	}
	if err := a.call(ctx, c, false, http.MethodPost, "/inApps/v1/notifications/test", nil, &out); err != nil {
		return "", err
	}
	return "Apple accepted the key and is sending a test notification to the project's notification URL.", nil
}
