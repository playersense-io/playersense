package purchases

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/purchaseprovider"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/stores"
)

// The keys a provider's config and secret hold, per store.
const (
	KeyBundleID    = "bundle_id"
	KeyAppAppleID  = "app_apple_id"
	KeyIssuerID    = "issuer_id"
	KeyKeyID       = "key_id"
	KeyPackageName = "package_name"
	KeyAppID       = "app_id"

	SecretPrivateKey     = "private_key"
	SecretServiceAccount = "service_account"
	SecretPublisherKey   = "publisher_key"
)

// CredentialsOf reads a provider as the credentials its store's adapter takes.
func CredentialsOf(p *ent.PurchaseProvider) stores.Credentials {
	c := stores.Credentials{Platform: stores.Platform(p.Platform)}
	str := func(k string) string {
		v, _ := p.Config[k].(string)
		return v
	}
	c.BundleID, c.IssuerID, c.KeyID = str(KeyBundleID), str(KeyIssuerID), str(KeyKeyID)
	c.PackageName, c.AppID = str(KeyPackageName), str(KeyAppID)
	switch v := p.Config[KeyAppAppleID].(type) {
	case float64:
		c.AppAppleID = int64(v)
	case string:
		c.AppAppleID, _ = strconv.ParseInt(v, 10, 64)
	}
	c.PrivateKey = p.Secret[SecretPrivateKey]
	c.ServiceAccount = p.Secret[SecretServiceAccount]
	c.PublisherKey = p.Secret[SecretPublisherKey]
	return c
}

// NotifyToken is a new token for a provider's notification URL.
func NotifyToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("notify token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// VerifiedStores is the stores a project verifies purchases with, each since it was connected:
// what the money pages count from verified purchases rather than the game's own reports.
func VerifiedStores(ctx context.Context, client *ent.Client, projectID int) ([]query.VerifiedStore, error) {
	provs, err := client.PurchaseProvider.Query().Where(purchaseprovider.ProjectID(projectID),
		purchaseprovider.Enabled(true)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]query.VerifiedStore, 0, len(provs))
	for _, p := range provs {
		out = append(out, query.VerifiedStore{Store: stores.Platform(p.Platform).StoreName(), Since: p.VerifiedSince})
	}
	return out, nil
}
