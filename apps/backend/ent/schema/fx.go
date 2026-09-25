package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// FxRate is one currency's rate on one day, as Open Exchange Rates published it: how many of the
// currency a US dollar bought. Revenue is shown in dollars at the day it was paid. A small
// table: a hundred and seventy currencies a day.
type FxRate struct{ ent.Schema }

func (FxRate) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (FxRate) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (FxRate) Fields() []ent.Field {
	return []ent.Field{
		// The UTC day, at midnight.
		field.Time("day").Comment(dataclass.None),
		field.String("currency").MaxLen(3).Comment(dataclass.None),
		field.Float("per_usd").Positive().Comment(dataclass.None),
	}
}

func (FxRate) Indexes() []ent.Index {
	return []ent.Index{index.Fields("day", "currency").Unique()}
}
