package authz

// Role keys. The two system ones are reserved: an organisation may rename them but not change
// what they may do, and may not delete them.
const (
	RoleOwner = "owner"
	RoleAdmin = "admin"

	RoleLiveOpsManager = "liveops_manager"
	RoleDeveloper      = "developer"
	RoleGrowthManager  = "growth_manager"
	RoleBilling        = "billing"
)

// Template is a role as it is seeded when an organisation is created.
type Template struct {
	Key  string
	Name string

	// System roles cannot have their permissions changed or be deleted. Everything else is
	// an ordinary editable row: a studio deletes what it does not want and changes what it
	// keeps.
	System bool

	// Nil for OWNER and ADMIN, whose permissions are computed rather than listed — a
	// permission added to the vocabulary must reach them without anybody remembering to
	// edit a list, or the day somebody adds `billing.refund` is the day an owner cannot
	// issue one.
	Permissions []Permission
}

// Templates are the six roles a new organisation starts with.
//
// Seeded as rows rather than hardcoded, so the four below are a starting point rather than a
// ruling: an organisation that does not run experiments deletes GROWTH MANAGER, and one that
// splits LiveOps differently edits it. What it cannot do is invent a permission — that
// vocabulary is ours, and it is what keeps the checker a map lookup.
func Templates() []Template {
	return []Template{
		{
			Key: RoleOwner, Name: "Owner", System: true,
			// Everything, including role and member management, which is not a
			// permission — see the package comment.
		},
		{
			Key: RoleAdmin, Name: "Admin", System: true,
			// Everything except destroying the organisation itself.
		},
		{
			Key: RoleLiveOpsManager, Name: "LiveOps Manager",
			// LiveOps and nothing else. Deliberately not analytics: reading what every
			// player did is a different privilege from scheduling what they will see,
			// and a role called "LiveOps Manager" that also reads revenue is a role
			// whose name lies.
			Permissions: []Permission{
				LiveOpsRead, LiveOpsWrite, LiveOpsPublish,
			},
		},
		{
			Key: RoleDeveloper, Name: "Developer",
			// Integrates the SDK and drafts configuration. No publish: shipping a
			// change to live players is the irreversible half, and it is the one thing
			// this role is deliberately short of.
			//
			// Individual players too: "is my phone's event arriving" is answered by one
			// player's timeline and an event's latest occurrences, and it is the first thing
			// somebody integrating the SDK asks.
			Permissions: []Permission{
				KeyRead, KeyCreate, KeyRevoke,
				LiveOpsRead, LiveOpsWrite,
				AnalyticsRead, PlayerRead,
				ReportsRead, ReportsWrite,
				// Connecting the stores is integration work, like the keys, and so are the
				// endpoints a studio's own backend listens on.
				PurchasesConfigure,
				WebhooksRead, WebhooksWrite,
			},
		},
		{
			Key: RoleGrowthManager, Name: "Growth Manager",
			// Reads the numbers and sees what is scheduled, changes neither, and builds the
			// reports that send those numbers on. Reads players
			// as well, since finding the whales and the lapsed is most of the job; a studio
			// that wants the numbers alone removes player.read.
			Permissions: []Permission{
				AnalyticsRead, PlayerRead, LiveOpsRead,
				// Reports are the numbers, sent somewhere on a schedule: the same job.
				ReportsRead, ReportsWrite,
			},
		},
		{
			Key: RoleBilling, Name: "Billing",
			// The bill and nothing else — which is the point of having it, because the
			// person who pays is often not the person who should read a player's data.
			Permissions: []Permission{
				OrgRead, BillingRead, BillingManage,
			},
		},
	}
}

// SystemPermissions returns what a system role may do.
//
// Computed from the vocabulary rather than listed, so a permission added tomorrow reaches an
// owner without anybody editing this file. A list would be a second place to remember, and the
// failure is silent: the first owner to discover they cannot do something new would report it
// as a bug months later.
func SystemPermissions(key string) []Permission {
	switch key {
	case RoleOwner:
		return All()
	case RoleAdmin:
		out := make([]Permission, 0, len(scopes))
		for _, permission := range All() {
			// Destroying the organisation is the owner's alone. Everything else an
			// admin can do, including billing — an admin who cannot pay the invoice is
			// an admin who has to wake the owner up.
			if permission == OrgDelete {
				continue
			}
			out = append(out, permission)
		}
		return out
	default:
		return nil
	}
}

// Seed returns the permission set a template is created with.
func (t Template) Seed() []Permission {
	if t.System {
		return SystemPermissions(t.Key)
	}
	return t.Permissions
}
