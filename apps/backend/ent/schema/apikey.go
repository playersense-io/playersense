package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/dataclass"
)

// APIKey is a credential scoped to one environment (§52, §53).
//
// A key is `<public>:<private>`. The public half is stored in clear and indexed; the
// private half is stored only as an Argon2id digest and is returned to the caller once,
// at creation, never again.
//
// The split is what makes a deliberately slow hash affordable. Verifying a presented key
// is one indexed lookup on the public half followed by exactly one Argon2 comparison —
// without it, checking a key would mean running a slow hash against every row, which is
// a denial-of-service vector rather than a defence.
type APIKey struct{ ent.Schema }

func (APIKey) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (APIKey) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		// project ships inside a game binary and is assumed discoverable (§71);
		// reporting reads analytics; server is the server-to-server credential §47 wants
		// for purchase verification.
		field.Enum("kind").Values("project", "reporting", "server").Immutable().Comment(dataclass.None),

		// The public half: `ps_<kind>_<env>_<random>`. Not a secret — it is the lookup
		// key, and it is what makes a leaked credential recognisable to a secret scanner
		// or to a person reading a diff.
		field.String("public_id").NotEmpty().MaxLen(64).Immutable().Unique().Comment(dataclass.None),

		// PHC-encoded Argon2id digest of the private half. Sensitive so it never appears
		// in a log line or an error, and not Immutable so a digest written under older
		// parameters can be upgraded in place when the key is next presented.
		field.String("secret_hash").NotEmpty().MaxLen(255).Sensitive().Comment(dataclass.None),

		field.String("name").MaxLen(120).Optional().Comment(dataclass.None),
		field.Time("last_used_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("revoked_at").Optional().Nillable().Comment(dataclass.None),
		field.Int("environment_id").Comment(dataclass.None),

		// Who minted it and who revoked it, as the audit log names an actor.
		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("revoked_by").MaxLen(254).Default("").Comment(dataclass.None),

		// A rolled key was replaced by another of its kind and environment. It stops at
		// expires_at: at once, or after a grace so a server fleet can be moved over without
		// an outage.
		field.Time("rolled_at").Optional().Nillable().Comment(dataclass.None),
		field.UUID("replaced_by", uuid.UUID{}).Optional().Nillable().Comment(dataclass.None),
		field.Time("expires_at").Optional().Nillable().Comment(dataclass.None),
	}
}

func (APIKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("environment", Environment.Type).Ref("api_keys").
			Field("environment_id").Unique().Required(),

		// What this key did. A key is an actor in the audit log exactly as a person is —
		// "who revoked this" has to be answerable when the answer is a CI job.
		edge.To("audit_events", AuditEvent.Type),
		edge.To("published_versions", ConfigVersion.Type),
	}
}

func (APIKey) Indexes() []ent.Index {
	// One index, on the half a presented key is looked up by. Nothing looks a key up by
	// its digest, because the digest cannot be recomputed without the secret.
	return []ent.Index{index.Fields("public_id").Unique()}
}
