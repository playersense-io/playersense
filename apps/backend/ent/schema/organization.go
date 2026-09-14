package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Organization is the billing and ownership boundary (§75).
type Organization struct{ ent.Schema }

func (Organization) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Organization) Annotations() []schema.Annotation { return []schema.Annotation{globalTier()} }

func (Organization) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(120).Comment(ClassNone),
		field.String("slug").NotEmpty().MaxLen(64).Unique().Comment(ClassNone),
	}
}

func (Organization) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("projects", Project.Type),
		edge.To("members", OrganizationMember.Type),
	}
}

// User is a person who signs in to the dashboard.
//
// An email address is personal data, but it belongs to a studio employee rather than to a
// player — §99's rule is about player identifiers, which is why this stays global.
type User struct{ ent.Schema }

func (User) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (User) Annotations() []schema.Annotation { return []schema.Annotation{globalTier()} }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").NotEmpty().MaxLen(254).Unique().Comment(ClassNone),
		field.String("name").MaxLen(120).Optional().Comment(ClassNone),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{edge.To("memberships", OrganizationMember.Type)}
}

// OrganizationMember is a user's role within an organization.
type OrganizationMember struct{ ent.Schema }

func (OrganizationMember) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (OrganizationMember) Annotations() []schema.Annotation {
	return []schema.Annotation{globalTier()}
}

func (OrganizationMember) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("role").Values("owner", "admin", "member", "viewer").Comment(ClassNone),
	}
}

func (OrganizationMember) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("members").Unique().Required(),
		edge.From("user", User.Type).Ref("memberships").Unique().Required(),
	}
}

func (OrganizationMember) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("organization", "user").Unique(),
	}
}
