package staffnotify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/user"
	"playersense.dev/backend/internal/notify"
)

// Digest is one UTC day of the business, and the totals as they stand when it is read.
//
// Money is read off the ledger, which is what §38 and ADR 0036 make the record of it, and only the
// entries that are money: a `topup` and an `invoice_payment` arrived, a `refund` left, and `usage`
// is what studios spent, which is the number that turns into next month's topups. `promo` and
// `expiry` are credit PlayerSense gave and took back, never revenue (ent/schema/billing.go), and
// are not here. Only US dollars are counted, because the ledger holds nothing else yet.
type Digest struct {
	Day time.Time // midnight UTC of the day covered

	TopupsMicros   int64
	InvoicesMicros int64
	RefundedMicros int64
	UsageMicros    int64

	NewOrganizations, TotalOrganizations int
	NewUsers, TotalUsers                 int
	NewProjects, TotalProjects           int

	// Owing is accounts past what they may owe: in their grace period or suspended.
	Owing int
}

// MoneyInMicros is what arrived, before refunds.
func (d Digest) MoneyInMicros() int64 { return d.TopupsMicros + d.InvoicesMicros }

// NetMicros is what arrived less what left.
func (d Digest) NetMicros() int64 { return d.MoneyInMicros() - d.RefundedMicros }

// Collect reads the day that starts at `day`, which is taken as UTC midnight.
func Collect(ctx context.Context, c *ent.Client, day time.Time) (Digest, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	d := Digest{Day: from}

	var sums []struct {
		Kind string `json:"kind"`
		Sum  int64  `json:"sum"`
	}
	err := c.LedgerEntry.Query().
		Where(ledgerentry.CreatedAtGTE(from), ledgerentry.CreatedAtLT(to), ledgerentry.Currency("usd"),
			ledgerentry.KindIn(ledgerentry.KindTopup, ledgerentry.KindInvoicePayment,
				ledgerentry.KindRefund, ledgerentry.KindUsage)).
		GroupBy(ledgerentry.FieldKind).
		Aggregate(ent.As(ent.Sum(ledgerentry.FieldAmountMicros), "sum")).
		Scan(ctx, &sums)
	if err != nil {
		return d, fmt.Errorf("sum the day's ledger: %w", err)
	}
	for _, s := range sums {
		switch ledgerentry.Kind(s.Kind) {
		case ledgerentry.KindTopup:
			d.TopupsMicros = s.Sum
		case ledgerentry.KindInvoicePayment:
			d.InvoicesMicros = s.Sum
		case ledgerentry.KindRefund:
			d.RefundedMicros = abs(s.Sum) // written negative: money out
		case ledgerentry.KindUsage:
			d.UsageMicros = abs(s.Sum) // written negative: spent
		}
	}

	counts := []struct {
		into *int
		what string
		read func() (int, error)
	}{
		{&d.NewOrganizations, "new organizations", func() (int, error) {
			return c.Organization.Query().Where(organization.CreatedAtGTE(from), organization.CreatedAtLT(to)).Count(ctx)
		}},
		{&d.TotalOrganizations, "organizations", func() (int, error) { return c.Organization.Query().Count(ctx) }},
		{&d.NewUsers, "new users", func() (int, error) {
			return c.User.Query().Where(user.CreatedAtGTE(from), user.CreatedAtLT(to)).Count(ctx)
		}},
		{&d.TotalUsers, "users", func() (int, error) { return c.User.Query().Count(ctx) }},
		{&d.NewProjects, "new projects", func() (int, error) {
			return c.Project.Query().Where(project.CreatedAtGTE(from), project.CreatedAtLT(to)).Count(ctx)
		}},
		{&d.TotalProjects, "projects", func() (int, error) { return c.Project.Query().Count(ctx) }},
		{&d.Owing, "accounts owing", func() (int, error) {
			return c.BillingAccount.Query().Where(billingaccount.StateIn(
				billingaccount.StateGracePeriod, billingaccount.StateSuspended)).Count(ctx)
		}},
	}
	for _, n := range counts {
		v, err := n.read()
		if err != nil {
			return d, fmt.Errorf("count %s: %w", n.what, err)
		}
		*n.into = v
	}
	return d, nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// Message renders the digest for Slack: a few aligned rows, which slackText puts in a code block.
func (d Digest) Message(dashboard string) notify.Message {
	rows := [][2]string{
		{"Money in", fmt.Sprintf("%s  (top-ups %s, invoices %s)", dollars(d.MoneyInMicros()), dollars(d.TopupsMicros), dollars(d.InvoicesMicros))},
		{"Refunded", dollars(d.RefundedMicros)},
		{"Net", dollars(d.NetMicros())},
		{"Usage charged", dollars(d.UsageMicros)},
		{"New organizations", fmt.Sprintf("%d  (%d in all)", d.NewOrganizations, d.TotalOrganizations)},
		{"New users", fmt.Sprintf("%d  (%d in all)", d.NewUsers, d.TotalUsers)},
		{"New projects", fmt.Sprintf("%d  (%d in all)", d.NewProjects, d.TotalProjects)},
		{"Accounts owing", fmt.Sprint(d.Owing)},
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r[0]))
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, r[0]+strings.Repeat(" ", width-len(r[0]))+"  "+r[1])
	}
	return notify.Message{
		Title:   "Growth, " + d.Day.Format("Mon 2 Jan 2006") + " (UTC)",
		Summary: "",
		Lines:   lines,
		Source:  "Daily growth digest",
		Link:    dashboard,
	}
}

// dollars renders micro-dollars as $1,234.56, rounded to the cent.
func dollars(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}
	cents := (micros + 5000) / 10000
	whole := fmt.Sprint(cents / 100)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return fmt.Sprintf("%s$%s.%02d", sign, whole, cents%100)
}
