package query

import (
	"context"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// Revenue in US dollars, what the stores keep, and what verification says: the parts of the
// Revenue page that are about money rather than counts.
//
// Every figure here reads revenue_daily or purchases alone, grouped by the currency it was paid
// in and the day, and is converted in Go at that day's rate (internal/fx): the rates are a small
// table in PostgreSQL, and a lookup per row in ClickHouse would be a join or a dictionary holding
// credentials, where a lookup per (day, currency) group here is a map.

// Rates converts an amount paid on a day to US dollars. fx.Table is one.
type Rates interface {
	USD(micros int64, currency string, day time.Time) (float64, bool)
}

// NetNote is why net revenue is an estimate, said wherever it is shown.
const NetNote = "Net is an estimate: gross, less refunds, less each store's share at the rate set for it. " +
	"What a store actually keeps depends on programs only the studio knows it is in (Apple's Small Business " +
	"Program and Google's first million at 15%, subscriptions after their first year, Steam's revenue tiers), " +
	"and on taxes the stores withhold by country before paying out. Set each store's share under the " +
	"organization's revenue settings to match your own statements."

// DefaultStoreFees is each store's share, in percent, before an organization sets its own.
var DefaultStoreFees = map[string]float64{"app_store": 30, "google_play": 30, "steam": 30, "web_shop": 3}

// FeeOf is a store's share as a fraction: the organization's, or the usual one.
func FeeOf(fees map[string]float64, store string) float64 {
	if pct, ok := fees[store]; ok {
		return pct / 100
	}
	return DefaultStoreFees[store] / 100
}

// money is one (day, store, currency) group of revenue_daily.
type money struct {
	day                time.Time
	store, currency    string
	gross, refunded    int64
	purchases, refunds int64
}

// revenueMoney adds the dollars: totals for the window and the one before, by day and by store,
// with estimated fees and net, and a dollar figure beside each product, store and country.
func (e *Engine) revenueMoney(ctx context.Context, scope Scope, w Window, f RevenueFilter, out *schemas.RevenueOut, withheld *Suppressed) error {
	if f.Rates == nil {
		return nil
	}
	prev := w.Previous()
	b := from(factRevenueDaily)
	b.column("toDateTime(day, 'UTC')", "d").
		column("toString(store)", "s").
		column("toString(currency)", "cur").
		column("sum(revenue_micros)", "rev").
		column("sum(refunded_micros)", "refunded").
		column("toInt64(sum(purchases))", "n").
		column("sum(refunds)", "r")
	scopeAnd(b, scope)
	b.between("day", date(prev.From), date(w.To))
	f.apply(b)
	b.groupOn("d", "s", "cur").withLimit(e.limits.MaxRows)
	var rows []money
	if err := e.read(ctx, b.statement(), func(r driver.Rows) error {
		var m money
		if err := r.Scan(&m.day, &m.store, &m.currency, &m.gross, &m.refunded, &m.purchases, &m.refunds); err != nil {
			return err
		}
		m.day = m.day.UTC()
		rows = append(rows, m)
		return nil
	}); err != nil {
		return failed("revenue_daily", err)
	}

	mo := &schemas.RevenueMoneyOut{Currency: "USD", Estimated: true, Note: NetNote, Unconverted: []string{}}
	unconverted := map[string]bool{}
	// Each currency's dollars and its own amount, so a product or country can be converted at the
	// rate its money was actually paid at over the window.
	curLocal, curUSD := map[string]float64{}, map[string]float64{}
	stores := map[string]*schemas.RevenueStoreMoneyOut{}
	days := map[time.Time]*schemas.RevenueMoneyDayOut{}
	for _, m := range rows {
		gross, ok1 := f.Rates.USD(m.gross, m.currency, m.day)
		refunded, ok2 := f.Rates.USD(m.refunded, m.currency, m.day)
		if !ok1 || !ok2 {
			unconverted[m.currency] = true
			continue
		}
		fee := FeeOf(f.Fees, m.store)
		fees := (gross - refunded) * fee
		if m.day.Before(w.From) {
			mo.PreviousGross += gross
			mo.PreviousNet += gross - refunded - fees
			continue
		}
		curLocal[m.currency] += float64(m.gross)
		curUSD[m.currency] += gross
		mo.Gross += gross
		mo.Refunded += refunded
		mo.Fees += fees
		st := stores[m.store]
		if st == nil {
			st = &schemas.RevenueStoreMoneyOut{Store: m.store, FeePercent: fee * 100}
			stores[m.store] = st
		}
		st.Gross += gross
		st.Refunded += refunded
		st.Fees += fees
		d := days[m.day]
		if d == nil {
			d = &schemas.RevenueMoneyDayOut{Day: m.day, ByStore: map[string]float64{}}
			days[m.day] = d
		}
		d.Gross += gross
		d.Refunded += refunded
		d.Fees += fees
		d.ByStore[m.store] += gross
	}
	mo.Net = mo.Gross - mo.Refunded - mo.Fees
	for c := range unconverted {
		mo.Unconverted = append(mo.Unconverted, c)
	}
	sort.Strings(mo.Unconverted)
	if exact, ok := f.Rates.(interface{ IsExact() bool }); ok {
		mo.Exact = exact.IsExact()
	}

	// The same disclosure rule as the counts: a window, a day or a store bought by too few carries
	// no money.
	if out.Totals.Buyers == nil {
		mo.Gross, mo.Refunded, mo.Fees, mo.Net = 0, 0, 0, 0
	}
	if out.Totals.PreviousBuyers == nil {
		mo.PreviousGross, mo.PreviousNet = 0, 0
	}
	shownStores := map[string]bool{}
	for _, s := range out.Stores {
		shownStores[s.Key] = true
	}
	for key, st := range stores {
		if !shownStores[key] {
			continue
		}
		st.Net = st.Gross - st.Refunded - st.Fees
		mo.Stores = append(mo.Stores, *st)
	}
	sort.Slice(mo.Stores, func(i, j int) bool { return mo.Stores[i].Gross > mo.Stores[j].Gross })
	withheldDay := map[time.Time]bool{}
	for _, d := range out.Days {
		if d.Withheld {
			withheldDay[d.Day.UTC()] = true
		}
	}
	for at := w.From; !at.After(w.To); at = at.AddDate(0, 0, 1) {
		row := schemas.RevenueMoneyDayOut{Day: at}
		if d := days[at.UTC()]; d != nil && !withheldDay[at.UTC()] {
			row = *d
			row.Net = row.Gross - row.Refunded - row.Fees
		}
		mo.Days = append(mo.Days, row)
	}
	out.Money = mo

	// A slice's dollars: each currency at the rate its money was paid at over the window.
	rate := func(currency string) (float64, bool) {
		if curLocal[currency] == 0 {
			return 0, false
		}
		return curUSD[currency] / curLocal[currency], true
	}
	for _, s := range []struct {
		column string
		into   []schemas.RevenueSliceOut
	}{{"product_id", out.Products}, {"store", out.Stores}, {"country_code", out.Countries}} {
		if len(s.into) == 0 {
			continue
		}
		byKey := map[string]float64{}
		sb := from(factRevenueDaily)
		sb.column("toString("+s.column+")", "k").column("toString(currency)", "cur").column("sum(revenue_micros)", "rev")
		scopeAnd(sb, scope)
		sb.between("day", date(w.From), date(w.To))
		f.apply(sb)
		sb.group("k", "cur").withLimit(e.limits.MaxRows)
		if err := e.read(ctx, sb.statement(), func(r driver.Rows) error {
			var k, cur string
			var rev int64
			if err := r.Scan(&k, &cur, &rev); err != nil {
				return err
			}
			if per, ok := rate(cur); ok {
				byKey[k] += float64(rev) * per
			}
			return nil
		}); err != nil {
			return failed("revenue_daily", err)
		}
		for i := range s.into {
			if usd, ok := byKey[s.into[i].Key]; ok {
				v := usd
				s.into[i].RevenueUSD = &v
			}
		}
	}
	return nil
}

// revenueRefunds is why purchases were given back in the window, and how long after buying:
// verified purchases only, since only a store says a purchase was refunded.
func (e *Engine) revenueRefunds(ctx context.Context, scope Scope, w Window, f RevenueFilter, out *schemas.RevenueOut) error {
	if len(f.Verified) == 0 {
		return nil
	}
	b := from(factPurchases).deduplicated()
	b.column("toString(refund_reason)", "reason").
		column("toString(currency)", "cur").
		column("toDateTime(toDate(assumeNotNull(refunded_at)), 'UTC')", "d").
		column("toUInt64(count())", "n").
		column("sum(revenue_micros)", "rev").
		column("quantile(0.5)(dateDiff('second', event_time, assumeNotNull(refunded_at)))", "secs")
	scopeAnd(b, scope)
	// A purchase is refunded within the stores' windows (Apple's ninety days, Google's and Steam's
	// shorter), so reading ninety days before the window finds every refund in it.
	b.between("purchase_date", date(w.From.AddDate(0, 0, -90)), date(w.To))
	b.and("source = 'verified'").and("status = 'refunded'").
		and("refunded_at >= ? AND refunded_at < ?", timestamp(w.From), timestamp(w.end()))
	f.applyNoCount(b)
	b.group("reason", "cur", "d").withLimit(e.limits.MaxRows)
	reasons := map[string]*schemas.RefundReasonOut{}
	var weighted, count float64
	if err := e.read(ctx, b.statement(), func(r driver.Rows) error {
		var reason, cur string
		var at time.Time
		var n uint64
		var rev int64
		var secs float64
		if err := r.Scan(&reason, &cur, &at, &n, &rev, &secs); err != nil {
			return err
		}
		if reason == "" {
			reason = "unknown"
		}
		ro := reasons[reason]
		if ro == nil {
			ro = &schemas.RefundReasonOut{Reason: reason}
			reasons[reason] = ro
		}
		ro.Refunds += n
		if f.Rates != nil {
			if usd, ok := f.Rates.USD(rev, cur, at); ok {
				ro.USD += usd
			}
		}
		weighted += secs * float64(n)
		count += float64(n)
		return nil
	}); err != nil {
		return failed("purchases", err)
	}
	ro := &schemas.RevenueRefundsOut{Reasons: []schemas.RefundReasonOut{}}
	for _, r := range reasons {
		ro.Reasons = append(ro.Reasons, *r)
		ro.Refunds += r.Refunds
	}
	sort.Slice(ro.Reasons, func(i, j int) bool { return ro.Reasons[i].Refunds > ro.Reasons[j].Refunds })
	if count > 0 {
		ro.MedianDays = weighted / count / 86400
	}
	out.Refunds = ro
	return nil
}

// revenueVerification is what the stores said about the receipts sent in the window: verified,
// test and rejected, by day and by store, how long they took, and why receipts were turned away.
func (e *Engine) revenueVerification(ctx context.Context, scope Scope, w Window, f RevenueFilter, out *schemas.RevenueOut) error {
	if len(f.Verified) == 0 {
		return nil
	}
	vo := &schemas.RevenueVerificationOut{Rejections: []schemas.RejectionOut{}, ByStore: []schemas.VerificationStoreOut{}}

	d := from(factRevenueDaily)
	d.column("toDateTime(day, 'UTC')", "d").
		column("sum(purchases) + sum(sandbox)", "ok").
		column("sum(sandbox)", "sb").
		column("sum(rejected)", "rej")
	scopeAnd(d, scope)
	d.between("day", date(w.From), date(w.To))
	d.in("store", strs(f.Stores)).in("country_code", strs(f.Countries)).in("product_id", strs(f.Products))
	d.and("source = 'verified'")
	d.groupOn("d")
	byDay := map[time.Time]schemas.VerificationDayOut{}
	if err := e.read(ctx, d.statement(), func(r driver.Rows) error {
		var at time.Time
		var v schemas.VerificationDayOut
		var sb uint64
		if err := r.Scan(&at, &v.Verified, &sb, &v.Rejected); err != nil {
			return err
		}
		v.Day = at.UTC()
		byDay[v.Day] = v
		vo.Verified += v.Verified
		vo.Sandbox += sb
		vo.Rejected += v.Rejected
		return nil
	}); err != nil {
		return failed("revenue_daily", err)
	}
	for at := w.From; !at.After(w.To); at = at.AddDate(0, 0, 1) {
		v, ok := byDay[at.UTC()]
		if !ok {
			v = schemas.VerificationDayOut{Day: at}
		}
		vo.Days = append(vo.Days, v)
	}

	p := from(factPurchases).deduplicated()
	p.column("toString(store)", "s").
		column("toUInt64(countIf(status != 'rejected'))", "ok").
		column("toUInt64(countIf(status = 'rejected'))", "rej").
		column("quantileIf(0.5)(verify_ms, status != 'rejected')", "ms")
	scopeAnd(p, scope)
	p.between("purchase_date", date(w.From), date(w.To))
	p.and("source = 'verified'")
	f.applyNoCount(p)
	p.group("s")
	if err := e.read(ctx, p.statement(), func(r driver.Rows) error {
		var s schemas.VerificationStoreOut
		var ms float64
		if err := r.Scan(&s.Store, &s.Verified, &s.Rejected, &ms); err != nil {
			return err
		}
		if ms == ms { // not NaN: a store with only rejections has no median
			s.MedianMs = ms
		}
		vo.ByStore = append(vo.ByStore, s)
		return nil
	}); err != nil {
		return failed("purchases", err)
	}

	rj := from(factPurchases).deduplicated()
	rj.column("toString(rejection)", "code").column("toUInt64(count())", "n")
	scopeAnd(rj, scope)
	rj.between("purchase_date", date(w.From), date(w.To))
	rj.and("source = 'verified'").and("status = 'rejected'")
	f.applyNoCount(rj)
	rj.group("code").order("n DESC")
	if err := e.read(ctx, rj.statement(), func(r driver.Rows) error {
		var x schemas.RejectionOut
		if err := r.Scan(&x.Code, &x.Count); err != nil {
			return err
		}
		vo.Rejections = append(vo.Rejections, x)
		return nil
	}); err != nil {
		return failed("purchases", err)
	}
	out.Verification = vo
	return nil
}

// applyNoCount narrows by what was bought and where, without choosing verified or reported rows:
// for reads that name the verified rows themselves.
func (f RevenueFilter) applyNoCount(b *builder) {
	b.in("store", strs(f.Stores)).in("country_code", strs(f.Countries)).in("product_id", strs(f.Products))
}
