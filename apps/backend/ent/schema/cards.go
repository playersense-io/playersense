package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// PaymentCard is what the billing page may show of a card saved at Stripe: its brand, last four
// digits, expiry and whether it is the default. The card itself is Stripe's and never passes
// through PlayerSense (ADR 0040); this is a copy of Stripe's own description of it, kept in step
// by the payment_method.* and customer.updated webhooks.
type PaymentCard struct{ ent.Schema }

func (PaymentCard) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (PaymentCard) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (PaymentCard) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),
		// "pm_..." Stripe's id: the thing to ask Stripe about, never a card number.
		field.String("stripe_payment_method_id").NotEmpty().MaxLen(255).Unique().Immutable().
			Comment(dataclass.None),
		field.String("brand").MaxLen(32).Default("").Comment(dataclass.Financial),
		field.String("last4").MaxLen(4).Default("").Comment(dataclass.Financial),
		field.Int("exp_month").Default(0).Comment(dataclass.Financial),
		field.Int("exp_year").Default(0).Comment(dataclass.Financial),
		field.String("funding").MaxLen(16).Default("").Comment(dataclass.None),
		field.String("country").MaxLen(2).Default("").Comment(dataclass.None),
		field.Bool("is_default").Default(false).Comment(dataclass.None),
		// When Stripe said it was removed. Kept rather than deleted, so a receipt that names the
		// card still has something to name.
		field.Time("detached_at").Optional().Nillable().Comment(dataclass.None),
	}
}

func (PaymentCard) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("cards").
			Field("account_id").Unique().Required().Immutable(),
	}
}

func (PaymentCard) Indexes() []ent.Index {
	return []ent.Index{index.Fields("account_id", "detached_at")}
}

// Receipt is a Stripe invoice for a top-up or a subscription: what the billing page lists under
// invoices and links to. Usage invoices are the Invoice table's, which bills them; these only
// describe payments Stripe already took.
type Receipt struct{ ent.Schema }

func (Receipt) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Receipt) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Receipt) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),
		field.String("stripe_invoice_id").NotEmpty().MaxLen(255).Unique().Immutable().Comment(dataclass.None),
		field.String("number").MaxLen(64).Default("").Comment(dataclass.None),
		field.Enum("kind").Values("top_up", "subscription").Comment(dataclass.None),
		field.String("description").MaxLen(255).Default("").Comment(dataclass.None),
		field.Enum("status").Values("draft", "open", "paid", "void", "uncollectible").Comment(dataclass.None),
		field.Int64("total_micros").Default(0).Comment(dataclass.Financial),
		field.String("currency").MaxLen(3).Default("usd").Comment(dataclass.Financial),
		field.Time("issued_at").Comment(dataclass.None),
		field.Time("paid_at").Optional().Nillable().Comment(dataclass.None),
		field.String("hosted_url").MaxLen(1024).Default("").Comment(dataclass.None),
		field.String("pdf_url").MaxLen(1024).Default("").Comment(dataclass.None),
		// Stripe's clock at the event that last wrote this row, so a late event cannot undo a
		// newer one.
		field.Time("stripe_updated_at").Comment(dataclass.None),
	}
}

func (Receipt) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("receipts").
			Field("account_id").Unique().Required().Immutable(),
	}
}

func (Receipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("account_id", "issued_at")}
}
