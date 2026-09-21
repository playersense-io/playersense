package core

import (
	"strings"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/ratelimit"
)

// The budgets the SDK paths count against, and where each number comes from.
//
// Every one is derived from something a real device does rather than chosen for roundness,
// because a limit nobody can explain is a limit nobody will dare change. The derivations are
// in `docs/rate-limits.md` and are summarised beside each constant.
//
// # The device budget is the one meant to bite
//
// A well-behaved installation sends a batch every 30 seconds (§7's flush interval) and asks
// for its configuration every fifteen minutes — call it three requests a minute at the very
// most, counting a session start and a purchase going immediately. The device budget is an
// order of magnitude above that, so the only device that reaches it is one that is looping.
//
// The wider budgets exist to protect the platform, and are sized so that reaching one means
// something is genuinely wrong rather than that a studio is popular.
const (
	// DeviceBurstWindow and the rest: one installation, on the ingestion path.
	DeviceIngestBurst     = 20
	DeviceIngestSustained = 120

	// One installation asking for configuration. Lower, because the SDK asks four times an
	// hour: anything above this is a game calling Refresh() in Update.
	DeviceConfigBurst     = 10
	DeviceConfigSustained = 60

	// One address inside one project. Sized for a shared network — an office, a LAN party,
	// a university — rather than for carrier NAT, which the project budget covers instead.
	AddressIngestSustained = 12_000
	AddressConfigSustained = 3_000

	// One project, across every device. This is the platform's own protection and the one
	// budget whose breach affects players who did nothing wrong, which is why it is set at
	// capacity rather than at a plan's worth of traffic.
	ProjectIngestSustained = 120_000
	ProjectConfigSustained = 30_000
)

// Windows. A second catches a loop; a minute catches a leak.
const (
	BurstWindow     = time.Second
	SustainedWindow = time.Minute
)

// IngestBudgets is what one batch is counted against.
//
// Ordered narrowest first, and the order is the error message: the first breach in this list
// is what the caller is told, so a looping device is told it is the device rather than being
// told its project is busy — which would send a developer to look at the wrong thing.
func IngestBudgets(tenant *Tenant, installID string, addr string) []ratelimit.Budget {
	return []ratelimit.Budget{
		{Quota: "device-burst", Subject: tenant.ProjectID + "|" + installID,
			Window: BurstWindow, Limit: DeviceIngestBurst},
		{Quota: "device-sustained", Subject: tenant.ProjectID + "|" + installID,
			Window: SustainedWindow, Limit: DeviceIngestSustained},
		{Quota: "address-sustained", Subject: tenant.ProjectID + "|" + addr,
			Window: SustainedWindow, Limit: AddressIngestSustained},
		{Quota: "project-sustained", Subject: tenant.ProjectID,
			Window: SustainedWindow, Limit: ProjectIngestSustained},
	}
}

// ConfigBudgets is what one configuration request is counted against.
func ConfigBudgets(tenant *Tenant, installID string, addr string) []ratelimit.Budget {
	return []ratelimit.Budget{
		{Quota: "device-config-burst", Subject: tenant.ProjectID + "|" + installID,
			Window: BurstWindow, Limit: DeviceConfigBurst},
		{Quota: "device-config-sustained", Subject: tenant.ProjectID + "|" + installID,
			Window: SustainedWindow, Limit: DeviceConfigSustained},
		{Quota: "address-config-sustained", Subject: tenant.ProjectID + "|" + addr,
			Window: SustainedWindow, Limit: AddressConfigSustained},
		{Quota: "project-config-sustained", Subject: tenant.ProjectID,
			Window: SustainedWindow, Limit: ProjectConfigSustained},
	}
}

// The invitation budgets, which are the only ones on the dashboard and the only ones that are
// not about load.
//
// Every other budget in this file protects the platform from a caller. These protect **somebody
// who is not making the request**: an invitation costs a stranger's attention, this deployment's
// sending reputation, and — once a provider is configured — money. That is a different argument
// from the rest of docs/rate-limits.md, and it is why a dashboard that is otherwise deliberately
// unlimited has these two.
//
// # Both are derived from what a person does, not from what a machine can do
//
// A studio inviting one colleague sends one message and, if the first went to a typo, a second.
// Three an hour to one address is generous for that and is nowhere near enough to use somebody's
// inbox as a weapon. An organization onboarding a whole team invites in a sitting — thirty an hour
// covers a studio larger than the member cap allows, so the limit can only be reached by something
// that is not onboarding.
//
// # These fail closed
//
// Unlike the SDK paths, where §65's rule is that losing Valkey costs speed rather than data.
// A message cannot be taken back, so when the counter cannot be read the answer is no.
const (
	// InvitationsPerAddress is how often one inbox may be written to.
	InvitationsPerAddress = 3

	// InvitationsPerOrganization bounds one studio's sending, so a compromised account cannot
	// use the product as a mailer.
	InvitationsPerOrganization = 30

	// InvitationWindow is the hour both are counted over. Long enough that a burst of
	// onboarding is one window rather than a queue somebody waits through.
	InvitationWindow = time.Hour
)

// InvitationBudgets is what one invitation is counted against, narrowest first.
//
// The order is the message: a person hitting the address limit is told about the address, which is
// the thing they can fix by checking the spelling, rather than being told their organization is
// busy.
//
// The address is lowered so that two spellings of one inbox share a counter — the alternative is a
// limit anybody can lift by pressing shift.
func InvitationBudgets(orgSlug, email string) []ratelimit.Budget {
	return []ratelimit.Budget{
		{Quota: "invitation-address", Subject: strings.ToLower(strings.TrimSpace(email)),
			Window: InvitationWindow, Limit: InvitationsPerAddress},
		{Quota: "invitation-organization", Subject: orgSlug,
			Window: InvitationWindow, Limit: InvitationsPerOrganization},
	}
}

// ClientAddressOf renders the caller's address for use as part of a budget's subject.
//
// The prefix rather than the exact address, for the reason [muzak.IPPrefixTracker] gives: an
// IPv6 /64 is what a provider hands one subscriber, so keying on the full address would give
// anyone holding one a fresh budget on every request.
func ClientAddressOf(ctx *muzak.Context) string {
	addr := ctx.ClientAddr()
	if !addr.IsValid() {
		return "unknown"
	}
	bits := 32
	if addr.Is6() {
		bits = 64
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return "unknown"
	}
	return prefix.String()
}
