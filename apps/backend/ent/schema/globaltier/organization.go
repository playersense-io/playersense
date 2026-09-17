package globaltier

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// withComments emits each field's class= comment into pg_description, which is what
// lets `migrate verify` enforce §96 against the live database.
func withComments() schema.Annotation {
	yes := true
	return entsql.Annotation{WithComments: &yes}
}

// Organization is the billing and ownership boundary (§75).
type Organization struct{ ent.Schema }

func (Organization) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Organization) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Organization) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("slug").NotEmpty().MaxLen(64).Unique().Comment(dataclass.None),

		// The ceiling on members, stored rather than hardcoded so a plan can raise it for
		// one organization without a deploy. Defaults to MaxMembers.
		//
		// Enforced when somebody is added rather than by a database constraint: the check
		// is "how many are there now", which SQL cannot express as a column constraint,
		// and a trigger would put the rule somewhere nobody reading this file would find
		// it.
		field.Int("member_limit").Default(MaxMembers).Positive().Comment(dataclass.None),
	}
}

// MaxMembers is the default ceiling on an organization's members.
//
// A number rather than "unlimited" because an organization is the billing and ownership
// boundary: a workspace that grows without bound is one where nobody knows who can reach the
// data, and §08's access review has nothing to review.
const MaxMembers = 50

func (Organization) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("projects", Project.Type),
		edge.To("members", OrganizationMember.Type),
		edge.To("invitations", Invitation.Type),
	}
}

// User is a person who signs in to the dashboard.
//
// An email address is personal data, but it belongs to a studio employee rather than to a
// player — §99's rule is about player identifiers, which is why this is global.
//
// # PlayerSense never holds a credential
//
// Firebase Auth owns authentication: passwords, social providers and email magic links. This
// row is the local projection of an identity Firebase already verified, joined by
// `firebase_uid`, and there is deliberately no password column to leak, no reset flow to get
// wrong and no session store to compromise. A credential PlayerSense does not have is a
// credential PlayerSense cannot lose.
//
// The trade is real and worth stating: signing in depends on a third party being reachable,
// and moving off Firebase later means re-verifying every user rather than copying a table.
type User struct{ ent.Schema }

func (User) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (User) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (User) Fields() []ent.Field {
	return []ent.Field{
		// The join to Firebase, and the only identifier that is stable.
		//
		// Immutable and unique. An email address changes — somebody marries, a studio
		// renames its domain — and a user matched on email would silently become a
		// different person. Firebase's uid never changes for an identity, which is why it
		// is the key and email is a label.
		field.String("firebase_uid").NotEmpty().MaxLen(128).Immutable().Unique().
			Comment(dataclass.None),

		// Mirrored from the verified token rather than typed by the user, so it cannot
		// disagree with the address that actually receives a magic link.
		field.String("email").NotEmpty().MaxLen(254).Unique().Comment(dataclass.None),

		// Whether Firebase considers the address verified. Kept because an invitation
		// accepted by an unverified address is a way to join an organization you were
		// never invited to.
		field.Bool("email_verified").Default(false).Comment(dataclass.None),

		field.String("name").MaxLen(120).Optional().Comment(dataclass.None),
		field.String("avatar_url").MaxLen(500).Optional().Comment(dataclass.None),

		// How they last signed in: "password", "google.com", "apple.com",
		// "microsoft.com", "github.com", "emailLink". Firebase's own provider ids, not a
		// translation of them, so a value here can be compared with a token without a
		// mapping table that will drift.
		//
		// A plain string rather than an enum: Firebase adds providers and an enum would
		// make adding one a migration.
		field.String("last_provider").MaxLen(64).Optional().Comment(dataclass.None),

		field.Time("last_seen_at").Optional().Nillable().Comment(dataclass.None),

		// Deactivated rather than deleted, so an audit entry naming them still resolves.
		// Their own erasure request is a different operation and removes the row.
		field.Bool("active").Default(true).Comment(dataclass.None),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("memberships", OrganizationMember.Type),
		edge.To("sent_invitations", Invitation.Type),
	}
}

// OrganizationMember places a user in an organization.
//
// Every user belongs to at least one: signing up creates a personal organization, so there is
// no state where somebody is logged in with nowhere to be. That invariant is what lets every
// project, key and invoice hang off an organization rather than off a user.
//
// # The role field is a placeholder
//
// `role` is the enum that was here before and is **not** the authorization design. Permissions,
// custom roles and per-project access are being specified separately; this field stays as it
// is until then rather than being replaced by a guess that would have to be migrated twice.
type OrganizationMember struct{ ent.Schema }

func (OrganizationMember) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (OrganizationMember) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (OrganizationMember) Fields() []ent.Field {
	return []ent.Field{
		// See the note above: a placeholder, not the authorization model.
		field.Enum("role").Values("owner", "admin", "member", "viewer").Comment(dataclass.None),

		field.Int("organization_id").Comment(dataclass.None),
		field.Int("user_id").Comment(dataclass.None),

		// When they joined, which is what an access review reads (§08). Distinct from the
		// row's created_at, because a membership can be recreated after a removal and the
		// question "since when has this person been able to reach this data" is about the
		// current one.
		field.Time("joined_at").Immutable().Default(now).Comment(dataclass.None),
	}
}

func (OrganizationMember) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("members").
			Field("organization_id").Unique().Required(),
		edge.From("user", User.Type).Ref("memberships").
			Field("user_id").Unique().Required(),
	}
}

func (OrganizationMember) Indexes() []ent.Index {
	return []ent.Index{index.Edges("organization", "user").Unique()}
}

// Invitation is a token that lets one email address join one organization.
//
// Token-based rather than "sign up and ask to be approved", because the invitation is the
// authorisation: an organization's data is a studio's players, and a join request somebody
// has to remember to reject is a join request that eventually gets accepted by mistake.
//
// # What is stored is a digest, not the token
//
// The token is generated once, sent in a link, and kept here only as a SHA-256 digest — the
// same reasoning as an API key's private half. An invitation link reaches an inbox and an
// inbox is not a secure channel, so the window in which one is useful has to be short and the
// database must not contain anything that could reissue it.
//
// SHA-256 rather than the Argon2id an API key uses, and the difference is deliberate: a
// token is 256 bits of randomness that this service generated, not a secret a person chose,
// so there is nothing to brute-force and no reason to pay a KDF's cost on every accept.
//
// # The authorization fields are deliberately absent
//
// An invitation should also carry the role and the project access being granted, and those
// are part of the authorization design being specified separately. Adding a guess now would
// mean migrating it twice, so this holds only what an invitation is regardless of what it
// grants: who is asked, by whom, until when, and whether it has been used.
type Invitation struct{ ent.Schema }

func (Invitation) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Invitation) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Invitation) Fields() []ent.Field {
	return []ent.Field{
		field.Int("organization_id").Immutable().Comment(dataclass.None),

		// Who it was sent to. Compared with the verified address on the Firebase token at
		// accept time, so forwarding the link to a colleague does not let them join in
		// somebody else's place.
		field.String("email").NotEmpty().MaxLen(254).Immutable().Comment(dataclass.None),

		// SHA-256 of the token, hex. Sensitive so it never reaches a log line or an error.
		field.String("token_digest").NotEmpty().MaxLen(64).Immutable().Sensitive().
			Comment(dataclass.None),

		// Who sent it, for the audit trail and for "this invitation looks wrong, who did
		// this". Nillable because the inviter may later be deactivated.
		field.Int("invited_by_id").Optional().Nillable().Immutable().Comment(dataclass.None),

		field.Enum("status").Values("pending", "accepted", "revoked", "expired").
			Default("pending").Comment(dataclass.None),

		// Short by default. An invitation is a bearer credential sitting in an inbox.
		field.Time("expires_at").Immutable().Comment(dataclass.None),
		field.Time("accepted_at").Optional().Nillable().Comment(dataclass.None),
	}
}

func (Invitation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("invitations").
			Field("organization_id").Unique().Required().Immutable(),
		edge.From("invited_by", User.Type).Ref("sent_invitations").
			Field("invited_by_id").Unique().Immutable(),
	}
}

func (Invitation) Indexes() []ent.Index {
	return []ent.Index{
		// Accepting is a lookup by digest, so it is the index that matters.
		index.Fields("token_digest").Unique(),
		// And "who is still outstanding for this organization", which the members screen
		// shows beside the joined ones.
		index.Fields("organization_id", "status"),
	}
}

// now is the default for a time field, named so the schema reads as a declaration rather
// than as a call.
func now() time.Time { return time.Now() }
