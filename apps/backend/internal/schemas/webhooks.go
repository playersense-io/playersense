package schemas

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// ---- endpoints -------------------------------------------------------------------------------

// WebhookParams names one endpoint.
type WebhookParams struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	WebhookID   uuid.UUID `path:"webhook"`
}

func (in *WebhookParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// WebhookHeaderIn is a header sent with every delivery. A secret one's value, once saved, is never
// shown again; sent empty on a change, the saved value is kept.
type WebhookHeaderIn struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitzero"`
	Secret bool   `json:"secret,omitzero"`
}

// WebhookBody is an endpoint's settings.
type WebhookBody struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitzero"`
	URL          string            `json:"url" doc:"https, on a public address"`
	Events       []string          `json:"events" doc:"Event types, or * for every event, those added later included"`
	Environments []string          `json:"environments" doc:"production, staging, development: whose events it hears"`
	Enabled      *bool             `json:"enabled,omitzero"`
	Headers      []WebhookHeaderIn `json:"headers,omitzero" doc:"Up to ten, for a gateway in front of the URL"`
}

func (in *WebhookBody) validate(v *muzak.Validation) {
	v.String(&in.Name).Trim().Required().MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(500)
	v.String(&in.URL).Trim().Required().MaxLen(2048)
	v.Slice(&in.Events).MinItems(1).MaxItems(64)
	v.Slice(&in.Environments).MinItems(1).MaxItems(3)
	v.Slice(&in.Headers).MaxItems(10)
}

// WebhookIn creates an endpoint.
type WebhookIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	WebhookBody
}

func (in *WebhookIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	in.validate(v)
}

// UpdateWebhookIn changes an endpoint.
type UpdateWebhookIn struct {
	WebhookParams
	WebhookBody
}

func (in *UpdateWebhookIn) Validate(v *muzak.Validation) {
	in.WebhookParams.Validate(v)
	in.validate(v)
}

// WebhookHeaderOut is a header as shown: a secret one's value never is.
type WebhookHeaderOut struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitzero"`
	Secret bool   `json:"secret"`
}

// WebhookSecretOut is the signing secret's dates; the secret itself only when asked for.
type WebhookSecretOut struct {
	CreatedAt         time.Time  `json:"created_at"`
	PreviousExpiresAt *time.Time `json:"previous_expires_at,omitzero" doc:"The replaced secret still signs beside the new one until then"`
}

// WebhookDayOut is an endpoint's last 24 hours.
type WebhookDayOut struct {
	Deliveries uint64  `json:"deliveries"`
	FirstTry   float64 `json:"first_try" doc:"The share delivered at their first try"`
	TrySuccess float64 `json:"try_success" doc:"The share of every try answered 2xx"`
	Succeeded  uint64  `json:"succeeded"`
	Failed     uint64  `json:"failed" doc:"Deliveries that gave up"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
}

// EndpointOut is a webhook endpoint.
type EndpointOut struct {
	ID             uuid.UUID          `json:"id"`
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	URL            string             `json:"url"`
	Events         []string           `json:"events"`
	Environments   []string           `json:"environments"`
	Status         string             `json:"status" enum:"enabled,disabled"`
	Health         string             `json:"health" enum:"healthy,failing,disabled"`
	DisabledReason string             `json:"disabled_reason,omitzero" enum:"manual,failing"`
	DisabledAt     *time.Time         `json:"disabled_at,omitzero"`
	FailingSince   *time.Time         `json:"failing_since,omitzero"`
	AutoDisableAt  *time.Time         `json:"auto_disable_at,omitzero" doc:"When it is turned off if nothing lands before"`
	APIVersion     string             `json:"api_version"`
	Headers        []WebhookHeaderOut `json:"headers"`
	Secret         WebhookSecretOut   `json:"secret"`
	// SigningSecret is returned when the endpoint is made, when its secret is rolled, and when it
	// is asked for.
	SigningSecret string         `json:"signing_secret,omitzero"`
	Retrying      int            `json:"retrying" doc:"Deliveries owed another try now"`
	LastDay       WebhookDayOut  `json:"last_day"`
	History       []WebhookEntry `json:"history,omitzero" doc:"Who changed it, newest first; on a single endpoint only"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	CreatedBy     string         `json:"created_by"`
	UpdatedBy     string         `json:"updated_by"`
}

// WebhookEntry is one change to an endpoint.
type WebhookEntry struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
}

// WebhookListOut is a project's endpoints.
type WebhookListOut struct {
	Webhooks []EndpointOut `json:"webhooks"`
}

// RollWebhookSecretIn rolls an endpoint's signing secret.
type RollWebhookSecretIn struct {
	WebhookParams
	// ExpireAfterHours is how long the old secret keeps signing beside the new one: 0 stops it now.
	ExpireAfterHours *int `json:"expire_after_hours,omitzero" doc:"0 to 168; 24 when absent"`
}

func (in *RollWebhookSecretIn) Validate(v *muzak.Validation) {
	in.WebhookParams.Validate(v)
	if in.ExpireAfterHours != nil {
		v.Number(in.ExpireAfterHours).Min(0).Max(168)
	}
}

// WebhookEventTypeOut is one kind of event.
type WebhookEventTypeOut struct {
	Type         string `json:"type"`
	Group        string `json:"group"`
	Summary      string `json:"summary"`
	Environments bool   `json:"environments" doc:"Whether it belongs to an environment; a project-level event is heard by every endpoint"`
	Emitted      bool   `json:"emitted" doc:"Whether PlayerSense sends it yet"`
}

// WebhookCatalogOut is every event type.
type WebhookCatalogOut struct {
	APIVersion string                `json:"api_version"`
	Events     []WebhookEventTypeOut `json:"events"`
}

// ---- sends -----------------------------------------------------------------------------------

// TestWebhookIn sends a made-up event.
type TestWebhookIn struct {
	WebhookParams
	Type        string `json:"type" doc:"The event type to send a made-up one of"`
	Environment string `json:"environment,omitzero" enum:"production,staging,development"`
}

func (in *TestWebhookIn) Validate(v *muzak.Validation) {
	in.WebhookParams.Validate(v)
	v.String(&in.Type).Trim().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().OneOf("", "production", "staging", "development")
}

// WebhookTryOut is one try.
type WebhookTryOut struct {
	Number   int        `json:"number"`
	At       time.Time  `json:"at"`
	Code     *int       `json:"code" doc:"The HTTP status; null when no answer came"`
	Error    string     `json:"error,omitzero"`
	Ms       int        `json:"ms"`
	Response string     `json:"response" doc:"The answer's body, cut at 4 KB"`
	Outcome  string     `json:"outcome" enum:"succeeded,retrying,failed"`
	NextAt   *time.Time `json:"next_at,omitzero"`
	Manual   bool       `json:"manual"`
}

// WebhookSentOut is a try made from the dashboard.
type WebhookSentOut struct {
	DeliveryID uuid.UUID     `json:"delivery_id"`
	EventID    uuid.UUID     `json:"event_id"`
	Try        WebhookTryOut `json:"try"`
}

// ---- deliveries ------------------------------------------------------------------------------

// WebhookDeliveriesParams pages an endpoint's deliveries.
type WebhookDeliveriesParams struct {
	WebhookParams
	Status string `query:"status,omitzero" doc:"Comma-separated: succeeded, retrying, failed"`
	Type   string `query:"type,omitzero"`
	Before string `query:"before,omitzero" doc:"The next cursor of the previous page"`
	Limit  int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *WebhookDeliveriesParams) Validate(v *muzak.Validation) {
	in.WebhookParams.Validate(v)
	v.String(&in.Status).Trim().MaxLen(64)
	v.String(&in.Type).Trim().MaxLen(64)
	v.String(&in.Before).Trim().MaxLen(96)
	v.Number(&in.Limit).Min(1).Max(200)
}

// Statuses reads the status filter.
func (in WebhookDeliveriesParams) Statuses() []string { return splitList(in.Status) }

// WebhookDeliveryOut is one delivery as it stands.
type WebhookDeliveryOut struct {
	ID          uuid.UUID  `json:"id"`
	EventID     uuid.UUID  `json:"event_id"`
	Type        string     `json:"type"`
	Environment string     `json:"environment,omitzero"`
	CreatedAt   time.Time  `json:"created_at"`
	Status      string     `json:"status" enum:"succeeded,retrying,failed"`
	Attempts    int        `json:"attempts"`
	LastAt      time.Time  `json:"last_at"`
	LastCode    *int       `json:"last_code"`
	LastError   string     `json:"last_error,omitzero"`
	NextAt      *time.Time `json:"next_at,omitzero"`
	Test        bool       `json:"test"`
}

// WebhookDeliveriesOut is one page.
type WebhookDeliveriesOut struct {
	Deliveries []WebhookDeliveryOut `json:"deliveries"`
	Next       string               `json:"next,omitzero"`
}

// WebhookDeliveryParams names one delivery.
type WebhookDeliveryParams struct {
	WebhookParams
	DeliveryID uuid.UUID `path:"delivery"`
}

// WebhookDeliveryDetailOut is a delivery, its tries, and what it carried.
type WebhookDeliveryDetailOut struct {
	WebhookDeliveryOut
	Tries []WebhookTryOut `json:"tries"`
	// Payload is the event as it was POSTed; empty once it has aged out of the thirty days kept.
	Payload json.RawMessage `json:"payload,omitzero"`
}

// ResendFailedIn queues what an endpoint missed.
type ResendFailedIn struct {
	WebhookParams
	Since *time.Time `json:"since,omitzero" doc:"From when; three days ago when absent"`
}

// ResendFailedOut is how many were queued.
type ResendFailedOut struct {
	Queued int `json:"queued"`
}

// WebhookHealthOut is an endpoint's last day against the day before, and its tries by hour.
type WebhookHealthOut struct {
	Current  WebhookDayOut    `json:"current"`
	Previous WebhookDayOut    `json:"previous"`
	Retrying int              `json:"retrying"`
	Hours    []WebhookHourOut `json:"hours"`
}

// WebhookHourOut is one hour's tries.
type WebhookHourOut struct {
	At     time.Time `json:"at"`
	OK     uint64    `json:"ok"`
	Failed uint64    `json:"failed"`
}

// ---- staff -----------------------------------------------------------------------------------

// OperatorWebhooksParams asks for webhooks across the platform.
type OperatorWebhooksParams struct {
	StaffTicket
}

// OperatorWebhooksOut is the last day of webhooks, the backlog, and the endpoints failing worst.
type OperatorWebhooksOut struct {
	LastDay WebhookDayOut `json:"last_day"`
	// Outbox is events not yet taken by the worker, and how old the oldest is: the lag.
	Outbox        int     `json:"outbox"`
	OutboxOldestS float64 `json:"outbox_oldest_s"`
	// Retrying is deliveries owed another try, and how many are due now.
	Retrying int                  `json:"retrying"`
	Due      int                  `json:"due"`
	Failing  []OperatorWebhookOut `json:"failing"`
	Disabled []OperatorWebhookOut `json:"disabled" doc:"Turned off for failing in the last week"`
}

// OperatorWebhookOut is one endpoint as staff see it.
type OperatorWebhookOut struct {
	ID           uuid.UUID     `json:"id"`
	Project      *ProjectBrief `json:"project,omitzero"`
	Name         string        `json:"name"`
	Host         string        `json:"host"`
	Enabled      bool          `json:"enabled"`
	FailingSince *time.Time    `json:"failing_since,omitzero"`
	DisabledAt   *time.Time    `json:"disabled_at,omitzero"`
	LastDay      WebhookDayOut `json:"last_day"`
}

// OperatorWebhookDeliveriesParams pages one project's endpoint's deliveries, for staff.
type OperatorWebhookDeliveriesParams struct {
	StaffTicket
	Project   string    `path:"project"`
	WebhookID uuid.UUID `path:"webhook"`
	Status    string    `query:"status,omitzero"`
	Before    string    `query:"before,omitzero"`
	Limit     int       `query:"limit,omitzero" default:"50"`
}

func (in *OperatorWebhookDeliveriesParams) Validate(v *muzak.Validation) {
	v.String(&in.Status).Trim().MaxLen(64)
	v.String(&in.Before).Trim().MaxLen(96)
	v.Number(&in.Limit).Min(1).Max(200)
}

// OperatorWebhookDeliveryParams names one delivery, for staff.
type OperatorWebhookDeliveryParams struct {
	StaffTicket
	Project    string    `path:"project"`
	WebhookID  uuid.UUID `path:"webhook"`
	DeliveryID uuid.UUID `path:"delivery"`
}
