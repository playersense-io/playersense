// Package storetest signs StoreKit transactions and App Store Server Notifications the way Apple
// does, under a root a test trusts in Apple's place, so a test can exercise the real signature
// checks rather than a fake that skips them.
package storetest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

// The extensions Apple puts on the certificates below its root (see internal/stores/apple.go).
var (
	oidAppleLeaf         = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 11, 1}
	oidAppleIntermediate = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 1}
)

// AppleCA stands in for Apple's root and the two certificates below it.
type AppleCA struct {
	// Roots is what the test's AppleStore trusts in place of Apple Root CA - G3.
	Roots *x509.CertPool

	leafKey           *ecdsa.PrivateKey
	leafDER, interDER []byte
}

// NewAppleCA makes a root, an intermediate and a signing leaf. With extensions false the two lack
// the App Store extensions, which a real check refuses.
func NewAppleCA(t testing.TB, extensions bool) *AppleCA {
	t.Helper()
	mk := func(tmpl, parent *x509.Certificate, pub, signer any) []byte {
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	now := time.Now()
	year := now.Add(365 * 24 * time.Hour)
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"},
		NotBefore: now.Add(-time.Hour), NotAfter: year, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	rootCert, _ := x509.ParseCertificate(mk(root, root, &rootKey.PublicKey, rootKey))

	interKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	inter := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Test WWDR"},
		NotBefore: now.Add(-time.Hour), NotAfter: year, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "Test StoreKit"},
		NotBefore: now.Add(-time.Hour), NotAfter: year, KeyUsage: x509.KeyUsageDigitalSignature}
	if extensions {
		inter.ExtraExtensions = []pkix.Extension{{Id: oidAppleIntermediate, Value: []byte{5, 0}}}
		leaf.ExtraExtensions = []pkix.Extension{{Id: oidAppleLeaf, Value: []byte{5, 0}}}
	}
	interDER := mk(inter, rootCert, &interKey.PublicKey, rootKey)
	interCert, _ := x509.ParseCertificate(interDER)
	leafDER := mk(leaf, interCert, &leafKey.PublicKey, interKey)

	pool := x509.NewCertPool()
	pool.AddCert(rootCert)
	return &AppleCA{Roots: pool, leafKey: leafKey, leafDER: leafDER, interDER: interDER}
}

// Sign makes a JWS the way Apple does: ES256, with the chain in x5c.
func (ca *AppleCA) Sign(t testing.TB, payload any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "ES256", "x5c": []string{
		base64.StdEncoding.EncodeToString(ca.leafDER), base64.StdEncoding.EncodeToString(ca.interDER)}})
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, ca.leafKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Transaction is a StoreKit 2 transaction's payload for a bundle, with everything a verification
// reads; change what a test needs.
func Transaction(bundleID, id, productID string, purchased time.Time) map[string]any {
	return map[string]any{
		"transactionId": id, "originalTransactionId": id, "bundleId": bundleID,
		"productId": productID, "purchaseDate": purchased.UnixMilli(), "quantity": 1,
		"type": "Consumable", "environment": "Production", "price": 4990, "currency": "USD",
		"storefront": "USA", "signedDate": time.Now().UnixMilli(),
	}
}

// Notification is an App Store Server Notification V2's payload carrying a signed transaction.
func (ca *AppleCA) Notification(t testing.TB, kind, bundleID string, transaction map[string]any) map[string]any {
	return map[string]any{
		"notificationType": kind, "notificationUUID": "n-" + time.Now().Format("150405.000000"),
		"data": map[string]any{"bundleId": bundleID, "environment": transaction["environment"],
			"signedTransactionInfo": ca.Sign(t, transaction)},
		"signedDate": time.Now().UnixMilli(),
	}
}
