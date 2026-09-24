package main

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// The explorer's pages, against the real stack: rows go into events_raw, every materialized
// view behind it fires, the nightly derivation runs, and the answer comes back through the
// dashboard's own routes with a signed-in person's credential.

// rawEvent is one row as analytics-writer would have stored it.
type rawEvent struct {
	install  string
	player   string
	session  string
	name     string
	kind     string
	platform string
	country  string
	version  string
	at       time.Time
	cohort   time.Time

	revenue  int64
	currency string
	playtime uint64

	// retention is the row's raw retention tier in days; 30 when unset. A row older than its
	// tier is expired by the first merge, so a test that needs history sets a longer one.
	retention uint16

	strs  map[string]string
	ints  map[string]int64
	bools map[string]uint8
}

// insert writes rows straight into events_raw, for the same reason seedCurve does: the cohort day
// is the server's ingest time, so a history can only be seeded by controlling it.
func (r *reporting) insert(t *testing.T, rows []rawEvent) {
	t.Helper()
	conn, err := r.ch.DB()
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	batch, err := conn.PrepareBatch(context.Background(), `INSERT INTO events_raw (
		org_seq, project_seq, environment_seq, org_id, project_id, environment_id,
		retention_tier, policy_tier, event_time, ingest_time, received_at,
		event_id, batch_id, session_id, player_id, install_id,
		event_name, event_type, platform, country_code, app_version, sdk_name, sdk_version,
		cohort_date, install_platform, install_country, first_app_version,
		consent_analytics, consent_policy_version, att_status,
		props_string, props_int, props_float, props_bool,
		revenue_micros, currency, playtime_ms, payload_bytes)`)
	if err != nil {
		t.Fatalf("prepare seed: %v", err)
	}
	for _, e := range rows {
		var player *string
		if e.player != "" {
			player = &e.player
		}
		var currency *string
		if e.currency != "" {
			currency = &e.currency
		}
		kind := e.kind
		if kind == "" {
			kind = "custom"
		}
		retention := e.retention
		if retention == 0 {
			retention = 30
		}
		cohort := e.cohort
		if cohort.IsZero() {
			cohort = e.at.Truncate(24 * time.Hour)
		}
		if err := batch.Append(
			uint32(r.project.OrgSeq), uint32(r.project.ProjectSeq), uint16(r.project.EnvironmentID),
			uuid.New(), uuid.MustParse(r.project.ProjectUID), uuid.New(),
			retention, "standard", e.at, e.at, e.at,
			uuid.New(), uuid.New(), e.session, player, uuid.MustParse(e.install),
			e.name, kind, e.platform, e.country, e.version, "unity", "1.4.0",
			cohort, e.platform, e.country, e.version,
			uint8(1), "2026-01-15", "not_determined",
			orEmpty(e.strs), orEmptyInt(e.ints), map[string]float64{}, orEmptyBool(e.bools),
			e.revenue, currency, e.playtime, uint32(256),
		); err != nil {
			t.Fatalf("append seed row: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("send seed: %v", err)
	}
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func orEmptyInt(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

func orEmptyBool(m map[string]uint8) map[string]uint8 {
	if m == nil {
		return map[string]uint8{}
	}
	return m
}

// get reads one explorer page as the given client and decodes it.
func get[T any](t *testing.T, client *testclient.Client, path string, params url.Values, want int) T {
	t.Helper()
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	res := client.Get(path)
	res.AssertStatus(want)
	var out T
	if want == http.StatusOK {
		res.JSON(&out)
	}
	return out
}

// yesterday is a whole day in the past, so nothing a test posts now falls into it by accident.
func yesterday() time.Time { return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1) }

func day(t time.Time) string { return t.Format("2006-01-02") }

// levelEvents is six players completing a level yesterday, four on iOS and two on Android, and two
// players sending an event rare enough that its player count identifies them.
func (r *reporting) levelEvents(t *testing.T) []string {
	t.Helper()
	at := yesterday().Add(10 * time.Hour)
	installs := make([]string, 6)
	var rows []rawEvent
	for i := range installs {
		installs[i] = uuid.NewString()
		platform := "ios"
		if i >= 4 {
			platform = "android"
		}
		level := int64(3)
		if i%2 == 1 {
			level = 4
		}
		rows = append(rows, rawEvent{
			install: installs[i], session: "s-" + installs[i][:8], name: "level_complete",
			platform: platform, country: "TR", version: "2.7.1", at: at.Add(time.Duration(i) * time.Minute),
			ints: map[string]int64{"level": level},
		})
	}
	for _, install := range installs[:2] {
		rows = append(rows, rawEvent{
			install: install, name: "rare_event", platform: "ios", country: "TR", version: "2.7.1",
			at: at.Add(time.Hour),
		})
	}
	r.insert(t, rows)
	if got := r.waitForRows(t, len(rows)); got != len(rows) {
		t.Fatalf("seeded %d of %d rows", got, len(rows))
	}
	return installs
}

func (r *reporting) yesterdayOnly() url.Values {
	return url.Values{"from": {day(yesterday())}, "to": {day(yesterday())}}
}

// TestTheEventCatalogueListsEveryEvent, with a count too thin to show withheld rather than
// zeroed.
func TestTheEventCatalogueListsEveryEvent(t *testing.T) {
	r := newReporting(t, "standard")
	r.levelEvents(t)

	out := get[schemas.EventCatalogueOut](t, r.member, r.analytics()+"/events", r.yesterdayOnly(), http.StatusOK)
	byName := map[string]schemas.EventSummaryOut{}
	for _, e := range out.Events {
		byName[e.Name] = e
	}

	level, ok := byName["level_complete"]
	if !ok {
		t.Fatalf("level_complete is missing from %+v", out.Events)
	}
	if level.Events != 6 || level.Players == nil || *level.Players != 6 || level.Properties != 1 {
		t.Fatalf("level_complete: %+v, want 6 events by 6 players carrying 1 property", level)
	}
	if level.PreviousEvents != 0 {
		t.Fatalf("level_complete has %d events the day before; it was sent once", level.PreviousEvents)
	}

	rare := byName["rare_event"]
	if rare.Events != 2 || rare.Players != nil {
		t.Fatalf("rare_event: %+v, want 2 events and its 2 players withheld", rare)
	}
	if out.SuppressedCells == 0 || out.SuppressionThreshold != 5 {
		t.Fatalf("withheld %d cells under %d; the answer must say so", out.SuppressedCells, out.SuppressionThreshold)
	}
	if !out.Approximate {
		t.Fatal("player counts from sketches are not marked approximate")
	}
}

// TestAnEventsPageSplitsItAndDescribesItsProperties.
func TestAnEventsPageSplitsItAndDescribesItsProperties(t *testing.T) {
	r := newReporting(t, "standard")
	r.levelEvents(t)

	out := get[schemas.EventDetailOut](t, r.member, r.analytics()+"/events/level_complete", r.yesterdayOnly(), http.StatusOK)
	if out.Totals.Events != 6 || out.Totals.Players == nil || *out.Totals.Players != 6 {
		t.Fatalf("totals %+v, want 6 events by 6 players", out.Totals)
	}
	if len(out.Series) != 1 || out.Series[0].Events != 6 {
		t.Fatalf("a one-day range at a day interval is %+v, want one bucket of 6", out.Series)
	}

	platforms := map[string]schemas.EventSliceOut{}
	for _, s := range out.Breakdowns["platform"] {
		platforms[s.Value] = s
	}
	if ios := platforms["ios"]; ios.Events != 4 || ios.Players != nil {
		// Four is under the threshold of five, so the players are withheld; the count of
		// events is a count of occurrences, not of people, and stays.
		t.Fatalf("ios: %+v, want 4 events with the players withheld", ios)
	}
	if android := platforms["android"]; android.Events != 2 {
		t.Fatalf("android: %+v, want 2 events", android)
	}

	if len(out.Properties) != 1 {
		t.Fatalf("properties %+v, want level alone", out.Properties)
	}
	level := out.Properties[0]
	if level.Key != "level" || level.Type != "int" || level.Occurrences != 6 || level.Fill != 1 || level.Distinct != 2 {
		t.Fatalf("level: %+v, want an int on every occurrence with 2 distinct values", level)
	}
	shares := map[string]float64{}
	for _, v := range level.Top {
		shares[v.Value] = v.Share
	}
	if shares["3"] != 0.5 || shares["4"] != 0.5 {
		t.Fatalf("level's top values %+v, want 3 and 4 half each", level.Top)
	}

	get[schemas.EventDetailOut](t, r.member, r.analytics()+"/events/never_sent", r.yesterdayOnly(), http.StatusNotFound)

	long := url.Values{"from": {day(yesterday().AddDate(0, 0, -20))}, "to": {day(yesterday())}, "interval": {"hour"}}
	get[schemas.EventDetailOut](t, r.member, r.analytics()+"/events/level_complete", long, http.StatusUnprocessableEntity)
}

// TestAnEventsOccurrencesNeedPlayerRead is the line between the numbers and the players.
func TestAnEventsOccurrencesNeedPlayerRead(t *testing.T) {
	r := newReporting(t, "standard")
	installs := r.levelEvents(t)

	out := get[schemas.EventOccurrencesOut](t, r.member, r.analytics()+"/events/level_complete/occurrences",
		url.Values{"limit": {"3"}}, http.StatusOK)
	if len(out.Occurrences) != 3 {
		t.Fatalf("%d occurrences, want the newest 3", len(out.Occurrences))
	}
	newest := out.Occurrences[0]
	if newest.InstallID != installs[5] || newest.Properties["level"] != float64(4) {
		t.Fatalf("the newest occurrence is %+v, want the last install's level 4", newest)
	}

	numbers := r.numbersOnly(t)
	get[schemas.EventOccurrencesOut](t, numbers, r.analytics()+"/events/level_complete/occurrences", nil, http.StatusForbidden)
	// The same person still reads the aggregates.
	get[schemas.EventCatalogueOut](t, numbers, r.analytics()+"/events", r.yesterdayOnly(), http.StatusOK)
}

// numbersOnly is a member whose role reads analytics and not players.
func (r *reporting) numbersOnly(t *testing.T) *testclient.Client {
	t.Helper()
	const key = "numbers_only"
	if _, err := r.db.Client.Role.Create().
		SetOrganizationID(r.project.OrgSeq).
		SetKey(key).SetName("Numbers only").
		SetPermissions([]string{"analytics.read"}).
		Save(context.Background()); err != nil {
		t.Fatalf("create the role: %v", err)
	}
	return r.join(t, key)
}

// sessionEvents plays three sessions two days ago: five minutes that ended cleanly, forty seconds
// the SDK had to close, and twenty minutes across midnight that ended cleanly the next day. Plus
// one session going on right now.
func (r *reporting) sessionEvents(t *testing.T) (now string) {
	t.Helper()
	d := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	a, b, c, live := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	ev := func(install, session, name string, at time.Time) rawEvent {
		return rawEvent{install: install, session: session, name: name, platform: "ios", version: "2.7.1",
			country: "TR", at: at, cohort: d, kind: "session", playtime: 1000}
	}
	r.insert(t, []rawEvent{
		ev(a, "short", "session_start", d.Add(10*time.Hour)),
		ev(a, "short", "level_complete", d.Add(10*time.Hour+4*time.Minute)),
		ev(a, "short", "session_end", d.Add(10*time.Hour+5*time.Minute)),

		ev(b, "brief", "session_start", d.Add(12*time.Hour)),
		ev(b, "brief", "level_complete", d.Add(12*time.Hour+40*time.Second)),

		ev(c, "late", "session_start", d.Add(23*time.Hour+50*time.Minute)),
		ev(c, "late", "session_end", d.Add(24*time.Hour+10*time.Minute)),

		ev(live, "live", "session_start", time.Now().UTC().Add(-10*time.Minute)),
		ev(live, "live", "level_complete", time.Now().UTC().Add(-5*time.Minute)),
	})
	if got := r.waitForRows(t, 9); got != 9 {
		t.Fatalf("seeded %d of 9 rows", got)
	}
	r.derive(t, d, time.Now().UTC())
	return live
}

// TestTheSessionsPageMeasuresWholeSessions.
//
// The session across midnight is the one that matters: its rows are on two days, and it is one
// session of twenty minutes counted on the day it started, not two short ones.
func TestTheSessionsPageMeasuresWholeSessions(t *testing.T) {
	r := newReporting(t, "standard")
	r.sessionEvents(t)

	d := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	out := get[schemas.SessionsOut](t, r.member, r.analytics()+"/sessions",
		url.Values{"from": {day(d)}, "to": {day(d)}, "tz": {"Europe/Istanbul"}}, http.StatusOK)

	if out.Totals.Sessions != 3 {
		t.Fatalf("%d sessions on the day, want 3", out.Totals.Sessions)
	}
	rungs := map[uint64]uint64{}
	for _, l := range out.Lengths {
		rungs[l.FromMs] = l.Sessions
	}
	// 40 seconds, 5 minutes and 20 minutes, each on its own rung.
	if rungs[30_000] != 1 || rungs[300_000] != 1 || rungs[1_200_000] != 1 {
		t.Fatalf("lengths %+v, want one session from 30s, one from 5m and one from 20m", out.Lengths)
	}
	outcomes := map[string]uint64{}
	for _, o := range out.Outcomes {
		outcomes[o.Outcome] = o.Sessions
	}
	if outcomes["ended"] != 2 || outcomes["timed_out"] != 1 {
		t.Fatalf("outcomes %+v, want 2 ended and 1 timed out", out.Outcomes)
	}
	if out.Totals.MedianMs < 300_000 || out.Totals.MedianMs > 600_000 {
		t.Fatalf("the median session is %dms; the middle one lasted five minutes", out.Totals.MedianMs)
	}

	// Istanbul is three hours ahead, so the session begun at 23:50 UTC began at 02:50 the next
	// morning there.
	var starts uint64
	for _, day := range out.Heatmap {
		for _, n := range day {
			starts += n
		}
	}
	next := (int(d.Weekday()) + 6 + 1) % 7 // Monday first, and the day after
	if starts != 3 || out.Heatmap[next][2] != 1 {
		t.Fatalf("heatmap %v holds %d starts, want 3 with one at 02:00 the next day", out.Heatmap, starts)
	}
	if !out.CompleteThrough.Equal(d.AddDate(0, 0, 1)) && !out.CompleteThrough.Equal(d) {
		t.Fatalf("complete through %s, want the last whole day seeded", out.CompleteThrough)
	}
}

// TestRecentSessionsAreWholeAndSayWhichAreGoingOn.
func TestRecentSessionsAreWholeAndSayWhichAreGoingOn(t *testing.T) {
	r := newReporting(t, "standard")
	live := r.sessionEvents(t)

	out := get[schemas.RecentSessionsOut](t, r.member, r.analytics()+"/sessions/recent",
		url.Values{"limit": {"10"}}, http.StatusOK)
	if len(out.Sessions) == 0 || out.Sessions[0].InstallID != live || out.Sessions[0].Outcome != "open" {
		t.Fatalf("the newest session is %+v, want the one going on now", out.Sessions)
	}
	for _, s := range out.Sessions {
		if s.SessionID == "late" && (s.DurationMs != 20*60*1000 || s.Outcome != "ended") {
			// Read from its second half alone it would have started after midnight.
			t.Fatalf("the session across midnight is %+v, want 20 minutes that ended", s)
		}
	}

	get[schemas.RecentSessionsOut](t, r.numbersOnly(t), r.analytics()+"/sessions/recent", nil, http.StatusForbidden)
}

// playerBase is five installs: two of one signed-in player, active yesterday; one lapsed ten days
// ago; one churned forty days ago; and one that has paid.
type playerBase struct {
	hero, heroTablet, lapsed, churned, payer string
}

func (r *reporting) playerBase(t *testing.T) playerBase {
	t.Helper()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	p := playerBase{
		hero: uuid.NewString(), heroTablet: uuid.NewString(),
		lapsed: uuid.NewString(), churned: uuid.NewString(), payer: uuid.NewString(),
	}
	at := func(daysAgo int) time.Time { return today.AddDate(0, 0, -daysAgo).Add(9 * time.Hour) }
	r.insert(t, []rawEvent{
		{install: p.hero, player: "hero-1", session: "h1", name: "session_start", kind: "session",
			platform: "ios", country: "TR", version: "2.7.1", at: at(1), playtime: 60_000},
		{install: p.hero, player: "hero-1", session: "h1", name: "level_complete",
			platform: "ios", country: "TR", version: "2.7.1", at: at(1).Add(time.Minute)},
		{install: p.heroTablet, player: "hero-1", session: "h2", name: "session_start", kind: "session",
			platform: "android", country: "TR", version: "2.7.1", at: at(1).Add(time.Hour)},
		{install: p.lapsed, session: "l1", name: "session_start", kind: "session",
			platform: "ios", country: "DE", version: "2.6.0", at: at(10)},
		{install: p.churned, session: "c1", name: "session_start", kind: "session", retention: 400,
			platform: "android", country: "DE", version: "2.5.0", at: at(40)},
		{install: p.payer, session: "p1", name: "gem_pack", kind: "purchase",
			platform: "ios", country: "US", version: "2.7.1", at: at(2), revenue: 990_000, currency: "USD"},
	})
	if got := r.waitForRows(t, 6); got < 5 {
		t.Fatalf("seeded %d of 6 rows", got)
	}
	return p
}

func installsOf(out schemas.PlayerListOut) map[string]schemas.PlayerSummaryOut {
	m := map[string]schemas.PlayerSummaryOut{}
	for _, p := range out.Players {
		m[p.InstallID] = p
	}
	return m
}

// TestThePlayerDirectoryFiltersFinishedPlayers.
//
// The filters apply to a device's whole life, which is only known once its partial rows are put
// together; a filter on one partial row would find a player "lapsed" on a row from last month.
func TestThePlayerDirectoryFiltersFinishedPlayers(t *testing.T) {
	r := newReporting(t, "standard")
	p := r.playerBase(t)
	path := r.analytics() + "/players"

	active := installsOf(get[schemas.PlayerListOut](t, r.member, path, url.Values{"status": {"active"}}, http.StatusOK))
	if _, ok := active[p.hero]; !ok || len(active) != 3 {
		t.Fatalf("active players %v, want the hero's two installs and the payer", active)
	}
	lapsed := installsOf(get[schemas.PlayerListOut](t, r.member, path, url.Values{"status": {"lapsed"}}, http.StatusOK))
	if len(lapsed) != 1 || lapsed[p.lapsed].Status != "lapsed" || lapsed[p.lapsed].Country != "DE" {
		t.Fatalf("lapsed players %v, want the one from Germany ten days ago", lapsed)
	}
	churned := installsOf(get[schemas.PlayerListOut](t, r.member, path, url.Values{"status": {"churned"}}, http.StatusOK))
	if _, ok := churned[p.churned]; !ok || len(churned) != 1 {
		t.Fatalf("churned players %v, want the one from forty days ago", churned)
	}
	paying := get[schemas.PlayerListOut](t, r.member, path, url.Values{"spend": {"paying"}}, http.StatusOK)
	if len(paying.Players) != 1 || paying.Players[0].InstallID != p.payer || paying.Players[0].RevenueMicros != 990_000 {
		t.Fatalf("paying players %+v, want the payer alone", paying.Players)
	}

	page := get[schemas.PlayerListOut](t, r.member, path, url.Values{"limit": {"2"}}, http.StatusOK)
	if len(page.Players) != 2 || page.NextOffset != 2 {
		t.Fatalf("a page of 2 of 5 is %+v", page)
	}
	rest := get[schemas.PlayerListOut](t, r.member, path, url.Values{"limit": {"10"}, "offset": {"2"}}, http.StatusOK)
	if len(rest.Players) != 3 || rest.NextOffset != 0 {
		t.Fatalf("the rest is %+v, want the last 3 and no next page", rest)
	}

	get[schemas.PlayerListOut](t, r.numbersOnly(t), path, nil, http.StatusForbidden)
}

// TestAPlayersPageIsAnInstallOrAnAccount.
func TestAPlayersPageIsAnInstallOrAnAccount(t *testing.T) {
	r := newReporting(t, "standard")
	p := r.playerBase(t)

	install := get[schemas.PlayerProfileOut](t, r.member, r.analytics()+"/players/"+p.hero, nil, http.StatusOK)
	if install.Kind != "install" || install.Summary.Platform != "ios" || install.Summary.Sessions != 1 {
		t.Fatalf("the install's page is %+v", install)
	}
	if len(install.Days) != 1 || len(install.Events) != 2 || install.Events[0].Name != "level_complete" {
		t.Fatalf("the install's days %+v and events %+v, want one day and two events newest first",
			install.Days, install.Events)
	}

	account := get[schemas.PlayerProfileOut](t, r.member, r.analytics()+"/players/hero-1", nil, http.StatusOK)
	if account.Kind != "account" || len(account.Installs) != 2 || account.Summary.Sessions != 2 {
		t.Fatalf("the account's page is %+v, want two installs and two sessions", account)
	}
	if len(account.Events) != 3 {
		t.Fatalf("the account's events are %+v, want all three across both installs", account.Events)
	}

	get[schemas.PlayerProfileOut](t, r.member, r.analytics()+"/players/"+uuid.NewString(), nil, http.StatusNotFound)
	get[schemas.PlayerProfileOut](t, r.member, r.analytics()+"/players/nobody", nil, http.StatusNotFound)
}

// TestNoPlayerIsListedOnAKidsProject is §98: the tier exists so that a child cannot be followed.
func TestNoPlayerIsListedOnAKidsProject(t *testing.T) {
	r := newReporting(t, "kids_strict")
	for _, path := range []string{"/players", "/players/" + uuid.NewString(), "/sessions/recent",
		"/events/session_start/occurrences"} {
		get[struct{}](t, r.member, r.analytics()+path, nil, http.StatusUnprocessableEntity)
	}
}
