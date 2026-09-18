package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/geoip"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/ratelimit"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/targeting"
)

// UpcomingWithin is how far ahead a response looks for events that have not started.
//
// A week: long enough for "back on Friday" and for a monthly event's last few days, short
// enough that a game is not told about something a player will have forgotten by the time it
// arrives. A schedule is evaluated per event, so this is also the only unbounded-looking work
// in the request and the bound is what keeps it cheap.
const UpcomingWithin = 7 * 24 * time.Hour

// ServeConfig answers what one player gets (§41, §42).
//
// Everything expensive was paid at publish: segments are inlined, audiences are validated, and
// the document already knows whether any rule in the project needs the player's history. What
// is left per request is a map lookup, a tree walk over a handful of conditions, and — for the
// minority of projects whose rules name `commerce.` or `activity.` — one read of a small
// record.
//
// # Where each fact is allowed to come from
//
// This is the whole of ADR 0027, and it is arranged so that the rule cannot be broken by
// forgetting it rather than enforced by remembering:
//
//	geo.*, network.*     derived here from the connection. The client never sends them.
//	commerce.*, activity.*  read from PlayerSense's own record of the player.
//	app.*, device.*, sdk.*  the client's, but the key names are ours.
//	context.*            the studio's own, and every client key is prefixed into it.
//
// A modified build that sends `"geo.country": "TR"` produces the fact `context.geo.country`,
// which no reserved rule reads. The forgery has nowhere to land.
func ServeConfig(ctx *muzak.Context, in schemas.ConfigIn) (protocol.ConfigResponse, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	store := muzak.From[*liveconfig.Store](ctx)
	home := muzak.From[core.CellIdentity](ctx)

	// Counted first, for the reason ingestion gives: this is the first point at which the
	// device is known, and a device is what "one player" means here.
	if limiter, found := muzak.TryFrom[*ratelimit.Limiter](ctx); found {
		if err := limiter.Allow(ctx,
			core.ConfigBudgets(tenant, in.InstallID, core.ClientAddressOf(ctx))...); err != nil {
			return protocol.ConfigResponse{}, err
		}
	}

	// Residency, for the same reason ingestion enforces it (§6, §99): this project's
	// players belong to another cell, and answering here would mean reading their history
	// from a database in another jurisdiction.
	if tenant.Cell != home.Slug {
		return protocol.ConfigResponse{}, muzak.NewHTTPError(http.StatusMisdirectedRequest,
			"this project belongs to cell "+tenant.Cell+"; ask "+tenant.ConfigURL+" instead").
			WithCode("wrong_cell").
			WithDetails(muzak.ErrorDetail{
				Field:    "config_url",
				Location: "header",
				Issue:    tenant.ConfigURL,
			})
	}

	// The environment comes from the key, never from the body. A build pointed at
	// production with a development key must get development's config — otherwise the
	// separation §76 exists for is one JSON field away from being bypassed.
	if in.Environment != "" && in.Environment != tenant.Environment {
		return protocol.ConfigResponse{}, muzak.Forbidden(
			"this key belongs to the " + tenant.Environment + " environment").
			WithCode("wrong_environment")
	}

	// EnvironmentSeq is the environment's own row id, narrowed to the width ClickHouse
	// sorts on (§23.1). It is the same number the store keys on, so no second lookup is
	// needed to turn a credential into a document.
	document, published, err := store.Current(ctx.Context(), int(tenant.EnvironmentSeq))
	if err != nil {
		return protocol.ConfigResponse{}, muzak.ServiceUnavailable(
			"cannot read this project's configuration right now").Wrap(err)
	}
	if !published {
		// Nothing published yet, which is where every project starts. An empty document
		// rather than a 404: a game whose first launch fails because nobody has pressed
		// publish is a worse first experience than one that runs on its own defaults.
		document = liveconfig.Document{}
	}

	subject := liveconfig.Subject{
		InstallID: in.InstallID,
		Context:   buildContext(ctx, in, document, tenant),
	}

	now := time.Now().UTC()
	result := document.Evaluate(now, tenant.ProjectID, subject)

	response := protocol.ConfigResponse{
		Version:      result.Version,
		Flags:        result.Flags,
		Events:       asEvents(result.Events),
		Upcoming:     asEvents(document.Upcoming(now, UpcomingWithin, tenant.ProjectID, subject)),
		ServerTimeMS: now.UnixMilli(),
	}
	if response.Flags == nil {
		response.Flags = map[string]any{}
	}

	// An SDK polls, and most polls are identical. The tag covers the decision and not the
	// clock — hashing ServerTimeMS would mean no two responses ever matched and this path
	// would never fire.
	tag := etag(response)
	ctx.SetHeader("ETag", tag)
	if match := ctx.Header("If-None-Match"); match != "" && match == tag {
		return protocol.ConfigResponse{}, muzak.NewHTTPError(http.StatusNotModified, "")
	}
	return response, nil
}

// buildContext assembles the facts a rule is evaluated over.
func buildContext(ctx *muzak.Context, in schemas.ConfigIn, document liveconfig.Document, tenant *core.Tenant) targeting.Context {
	out := make(targeting.Context, 16+len(in.Context))

	// The studio's own, namespaced by us. See the note on ConfigRequest.Context: this is
	// what makes a reserved key unreachable from a client rather than merely refused.
	for key, value := range in.Context {
		out[targeting.NamespaceContext+key] = value
	}

	// The client's, under names we own.
	out["app.version"] = in.App.Version
	if in.App.Build != "" {
		out["app.build"] = in.App.Build
	}
	out["device.platform"] = in.Device.Platform
	if in.Device.OSVersion != "" {
		out["device.os_version"] = in.Device.OSVersion
	}
	if in.Device.Locale != "" {
		out["device.locale"] = in.Device.Locale
	}
	out["sdk.name"] = in.SDK.Name
	out["sdk.version"] = in.SDK.Version

	// Derived from the connection, never sent. ClientAddr is the framework's, which means
	// it honours the trusted-proxy configuration rather than believing any forwarding
	// header that turns up.
	if resolver, found := muzak.TryFrom[*geoip.Resolver](ctx); found {
		for key, value := range resolver.Resolve(ctx.ClientAddr()).Context() {
			out[key] = value
		}
	}

	// The player's own history, and only when some rule in the project actually asks for
	// it. Most projects never do: a flag rolled out to everybody has no audience at all,
	// and geography and app version need no lookup. That decision was made once at publish.
	if document.NeedsPlayerFacts {
		addPlayerFacts(ctx, in, tenant, out)
	}
	return out
}

// addPlayerFacts reads what PlayerSense knows about this player.
//
// Two subjects, because they answer different questions and a studio must not have to guess
// which one it got: the installation is this device, the player is the account across all of
// them. An anonymous request has no account, so only the installation family is filled — and a
// rule naming `commerce.player.*` then falls through to NotExists rather than matching a zero,
// which is the difference between "has not bought" and "we have no idea who this is".
func addPlayerFacts(ctx *muzak.Context, in schemas.ConfigIn, tenant *core.Tenant, into targeting.Context) {
	cache, found := muzak.TryFrom[*facts.Cache](ctx)
	if !found {
		return
	}

	if record, ok := cache.Get(ctx.Context(),
		tenant.ProjectSeq, tenant.EnvironmentSeq, "install", in.InstallID); ok {
		record.Context("install", into)
	}
	if in.PlayerID != "" {
		if record, ok := cache.Get(ctx.Context(),
			tenant.ProjectSeq, tenant.EnvironmentSeq, "player", in.PlayerID); ok {
			record.Context("player", into)
		}
	}
}

func asEvents(in []liveconfig.ActiveEvent) []protocol.ConfigEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]protocol.ConfigEvent, 0, len(in))
	for _, event := range in {
		out = append(out, protocol.ConfigEvent{
			Key: event.Key, Kind: event.Kind, Priority: event.Priority,
			StartsAtMS: event.StartsAtMS, EndsAtMS: event.EndsAtMS,
			NextStartsAtMS: event.NextStartsAtMS,
			Payload:        event.Payload,
		})
	}
	return out
}

// etag hashes the decision, so that a polling SDK that would receive the same answer receives
// no answer at all.
//
// Over the marshalled response minus the clock. Go's encoder writes struct fields in
// declaration order and sorts map keys, so the same decision always produces the same bytes —
// which is what makes the comparison meaningful rather than accidental.
func etag(response protocol.ConfigResponse) string {
	decision := response
	decision.ServerTimeMS = 0

	encoded, err := json.Marshal(decision)
	if err != nil {
		// Unreachable for a value this package built. An empty tag is returned rather
		// than a wrong one, because a tag that collides serves a stale answer forever.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`
}
