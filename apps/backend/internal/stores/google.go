package stores

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// androidPublisherScope is the one scope the Play Developer API needs.
const androidPublisherScope = "https://www.googleapis.com/auth/androidpublisher"

// GoogleStore checks Google Play purchases with the Play Developer API.
type GoogleStore struct {
	HTTP *http.Client
	// API overrides https://androidpublisher.googleapis.com, for tests.
	API string

	mu      sync.Mutex
	sources map[string]oauth2.TokenSource
}

func (g *GoogleStore) api() string {
	if g.API != "" {
		return g.API
	}
	return "https://androidpublisher.googleapis.com"
}

// token is an access token for the service account, reused until it expires. One token source
// per service account, so a verification costs one Google call rather than two.
func (g *GoogleStore) token(ctx context.Context, c Credentials) (string, error) {
	if c.ServiceAccount == "" {
		return "", fmt.Errorf("%w: the service account key is not set", ErrNeedsKey)
	}
	key := Digest(c.ServiceAccount)
	g.mu.Lock()
	source := g.sources[key]
	if source == nil {
		cfg, err := google.JWTConfigFromJSON([]byte(c.ServiceAccount), androidPublisherScope)
		if err != nil {
			g.mu.Unlock()
			return "", fmt.Errorf("%w: the service account key is not a Google JSON key: %v", ErrCredentials, err)
		}
		// The token exchange runs on the adapter's client, never the request's context: a source
		// outlives the request that made it.
		base := context.WithValue(context.Background(), oauth2.HTTPClient, g.HTTP)
		source = oauth2.ReuseTokenSource(nil, cfg.TokenSource(base))
		if g.sources == nil {
			g.sources = map[string]oauth2.TokenSource{}
		}
		g.sources[key] = source
	}
	g.mu.Unlock()
	tok, err := source.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < 500 {
			return "", fmt.Errorf("%w: Google would not issue a token: %s", ErrCredentials, snippet(re.Body))
		}
		return "", &Unavailable{Detail: "Google's token service: " + err.Error()}
	}
	return tok.AccessToken, nil
}

// get sends one Play Developer API request and reads a 2xx body into out. A 400, 404 or 410
// answers found=false: Google's word for a token it does not know.
func (g *GoogleStore) get(ctx context.Context, c Credentials, path string, out any) (bool, error) {
	token, err := g.token(ctx, c)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api()+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return false, &Unavailable{Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err := statusError(resp.StatusCode, raw); err != nil {
		return false, err
	}
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusGone:
		return false, nil
	}
	if resp.StatusCode >= 300 {
		return false, &Unavailable{Status: resp.StatusCode, Detail: snippet(raw)}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, &Unavailable{Status: resp.StatusCode, Detail: "an answer that is not JSON"}
	}
	return true, nil
}

// productPurchase is the Play Developer API's ProductPurchase, the fields read here.
type productPurchase struct {
	PurchaseTimeMillis string `json:"purchaseTimeMillis"`
	PurchaseState      *int   `json:"purchaseState"`
	OrderID            string `json:"orderId"`
	PurchaseType       *int   `json:"purchaseType"`
	RegionCode         string `json:"regionCode"`
	Quantity           int    `json:"quantity"`
	ProductID          string `json:"productId"`
}

// Verify looks a one-time product's purchase token up.
func (g *GoogleStore) Verify(ctx context.Context, c Credentials, r Receipt, t *Trace) (Transaction, error) {
	if c.PackageName == "" {
		return Transaction{}, fmt.Errorf("%w: the package name is not set", ErrNeedsKey)
	}
	if r.PurchaseToken == "" || r.ProductID == "" {
		return Transaction{}, reject(InvalidReceipt, "send the purchase's purchase_token and product_id")
	}
	var p productPurchase
	path := "/androidpublisher/v3/applications/" + url.PathEscape(c.PackageName) + "/purchases/products/" +
		url.PathEscape(r.ProductID) + "/tokens/" + url.PathEscape(r.PurchaseToken)
	found, err := g.get(ctx, c, path, &p)
	if err != nil {
		t.step("lookup", false, "%v", err)
		return Transaction{}, err
	}
	if !found {
		t.step("lookup", false, "Google does not know the token for %s in %s", r.ProductID, c.PackageName)
		return Transaction{}, reject(InvalidReceipt, "Google Play knows no purchase of %q in %s with that token", r.ProductID, c.PackageName)
	}
	t.step("lookup", true, "order %q", p.OrderID)

	state := 0
	if p.PurchaseState != nil {
		state = *p.PurchaseState
	}
	switch state {
	case 0:
	case 1:
		t.step("state", false, "canceled")
		return Transaction{}, reject(NotPurchased, "the purchase was canceled")
	case 2:
		t.step("state", false, "pending")
		return Transaction{}, reject(NotPurchased, "the purchase is pending: the player has not paid yet")
	default:
		return Transaction{}, reject(NotPurchased, "the purchase is in state %d", state)
	}
	if p.ProductID != "" && p.ProductID != r.ProductID {
		return Transaction{}, reject(ProductMismatch, "the token bought %q, not %q", p.ProductID, r.ProductID)
	}
	ms, _ := strconv.ParseInt(p.PurchaseTimeMillis, 10, 64)
	digest := Digest(r.PurchaseToken)
	tx := Transaction{
		TransactionID: p.OrderID,
		TokenDigest:   digest,
		AppID:         c.PackageName,
		ProductID:     r.ProductID,
		Kind:          UnknownKind,
		Quantity:      max(p.Quantity, 1),
		PurchasedAt:   time.UnixMilli(ms).UTC(),
		// The purchase carries no price: what the game showed from ProductDetails is recorded.
		AmountMicros: r.PriceMicros,
		Currency:     strings.ToUpper(r.Currency),
		Country:      strings.ToUpper(p.RegionCode),
		// purchaseType 0 is a license tester's test purchase.
		Sandbox: p.PurchaseType != nil && *p.PurchaseType == 0,
	}
	if tx.TransactionID == "" {
		// A test purchase has no order id; the token is the only thing that names it.
		tx.TransactionID = "token:" + digest
	}
	if ms == 0 {
		tx.PurchasedAt = time.Now().UTC()
	}
	t.step("state", true, "purchased%s", map[bool]string{true: ", by a license tester", false: ""}[tx.Sandbox])
	return tx, nil
}

// googleVoidedReason reads voidedReason.
func googleVoidedReason(reason int) RefundReason {
	switch reason {
	case 1:
		return RefundCustomerRequest // remorse
	case 2:
		return RefundNotReceived
	case 3:
		return RefundDefective
	case 4:
		return RefundAccidental
	case 5:
		return RefundFraud
	case 6, 7:
		return RefundChargeback // friendly fraud, chargeback
	}
	return RefundOther
}

// Refunds reads the voided purchases list: every purchase refunded, charged back or revoked
// since a moment. Google keeps thirty days of it.
func (g *GoogleStore) Refunds(ctx context.Context, c Credentials, since time.Time) ([]Refund, time.Time, error) {
	if c.PackageName == "" || c.ServiceAccount == "" {
		return nil, since, nil
	}
	now := time.Now()
	if floor := now.AddDate(0, 0, -29); since.Before(floor) {
		since = floor
	}
	var out []Refund
	next := since
	pageToken := ""
	for page := 0; page < 100; page++ {
		q := url.Values{"startTime": {strconv.FormatInt(since.UnixMilli(), 10)}, "maxResults": {"1000"}}
		if pageToken != "" {
			q.Set("token", pageToken)
		}
		var resp struct {
			VoidedPurchases []struct {
				PurchaseToken    string `json:"purchaseToken"`
				OrderID          string `json:"orderId"`
				VoidedTimeMillis string `json:"voidedTimeMillis"`
				VoidedReason     int    `json:"voidedReason"`
			} `json:"voidedPurchases"`
			TokenPagination struct {
				NextPageToken string `json:"nextPageToken"`
			} `json:"tokenPagination"`
		}
		path := "/androidpublisher/v3/applications/" + url.PathEscape(c.PackageName) + "/purchases/voidedpurchases?" + q.Encode()
		found, err := g.get(ctx, c, path, &resp)
		if err != nil {
			return out, since, err
		}
		if !found {
			break
		}
		for _, v := range resp.VoidedPurchases {
			ms, _ := strconv.ParseInt(v.VoidedTimeMillis, 10, 64)
			at := time.UnixMilli(ms).UTC()
			if at.After(next) {
				next = at
			}
			out = append(out, Refund{Platform: Google, TransactionID: v.OrderID, TokenDigest: Digest(v.PurchaseToken),
				At: at, Reason: googleVoidedReason(v.VoidedReason)})
		}
		if resp.TokenPagination.NextPageToken == "" {
			break
		}
		pageToken = resp.TokenPagination.NextPageToken
	}
	return out, next, nil
}

// GoogleNotification is a Real-time Developer Notification, as Pub/Sub pushes it.
type GoogleNotification struct {
	MessageID   string
	PackageName string
	Test        bool
	// Voided is set for a voidedPurchaseNotification: the purchase was refunded or charged back.
	Voided *Refund
}

// DecodeNotification reads a Pub/Sub push. It proves nothing by itself: the push URL's token says
// who sent it, and a voided purchase is believed because a sweep of the voided list would find
// it too.
func DecodeNotification(body []byte) (GoogleNotification, error) {
	var push struct {
		Message struct {
			Data        string    `json:"data"`
			MessageID   string    `json:"messageId"`
			PublishTime time.Time `json:"publishTime"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &push); err != nil {
		return GoogleNotification{}, fmt.Errorf("not a Pub/Sub push: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(push.Message.Data)
	if err != nil {
		return GoogleNotification{}, fmt.Errorf("the message data is not base64: %w", err)
	}
	var rtdn struct {
		PackageName                string          `json:"packageName"`
		EventTimeMillis            string          `json:"eventTimeMillis"`
		TestNotification           json.RawMessage `json:"testNotification"`
		VoidedPurchaseNotification *struct {
			PurchaseToken string `json:"purchaseToken"`
			OrderID       string `json:"orderId"`
		} `json:"voidedPurchaseNotification"`
	}
	if err := json.Unmarshal(data, &rtdn); err != nil {
		return GoogleNotification{}, fmt.Errorf("the message is not a developer notification: %w", err)
	}
	n := GoogleNotification{MessageID: push.Message.MessageID, PackageName: rtdn.PackageName,
		Test: len(rtdn.TestNotification) > 0}
	if v := rtdn.VoidedPurchaseNotification; v != nil {
		ms, _ := strconv.ParseInt(rtdn.EventTimeMillis, 10, 64)
		at := time.UnixMilli(ms).UTC()
		if ms == 0 {
			at = push.Message.PublishTime.UTC()
		}
		n.Voided = &Refund{Platform: Google, TransactionID: v.OrderID, TokenDigest: Digest(v.PurchaseToken), At: at}
	}
	return n, nil
}

// Test gets a token and reads one voided purchase, which needs the service account to have
// access to the app.
func (g *GoogleStore) Test(ctx context.Context, c Credentials) (string, error) {
	if c.PackageName == "" {
		return "", fmt.Errorf("%w: the package name is not set", ErrNeedsKey)
	}
	var resp map[string]any
	found, err := g.get(ctx, c, "/androidpublisher/v3/applications/"+url.PathEscape(c.PackageName)+
		"/purchases/voidedpurchases?maxResults=1", &resp)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: Google Play knows no app %s that this service account can read", ErrCredentials, c.PackageName)
	}
	return "The service account can read " + c.PackageName + "'s purchases.", nil
}
