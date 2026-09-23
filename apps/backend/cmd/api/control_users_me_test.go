package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/wallet"
)

// GET /v1/users/me: the person, where they belong, and — where their role may read billing — what
// each organization has to spend, live.

type meOut struct {
	Person struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	} `json:"person"`
	Organizations []struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
		Role struct {
			Key string `json:"key"`
		} `json:"role"`
		Balance *meBalance `json:"balance"`
	} `json:"organizations"`
}

type meBalance struct {
	State           string `json:"state"`
	AvailableMicros int64  `json:"available_micros"`
	Available       string `json:"available"`
	OwedMicros      int64  `json:"owed_micros"`
	PendingMicros   int64  `json:"pending_micros"`
}

func (w *money) me(t *testing.T, who fbtest.Person) meOut {
	t.Helper()
	return testclient.Decoded[meOut](w.as(t, who).Get("/v1/users/me").AssertStatus(http.StatusOK))
}

// entry is one organization in a /me response, by slug.
func entry(t *testing.T, me meOut, slug string) (found bool, role string, balance *meBalance) {
	t.Helper()
	for _, org := range me.Organizations {
		if org.Slug == slug {
			return true, org.Role.Key, org.Balance
		}
	}
	return false, "", nil
}

// The balance on /me is the billing page's live headline: credit left after usage not yet settled.
func TestMeShowsTheLiveBalance(t *testing.T) {
	w := newWallet(t)
	w.grant(t, map[string]any{"kind": wallet.Promo, "amount_micros": dollars(10), "description": "Launch credit"})
	w.grant(t, map[string]any{"kind": wallet.Purchase, "amount_micros": dollars(30), "description": "Top-up"})
	w.queue(t, dollars(15))

	me := w.me(t, w.owner)
	if me.Person.Email != w.owner.Email || !me.Person.EmailVerified {
		t.Errorf("person = %+v; want %s, verified", me.Person, w.owner.Email)
	}
	found, role, balance := entry(t, me, w.slug)
	if !found || role != authz.RoleOwner {
		t.Fatalf("%s: found %v, role %q; want the owner's organization listed", w.slug, found, role)
	}
	if balance == nil {
		t.Fatal("no balance for the owner")
	}
	if balance.AvailableMicros != dollars(25) || balance.Available != "$25.00" ||
		balance.OwedMicros != 0 || balance.PendingMicros != dollars(15) || balance.State != "active" {
		t.Errorf("balance = %+v; want $25.00 available with $15 pending", *balance)
	}
	if live := w.live(t).Live; live.AvailableMicros != balance.AvailableMicros || live.OwedMicros != balance.OwedMicros {
		t.Errorf("/me says %s available, %s owed; the billing page says %s and %s",
			billing.USD(balance.AvailableMicros), billing.USD(balance.OwedMicros),
			billing.USD(live.AvailableMicros), billing.USD(live.OwedMicros))
	}

	// Past the credit, the headline is what it owes.
	w.queue(t, dollars(40))
	_, _, balance = entry(t, w.me(t, w.owner), w.slug)
	if balance == nil || balance.AvailableMicros != 0 || balance.OwedMicros != dollars(15) {
		t.Errorf("balance = %+v; want nothing available and $15 owed", balance)
	}
}

// A balance shows exactly where GET …/billing would answer: the billing role sees it, a developer
// sees the organization and a null balance.
func TestMeShowsABalanceOnlyToThoseWhoMayReadBilling(t *testing.T) {
	w := newWallet(t)
	w.grant(t, map[string]any{"kind": wallet.Purchase, "amount_micros": dollars(30), "description": "Top-up"})
	biller := person(t)
	w.join(t, w.slug, biller, authz.RoleBilling, true)
	developer := person(t)
	w.join(t, w.slug, developer, authz.RoleDeveloper, true)

	if found, role, balance := entry(t, w.me(t, biller), w.slug); !found || role != authz.RoleBilling ||
		balance == nil || balance.AvailableMicros != dollars(30) {
		t.Errorf("billing role: found %v, role %q, balance %+v; want $30", found, role, balance)
	}
	found, role, balance := entry(t, w.me(t, developer), w.slug)
	if !found || role != authz.RoleDeveloper {
		t.Fatalf("developer: found %v, role %q; want the organization listed", found, role)
	}
	if balance != nil {
		t.Errorf("a developer was shown the balance %+v, which GET …/billing refuses them", *balance)
	}
	w.as(t, developer).Get(w.base() + "/billing").AssertStatus(http.StatusForbidden)
}

// Only a signed-in person has a /me.
func TestMeNeedsASignIn(t *testing.T) {
	w := newWallet(t)
	testclient.New(t, w.app).Get("/v1/users/me").AssertStatus(http.StatusUnauthorized)
}
