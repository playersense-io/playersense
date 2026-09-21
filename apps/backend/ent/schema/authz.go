package schema

import (
	"context"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/dataclass"
)

// Hooks refuses a permission outside the vocabulary, on every write.
//
// A hook rather than a check in a handler, so there is no path that writes a role without
// passing through it — including a migration, a seed and whatever the admin console turns out
// to be. A role holding "liveops.publsh" would otherwise grant nothing, silently, and the
// person who typed it would find out when somebody could not publish.
//
// ent's JSON fields take no field validator, and a schema cannot import the package generated
// from it. Hence the structural type assertion: the generated mutation has the getter, this
// file names its shape without depending on it.
func (Role) Hooks() []ent.Hook {
	return []ent.Hook{validatePermissionsHook}
}

func validatePermissionsHook(next ent.Mutator) ent.Mutator {
	return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
		if mutation, ok := m.(interface{ Permissions() ([]string, bool) }); ok {
			// The second value is "was this field set by this mutation". An update
			// that touches only the name must not be made to re-validate a list it
			// never looked at.
			if names, set := mutation.Permissions(); set {
				permissions := make([]authz.Permission, len(names))
				for i, name := range names {
					permissions[i] = authz.Permission(name)
				}
				if err := authz.Validate(permissions); err != nil {
					return nil, err
				}
			}
		}
		return next.Mutate(ctx, m)
	})
}

// Role is what a person may do, as a row an organisation owns.
//
// The same shape as a targeting segment, for the same reason: PlayerSense owns the vocabulary
// and the organisation composes it. Six roles are seeded when an organisation is created —
// two system ones and four ordinary rows — and a studio deletes what it does not want and
// edits what it keeps. What it cannot do is invent a permission, which is what keeps the
// checker a map lookup rather than an interpreter.
//
// # Role management is not a permission
//
// There is deliberately no `role.manage` in the vocabulary. If there were, it would be
// silently equivalent to OWNER — anyone holding it could write themselves a role containing
// everything — so creating roles and assigning them is reserved to the two system roles
// instead. That removes the privilege-escalation question rather than defending against it.
type Role struct{ ent.Schema }

func (Role) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Role) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Role) Fields() []ent.Field {
	return []ent.Field{
		field.Int("organization_id").Immutable().Comment(dataclass.None),

		// Stable, and immutable for the same reason a segment's key is: memberships point
		// at the row, but a seeded role is found by key when an organisation is repaired
		// or a new permission is rolled out, and renaming one would orphan that.
		field.String("key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		// What the dashboard shows. Editable even for a system role: an organisation that
		// calls its owners "Studio Leads" is renaming a label, not changing what they may
		// do.
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),

		// The permissions, as authz.Permission strings. A JSON array rather than a join
		// table because it is a set nobody joins on — every read is "what may this member
		// do", which loads the one role anyway — and the names are validated in Go against
		// the closed vocabulary exactly as a rule's fact keys are.
		field.JSON("permissions", []string{}).Default([]string{}).Comment(dataclass.None),

		// A system role's permissions are computed from the vocabulary rather than read
		// from this row, so a permission added tomorrow reaches every owner without a
		// migration. The column is still written at seed time so that a person reading the
		// table sees what it means, and immutable so nothing can quietly promote a custom
		// role into one.
		field.Bool("system").Default(false).Immutable().Comment(dataclass.None),
	}
}

func (Role) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("roles").
			Field("organization_id").Unique().Required().Immutable(),
		edge.To("members", OrganizationMember.Type),
		edge.To("invitations", Invitation.Type),
	}
}

func (Role) Indexes() []ent.Index {
	return []ent.Index{index.Fields("key").Edges("organization").Unique()}
}

// MemberProject is which projects one member can see.
//
// The second axis, and keeping it separate from the role is what stops this from becoming an
// IAM nobody can read: **the role answers *what*, this answers *where*.** A LiveOps manager on
// two of a studio's five games holds one role and two of these rows, rather than two roles
// whose names have to encode a game.
//
// Rows exist only when the membership's `all_projects` is false. The common case — everybody
// sees everything — therefore costs no rows at all, and "this person sees every project,
// including ones created tomorrow" is expressible, which a list of rows cannot say.
type MemberProject struct{ ent.Schema }

func (MemberProject) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (MemberProject) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (MemberProject) Fields() []ent.Field {
	return []ent.Field{
		field.Int("member_id").Immutable().Comment(dataclass.None),
		field.Int("project_id").Immutable().Comment(dataclass.None),

		// Granted rather than created: the row's created_at is when it was written, and
		// these agree until somebody re-grants access that was removed. An access review
		// (§08) asks "since when", which is about the current grant.
		field.Time("granted_at").Immutable().Default(now).Comment(dataclass.None),
	}
}

func (MemberProject) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("member", OrganizationMember.Type).Ref("project_access").
			Field("member_id").Unique().Required().Immutable(),
		edge.From("project", Project.Type).Ref("member_access").
			Field("project_id").Unique().Required().Immutable(),
	}
}

func (MemberProject) Indexes() []ent.Index {
	return []ent.Index{index.Edges("member", "project").Unique()}
}

// AuditEvent is the customer's own log: who published, who invited, who revoked a key, who
// erased a player.
//
// Shown in their dashboard, which is the point — it answers "this looks wrong, who did this"
// for a studio, not for us. Tens of rows a day for an organisation, so it is an ordinary
// PostgreSQL table rather than anything cleverer.
//
// # Append-only by construction
//
// Every field is Immutable and the mixin omits `updated_at`, so the generated update builder
// has **no setter on it at all** — `Where`, `Save`, `Exec`, and no method that could change
// what a row says. §08's "a log the operator can edit proves nothing" becomes a compile error
// rather than a policy somebody has to remember. Deleting is still possible, because a row has
// to be removable when its organisation is.
type AuditEvent struct{ ent.Schema }

func (AuditEvent) Mixin() []ent.Mixin               { return []ent.Mixin{appendOnlyMixin{}} }
func (AuditEvent) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (AuditEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int("organization_id").Immutable().Comment(dataclass.None),

		// The actor, as one of three things. A user and a key are edges so the dashboard
		// can show a name and a face; `actor_label` is what the row says when neither
		// resolves — a deactivated user, a revoked key, or the scheduler acting on
		// nobody's behalf. Written at the time rather than resolved at read time, because
		// an audit entry that becomes "unknown" when somebody leaves the company is an
		// audit entry that has lost the thing it was for.
		field.Int("actor_user_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.Int("actor_key_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.String("actor_label").MaxLen(254).Default("").Immutable().Comment(dataclass.None),

		// What happened, as a dotted name from the same family as the permissions —
		// "liveops.publish", "member.invite", "key.revoke". Not an enum: an enum makes
		// logging a new kind of action a migration, and the day that friction exists is
		// the day something stops being logged.
		field.String("action").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		// What it happened to. A type and an id rather than an edge per kind, because a
		// log that needed a foreign key per target type could not record the deletion of
		// the thing it points at.
		field.String("target_type").MaxLen(64).Default("").Immutable().Comment(dataclass.None),
		field.String("target_id").MaxLen(128).Default("").Immutable().Comment(dataclass.None),

		// Whatever the action needs to be explicable a year later: the version published,
		// the role granted, the previous value. Deliberately shapeless — an audit entry
		// that could not say what changed would be a timestamp.
		field.JSON("metadata", map[string]any{}).Default(map[string]any{}).Immutable().
			Comment(dataclass.None),

		// Where from. class=none rather than class=identifier, on the same reading as
		// User.email: §99's rule is about *player* identifiers, and this is a studio
		// employee's own administrative trail. It is still personal data about that
		// employee, and it is why these rows go when their organisation does.
		field.String("ip").MaxLen(45).Default("").Immutable().Comment(dataclass.None),
	}
}

func (AuditEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("audit_events").
			Field("organization_id").Unique().Required().Immutable(),
		edge.From("actor_user", User.Type).Ref("audit_events").
			Field("actor_user_id").Unique().Immutable(),
		edge.From("actor_key", APIKey.Type).Ref("audit_events").
			Field("actor_key_id").Unique().Immutable(),
	}
}

func (AuditEvent) Indexes() []ent.Index {
	return []ent.Index{
		// The only query: one organisation's log, newest first, sometimes filtered to one
		// action. Nothing reads these by actor across organisations, and an index that
		// allowed it would be an index that invited it.
		index.Fields("organization_id", "created_at"),
		index.Fields("organization_id", "action", "created_at"),
	}
}

// StaffAccess is every time a PlayerSense employee reads a customer's data.
//
// A stricter thing than AuditEvent and a separate table on purpose. §08 requires it, and
// requires a reason per read.
//
// # The reason is a ticket, not free text
//
// `ticket` is required and is meant to be a reference into a system the employee does not
// control. "debugging" typed into a box proves nothing and is what a log of reasons degrades
// into within a month; a ticket number is checkable by somebody who was not there.
//
// # It has no caller yet
//
// The admin console that will write these does not exist. Built now anyway, because the
// alternative is a console built first and instrumented afterwards, and "afterwards" is how a
// read that was never logged becomes a read nobody can rule out.
type StaffAccess struct{ ent.Schema }

func (StaffAccess) Mixin() []ent.Mixin { return []ent.Mixin{appendOnlyMixin{}} }

// Annotations names the table, because ent's pluralizer produces `staff_accesses` and "staff
// access" is already the plural of itself.
func (StaffAccess) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments(), entsql.Annotation{Table: "staff_access"}}
}

func (StaffAccess) Fields() []ent.Field {
	return []ent.Field{
		// The employee, by the address on their verified token. A string rather than an
		// edge to User: staff sign in to an admin console, not to a customer's dashboard,
		// and an edge would imply they are a member of something.
		field.String("staff_email").NotEmpty().MaxLen(254).Immutable().Comment(dataclass.None),

		// Whose data, and how far in. Both nillable: a fleet-wide operation names no
		// organisation, and an organisation-level read names no project.
		//
		// Plain integers with no edge, which is the one place this file skips a foreign
		// key on purpose. A record of who read a customer's data has to outlive that
		// customer: with an edge, deleting the organisation would either cascade the
		// evidence away or refuse the deletion, and §08 wants neither.
		field.Int("organization_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.Int("project_id").Optional().Nillable().Immutable().Comment(dataclass.None),

		field.String("action").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		// Required, and the reason this table is worth having.
		field.String("ticket").NotEmpty().MaxLen(128).Immutable().Comment(dataclass.None),

		field.String("note").MaxLen(500).Default("").Immutable().Comment(dataclass.None),
		field.String("ip").MaxLen(45).Default("").Immutable().Comment(dataclass.None),
	}
}

func (StaffAccess) Edges() []ent.Edge { return nil }

func (StaffAccess) Indexes() []ent.Index {
	return []ent.Index{
		// "Who reached this customer's data, and when" — the access review's question.
		index.Fields("organization_id", "created_at"),
		// And its mirror: everything one employee touched, which is what a leaver's
		// review reads.
		index.Fields("staff_email", "created_at"),
	}
}
