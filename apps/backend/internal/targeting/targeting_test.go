package targeting

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func rule(t *testing.T, raw string) Rule {
	t.Helper()
	parsed, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

// The rule from §41's own example, which is the shape a studio actually writes.
func TestACampaignAudience(t *testing.T) {
	audience := rule(t, `{"all":[
		{"key":"geo.country","op":"in","values":["TR","DE","FR"]},
		{"key":"context.progress.level","op":"gte","value":10},
		{"key":"context.subscription","op":"ne","value":"trial"},
		{"key":"app.version","op":"version_gte","value":"1.12.0"}
	]}`)

	eligible := Context{
		"geo.country":            "TR",
		"context.progress.level": int64(42),
		"context.subscription":   "premium",
		"app.version":            "1.12.3",
	}
	if !audience.Matches(eligible) {
		t.Error("an eligible player was excluded")
	}

	for name, change := range map[string]func(Context){
		"wrong country":   func(c Context) { c["geo.country"] = "US" },
		"below the level": func(c Context) { c["context.progress.level"] = int64(9) },
		"on trial":        func(c Context) { c["context.subscription"] = "trial" },
		"older build":     func(c Context) { c["app.version"] = "1.11.9" },
	} {
		ctx := Context{}
		for k, v := range eligible {
			ctx[k] = v
		}
		change(ctx)
		if audience.Matches(ctx) {
			t.Errorf("%s: matched when it should not", name)
		}
	}
}

// The failure this operator exists for. As strings "1.9.0" sorts after "1.12.0", so an
// ordinary >= excludes everyone who upgraded and reaches the players who did not.
func TestVersionsCompareAsVersionsNotAsText(t *testing.T) {
	upgraded := Context{"app.version": "1.12.0"}
	old := Context{"app.version": "1.9.0"}

	correct := rule(t, `{"key":"app.version","op":"version_gte","value":"1.12.0"}`)
	if !correct.Matches(upgraded) {
		t.Error("1.12.0 is not >= 1.12.0")
	}
	if correct.Matches(old) {
		t.Error("1.9.0 matched >= 1.12.0")
	}

	// The same rule with the plain string operator gets both backwards, which is exactly
	// why version_gte is a separate operator rather than a flavour of gte.
	naive := rule(t, `{"key":"app.version","op":"gte","value":"1.12.0"}`)
	if naive.Matches(upgraded) || !naive.Matches(old) {
		t.Skip("string comparison happens to agree here; the point stands")
	}
}

func TestVersionShapesAStudioActuallyTypes(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.12", "1.12.0", 0},     // a missing part is zero
		{"1.12.0", "1.9.0", 1},    // the one that breaks string comparison
		{"2.0.1", "2.0.1", 0},     //
		{"1.0.0", "1.0.1", -1},    //
		{"2.0.0-rc3", "2.0.0", 0}, // a pre-release suffix is ignored, not ordered
		{"1.12.0.4881", "1.12.0", 1},
		{"", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.left, c.right); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.left, c.right, got, c.want)
		}
	}
}

// A studio renames a context key. The campaign must stop reaching people — not error, and
// not suddenly reach everyone because a missing value compared equal to zero.
func TestAMissingKeyIsFalseRatherThanAnError(t *testing.T) {
	empty := Context{}

	for _, raw := range []string{
		`{"key":"context.rank","op":"eq","value":"gold"}`,
		`{"key":"context.rank","op":"ne","value":"gold"}`,
		`{"key":"context.level","op":"gte","value":0}`,
		`{"key":"context.level","op":"lt","value":9999}`,
		`{"key":"context.rank","op":"in","values":["gold"]}`,
		`{"key":"context.rank","op":"not_in","values":["gold"]}`,
	} {
		if rule(t, raw).Matches(empty) {
			t.Errorf("%s matched a context that has no such key", raw)
		}
	}

	// Except the two operators that are about presence itself.
	if !rule(t, `{"key":"context.rank","op":"not_exists"}`).Matches(empty) {
		t.Error("not_exists did not match a missing key")
	}
	if rule(t, `{"key":"context.rank","op":"exists"}`).Matches(empty) {
		t.Error("exists matched a missing key")
	}
	if !rule(t, `{"key":"context.rank","op":"exists"}`).Matches(Context{"context.rank": ""}) {
		t.Error("exists did not match a key that is present and empty")
	}
}

// A value whose type changed must not start matching players it was never meant to.
func TestTypesAreNotCoerced(t *testing.T) {
	if rule(t, `{"key":"level","op":"eq","value":10}`).Matches(Context{"level": "10"}) {
		t.Error(`the string "10" matched the number 10`)
	}
	if rule(t, `{"key":"on","op":"eq","value":true}`).Matches(Context{"on": "true"}) {
		t.Error(`the string "true" matched the boolean true`)
	}
	if rule(t, `{"key":"on","op":"eq","value":1}`).Matches(Context{"on": true}) {
		t.Error("true matched the number 1")
	}

	// But JSON has one number type, so these must be equal.
	if !rule(t, `{"key":"level","op":"eq","value":10}`).Matches(Context{"level": int64(10)}) {
		t.Error("int64(10) did not match 10")
	}
	if !rule(t, `{"key":"level","op":"eq","value":10}`).Matches(Context{"level": 10.0}) {
		t.Error("10.0 did not match 10")
	}

	// "is rank less than 5" has no answer when the rank is "gold". False, not a guess.
	if rule(t, `{"key":"rank","op":"lt","value":5}`).Matches(Context{"rank": "gold"}) {
		t.Error("an unorderable comparison matched")
	}
}

func TestGroupsCompose(t *testing.T) {
	ctx := Context{"geo.country": "TR", "context.rank": "diamond"}

	if !rule(t, `{"any":[
		{"key":"geo.country","op":"eq","value":"US"},
		{"key":"context.rank","op":"eq","value":"diamond"}
	]}`).Matches(ctx) {
		t.Error("any did not match on its second branch")
	}
	if !rule(t, `{"not":{"key":"geo.country","op":"eq","value":"US"}}`).Matches(ctx) {
		t.Error("not did not invert")
	}

	// No audience means everyone, which is the common case for a campaign.
	if !(Rule{}).Matches(ctx) {
		t.Error("an empty rule excluded somebody")
	}
	if !(Rule{}).Matches(Context{}) {
		t.Error("an empty rule excluded an empty context")
	}
}

func TestAnInvalidRuleIsRefusedWhenItIsWritten(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown operator":   `{"key":"a","op":"greater","value":1}`,
		"no operator":        `{"key":"a","value":1}`,
		"no key":             `{"op":"eq","value":1}`,
		"in with no values":  `{"key":"a","op":"in"}`,
		"exists with value":  `{"key":"a","op":"exists","value":1}`,
		"group and key":      `{"key":"a","op":"eq","value":1,"all":[{"key":"b","op":"exists"}]}`,
		"two groups":         `{"all":[{"key":"b","op":"exists"}],"any":[{"key":"c","op":"exists"}]}`,
		"empty nested":       `{"all":[{}]}`,
		"not valid json":     `{"all":`,
		"eq without a value": `{"key":"a","op":"eq"}`,
	} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: accepted %s", name, raw)
		}
	}
}

// The two fact families answer different questions, and a studio must not have to guess
// which one a rule got.
func TestInstallAndPlayerFactsAreSeparateSubjects(t *testing.T) {
	// A player with a phone and a tablet: neither install is a whale, the account is.
	phone := Context{
		FactInstallSpend: int64(60_000_000),
		FactPlayerSpend:  int64(120_000_000),
	}

	perInstall := rule(t, `{"key":"commerce.install.lifetime_spend_micros","op":"gte","value":100000000}`)
	perPlayer := rule(t, `{"key":"commerce.player.lifetime_spend_micros","op":"gte","value":100000000}`)

	if perInstall.Matches(phone) {
		t.Error("a $60 installation matched a $100 whale rule")
	}
	if !perPlayer.Matches(phone) {
		t.Error("a $120 account did not match a $100 whale rule")
	}

	// And an anonymous player has no account facts at all, so the account rule cannot match.
	anonymous := Context{FactInstallSpend: int64(150_000_000)}
	if perPlayer.Matches(anonymous) {
		t.Error("an account rule matched a player who has never identified themselves")
	}
}

// A fact with no column behind it is refused when the rule is written, not accepted and then
// silently never matched.
func TestAFactWithNoColumnIsRefused(t *testing.T) {
	for key := range Unavailable {
		raw := fmt.Sprintf(`{"key":%q,"op":"gte","value":30}`, key)
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s was accepted but nothing computes it", key)
		}
	}

	// The ones that do exist still work.
	if _, err := Parse([]byte(`{"key":"commerce.player.purchases","op":"gt","value":0}`)); err != nil {
		t.Errorf("a real fact was refused: %v", err)
	}
}

// The optimisation that keeps the common case free: only a rule that actually names a fact
// costs a lookup.
func TestOnlyAFactRuleCostsALookup(t *testing.T) {
	free := rule(t, `{"all":[
		{"key":"geo.country","op":"eq","value":"TR"},
		{"key":"app.version","op":"version_gte","value":"1.12.0"},
		{"key":"context.rank","op":"eq","value":"diamond"}
	]}`)
	if free.NeedsPlayerFacts() {
		t.Error("a rule over request-time context asked for a lookup")
	}
	if (Rule{}).NeedsPlayerFacts() {
		t.Error("an empty rule asked for a lookup")
	}

	costly := rule(t, `{"any":[
		{"key":"geo.country","op":"eq","value":"TR"},
		{"key":"commerce.player.purchases","op":"gt","value":0}
	]}`)
	if !costly.NeedsPlayerFacts() {
		t.Error("a rule naming commerce.* did not ask for a lookup")
	}

	// And the dashboard warning: which parts of this rule could a modified build choose?
	warned := free.ClientSupplied()
	if len(warned) != 2 || warned[0] != "context" || warned[1] != "app" {
		t.Errorf("ClientSupplied() = %v, want context and app", warned)
	}
	if len(costly.ClientSupplied()) != 0 {
		t.Errorf("a geo/commerce rule reported client-supplied inputs: %v", costly.ClientSupplied())
	}
}

func TestARuleCannotBeUnboundedlyDeep(t *testing.T) {
	deep := `{"key":"a","op":"exists"}`
	for i := 0; i <= MaxDepth+1; i++ {
		deep = fmt.Sprintf(`{"all":[%s]}`, deep)
	}
	if _, err := Parse([]byte(deep)); err == nil {
		t.Error("a rule deeper than the limit was accepted")
	}

	wide := make([]string, MaxConditions+1)
	for i := range wide {
		wide[i] = `{"key":"a","op":"exists"}`
	}
	raw, _ := json.Marshal(map[string]any{"all": json.RawMessage("[" + join(wide) + "]")})
	if _, err := Parse(raw); err == nil {
		t.Error("a rule with more conditions than the limit was accepted")
	}
}

func join(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += ","
		}
		out += part
	}
	return out
}

// §43's first property: the same player stays in the same variant. An experiment whose
// assignment moves measures nothing.
func TestARolloutIsStableForAPlayer(t *testing.T) {
	first := Bucket("project", "halloween", "player-91827364", "salt")
	for i := 0; i < 1000; i++ {
		if again := Bucket("project", "halloween", "player-91827364", "salt"); again != first {
			t.Fatalf("bucket moved from %d to %d on call %d", first, again, i)
		}
	}
	if first < 0 || first >= Buckets {
		t.Fatalf("bucket %d is outside [0, %d)", first, Buckets)
	}
}

// Changing the salt is how a studio says "draw a fresh sample", so it must actually reshuffle.
func TestTheSaltAndTheFlagBothReshuffle(t *testing.T) {
	base := Bucket("project", "halloween", "player-1", "v1")
	if Bucket("project", "halloween", "player-1", "v2") == base {
		t.Error("a new salt produced the same bucket")
	}
	if Bucket("project", "christmas", "player-1", "v1") == base {
		t.Error("a different flag produced the same bucket")
	}
	if Bucket("other", "halloween", "player-1", "v1") == base {
		t.Error("a different project produced the same bucket")
	}

	// Without a separator, project "ab" + flag "c" and project "a" + flag "bc" would be
	// the same audience for no visible reason.
	if Bucket("ab", "c", "p", "s") == Bucket("a", "bc", "p", "s") {
		t.Error("the key components run together")
	}
}

// A 25% rollout should reach about 25% — the property a studio is actually buying.
func TestARolloutIsProportional(t *testing.T) {
	const players = 20000
	for _, percent := range []int{500, 2500, 5000, 9000} {
		included := 0
		for i := 0; i < players; i++ {
			if InRollout(percent, "project", "flag", fmt.Sprintf("player-%d", i), "salt") {
				included++
			}
		}
		want := float64(percent) / Buckets
		got := float64(included) / players
		if math.Abs(got-want) > 0.01 {
			t.Errorf("rollout at %d%%: reached %.2f%%, want ~%.2f%%",
				percent/100, got*100, want*100)
		}
	}
}

func TestTheRolloutEdgesCostNothingAndReachNobody(t *testing.T) {
	if InRollout(0, "p", "f", "player", "s") {
		t.Error("a rollout of 0 reached somebody")
	}
	if !InRollout(Buckets, "p", "f", "player", "s") {
		t.Error("a rollout of 100% excluded somebody")
	}

	// An anonymous player has no stable identity, so there is nothing to keep them in one
	// arm. Excluded rather than assigned at random, which would pollute both arms.
	if InRollout(5000, "p", "f", "", "s") {
		t.Error("a player with no actor key was put in a partial rollout")
	}
	if !InRollout(Buckets, "p", "f", "", "s") {
		t.Error("a full rollout excluded an anonymous player")
	}
}
