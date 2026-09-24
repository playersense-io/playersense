package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"

	"playersense.dev/backend/internal/dataclass"
)

// StaffMember is somebody at PlayerSense who may use the operations console (§56, §104).
//
// The console's allowlist, and its access review: who can reach the deployment, with what, and
// since when. Staff sign in with the same Firebase project as customers; what makes an address
// staff is a row here, active, and nothing else. A customer who signs in with an address that is
// not here reaches no /v1/operator route, whatever their token says.
//
// Not an edge to User, for the reason StaffAccess gives: staff are not members of anything, and
// an employee who is also a customer somewhere must not have the one grant leak into the other.
//
// A leaver is deactivated rather than deleted, so "who could reach the console in March" stays
// answerable; staff_access keeps what each of them read.
type StaffMember struct{ ent.Schema }

func (StaffMember) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (StaffMember) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (StaffMember) Fields() []ent.Field {
	return []ent.Field{
		// Lowercased when written, so the verified token's address matches whatever case
		// somebody typed when adding them.
		field.String("email").NotEmpty().MaxLen(254).Unique().Comment(dataclass.None),
		field.String("name").MaxLen(120).Default("").Comment(dataclass.None),

		// What they may do, from internal/staff's closed list: ops, support, billing, admin.
		// A list because the roles are separable: somebody on the billing desk reads wallets
		// and grants credit without being able to open a customer's audit log.
		field.Strings("roles").Default([]string{}).Comment(dataclass.None),

		field.Bool("active").Default(true).Comment(dataclass.None),
		field.Time("deactivated_at").Optional().Nillable().Comment(dataclass.None),

		// Who added them: another staff member's address, or "bootstrap" for the first,
		// who is added from the command line because nobody exists yet to add them.
		field.String("created_by").NotEmpty().MaxLen(254).Immutable().Comment(dataclass.None),
	}
}

func (StaffMember) Edges() []ent.Edge { return nil }
