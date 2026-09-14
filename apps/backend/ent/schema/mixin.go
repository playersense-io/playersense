package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// classified is the §96 data class carried on every column.
//
// The classification is not documentation. `migrate verify` reads it back out of
// pg_description and fails when a column has none, which is what makes §96's claim — that
// the Article 30 record, the Play Data Safety declaration and the Apple privacy manifest
// all generate from one source — true rather than aspirational.
const (
	ClassIdentifier  = "class=identifier"  // identifies a person or device
	ClassBehavioural = "class=behavioural" // what a person did
	ClassDerived     = "class=derived"     // computed from behaviour, still attributable
	ClassFinancial   = "class=financial"   // revenue, purchases, entitlements
	ClassNone        = "class=none"        // organization, project, infrastructure
)

// Tier names the PostgreSQL schema an entity lives in (§99).
//
// Global holds nothing about a player. Regional holds everything that does, and never
// leaves the region that produced it. The governing rule is one line: a player identifier
// never appears in the global tier.
const (
	TierGlobal   = "global"
	TierRegional = "regional"
)

// mixin gives every entity an integer primary key and a public UUID.
//
// The integer primary key is deliberate. ClickHouse sorts on org_seq / project_seq /
// environment_seq (§23.1), and three UUIDs there would be 48 bytes of incompressible
// prefix on every row of every analytics table. So the integer id allocated here is the
// surrogate the data plane sorts on, and the UUID is what the outside world sees.
type mixin struct{ ent.Schema }

func (mixin) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("uid", uuid.UUID{}).
			Default(uuid.New).
			Immutable().
			Unique().
			Comment(ClassNone),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment(ClassNone),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment(ClassNone),
	}
}

// globalTier annotates an entity as belonging to the global control plane.
func globalTier() schema.Annotation {
	return entsql.Annotation{Schema: TierGlobal, WithComments: ptr(true)}
}

// regionalTier annotates an entity as belonging to a region's control plane.
func regionalTier() schema.Annotation {
	return entsql.Annotation{Schema: TierRegional, WithComments: ptr(true)}
}

func ptr[T any](v T) *T { return &v }
