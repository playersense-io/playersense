package core

import (
	"time"

	"muzak.dev/framework"

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

// Email budgets.
//
// The one thing on the dashboard that is limited, and the reason is not load. A verification
// link, an invitation and a password reset each cost money, cost sender reputation, and arrive
// in **somebody else's inbox** whether or not they asked — so the person being protected is
// not the one making the request. That is a different argument from every other limit in this
// product, and it is why these survive while the dashboard's own limit does not.
//
// Two budgets, because they stop two different things:
//
//   - **Per address**: one person cannot be mailed repeatedly. This is the one that matters,
//     because the address belongs to whoever is being written to rather than to the caller.
//   - **Per organisation**: a studio cannot invite a thousand strangers in an afternoon. A
//     mailbox provider does not distinguish an enthusiastic onboarding from a spam run, and
//     the consequence — a domain on a blocklist — lands on every customer at once.
const (
	// EmailPerAddressWindow is long on purpose. Nobody needs the same link twice in a
	// quarter of an hour, and a person who did not receive the first one will not receive
	// the second either — the problem is somewhere else.
	EmailPerAddressWindow = 15 * time.Minute
	EmailPerAddressLimit  = 1

	EmailPerOrgWindow = time.Hour
	EmailPerOrgLimit  = 60
)

// EmailBudgets is what one outbound message is counted against.
//
// The address first, so somebody being mailed repeatedly is told that rather than being told
// their organisation is busy — which would be the wrong sentence for the person it protects.
func EmailBudgets(kind, address string, organizationID int) []ratelimit.Budget {
	budgets := []ratelimit.Budget{
		{Quota: "email-" + kind, Subject: address,
			Window: EmailPerAddressWindow, Limit: EmailPerAddressLimit},
	}
	if organizationID != 0 {
		budgets = append(budgets, ratelimit.Budget{
			Quota: "email-org", Subject: itoa(organizationID),
			Window: EmailPerOrgWindow, Limit: EmailPerOrgLimit,
		})
	}
	return budgets
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
