package query

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/schemas"
)

// The Revenue and Purchases pages (00016): revenue_daily for everything summed, payers for
// everything about the people paying, purchases for the ledger, and activity_1h for when.

// RevenueFilter narrows revenue_daily and purchases by what was bought and where.
type RevenueFilter struct {
	Stores, Countries, Products []string

	// Verified is the stores the project verifies purchases with, each from the day it began.
	// Those stores' money is their verified purchases; every other store's is what the game
	// reported ("verified wins", ADR 0048). Empty, everything is what the game reported.
	Verified []VerifiedStore
	// Statuses narrows the ledger to verified, refunded, sandbox or rejected purchases, or ""
	// for reported ones.
	Statuses []string
}

// VerifiedStore is a store whose purchases are verified, and since when.
type VerifiedStore struct {
	Store string
	Since time.Time
}

func (f RevenueFilter) apply(b *builder) { f.applyOn(b, "day") }

// applyOn narrows a table whose day column is `day`, and picks the purchases it counts.
func (f RevenueFilter) applyOn(b *builder, day string) {
	b.in("store", strs(f.Stores)).in("country_code", strs(f.Countries)).in("product_id", strs(f.Products))
	f.counted(b, day)
}

// counted is "verified wins" as a predicate: for a store the project verifies with, from the day
// it began, the verified rows; for the rest, the reported ones. One condition on columns the
// table already has, so it costs a filter and never a join.
func (f RevenueFilter) counted(b *builder, day string) {
	if len(f.Verified) == 0 {
		b.and("source = 'reported'")
		return
	}
	var conds []string
	var args []any
	for _, v := range f.Verified {
		conds = append(conds, "(store = ? AND "+day+" >= ?)")
		args = append(args, v.Store, date(v.Since))
	}
	b.and("if("+strings.Join(conds, " OR ")+", source = 'verified', source = 'reported')", args...)
}

// payersCounted is the same choice for payers, which has no day: a verified store's payers are
// its verified buyers.
func (f RevenueFilter) payersCounted(b *builder) {
	if len(f.Verified) == 0 {
		b.and("source = 'reported'")
		return
	}
	stores := make([]string, len(f.Verified))
	args := make([]any, len(f.Verified))
	for i, v := range f.Verified {
		stores[i], args[i] = "?", v.Store
	}
	b.and("if(store IN ("+strings.Join(stores, ", ")+"), source = 'verified', source = 'reported')", args...)
}

// SpenderLadder is the lifetime spend bands of the spender tiers, in micros of the reported
// currency: under 1, then 1, 5, 20, 50, 100 and 500 and above.
var SpenderLadder = []int64{1_000_000, 5_000_000, 20_000_000, 50_000_000, 100_000_000, 500_000_000}

// maxSlices bounds each of the products, stores and countries lists.
const maxSlices = 100

// Revenue answers the Revenue page.
func (e *Engine) Revenue(ctx context.Context, scope Scope, w Window, f RevenueFilter, tz *time.Location) (*schemas.RevenueOut, error) {
	prev := w.Previous()
	cut := date(w.From)
	withheld := Suppressed{Threshold: e.k}
	out := &schemas.RevenueOut{
		From: w.From, To: w.To, Timezone: tz.String(),
		Approximate: true, SuppressionThreshold: e.k,
	}

	// The totals, both windows in one scan.
	t := from(factRevenueDaily)
	t.columnWith("sumIf(revenue_micros, day >= ?)", "rev", cut).
		columnWith("sumIf(purchases, day >= ?)", "n", cut).
		columnWith("uniqCombinedMergeIf(12)(buyers, day >= ?)", "cur_buyers", cut).
		columnWith("sumIf(revenue_micros, day < ?)", "prev_rev", cut).
		columnWith("sumIf(purchases, day < ?)", "prev_n", cut).
		columnWith("uniqCombinedMergeIf(12)(buyers, day < ?)", "prev_buyers", cut).
		columnWith("sumIf(refunds, day >= ?)", "refunds", cut).
		columnWith("sumIf(refunded_micros, day >= ?)", "refunded", cut).
		columnWith("sumIf(sandbox, day >= ?)", "sandbox", cut).
		columnWith("sumIf(rejected, day >= ?)", "rejected", cut)
	scopeAnd(t, scope)
	t.between("day", date(prev.From), date(w.To))
	f.apply(t)
	var buyers, prevBuyers uint64
	if err := e.read(ctx, t.statement(), func(rows driver.Rows) error {
		return rows.Scan(&out.Totals.RevenueMicros, &out.Totals.Purchases, &buyers,
			&out.Totals.PreviousRevenueMicros, &out.Totals.PreviousPurchases, &prevBuyers,
			&out.Totals.Refunds, &out.Totals.RefundedMicros, &out.Totals.Sandbox, &out.Totals.Rejected)
	}); err != nil {
		return nil, failed("revenue_daily", err)
	}
	out.Totals.Buyers = shown(&withheld, buyers)
	out.Totals.PreviousBuyers = shown(&withheld, prevBuyers)
	if out.Totals.Buyers == nil {
		// The whole range was bought by fewer than the threshold: what they spent is theirs.
		out.Totals.RevenueMicros, out.Totals.Purchases = 0, 0
		out.Totals.Refunds, out.Totals.RefundedMicros = 0, 0
	}
	out.Totals.Verified = len(f.Verified) > 0
	if out.Totals.PreviousBuyers == nil {
		out.Totals.PreviousRevenueMicros, out.Totals.PreviousPurchases = 0, 0
	}

	if err := e.revenueDays(ctx, scope, w, f, out, &withheld); err != nil {
		return nil, err
	}
	for _, s := range []struct {
		column string
		into   *[]schemas.RevenueSliceOut
	}{
		{"product_id", &out.Products}, {"store", &out.Stores}, {"country_code", &out.Countries},
	} {
		found, err := e.revenueSlices(ctx, scope, w, f, s.column, &withheld)
		if err != nil {
			return nil, err
		}
		*s.into = found
	}

	p := from(factRevenueDaily)
	p.column("price_micros", "price").
		column("toString(currency)", "cur").
		column("sum(purchases)", "n").
		column("sum(revenue_micros)", "rev")
	scopeAnd(p, scope)
	p.between("day", date(w.From), date(w.To))
	f.apply(p)
	p.group("price", "cur").order("n DESC", "price").withLimit(maxSlices)
	currencies := map[string]bool{}
	if err := e.read(ctx, p.statement(), func(rows driver.Rows) error {
		var pp schemas.PricePointOut
		if err := rows.Scan(&pp.PriceMicros, &pp.Currency, &pp.Purchases, &pp.RevenueMicros); err != nil {
			return err
		}
		currencies[pp.Currency] = true
		out.PricePoints = append(out.PricePoints, pp)
		return nil
	}); err != nil {
		return nil, failed("revenue_daily", err)
	}
	for c := range currencies {
		out.Currencies = append(out.Currencies, c)
	}
	slices.Sort(out.Currencies)

	if err := e.newPayers(ctx, scope, w, f, out, &withheld); err != nil {
		return nil, err
	}
	if err := e.spenderTiers(ctx, scope, w, f, out, &withheld); err != nil {
		return nil, err
	}

	h := from(factActivity1h)
	h.columnWith("toDayOfWeek(hour, 0, ?)", "weekday", tz.String()).
		columnWith("toHour(toTimeZone(hour, ?))", "clock", tz.String()).
		column("sum(purchases)", "n")
	scopeAnd(h, scope)
	h.gte("hour", timestamp(w.From)).lt("hour", timestamp(w.end()))
	h.groupOn("weekday", "clock")
	if err := e.read(ctx, h.statement(), func(rows driver.Rows) error {
		var weekday, clock uint8
		var n uint64
		if err := rows.Scan(&weekday, &clock, &n); err != nil {
			return err
		}
		if weekday >= 1 && weekday <= 7 && clock < 24 {
			out.Heatmap[weekday-1][clock] += n
		}
		return nil
	}); err != nil {
		return nil, failed("activity_1h", err)
	}

	out.SuppressedCells = withheld.Cells
	return out, nil
}

// revenueDays is each day of the window, split by store, with the money withheld on a day bought
// by too few.
func (e *Engine) revenueDays(ctx context.Context, scope Scope, w Window, f RevenueFilter, out *schemas.RevenueOut, withheld *Suppressed) error {
	type day struct {
		rev     int64
		n       uint64
		byStore map[string]int64
	}
	days := map[time.Time]*day{}
	get := func(at time.Time) *day {
		d := days[at]
		if d == nil {
			d = &day{byStore: map[string]int64{}}
			days[at] = d
		}
		return d
	}

	b := from(factRevenueDaily)
	b.column("toDateTime(day, 'UTC')", "d").
		column("toString(store)", "s").
		column("sum(revenue_micros)", "rev").
		column("sum(purchases)", "n")
	scopeAnd(b, scope)
	b.between("day", date(w.From), date(w.To))
	f.apply(b)
	b.groupOn("d", "s").withLimit(e.limits.MaxRows)
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var at time.Time
		var store string
		var rev int64
		var n uint64
		if err := rows.Scan(&at, &store, &rev, &n); err != nil {
			return err
		}
		d := get(at.UTC())
		d.rev += rev
		d.n += n
		d.byStore[store] += rev
		return nil
	}); err != nil {
		return failed("revenue_daily", err)
	}

	buyers := map[time.Time]uint64{}
	u := from(factRevenueDaily)
	u.column("toDateTime(day, 'UTC')", "d").column("uniqCombinedMerge(12)(buyers)", "n")
	scopeAnd(u, scope)
	u.between("day", date(w.From), date(w.To))
	f.apply(u)
	u.groupOn("d").withLimit(e.limits.MaxRows)
	if err := e.read(ctx, u.statement(), func(rows driver.Rows) error {
		var at time.Time
		var n uint64
		if err := rows.Scan(&at, &n); err != nil {
			return err
		}
		buyers[at.UTC()] = n
		return nil
	}); err != nil {
		return failed("revenue_daily", err)
	}

	for at := w.From; !at.After(w.To); at = at.AddDate(0, 0, 1) {
		row := schemas.RevenueDayOut{Day: at}
		if d := days[at]; d != nil {
			if withheld.withheld(buyers[at]) {
				row.Withheld = true
			} else {
				row.RevenueMicros, row.Purchases, row.ByStore = d.rev, d.n, d.byStore
			}
		}
		out.Days = append(out.Days, row)
	}
	return nil
}

// revenueSlices is the busiest values of one column over the window, with the window before.
func (e *Engine) revenueSlices(ctx context.Context, scope Scope, w Window, f RevenueFilter, column string, withheld *Suppressed) ([]schemas.RevenueSliceOut, error) {
	cut := date(w.From)
	b := from(factRevenueDaily)
	b.column("toString("+column+")", "k").
		columnWith("sumIf(revenue_micros, day >= ?)", "rev", cut).
		columnWith("sumIf(purchases, day >= ?)", "n", cut).
		columnWith("uniqCombinedMergeIf(12)(buyers, day >= ?)", "cur_buyers", cut).
		columnWith("sumIf(revenue_micros, day < ?)", "prev_rev", cut)
	scopeAnd(b, scope)
	b.between("day", date(w.Previous().From), date(w.To))
	f.apply(b)
	b.group("k").order("rev DESC", "k").withLimit(maxSlices)

	var out []schemas.RevenueSliceOut
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var s schemas.RevenueSliceOut
		if err := rows.Scan(&s.Key, &s.RevenueMicros, &s.Purchases, &s.Buyers, &s.PreviousRevenueMicros); err != nil {
			return err
		}
		if s.Purchases == 0 && s.PreviousRevenueMicros == 0 {
			return nil
		}
		if withheld.withheld(s.Buyers) {
			return nil
		}
		out = append(out, s)
		return nil
	}); err != nil {
		return nil, failed("revenue_daily", err)
	}
	return out, nil
}

// newPayers counts, for each day of the window, the payers whose first purchase fell on it.
//
// payers holds each paying device as partial rows, so the inner statement finishes each device in
// key order before its first purchase is true; the outer counts them by day. Both read payers
// alone, which is a few percent of the players.
func (e *Engine) newPayers(ctx context.Context, scope Scope, w Window, f RevenueFilter, out *schemas.RevenueOut, withheld *Suppressed) error {
	inner := from(factPayers)
	inner.column("min(first_purchase_at)", "first")
	scopeAnd(inner, scope)
	f.payersCounted(inner)
	inner.group("project_seq", "environment_seq", "device_id").
		havingThat("first >= ? AND first < ?", timestamp(w.From), timestamp(w.end()))

	b := over(inner)
	b.column("toDateTime(toDate(first), 'UTC')", "d").column("count()", "n")
	b.groupOn("d")

	counts := map[time.Time]uint64{}
	if err := e.readInOrder(ctx, b.statement(), func(rows driver.Rows) error {
		var at time.Time
		var n uint64
		if err := rows.Scan(&at, &n); err != nil {
			return err
		}
		counts[at.UTC()] = n
		return nil
	}); err != nil {
		return failed("payers", err)
	}
	for at := w.From; !at.After(w.To); at = at.AddDate(0, 0, 1) {
		out.NewPayers = append(out.NewPayers, schemas.NewPayersOut{Day: at, Payers: shown(withheld, counts[at])})
	}
	return nil
}

// spenderTiers splits everybody who bought in the window by what they have spent in their life.
func (e *Engine) spenderTiers(ctx context.Context, scope Scope, w Window, f RevenueFilter, out *schemas.RevenueOut, withheld *Suppressed) error {
	ladder := make([]string, len(SpenderLadder))
	for i, b := range SpenderLadder {
		ladder[i] = strconv.FormatInt(b, 10)
	}

	inner := from(factPayers)
	inner.column("sum(revenue_micros)", "spent").column("max(last_purchase_at)", "last")
	scopeAnd(inner, scope)
	f.payersCounted(inner)
	inner.group("project_seq", "environment_seq", "device_id").
		havingThat("last >= ? AND spent > 0", timestamp(w.From))

	b := over(inner)
	b.column("toUInt8(arrayCount(b -> spent >= b, ["+strings.Join(ladder, ", ")+"]))", "rung").
		column("count()", "n").
		column("sum(spent)", "rev")
	b.groupOn("rung")

	tiers := make([]schemas.SpenderTierOut, len(SpenderLadder)+1)
	var lower int64
	for i := range tiers {
		tiers[i].FromMicros = lower
		if i < len(SpenderLadder) {
			tiers[i].ToMicros = SpenderLadder[i]
			lower = SpenderLadder[i]
		}
		zero := uint64(0)
		tiers[i].Payers = &zero
	}
	if err := e.readInOrder(ctx, b.statement(), func(rows driver.Rows) error {
		var rung uint8
		var n uint64
		var rev int64
		if err := rows.Scan(&rung, &n, &rev); err != nil {
			return err
		}
		if int(rung) >= len(tiers) {
			return nil
		}
		if withheld.withheld(n) {
			tiers[rung].Payers = nil
			return nil
		}
		tiers[rung].Payers, tiers[rung].RevenueMicros = &n, &rev
		return nil
	}); err != nil {
		return failed("payers", err)
	}
	out.SpenderTiers = tiers
	return nil
}

// Purchases reads one page of the ledger, newest first.
//
// FINAL, so a purchase whose batch was stored twice is read once; ordered along the sort key,
// descending, so ClickHouse reads the newest parts first and stops once the page is full. The
// cursor is the last purchase's time and id, which stays stable while new purchases arrive above
// it.
func (e *Engine) Purchases(ctx context.Context, scope Scope, w Window, f RevenueFilter, limit int, before string) (*schemas.PurchasesOut, error) {
	if err := refuseKids(scope); err != nil {
		return nil, err
	}

	b := from(factPurchases).deduplicated()
	b.column("toString(event_id)", "id").
		column("event_time", "at").
		column("toString(install_id)", "install").
		column("ifNull(player_id, '')", "player").
		column("session_id", "session").
		column("toString(product_id)", "product").
		column("toString(store)", "st").
		column("revenue_micros", "rev").
		column("toString(currency)", "cur").
		column("toString(platform)", "p").
		column("toString(country_code)", "c").
		column("toString(app_version)", "v").
		column("device_id", "device").
		column("toString(source)", "src").
		column("toString(status)", "status").
		column("store_transaction_id", "txn").
		column("refunded_at", "refunded").
		column("toString(refund_reason)", "reason").
		column("toString(rejection)", "rej").
		column("verify_ms", "vms")
	scopeAnd(b, scope)
	b.between("purchase_date", date(w.From), date(w.To))
	f.applyOn(b, "purchase_date")
	b.in("status", strs(f.Statuses))
	if before != "" {
		at, id, err := parseCursor(before)
		if err != nil {
			return nil, refuse("before", "the cursor is not one this API returned")
		}
		b.and("(event_time, event_id) < (toDateTime64(?, 3, 'UTC'), toUUID(?))", at, id)
	}
	b.order("purchase_date DESC", "event_time DESC", "event_id DESC").withLimit(limit + 1)

	out := &schemas.PurchasesOut{Verified: len(f.Verified) > 0}
	var devices []uint64
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var p schemas.PurchaseOut
		var device uint64
		var verifyMs uint32
		if err := rows.Scan(&p.EventID, &p.At, &p.InstallID, &p.PlayerID, &p.SessionID, &p.ProductID,
			&p.Store, &p.RevenueMicros, &p.Currency, &p.Platform, &p.Country, &p.AppVersion, &device,
			&p.Source, &p.Status, &p.StoreTransactionID, &p.RefundedAt, &p.RefundReason, &p.Rejection, &verifyMs); err != nil {
			return err
		}
		p.VerifyMs = int(verifyMs)
		if p.Status == "" {
			p.Status = "reported"
		}
		out.Purchases = append(out.Purchases, p)
		devices = append(devices, device)
		return nil
	}); err != nil {
		return nil, failed("purchases", err)
	}
	if len(out.Purchases) > limit {
		out.Purchases, devices = out.Purchases[:limit], devices[:limit]
		last := out.Purchases[limit-1]
		out.Next = last.At.UTC().Format("2006-01-02T15:04:05.000Z") + "_" + last.EventID
	}
	if len(devices) == 0 {
		return out, nil
	}

	// Whether each was the install's first purchase: a second statement for the page's devices,
	// a lookup on payers' sort key, rather than anything that would join the two tables.
	firsts := map[uint64]time.Time{}
	fb := from(factPayers)
	fb.column("device_id", "device").column("min(first_purchase_at)", "first")
	scopeAnd(fb, scope)
	f.payersCounted(fb)
	fb.in("device_id", uniqueDevices(devices))
	fb.group("device_id")
	if err := e.read(ctx, fb.statement(), func(rows driver.Rows) error {
		var device uint64
		var first time.Time
		if err := rows.Scan(&device, &first); err != nil {
			return err
		}
		firsts[device] = first
		return nil
	}); err != nil {
		return nil, failed("payers", err)
	}
	for i := range out.Purchases {
		out.Purchases[i].FirstPurchase = out.Purchases[i].At.Equal(firsts[devices[i]])
	}
	return out, nil
}

func uniqueDevices(devices []uint64) []any {
	seen := map[uint64]bool{}
	var out []any
	for _, d := range devices {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// parseCursor reads "<time>_<event id>".
func parseCursor(s string) (string, string, error) {
	at, id, ok := strings.Cut(s, "_")
	if !ok {
		return "", "", fmt.Errorf("no separator")
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", at)
	if err != nil {
		return "", "", err
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", "", err
	}
	return t.UTC().Format("2006-01-02 15:04:05.000"), id, nil
}
