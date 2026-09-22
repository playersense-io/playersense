package schema

import (
	"context"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/dataclass"
)

// Real-time usage charging (ADR 0039): what a player costs, who has already been counted this
// month, and the queue of charges waiting for the wallet.
//
// Three tables, and the split is the design. Pricing a player and taking money from a wallet are
// two different operations with two different failure modes, and a queue between them is what lets
// each fail without the other noticing:
//
//	billable_player   who has been counted this month — the only thing that decides "new"
//	usage_charge      what those new players cost, queued; the wallet has not been touched
//	ledger_entry      what the wallet paid, one entry per drain
//
// A player is priced when first seen. The charge sits in the queue until the drain, every few
// seconds, nets an account's queue into one spend. Neither step blocks ingestion: the SDK's batch
// has already been answered 202 before the charger sees it.

// RateCard is a price list for one meter on one plan, as it stood from one moment.
//
// **Immutable, and that is how prices change.** Staff never edit a card; they publish a new one with
// a later `effective_from`, and the charger prices each player at the card in force when that player
// arrived. So a price change mid-month is exact — players before it keep the old price, players after
// it get the new one — and "what did this charge cost per player" is answerable forever from the row
// the charge points at. An editable card would reprice history silently, which is the one thing a
// price list must not do.
type RateCard struct{ ent.Schema }

func (RateCard) Mixin() []ent.Mixin               { return []ent.Mixin{appendOnlyMixin{}} }
func (RateCard) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (RateCard) Fields() []ent.Field {
	return []ent.Field{
		field.Int("plan_id").Immutable().Comment(dataclass.None),

		// Only tracked players today. An enum rather than a string so a card for a meter
		// nothing charges cannot be published and then quietly never apply.
		field.Enum("meter").Values(string(billing.TrackedPlayers)).Immutable().
			Comment(dataclass.None),

		//	graduated  each unit at the band its position falls in (the default)
		//	volume     every unit at the band the month's total reached
		field.Enum("mode").Values(string(billing.Graduated), string(billing.Volume)).
			Default(string(billing.Graduated)).Immutable().Comment(dataclass.None),

		field.String("currency").MaxLen(3).Default("usd").Immutable().Comment(dataclass.Financial),

		// The bands, lowest first. Validated by the hook below, because a JSON field takes
		// no validator and a gap between bands is a player with no price.
		field.JSON("tiers", billing.Tiers{}).Immutable().Comment(dataclass.Financial),

		// From when this card prices new players. A card is in force from here until the
		// next card's effective_from.
		field.Time("effective_from").Immutable().Comment(dataclass.None),

		// Why it changed, for the next person to ask.
		field.String("note").MaxLen(500).Default("").Immutable().Comment(dataclass.None),

		// Who published it, self-asserted, for the reason the operator credit routes give:
		// the shared operator token cannot say who is holding it.
		field.String("created_by").MaxLen(254).Immutable().Comment(dataclass.None),
	}
}

func (RateCard) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("plan", Plan.Type).Ref("rate_cards").
			Field("plan_id").Unique().Required().Immutable(),
		edge.To("charges", UsageCharge.Type),
	}
}

func (RateCard) Indexes() []ent.Index {
	return []ent.Index{
		// The lookup the charger makes: the latest card for this plan and meter that is
		// already in force. Unique, so two cards cannot both claim the same instant.
		index.Fields("meter", "effective_from").Edges("plan").Unique(),
	}
}

func (RateCard) Hooks() []ent.Hook {
	return []ent.Hook{func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			mutation, ok := m.(interface {
				Tiers() (billing.Tiers, bool)
				Mode() (string, bool)
			})
			if !ok {
				return next.Mutate(ctx, m)
			}
			if tiers, set := mutation.Tiers(); set {
				mode := billing.Graduated
				if chosen, set := mutation.Mode(); set {
					mode = billing.Mode(chosen)
				}
				if err := (billing.Card{Mode: mode, Tiers: tiers}).Validate(); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	}}
}

// BillablePlayer is one player counted for one account in one month.
//
// The whole of "is this player new": a unique row per (account, month, digest), inserted with ON
// CONFLICT DO NOTHING, and the rows that were actually inserted are the new players. That makes
// counting idempotent by construction — a redelivered batch, a replayed delivery or a second
// consumer racing the first all find the row already there and count nothing. No read-then-write
// window exists to double-count in.
//
// # The digest, not the player
//
// A keyed HMAC of the device, salted with the month (internal/charging). Without the key the column
// cannot be joined to events_raw; with the month in the input, a player's January and February rows
// are unrelated values, so the table cannot follow a person across months even to someone who holds
// it. Still classified `identifier`, because a keyed pseudonym is personal data and §96 says what a
// column *is*, not how hard it is to read.
//
// Pruned once a month is settled: §38's "the actor set itself is never stored" is kept as far as a
// real-time meter allows — for the month it is counting, and no longer.
type BillablePlayer struct{ ent.Schema }

func (BillablePlayer) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments(), entsql.Annotation{Table: "billable_player"}}
}

// Fields carry no uid and no timestamps. This is the one table that grows with players rather than
// with customers, and a UUID column and its unique index would double the row for nothing anybody
// reads.
func (BillablePlayer) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Comment(dataclass.None),
		field.Int("account_id").Immutable().Comment(dataclass.None),
		field.Time("period_start").Immutable().Comment(dataclass.None),
		field.Bytes("digest").MinLen(16).MaxLen(32).Immutable().Comment(dataclass.Identifier),
	}
}

func (BillablePlayer) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "period_start", "digest").Unique(),
		// The prune: everything older than the month before last.
		index.Fields("period_start"),
	}
}

// MonthlyUsage is one account's running count for one month, and the lock that orders its charges.
//
// Every accrual for an account locks this row first, so two consumers pricing the same account's
// players take turns: the second sees the first's count and prices its players at the positions
// after them. Without the lock, two batches could both price "the 100,000th player" at band one.
type MonthlyUsage struct{ ent.Schema }

func (MonthlyUsage) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (MonthlyUsage) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments(), entsql.Annotation{Table: "monthly_usage"}}
}

func (MonthlyUsage) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),
		field.Time("period_start").Immutable().Comment(dataclass.None),

		// Distinct players counted so far this month. Exact: it moves only by the number of
		// billable_player rows an accrual actually inserted.
		field.Int64("tracked_players").Default(0).Min(0).Comment(dataclass.None),

		// The sum of every charge queued for this month, settled or not. Equal to the card's
		// Total(tracked_players) when one card priced the whole month.
		field.Int64("charged_micros").Default(0).Comment(dataclass.Financial),
	}
}

func (MonthlyUsage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("monthly_usage").
			Field("account_id").Unique().Required().Immutable(),
	}
}

func (MonthlyUsage) Indexes() []ent.Index {
	return []ent.Index{index.Fields("period_start").Edges("account").Unique()}
}

// UsageCharge is one priced group of new players, waiting for — or already paid by — the wallet.
//
// The queue. A row is written in the same transaction that counted its players, so a player counted
// is a charge queued: there is no moment at which the count moved and the money was not owed. A row
// is settled by the drain, in the same transaction that spent from the wallet, so a charge paid is a
// charge marked: there is no moment at which the wallet moved and the row still looked unpaid.
//
// **Nothing here is ever deleted or rewritten except `settled_at` and `ledger_entry_id`.** A drain
// that fails leaves the row unsettled and the next drain takes it; a wallet that cannot cover it
// covers what it can and the rest becomes arrears. So a charge cannot be lost by failing — the only
// way out of the queue is being paid for.
type UsageCharge struct{ ent.Schema }

func (UsageCharge) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (UsageCharge) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (UsageCharge) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),
		field.Time("period_start").Immutable().Comment(dataclass.None),
		field.Int("rate_card_id").Immutable().Comment(dataclass.None),

		// Positions within the month, inclusive: this charge is for the players counted
		// first_unit through last_unit. Contiguous across an account's charges for a month,
		// which is what makes a statement line say "players 100,001 to 100,142".
		field.Int64("first_unit").Positive().Immutable().Comment(dataclass.None),
		field.Int64("last_unit").Positive().Immutable().Comment(dataclass.None),

		// Signed. Negative only on a volume card crossing into a cheaper band.
		field.Int64("amount_micros").Immutable().Comment(dataclass.Financial),
		field.String("currency").MaxLen(3).Default("usd").Immutable().Comment(dataclass.Financial),

		// When the drain paid it, and the entry it paid it with.
		field.Time("settled_at").Optional().Nillable().Comment(dataclass.None),
		field.Int("ledger_entry_id").Optional().Nillable().Comment(dataclass.None),
	}
}

func (UsageCharge) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("usage_charges").
			Field("account_id").Unique().Required().Immutable(),
		edge.From("rate_card", RateCard.Type).Ref("charges").
			Field("rate_card_id").Unique().Required().Immutable(),
		// One-way. A back-reference from LedgerEntry would give the append-only ledger an
		// edge setter, and TestAnAppendOnlyRowHasNoSetter is right to refuse that: the
		// foreign key lives here, on the row that is allowed to change.
		edge.To("ledger_entry", LedgerEntry.Type).
			Field("ledger_entry_id").Unique(),
	}
}

func (UsageCharge) Indexes() []ent.Index {
	return []ent.Index{
		// The second guarantee against charging twice. The first is billable_player: an
		// accrual that counted nothing queues nothing. This one holds even if that were
		// somehow bypassed — two charges for the same players in the same month cannot
		// both exist.
		index.Fields("period_start", "first_unit").Edges("account").Unique(),

		// The drain's scan: unsettled charges only, which is a handful of rows however
		// large the table grows.
		index.Fields("account_id").
			Annotations(entsql.IndexWhere("settled_at IS NULL")),
	}
}
