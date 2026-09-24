package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// The operations console's view of one customer (§56): finding them, what they are on, what they
// may use, and what their wallet and their own log say. Every input here embeds StaffTicket,
// because every one of these reads names a customer (§104).

// ---- finding a customer -----------------------------------------------------

// OperatorOrganizationSearchIn finds organizations.
type OperatorOrganizationSearchIn struct {
	StaffTicket

	Q string `query:"q,omitzero" doc:"A slug prefix, part of a name, an organization id or sequence number, or a member's address"`

	Plan       string `query:"plan,omitzero" doc:"A plan key"`
	State      string `query:"state,omitzero" doc:"active, grace_period, restricted, suspended or canceled"`
	Collection string `query:"collection,omitzero" doc:"prepaid or invoiced"`

	Before string `query:"before,omitzero" doc:"The next cursor of the previous page"`
	Limit  int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *OperatorOrganizationSearchIn) Validate(v *muzak.Validation) {
	v.String(&in.Q).Trim().MaxLen(254)
	v.String(&in.Plan).Trim().Lower().MaxLen(64)
	v.String(&in.State).Trim().Lower().OneOf("", "active", "grace_period", "restricted", "suspended", "canceled")
	v.String(&in.Collection).Trim().Lower().OneOf("", "prepaid", "invoiced")
	v.String(&in.Before).Trim().MaxLen(20)
	v.Number(&in.Limit).Min(1).Max(200)
}

// OperatorOrganizationListOut is one page of organizations.
type OperatorOrganizationListOut struct {
	Organizations []OperatorOrganizationSummaryOut `json:"organizations"`
	Next          string                           `json:"next,omitzero" doc:"Pass as before for the page after this one; absent on the last"`
}

// OperatorOrganizationSummaryOut is one row of the directory.
type OperatorOrganizationSummaryOut struct {
	ID        uuid.UUID          `json:"id"`
	Seq       int                `json:"seq" doc:"The number the data plane knows it by"`
	Slug      string             `json:"slug"`
	Name      string             `json:"name"`
	CreatedAt time.Time          `json:"created_at"`
	Plan      string             `json:"plan,omitzero" doc:"The plan key; absent with no billing account"`
	Balance   *BalanceSummaryOut `json:"balance,omitzero"`
	Projects  int                `json:"projects"`
	Members   int                `json:"members"`
}

// OperatorOrganizationOut is one organization, as support needs it on one screen.
type OperatorOrganizationOut struct {
	OperatorOrganizationSummaryOut

	MemberLimit        int                     `json:"member_limit"`
	PendingInvitations int                     `json:"pending_invitations"`
	ProjectList        []OperatorProjectRefOut `json:"project_list"`
	MemberList         []OperatorMemberOut     `json:"member_list"`

	// Absent with no billing account.
	Account *OperatorAccountOut `json:"account,omitzero"`
}

// OperatorAccountOut is the billing account's identity at Stripe and its terms, without the wallet
// (which is the billing route's).
type OperatorAccountOut struct {
	ID                   uuid.UUID        `json:"id"`
	State                string           `json:"state"`
	StateChangedAt       time.Time        `json:"state_changed_at"`
	Collection           string           `json:"collection"`
	StripeCustomerID     string           `json:"stripe_customer_id,omitzero"`
	StripeSubscriptionID string           `json:"stripe_subscription_id,omitzero"`
	SubscriptionStatus   string           `json:"subscription_status,omitzero"`
	SubscriptionInterval string           `json:"subscription_interval,omitzero"`
	SubscriptionEnds     *time.Time       `json:"subscription_period_end,omitzero"`
	BillingEmail         string           `json:"billing_email,omitzero"`
	Overrides            map[string]int64 `json:"entitlement_overrides,omitzero"`
}

// OperatorMemberOut is one member of a customer's organization.
type OperatorMemberOut struct {
	Email       string     `json:"email"`
	Name        string     `json:"name,omitzero"`
	Role        string     `json:"role"`
	AllProjects bool       `json:"all_projects"`
	JoinedAt    time.Time  `json:"joined_at"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitzero"`
	Active      bool       `json:"active" doc:"false when their PlayerSense account is deactivated"`
}

// OperatorProjectRefOut is a project in a list.
type OperatorProjectRefOut struct {
	ID            uuid.UUID `json:"id"`
	Seq           int       `json:"seq" doc:"The number the data plane knows it by: project_seq in ClickHouse"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	PolicyTier    string    `json:"policy_tier"`
	RetentionDays uint16    `json:"retention_days"`
	CreatedAt     time.Time `json:"created_at"`
}

// OperatorProjectSearchIn finds projects.
type OperatorProjectSearchIn struct {
	StaffTicket

	Q    string `query:"q,omitzero" doc:"A slug prefix, part of a name, a project id, or a project sequence number from the ingestion or storage pages"`
	Tier string `query:"policy_tier,omitzero" doc:"standard, child_directed or kids_strict"`

	Before string `query:"before,omitzero" doc:"The next cursor of the previous page"`
	Limit  int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *OperatorProjectSearchIn) Validate(v *muzak.Validation) {
	v.String(&in.Q).Trim().MaxLen(120)
	v.String(&in.Tier).Trim().Lower().OneOf("", "standard", "child_directed", "kids_strict")
	v.String(&in.Before).Trim().MaxLen(20)
	v.Number(&in.Limit).Min(1).Max(200)
}

// OperatorProjectListOut is one page of projects.
type OperatorProjectListOut struct {
	Projects []OperatorProjectSummaryOut `json:"projects"`
	Next     string                      `json:"next,omitzero"`
}

// OperatorProjectSummaryOut is a project and whose it is.
type OperatorProjectSummaryOut struct {
	OperatorProjectRefOut
	Organization OrganizationRefOut `json:"organization"`
}

// OrganizationRefOut names an organization.
type OrganizationRefOut struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// OperatorProjectParams names one project by id, for an operator route.
type OperatorProjectParams struct {
	StaffTicket
	Project string `path:"project" doc:"The project's id"`
}

func (in *OperatorProjectParams) Validate(v *muzak.Validation) {
	v.String(&in.Project).Trim().Lower().Required().MaxLen(36)
}

// OperatorProjectOut is one project: its environments and keys, what it holds, and what it used.
type OperatorProjectOut struct {
	OperatorProjectSummaryOut

	Environments []OperatorEnvironmentOut `json:"environments"`

	Segments       int `json:"segments"`
	Flags          int `json:"flags"`
	LiveOpsEvents  int `json:"liveops_events"`
	ConfigVersions int `json:"config_versions"`

	// Usage is the last thirty days from the meter.
	Usage UsageTotalsOut `json:"usage_30d"`
	Days  []UsageDayOut  `json:"days"`
}

// OperatorEnvironmentOut is one environment and every key it has had.
type OperatorEnvironmentOut struct {
	ID   uuid.UUID   `json:"id"`
	Seq  int         `json:"seq" doc:"environment_seq in ClickHouse"`
	Kind string      `json:"kind"`
	Keys []APIKeyOut `json:"keys"`
}

// ---- entitlements -----------------------------------------------------------

// EntitlementsOut is what an organization may use, and where each number comes from.
type EntitlementsOut struct {
	Plan string `json:"plan"`

	PlanEntitlements map[string]int64 `json:"plan_entitlements" doc:"What the plan includes"`
	Overrides        map[string]int64 `json:"overrides" doc:"What PlayerSense set for this organization alone"`
	Resolved         map[string]int64 `json:"resolved" doc:"The two layered: what is enforced"`
	Vocabulary       []string         `json:"vocabulary" doc:"Every entitlement an override may name"`

	MemberLimit         int `json:"member_limit" doc:"How many members the organization may have"`
	AutoTopUpDailyLimit int `json:"auto_topup_daily_limit" doc:"Automatic charges allowed in any 24 hours"`
}

// SetEntitlementsIn changes what one organization may use.
//
// A merge rather than a replacement: a number sets that override, null clears it back to the
// plan's, and a name not sent is left alone. Two people changing two different allowances do not
// undo each other.
type SetEntitlementsIn struct {
	StaffTicket
	Slug string `path:"org"`

	Overrides           map[string]*int64 `json:"overrides,omitzero" doc:"Entitlement to amount; null clears it"`
	MemberLimit         *int              `json:"member_limit,omitzero" doc:"1 to 10000"`
	AutoTopUpDailyLimit *int              `json:"auto_topup_daily_limit,omitzero" doc:"1 to 48"`
	Reason              string            `json:"reason" doc:"Why, for the customer's audit log: the deal, the ticket's summary"`
}

func (in *SetEntitlementsIn) Validate(v *muzak.Validation) {
	v.String(&in.Slug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Reason).Trim().Required().MaxLen(500)
	if in.MemberLimit != nil {
		v.Number(in.MemberLimit).Min(1).Max(10_000)
	}
	if in.AutoTopUpDailyLimit != nil {
		v.Number(in.AutoTopUpDailyLimit).Min(1).Max(48)
	}
}

// ---- the wallet, as staff see it --------------------------------------------

// OperatorBillingOut is the customer's billing page plus what only staff see.
type OperatorBillingOut struct {
	BillingOut
	Account OperatorAccountOut `json:"account"`

	// The in-flight automatic top-up, when there is one: the key Stripe was sent, when, and for
	// how much. What "pending" means on the customer's page.
	AutoTopUpPendingKey    string     `json:"auto_topup_pending_key,omitzero"`
	AutoTopUpPendingAt     *time.Time `json:"auto_topup_pending_at,omitzero"`
	AutoTopUpPendingMicros *int64     `json:"auto_topup_pending_micros,omitzero"`

	InvoicedThrough *time.Time `json:"invoiced_through,omitzero"`
}

// OperatorLedgerIn pages a customer's statement.
type OperatorLedgerIn struct {
	StaffTicket
	Slug   string `path:"org"`
	Limit  int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
	Before string `query:"before,omitzero" doc:"The next cursor of the previous page"`
}

func (in *OperatorLedgerIn) Validate(v *muzak.Validation) {
	v.String(&in.Slug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.Limit).Min(1).Max(200)
	v.String(&in.Before).Trim().MaxLen(20)
}

// OperatorCreditLotsIn lists a customer's grants.
type OperatorCreditLotsIn struct {
	StaffTicket
	Slug string `path:"org"`
	All  bool   `query:"all,omitzero" doc:"Include spent and written-off grants, not only those with credit left"`
}

func (in *OperatorCreditLotsIn) Validate(v *muzak.Validation) {
	v.String(&in.Slug).Trim().Lower().Required().MaxLen(64)
}

// OperatorCreditLotsOut is every grant asked for, newest first.
type OperatorCreditLotsOut struct {
	Lots []OperatorCreditLotOut `json:"lots"`
}

// OperatorCreditLotOut is one grant with where it came from.
type OperatorCreditLotOut struct {
	CreditLotOut
	GrantedByStaff  string `json:"granted_by_staff,omitzero" doc:"The staff member's verified address, for a grant made in the console"`
	StripeReference string `json:"stripe_reference,omitzero" doc:"The payment that bought it"`
	Reference       string `json:"reference" doc:"The idempotency key it was granted under"`
}

// OperatorUsageIn is a customer's usage over a range.
type OperatorUsageIn struct {
	StaffTicket
	Slug string `path:"org"`
	From string `query:"from,omitzero" doc:"YYYY-MM-DD. Defaults to the first of the current month"`
	To   string `query:"to,omitzero" doc:"YYYY-MM-DD. Defaults to today"`
}

func (in *OperatorUsageIn) Validate(v *muzak.Validation) {
	v.String(&in.Slug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.From).Trim().MaxLen(10)
	v.String(&in.To).Trim().MaxLen(10)
}

// OperatorAuditIn is staff reading a customer's own log.
type OperatorAuditIn struct {
	StaffTicket
	Slug string `path:"org"`
	AuditFilter
}

func (in *OperatorAuditIn) Validate(v *muzak.Validation) {
	v.String(&in.Slug).Trim().Lower().Required().MaxLen(64)
	in.AuditFilter.Validate(v)
}
