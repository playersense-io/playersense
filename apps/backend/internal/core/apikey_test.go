package core_test

import (
	"strings"
	"testing"

	"playersense.dev/backend/internal/core"
)

func TestKeyFormat(t *testing.T) {
	for _, kind := range []string{core.KindProject, core.KindReporting, core.KindServer} {
		got, err := core.NewAPIKey(kind, "production")
		if err != nil {
			t.Fatalf("NewAPIKey(%q) = %v", kind, err)
		}

		public, private, err := core.ParseKey(got.Secret)
		if err != nil {
			t.Fatalf("ParseKey(%q) = %v", got.Secret, err)
		}
		if public != got.PublicID {
			t.Errorf("public half = %q, want %q", public, got.PublicID)
		}
		if !strings.HasPrefix(public, "ps_"+kind+"_live_") {
			t.Errorf("public half %q does not name its kind and environment", public)
		}
		if core.KindOf(public) != kind {
			t.Errorf("KindOf(%q) = %q, want %q", public, core.KindOf(public), kind)
		}

		// The stored digest must not contain the secret in any recoverable form.
		if strings.Contains(got.Hash, private) || strings.Contains(got.Hash, got.Secret) {
			t.Fatal("the stored hash contains the secret")
		}
		if !strings.HasPrefix(got.Hash, "$argon2id$") {
			t.Errorf("hash = %q, want a PHC-encoded argon2id digest", got.Hash)
		}
	}
}

func TestKeyEnvironmentIsVisible(t *testing.T) {
	// A development key pasted into a production build should be visibly wrong before it
	// is ever sent (§76).
	dev, _ := core.NewAPIKey(core.KindProject, "development")
	if !strings.Contains(dev.PublicID, "_development_") {
		t.Errorf("public half %q does not name the environment", dev.PublicID)
	}
}

func TestVerifySecret(t *testing.T) {
	got, err := core.NewAPIKey(core.KindProject, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, private, _ := core.ParseKey(got.Secret)

	if !core.VerifySecret(private, got.Hash) {
		t.Error("the minted secret does not verify against its own digest")
	}
	if core.VerifySecret(private+"x", got.Hash) {
		t.Error("a wrong secret verified")
	}
	if core.VerifySecret("", got.Hash) {
		t.Error("an empty secret verified")
	}
	// Two keys minted from the same inputs must not collide: the salt differs.
	other, _ := core.NewAPIKey(core.KindProject, "production")
	if other.Hash == got.Hash {
		t.Error("two keys produced the same digest; the salt is not random")
	}
	if core.VerifySecret(private, other.Hash) {
		t.Error("one key's secret verified against another key's digest")
	}
}

func TestMalformedKeysAreRefusedBeforeAnyLookup(t *testing.T) {
	// Nothing shaped wrong should reach a database query or an Argon2 comparison. This
	// is what keeps a guessing flood cheap (§71).
	for _, bad := range []string{
		"", ":", "nosep", "ps_project_live_abc", // no private half
		":private", "ps_project_live_abc:", // empty half
		"xx_project_live_abc:private", // wrong prefix
		"ps_unknown_live_abc:private", // not a kind we issue
		"ps_project_abc:private",      // too few segments
		"ps_project_live_a_b:private", // too many segments
	} {
		if _, _, err := core.ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) was accepted; it must be refused", bad)
		}
	}
}

func TestCacheKeyRevealsNothing(t *testing.T) {
	got, _ := core.NewAPIKey(core.KindProject, "production")

	key := core.CacheKey(got.Secret)
	if strings.Contains(key, got.Secret) {
		t.Fatal("the cache key contains the credential")
	}
	// Same credential, same key; different credential, different key.
	if key != core.CacheKey(got.Secret) {
		t.Error("the cache key is not stable")
	}
	other, _ := core.NewAPIKey(core.KindProject, "production")
	if key == core.CacheKey(other.Secret) {
		t.Error("two credentials share a cache key")
	}
}

func TestNeedsRehash(t *testing.T) {
	got, _ := core.NewAPIKey(core.KindProject, "production")
	if core.NeedsRehash(got.Hash) {
		t.Error("a freshly minted digest should not need rehashing")
	}
	// A digest written under weaker parameters is upgraded on next use, so raising the
	// cost later does not invalidate anyone's key.
	weak := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"
	if !core.NeedsRehash(weak) {
		t.Error("a weaker digest should be flagged for rehashing")
	}
	if !core.NeedsRehash("not a phc string") {
		t.Error("an unparseable digest should be flagged")
	}
}

func TestUnknownKindIsRefused(t *testing.T) {
	if _, err := core.NewAPIKey("publishable", "production"); err == nil {
		t.Error("a kind this build does not issue was minted")
	}
}

// TestGeneratedKeysAlwaysParse is a property test, and it exists because the first
// version of this code failed roughly one key in eight.
//
// The public half is parsed by splitting on underscores, and the random part was encoded
// with base64url — whose alphabet contains `_`. A key that happened to draw one became
// unparseable and was refused as invalid, intermittently, in a way that looked like a bad
// credential rather than a generator bug.
func TestGeneratedKeysAlwaysParse(t *testing.T) {
	// 300 is more than enough. The bug this catches made roughly one key in eight
	// unparseable, so the chance of 300 draws all passing by luck is about 10^-17.
	// Every iteration pays for an Argon2 hash, so a larger number buys nothing but a
	// slower suite.
	for i := range 300 {
		got, err := core.NewAPIKey(core.KindProject, "production")
		if err != nil {
			t.Fatalf("iteration %d: NewAPIKey = %v", i, err)
		}
		public, private, err := core.ParseKey(got.Secret)
		if err != nil {
			t.Fatalf("iteration %d: generated key %q does not parse: %v", i, got.Secret, err)
		}
		if public != got.PublicID {
			t.Fatalf("iteration %d: public half round-tripped as %q, want %q", i, public, got.PublicID)
		}
		if private == "" {
			t.Fatalf("iteration %d: empty private half", i)
		}
		// The separator must appear exactly where the parser expects it and nowhere else
		// in the public half.
		if strings.Count(public, "_") != 3 {
			t.Fatalf("iteration %d: public half %q has %d separators, want 3",
				i, public, strings.Count(public, "_"))
		}
	}
}
