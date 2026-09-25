package stores

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SteamStore checks Steam microtransactions with the ISteamMicroTxn Web API.
type SteamStore struct {
	HTTP *http.Client
	// API overrides https://partner.steam-api.com, for tests.
	API string
}

func (s *SteamStore) api() string {
	if s.API != "" {
		return s.API
	}
	return "https://partner.steam-api.com"
}

// flexInt reads a number Steam writes either as a number or as a string of one.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	raw := strings.Trim(string(b), `"`)
	if raw == "" || raw == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	*f = flexInt(n)
	return err
}

// steamOrder is an order as QueryTxn and GetReport describe it.
type steamOrder struct {
	OrderID  string `json:"orderid"`
	TransID  string `json:"transid"`
	Status   string `json:"status"`
	Currency string `json:"currency"`
	Time     string `json:"time"`
	Country  string `json:"country"`
	Items    []struct {
		ItemID string  `json:"itemid"`
		Qty    flexInt `json:"qty"`
		Amount flexInt `json:"amount"`
	} `json:"items"`
}

type steamAnswer struct {
	Response struct {
		Result string          `json:"result"`
		Params json.RawMessage `json:"params"`
		Error  struct {
			Code flexInt `json:"errorcode"`
			Desc string  `json:"errordesc"`
		} `json:"error"`
	} `json:"response"`
}

// call sends one ISteamMicroTxn request. ok is false for a "Failure" answer, whose description
// comes back as detail.
func (s *SteamStore) call(ctx context.Context, sandbox bool, method string, q url.Values, out any) (ok bool, detail string, err error) {
	iface := "ISteamMicroTxn"
	if sandbox {
		iface = "ISteamMicroTxnSandbox"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.api()+"/"+iface+"/"+method+"/?"+q.Encode(), nil)
	if err != nil {
		return false, "", err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return false, "", &Unavailable{Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err := statusError(resp.StatusCode, raw); err != nil {
		return false, "", err
	}
	if resp.StatusCode >= 300 {
		return false, "", &Unavailable{Status: resp.StatusCode, Detail: snippet(raw)}
	}
	var a steamAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return false, "", &Unavailable{Status: resp.StatusCode, Detail: "an answer that is not JSON"}
	}
	if a.Response.Result != "OK" {
		return false, fmt.Sprintf("%s (%d)", a.Response.Error.Desc, a.Response.Error.Code), nil
	}
	if out != nil {
		if err := json.Unmarshal(a.Response.Params, out); err != nil {
			return false, "", &Unavailable{Detail: "params that do not parse"}
		}
	}
	return true, "", nil
}

func (c Credentials) steamQuery() url.Values {
	return url.Values{"key": {c.PublisherKey}, "appid": {c.AppID}}
}

// Verify queries the order in Steam, then in Steam's sandbox.
func (s *SteamStore) Verify(ctx context.Context, c Credentials, r Receipt, t *Trace) (Transaction, error) {
	if c.AppID == "" || c.PublisherKey == "" {
		return Transaction{}, fmt.Errorf("%w: the Steam app id and publisher key are not set", ErrNeedsKey)
	}
	if r.OrderID == "" {
		return Transaction{}, reject(InvalidReceipt, "send the order_id the game gave InitTxn")
	}
	var order steamOrder
	sandbox := false
	found := false
	var detail string
	for _, sb := range []bool{false, true} {
		q := c.steamQuery()
		q.Set("orderid", r.OrderID)
		ok, d, err := s.call(ctx, sb, "QueryTxn/v3", q, &order)
		if err != nil {
			t.step("lookup", false, "%v", err)
			return Transaction{}, err
		}
		if ok {
			sandbox, found = sb, true
			break
		}
		detail = d
	}
	if !found {
		t.step("lookup", false, "%s", detail)
		return Transaction{}, reject(InvalidReceipt, "Steam knows no order %s for app %s: %s", r.OrderID, c.AppID, detail)
	}
	t.step("lookup", true, "order %s, transaction %s, %s", order.OrderID, order.TransID, order.Status)

	tx, refunded, err := steamTransaction(c, order, sandbox)
	if err != nil {
		t.step("state", false, "%v", err)
		return Transaction{}, err
	}
	if r.ProductID != "" && !slicesContainsItem(order, r.ProductID) {
		return Transaction{}, reject(ProductMismatch, "order %s bought %q, not %q", order.OrderID, tx.ProductID, r.ProductID)
	}
	if refunded != "" {
		now := time.Now().UTC()
		tx.RefundedAt, tx.RefundReason = &now, steamRefundReason(refunded)
	}
	t.step("state", true, "%s", order.Status)
	return tx, nil
}

func slicesContainsItem(o steamOrder, item string) bool {
	for _, i := range o.Items {
		if i.ItemID == item {
			return true
		}
	}
	return false
}

// steamTransaction reads an order. refunded is its status when Steam already gave it back.
func steamTransaction(c Credentials, o steamOrder, sandbox bool) (Transaction, string, error) {
	switch o.Status {
	case "Succeeded":
	case "Refunded", "PartialRefund", "Chargedback", "RefundedSuspectedFraud", "RefundedFriendlyFraud":
	case "Init", "Approved":
		return Transaction{}, "", reject(NotPurchased, "the order is %s: the game has not finalized it", o.Status)
	default:
		return Transaction{}, "", reject(NotPurchased, "the order is %s", o.Status)
	}
	if len(o.Items) == 0 || o.TransID == "" {
		return Transaction{}, "", reject(InvalidReceipt, "the order has no transaction or no items")
	}
	var amount, qty int64
	for _, i := range o.Items {
		amount += int64(i.Amount)
		qty += int64(i.Qty)
	}
	at, err := time.Parse(time.RFC3339, o.Time)
	if err != nil {
		at = time.Now()
	}
	country := strings.ToUpper(o.Country)
	if country == "UK" {
		country = "GB"
	}
	tx := Transaction{
		// transid is Steam's, and unique across Steam; the order id is the game's own.
		TransactionID: o.TransID,
		OrderID:       o.OrderID,
		AppID:         c.AppID,
		ProductID:     o.Items[0].ItemID,
		Kind:          UnknownKind,
		Quantity:      int(max(qty, 1)),
		PurchasedAt:   at.UTC(),
		// Amounts are in hundredths of the currency.
		AmountMicros:   amount * 10_000,
		Currency:       strings.ToUpper(o.Currency),
		PriceFromStore: true,
		Country:        country,
		Sandbox:        sandbox,
	}
	refunded := ""
	if o.Status != "Succeeded" {
		refunded = o.Status
	}
	return tx, refunded, nil
}

func steamRefundReason(status string) RefundReason {
	switch status {
	case "Chargedback", "RefundedFriendlyFraud":
		return RefundChargeback
	case "RefundedSuspectedFraud":
		return RefundFraud
	}
	return RefundCustomerRequest
}

// Refunds reads the orders whose state changed since a moment, from GetReport, and keeps the ones
// given back.
func (s *SteamStore) Refunds(ctx context.Context, c Credentials, since time.Time) ([]Refund, time.Time, error) {
	if c.AppID == "" || c.PublisherKey == "" {
		return nil, since, nil
	}
	var out []Refund
	next := since
	for page := 0; page < 50; page++ {
		q := c.steamQuery()
		q.Set("type", "GAMESALES")
		q.Set("time", next.UTC().Format(time.RFC3339))
		q.Set("maxresults", "1000")
		var params struct {
			Count  int          `json:"count"`
			Orders []steamOrder `json:"orders"`
		}
		ok, detail, err := s.call(ctx, false, "GetReport/v5", q, &params)
		if err != nil {
			return out, next, err
		}
		if !ok {
			return out, next, &Unavailable{Detail: "GetReport: " + detail}
		}
		advanced := next
		for _, o := range params.Orders {
			if at, err := time.Parse(time.RFC3339, o.Time); err == nil && at.After(advanced) {
				advanced = at
			}
			if reason := steamRefundReason(o.Status); o.Status != "Succeeded" && o.Status != "Init" &&
				o.Status != "Approved" && o.Status != "Failed" && o.TransID != "" {
				out = append(out, Refund{Platform: Steam, TransactionID: o.TransID, OrderID: o.OrderID,
					At: time.Now().UTC(), Reason: reason})
			}
		}
		if len(params.Orders) < 1000 || !advanced.After(next) {
			next = advanced
			break
		}
		next = advanced
	}
	return out, next, nil
}

// Test reads one line of the sales report, which needs the publisher key to own the app.
func (s *SteamStore) Test(ctx context.Context, c Credentials) (string, error) {
	if c.AppID == "" || c.PublisherKey == "" {
		return "", fmt.Errorf("%w: the Steam app id and publisher key are not set", ErrNeedsKey)
	}
	q := c.steamQuery()
	q.Set("type", "GAMESALES")
	q.Set("time", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339))
	q.Set("maxresults", "1")
	ok, detail, err := s.call(ctx, false, "GetReport/v5", q, nil)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: Steam refused: %s", ErrCredentials, detail)
	}
	return "The publisher key can read app " + c.AppID + "'s transactions.", nil
}
