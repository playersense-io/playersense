package stores

import (
	"context"
	"sync"
	"time"
)

// Fake is a store that answers from a table, for tests and local development: a receipt is found
// by its signed transaction, transaction id, purchase token or order id, whichever it carries.
type Fake struct {
	mu           sync.Mutex
	transactions map[string]Transaction
	rejections   map[string]*Rejection
	refunds      []Refund
	down         bool
	calls        int
}

// NewFake is an empty fake store.
func NewFake() *Fake {
	return &Fake{transactions: map[string]Transaction{}, rejections: map[string]*Rejection{}}
}

func receiptKey(r Receipt) string {
	for _, k := range []string{r.SignedTransaction, r.TransactionID, r.PurchaseToken, r.OrderID} {
		if k != "" {
			return k
		}
	}
	return ""
}

// Sells makes a receipt verify as a transaction.
func (f *Fake) Sells(receipt string, tx Transaction) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transactions[receipt] = tx
}

// Rejects makes a receipt turn away.
func (f *Fake) Rejects(receipt string, code Code, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejections[receipt] = &Rejection{Code: code, Detail: detail}
}

// Refunded adds a refund to what the store says it gave back.
func (f *Fake) Refunded(r Refund) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds = append(f.refunds, r)
}

// Down makes every call fail as a store that cannot be reached.
func (f *Fake) Down(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// Calls is how many verifications reached the store.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *Fake) Verify(_ context.Context, _ Credentials, r Receipt, t *Trace) (Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.down {
		t.step("lookup", false, "the store is down")
		return Transaction{}, &Unavailable{Status: 503, Detail: "the fake store is down"}
	}
	key := receiptKey(r)
	if rej := f.rejections[key]; rej != nil {
		t.step("lookup", false, "%s", rej.Detail)
		return Transaction{}, rej
	}
	tx, ok := f.transactions[key]
	if !ok {
		t.step("lookup", false, "unknown receipt")
		return Transaction{}, reject(InvalidReceipt, "the store knows no such purchase")
	}
	if r.ProductID != "" && tx.ProductID != r.ProductID {
		return Transaction{}, reject(ProductMismatch, "the receipt bought %q, not %q", tx.ProductID, r.ProductID)
	}
	if !tx.PriceFromStore && tx.AmountMicros == 0 {
		tx.AmountMicros, tx.Currency = r.PriceMicros, r.Currency
	}
	t.step("lookup", true, "found %s", tx.TransactionID)
	return tx, nil
}

func (f *Fake) Refunds(_ context.Context, _ Credentials, since time.Time) ([]Refund, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, since, &Unavailable{Status: 503, Detail: "the fake store is down"}
	}
	// As a store's list does: everything given back since the moment asked about, however many
	// times it is asked.
	var out []Refund
	for _, r := range f.refunds {
		if !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out, time.Now().UTC(), nil
}

func (f *Fake) Test(context.Context, Credentials) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return "", &Unavailable{Status: 503, Detail: "the fake store is down"}
	}
	return "The fake store accepted the credentials.", nil
}
