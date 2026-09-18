package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/configversion"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/firebase/fbtest"
)

// The authoring surface: §41's three tables, which nothing could write until these routes
// existed. The publish endpoint had been reading them since the day it shipped.

// authoring is one organization, one project and the base path of each resource.
type authoring struct {
	*stack
	owner    fbtest.Person
	orgSlug  string
	projSlug string
}

func newAuthoring(t *testing.T) *authoring {
	t.Helper()
	s := newStack(t)
	owner := person(t)
	orgSlug := s.createOrg(t, owner)
	return &authoring{
		stack: s, owner: owner, orgSlug: orgSlug,
		projSlug: s.createProject(t, owner, orgSlug),
	}
}

func (a *authoring) base() string {
	return "/v1/organizations/" + a.orgSlug + "/projects/" + a.projSlug
}

func (a *authoring) client(t *testing.T) *testclient.Client {
	t.Helper()
	return a.as(t, a.owner)
}

// TestABoolFlagCanBeStored is the regression for the reason this could not have worked.
//
// `fallback` was declared `map[string]any`, and `false` does not unmarshal into a map — so the
// commonest kind of flag, the one every test in internal/liveconfig uses, could not be written at
// all. The column was jsonb either way; only Go's idea of it was wrong, and nothing had ever
// written a row to find out.
//
// All four kinds, because the bug was a whole class: a string, a number and a JSON object each
// went through the same field.
func TestABoolFlagCanBeStored(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)

	for _, tc := range []struct {
		kind     string
		fallback string
	}{
		{"bool", `false`},
		{"bool", `true`},
		{"string", `"none"`},
		{"number", `50`},
		{"json", `{"rows":3,"title":"Shop"}`},
		{"json", `[1,2,3]`},
	} {
		key := unique("flag")
		created := testclient.Decoded[flagOut](client.Post(a.base()+"/flags",
			testclient.RawJSON(`{"key":"`+key+`","name":"A flag","kind":"`+tc.kind+
				`","fallback":`+tc.fallback+`}`)).
			AssertStatus(http.StatusCreated))

		if got := compact(t, created.Fallback); got != tc.fallback {
			t.Errorf("%s flag stored fallback %s, want %s", tc.kind, got, tc.fallback)
		}

		// And it survives the round trip to the database, which is the half a response
		// built from the in-memory row would not prove.
		read := testclient.Decoded[flagOut](client.Get(a.base() + "/flags/" + key).
			AssertStatus(http.StatusOK))
		if got := compact(t, read.Fallback); got != tc.fallback {
			t.Errorf("%s flag read back fallback %s, want %s", tc.kind, got, tc.fallback)
		}
	}
}

// TestAFlagRefusesAValueOfTheWrongKind is the check FeatureFlag's own comment promised — "a
// dashboard can refuse `true` in a number flag while somebody is typing it, rather than at the
// point a game reads it" — and until these routes existed there was nowhere to make it.
//
// The rule case is the one that matters more. A wrong fallback is visible in the flag's own
// form; a wrong value on the fourth rule of a rollout is not, and the SDK's typed reader is what
// discovers it, in a build that has already shipped.
func TestAFlagRefusesAValueOfTheWrongKind(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)

	refused := client.Post(a.base()+"/flags",
		testclient.RawJSON(`{"key":"`+unique("flag")+`","name":"Max level","kind":"number","fallback":true}`)).
		AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "number flag takes a number") {
		t.Errorf("the refusal does not say what a number flag takes: %s", refused)
	}

	// A string where a number belongs, on the second rule. The position is named, one-based,
	// the same way the compiler names it.
	refused = client.Post(a.base()+"/flags", testclient.RawJSON(`{
		"key":"`+unique("flag")+`","name":"Max level","kind":"number","fallback":50,
		"rules":[
			{"value":60},
			{"value":"seventy"}
		]}`)).
		AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "rule 2") {
		t.Errorf("the refusal does not name which rule was wrong: %s", refused)
	}

	// Absence is refused for every kind, including json: a flag exists to answer without the
	// network, so a fallback of null is a flag with no answer.
	client.Post(a.base()+"/flags",
		testclient.RawJSON(`{"key":"`+unique("flag")+`","name":"Shop","kind":"json","fallback":null}`)).
		AssertStatus(http.StatusUnprocessableEntity)
}

// TestTheKindCannotBeChanged is the immutability an installed build depends on.
//
// A game calls a typed reader on the key. A flag that was a bool yesterday and is a number today
// is a document the shipped build cannot read, and a build is the one thing a backend cannot
// migrate. The update body has no `kind` member at all, so sending one is a 422 from the binder
// rather than a 200 that changed nothing — which is the distinction that matters, because
// somebody will send it and believe the response.
func TestTheKindCannotBeChanged(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	key := unique("flag")

	client.Post(a.base()+"/flags",
		testclient.RawJSON(`{"key":"`+key+`","name":"HUD v2","kind":"bool","fallback":false}`)).
		AssertStatus(http.StatusCreated)

	client.Put(a.base()+"/flags/"+key,
		testclient.RawJSON(`{"name":"HUD v2","kind":"number","fallback":1}`)).
		AssertStatus(http.StatusUnprocessableEntity)

	// Without it the update is ordinary, and the kind is still what it was.
	updated := testclient.Decoded[flagOut](client.Put(a.base()+"/flags/"+key,
		testclient.RawJSON(`{"name":"HUD v2","fallback":true}`)).
		AssertStatus(http.StatusOK))
	if updated.Kind != "bool" {
		t.Errorf("kind = %q, want bool", updated.Kind)
	}
}

// TestASegmentCycleIsRefusedByTheWriteThatClosesIt is the argument for validating here at its
// strongest.
//
// A -> B -> A cannot exist until the write that closes the loop, so that write is the only moment
// at which one person, one rule and one error coincide. Afterwards the project simply does not
// publish, and nothing points at which of the two rules to change.
func TestASegmentCycleIsRefusedByTheWriteThatClosesIt(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	segments := a.base() + "/segments"

	// The simple case first: a segment naming itself.
	refused := client.Post(segments, testclient.RawJSON(
		`{"key":"loop","name":"Loop","rule":{"segment":"loop"}}`)).
		AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "refers to itself") {
		t.Errorf("the refusal does not say it is a cycle: %s", refused)
	}

	// And the one only an update can create. B is plain, A names B, and then B is edited to
	// name A — which is the edit that has to fail.
	client.Post(segments, testclient.RawJSON(
		`{"key":"payers","name":"Payers","rule":{"key":"commerce.revenue","op":"gt","value":0}}`)).
		AssertStatus(http.StatusCreated)
	client.Post(segments, testclient.RawJSON(
		`{"key":"whales","name":"Whales","rule":{"all":[{"segment":"payers"},{"key":"commerce.revenue","op":"gte","value":100}]}}`)).
		AssertStatus(http.StatusCreated)

	refused = client.Put(segments+"/payers", testclient.RawJSON(
		`{"name":"Payers","rule":{"segment":"whales"}}`)).
		AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "refers to itself") {
		t.Errorf("closing the loop was not refused as a cycle: %s", refused)
	}

	// The rule that was there is still there: a refused write changes nothing.
	kept := testclient.Decoded[segmentOut](client.Get(segments + "/payers"))
	if !strings.Contains(string(kept.Rule), "commerce.revenue") {
		t.Errorf("the refused update overwrote the rule: %s", kept.Rule)
	}
}

// TestAnUnknownSegmentIsRefusedAtTheWrite moves the message to the person who can act on it.
//
// The publish says "segment \"whals\" does not exist" too — but it says it to whoever holds
// liveops.publish, hours later, about a rule they did not write. Here it is a 422 to the request
// that made the typo.
func TestAnUnknownSegmentIsRefusedAtTheWrite(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)

	for _, path := range []string{"/segments", "/flags", "/liveops-events"} {
		var body string
		switch path {
		case "/segments":
			body = `{"key":"` + unique("seg") + `","name":"Seg","rule":{"segment":"whals"}}`
		case "/flags":
			body = `{"key":"` + unique("flag") + `","name":"Flag","kind":"bool","fallback":false,
				"rules":[{"audience":{"segment":"whals"},"value":true}]}`
		default:
			body = `{"key":"` + unique("ev") + `","name":"Event","kind":"offer",
				"schedule":{"mode":"daily","anchor":"2026-01-01T18:00:00Z","duration":"2h"},
				"audience":{"segment":"whals"},"payload":{"discount":10}}`
		}
		refused := client.Post(a.base()+path, testclient.RawJSON(body)).
			AssertStatus(http.StatusUnprocessableEntity)
		if !strings.Contains(refused.String(), "whals") {
			t.Errorf("%s: the refusal does not name the missing segment: %s", path, refused)
		}
	}
}

// TestAMalformedRuleIsRefusedWithTheOperatorNamed keeps internal/targeting the single authority
// on the grammar.
//
// The wire type is raw JSON precisely so that this package does not carry a second, weaker
// description of the rule language — and the proof is that the refusal here is the parser's own
// sentence.
func TestAMalformedRuleIsRefusedWithTheOperatorNamed(t *testing.T) {
	a := newAuthoring(t)

	refused := a.client(t).Post(a.base()+"/segments", testclient.RawJSON(
		`{"key":"`+unique("seg")+`","name":"Seg","rule":{"key":"app.version","op":"greater","value":"1.12.0"}}`)).
		AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "greater") {
		t.Errorf("the refusal does not name the unknown operator: %s", refused)
	}
}

// TestAScheduleWithAUnitlessDurationIsRefused is the mistake that would otherwise publish.
//
// A studio typing 5 for "five days" against a server reading nanoseconds is a happy-hour event
// lasting five billionths of a second, and nothing downstream would complain — the schedule is
// structurally perfect. So the wire type is a string with a unit, and a number is not one.
func TestAScheduleWithAUnitlessDurationIsRefused(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)

	refused := client.Post(a.base()+"/liveops-events", testclient.RawJSON(`{
		"key":"`+unique("ev")+`","name":"Happy hour","kind":"offer",
		"schedule":{"mode":"daily","anchor":"2026-01-01T18:00:00Z","duration":"5"},
		"payload":{"discount":10}}`)).
		AssertStatus(http.StatusUnprocessableEntity)
	if !strings.Contains(refused.String(), "unit") {
		t.Errorf("the refusal does not tell them to write a unit: %s", refused)
	}
}

// TestAnEventAnswersWithConcreteWindows is why the response carries dates.
//
// "The first five days of every month" is a mode, an anchor and a duration, and nobody can read
// back from those three whether they got it right. The expansion is the same code the config path
// runs, so these are the dates a player will get rather than a second opinion about them.
func TestAnEventAnswersWithConcreteWindows(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)

	created := testclient.Decoded[eventOut](client.Post(a.base()+"/liveops-events", testclient.RawJSON(`{
		"key":"`+unique("ev")+`","name":"Double XP weekend","kind":"seasonal_event",
		"schedule":{"mode":"weekly","anchor":"2026-01-02T18:00:00Z","duration":"48h","weekdays":[5]},
		"payload":{"multiplier":2}}`)).
		AssertStatus(http.StatusCreated))

	if len(created.Occurrences) == 0 {
		t.Fatal("a weekly event must answer with the windows it produces")
	}
	for _, window := range created.Occurrences {
		if !window.End.After(window.Start) {
			t.Errorf("window %v does not end after it starts", window)
		}
	}

	// Created as a draft, because publishing reads only the active ones: an event must not be
	// able to reach a player by being typed.
	if created.Status != "draft" {
		t.Errorf("status = %q, want draft", created.Status)
	}

	// A payload PlayerSense does not understand, stored as it was written. The scalar is the
	// case a map-typed column could not hold, which is the same bug `fallback` had.
	if got := compact(t, created.Payload); got != `{"multiplier":2}` {
		t.Errorf("payload = %s, want {\"multiplier\":2}", got)
	}
}

// TestArchivingIsNotDeleting is the property every one of these tables depends on.
//
// A published version holds each rule inlined, so a deleted row would leave a running
// configuration nobody can explain. For a flag it is worse: a key that vanishes from the next
// document is a key whose SDK falls back, and the fallback is the value a studio chose for an
// offline device rather than for this.
func TestArchivingIsNotDeleting(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	key := unique("flag")

	client.Post(a.base()+"/flags",
		testclient.RawJSON(`{"key":"`+key+`","name":"Shop v2","kind":"bool","fallback":false}`)).
		AssertStatus(http.StatusCreated)

	archived := testclient.Decoded[flagOut](client.Delete(a.base() + "/flags/" + key).
		AssertStatus(http.StatusOK))
	if !archived.Archived {
		t.Error("DELETE did not archive the flag")
	}

	// Still readable, and still listed. The row is the only thing that can say what the flag
	// in version 7 was called.
	client.Get(a.base() + "/flags/" + key).AssertStatus(http.StatusOK)
	listed := testclient.Decoded[flagListOut](client.Get(a.base() + "/flags"))
	found := false
	for _, flag := range listed.Flags {
		if flag.Key == key {
			found = true
		}
	}
	if !found {
		t.Error("an archived flag disappeared from the list")
	}

	// Twice is a conflict rather than a silent success, the same way revoking a key twice is.
	client.Delete(a.base() + "/flags/" + key).AssertStatus(http.StatusConflict)

	// And an ordinary edit does not resurrect it. A PUT is a replace, and `archived` is the
	// one field exempt: a dashboard correcting the name without echoing the member back would
	// otherwise return the flag to the next published document, which a shipped game starts
	// reading again — a change to live behaviour that nobody asked for and no response
	// mentions.
	edited := testclient.Decoded[flagOut](client.Put(a.base()+"/flags/"+key,
		testclient.RawJSON(`{"name":"Shop v2 renamed","fallback":false}`)).
		AssertStatus(http.StatusOK))
	if !edited.Archived {
		t.Error("editing an archived flag un-archived it")
	}

	// Un-archiving is said out loud, and then it happens.
	restored := testclient.Decoded[flagOut](client.Put(a.base()+"/flags/"+key,
		testclient.RawJSON(`{"name":"Shop v2","fallback":false,"archived":false}`)).
		AssertStatus(http.StatusOK))
	if restored.Archived {
		t.Error("an explicit archived:false did not un-archive the flag")
	}
}

// TestTheAuthoringSurfaceSplitsReadingFromWriting is the permission split, observed.
//
// GROWTH MANAGER holds liveops.read and no write; DEVELOPER holds both and no publish. The
// second is the load-bearing one: it is what lets a junior draft a rollout and hold no power to
// ship it, and it is the reason this surface exists separately from the publish at all.
func TestTheAuthoringSurfaceSplitsReadingFromWriting(t *testing.T) {
	a := newAuthoring(t)

	growth := person(t)
	a.join(t, a.orgSlug, growth, authz.RoleGrowthManager, true)
	developer := person(t)
	a.join(t, a.orgSlug, developer, authz.RoleDeveloper, true)

	newFlag := testclient.RawJSON(
		`{"key":"` + unique("flag") + `","name":"Shop v2","kind":"bool","fallback":false}`)

	// Reads, for both.
	a.as(t, growth).Get(a.base() + "/flags").AssertStatus(http.StatusOK)
	a.as(t, developer).Get(a.base() + "/flags").AssertStatus(http.StatusOK)

	// The write, for one of them, and the refusal names what is missing.
	refused := a.as(t, growth).Post(a.base()+"/flags", newFlag).
		AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), string(authz.LiveOpsWrite)) {
		t.Errorf("the refusal does not name the missing permission: %s", refused)
	}
	a.as(t, developer).Post(a.base()+"/flags", newFlag).AssertStatus(http.StatusCreated)

	// And the developer who just wrote it still cannot ship it.
	refused = a.as(t, developer).Post(a.base()+"/config/production/publish",
		testclient.JSON(map[string]string{"environment": "production"})).
		AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), string(authz.LiveOpsPublish)) {
		t.Errorf("the refusal does not name liveops.publish: %s", refused)
	}
}

// TestAnotherStudiosFlagsAreInvisible is the tenancy check.
//
// Not forbidden — absent. A 403 would confirm the project is real, which turns a key into a way
// to ask whether another studio has a flag by that name.
func TestAnotherStudiosFlagsAreInvisible(t *testing.T) {
	a := newAuthoring(t)
	key := unique("flag")
	a.client(t).Post(a.base()+"/flags",
		testclient.RawJSON(`{"key":"`+key+`","name":"Secret","kind":"bool","fallback":true}`)).
		AssertStatus(http.StatusCreated)

	stranger := person(t)
	a.as(t, stranger).Get(a.base() + "/flags").AssertStatus(http.StatusNotFound)
	a.as(t, stranger).Get(a.base() + "/flags/" + key).AssertStatus(http.StatusNotFound)
}

// TestADraftSaysWhatThePublishWouldDo is the route the person holding publish reads first.
//
// A publish that does not compile is a 422 and nothing changes, so nothing is lost — but a studio
// finding out at that moment is a studio whose release is blocked on debugging a rule. Here the
// same answer costs a GET, and it also names what is deliberately being left out, because "my
// event is missing" is otherwise indistinguishable from a bug.
func TestADraftSaysWhatThePublishWouldDo(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	draft := a.base() + "/config/production/draft"

	// Nothing authored yet: it compiles, and it is empty.
	empty := testclient.Decoded[draftOut](client.Get(draft).AssertStatus(http.StatusOK))
	if !empty.Compiles || empty.PublishedVersion != 0 || empty.NextVersion != 1 {
		t.Fatalf("an empty project's draft = %+v", empty)
	}

	flagKey := unique("flag")
	client.Post(a.base()+"/flags",
		testclient.RawJSON(`{"key":"`+flagKey+`","name":"HUD v2","kind":"bool","fallback":false,
			"rollout":2500}`)).
		AssertStatus(http.StatusCreated)

	// A rule reading the player's history, which is the one fact a draft has to surface: it
	// is the difference between a config request costing a lookup and costing nothing.
	client.Post(a.base()+"/segments", testclient.RawJSON(
		`{"key":"payers","name":"Payers","rule":{"key":"commerce.revenue","op":"gt","value":0}}`)).
		AssertStatus(http.StatusCreated)
	client.Post(a.base()+"/flags", testclient.RawJSON(`{
		"key":"`+unique("flag")+`","name":"VIP shop","kind":"string","fallback":"none",
		"rules":[{"audience":{"segment":"payers"},"value":"vip"}]}`)).
		AssertStatus(http.StatusCreated)

	withFlags := testclient.Decoded[draftOut](client.Get(draft).AssertStatus(http.StatusOK))
	if !withFlags.Compiles || withFlags.Flags != 2 {
		t.Fatalf("draft = %+v, want two flags that compile", withFlags)
	}
	if !withFlags.NeedsPlayerFacts {
		t.Error("a rule on commerce.revenue must show up as needing the player's history")
	}

	// Archiving takes it out, and the draft says so by name rather than by the count alone.
	client.Delete(a.base() + "/flags/" + flagKey).AssertStatus(http.StatusOK)
	after := testclient.Decoded[draftOut](client.Get(draft).AssertStatus(http.StatusOK))
	if after.Flags != 1 {
		t.Errorf("draft carries %d flags after archiving one, want 1", after.Flags)
	}
	named := false
	for _, excluded := range after.Excluded {
		if excluded.Key == flagKey && excluded.Kind == "flag" {
			named = true
			if excluded.Reason == "" {
				t.Error("an exclusion with no reason is a bug report waiting to happen")
			}
		}
	}
	if !named {
		t.Errorf("the draft does not say the archived flag is being left out: %+v", after.Excluded)
	}
}

// TestWhatWasAuthoredIsWhatIsPublished is the whole vertical, end to end.
//
// Every layer below this existed already — the tables, the compiler, the evaluator, config-api,
// the Unity binding, the exposure events — and none of it could be reached, because there was no
// way to create a flag. This is the test that says the path is joined up.
func TestWhatWasAuthoredIsWhatIsPublished(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)

	client.Post(a.base()+"/segments", testclient.RawJSON(
		`{"key":"turkey","name":"Turkey","rule":{"key":"geo.country","op":"eq","value":"TR"}}`)).
		AssertStatus(http.StatusCreated)

	client.Post(a.base()+"/flags", testclient.RawJSON(`{
		"key":"`+unique("flag")+`","name":"Starter offer","kind":"string","fallback":"none",
		"rules":[{"audience":{"segment":"turkey"},"value":"try_pack","rollout":5000}],
		"rollout":10000}`)).
		AssertStatus(http.StatusCreated)

	// A bool flag as well, because its fallback is the value that could not be stored at all
	// and the compiled document is where a mistake about it would surface: what an SDK reads is
	// this document, not the row.
	client.Post(a.base()+"/flags", testclient.RawJSON(`{
		"key":"hud_v2","name":"HUD v2","kind":"bool","fallback":false}`)).
		AssertStatus(http.StatusCreated)

	// Active, not draft: a publish carries only the active ones, so this is the setting that
	// makes the event part of the test rather than part of the exclusions.
	client.Post(a.base()+"/liveops-events", testclient.RawJSON(`{
		"key":"`+unique("ev")+`","name":"Weekend event","kind":"seasonal_event","status":"active",
		"schedule":{"mode":"weekly","anchor":"2026-01-02T18:00:00Z","duration":"48h"},
		"audience":{"segment":"turkey"},"payload":{"multiplier":2},"priority":10}`)).
		AssertStatus(http.StatusCreated)

	published := testclient.Decoded[publishedOut](client.Post(a.base()+"/config/production/publish",
		testclient.JSON(map[string]string{"environment": "production", "note": "first"})).
		AssertStatus(http.StatusCreated))

	if published.Version != 1 {
		t.Errorf("version = %d, want 1", published.Version)
	}
	if published.Flags != 2 || published.Events != 1 {
		t.Errorf("published %d flags and %d events, want two and one", published.Flags, published.Events)
	}
	// The segment named geo.country, which is context the request carries — so serving this
	// document costs no history lookup, and the draft and the publish agree about that.
	if published.NeedsPlayerFacts {
		t.Error("a rule on geo.country must not make every config request cost a lookup")
	}

	// The document itself, which is the thing an SDK is served and the only place a mistake
	// about a value's type would show. `false` has to arrive as a JSON boolean: a fallback that
	// round-tripped through the wrong Go type would land here as a string, or as the base64 of
	// a byte slice, and every game reading the flag would get a truthy value it never chose.
	assertPublishedFallback(t, a, "hud_v2", false)

	// And the draft now reports it as published, with the next version waiting.
	draft := testclient.Decoded[draftOut](client.Get(a.base() + "/config/production/draft"))
	if draft.PublishedVersion != 1 || draft.NextVersion != 2 {
		t.Errorf("draft = %+v, want published 1 and next 2", draft)
	}
}

// assertPublishedFallback reads the compiled document out of the control plane and checks one
// flag's fallback against the value a game should receive.
//
// Straight from the row rather than through config-api, because the question is what was
// *written*: config-api's own suite proves that a document it is given is served faithfully, and
// this proves the document says what the studio typed.
func assertPublishedFallback(t *testing.T, a *authoring, key string, want any) {
	t.Helper()

	proj, err := a.db.Client.Project.Query().
		Where(project.Slug(a.projSlug)).Only(t.Context())
	if err != nil {
		t.Fatalf("find the project: %v", err)
	}
	version, err := a.db.Client.ConfigVersion.Query().
		Where(configversion.ProjectID(proj.ID)).
		Order(global.Desc(configversion.FieldVersion)).
		First(t.Context())
	if err != nil {
		t.Fatalf("read the published version: %v", err)
	}

	encoded, err := json.Marshal(version.Document)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Flags []struct {
			Key      string `json:"key"`
			Fallback any    `json:"fallback"`
		} `json:"flags"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("the published document does not decode: %v", err)
	}
	for _, flag := range document.Flags {
		if flag.Key != key {
			continue
		}
		if flag.Fallback != want {
			t.Errorf("published fallback for %s = %#v, want %#v", key, flag.Fallback, want)
		}
		return
	}
	t.Errorf("the published document has no flag %s", key)
}

// compact renders a stored JSON value the way the assertion wants to read it.
func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("stored value %q is not JSON: %v", raw, err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("re-encode %v: %v", value, err)
	}
	return string(out)
}

// Local decoding types, so these tests assert on the wire format rather than importing the
// schema package and testing it against itself.

type flagOut struct {
	Key      string          `json:"key"`
	Kind     string          `json:"kind"`
	Fallback json.RawMessage `json:"fallback"`
	Rollout  int             `json:"rollout"`
	Archived bool            `json:"archived"`
	Rules    []struct {
		Audience json.RawMessage `json:"audience"`
		Value    json.RawMessage `json:"value"`
		Rollout  int             `json:"rollout"`
	} `json:"rules"`
}

type flagListOut struct {
	Flags []flagOut `json:"flags"`
}

type segmentOut struct {
	Key      string          `json:"key"`
	Rule     json.RawMessage `json:"rule"`
	Archived bool            `json:"archived"`
}

type eventOut struct {
	Key         string          `json:"key"`
	Status      string          `json:"status"`
	Payload     json.RawMessage `json:"payload"`
	Occurrences []window        `json:"occurrences"`
}

type window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type draftOut struct {
	PublishedVersion int    `json:"published_version"`
	NextVersion      int    `json:"next_version"`
	Compiles         bool   `json:"compiles"`
	Problem          string `json:"problem"`
	Flags            int    `json:"flags"`
	Events           int    `json:"liveops_events"`
	NeedsPlayerFacts bool   `json:"needs_player_facts"`
	Excluded         []struct {
		Kind   string `json:"kind"`
		Key    string `json:"key"`
		Reason string `json:"reason"`
	} `json:"excluded"`
}

type publishedOut struct {
	Version          int  `json:"version"`
	Flags            int  `json:"flags"`
	Events           int  `json:"liveops_events"`
	NeedsPlayerFacts bool `json:"needs_player_facts"`
}
