package handlers

import (
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/apikey"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/valkey"
)

func projectKey(ctx *muzak.Context, id schemas.RollAPIKeyIn) (*ent.APIKey, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	key, err := db.Client.APIKey.Query().
		Where(apikey.UID(id.KeyID), apikey.HasEnvironmentWith(environment.ProjectID(proj.ID))).
		WithEnvironment().Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no such key")
		}
		return nil, core.AsHTTPError(err)
	}
	return key, nil
}

// RollAPIKey replaces a key with a new one of its kind, environment and name, shown once. The old
// key stops at once, or after a grace so a fleet of servers can be moved over without an outage.
// An SDK key is inside shipped builds, so rolling one without a grace stops every one of them.
func RollAPIKey(ctx *muzak.Context, in schemas.RollAPIKeyIn) (schemas.RolledAPIKeyOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	old, err := projectKey(ctx, in)
	if err != nil {
		return schemas.RolledAPIKeyOut{}, err
	}
	if old.RevokedAt != nil || old.RolledAt != nil {
		return schemas.RolledAPIKeyOut{}, muzak.Conflict("that key is already revoked or rolled").WithCode("key_retired")
	}
	env := old.Edges.Environment
	generated, err := core.NewAPIKey(string(old.Kind), string(env.Kind))
	if err != nil {
		return schemas.RolledAPIKeyOut{}, core.AsHTTPError(err)
	}
	now := nowUTC()
	stops := now.Add(time.Duration(in.GraceHours) * time.Hour)
	who := actorOf(ctx).Label

	type rolled struct{ fresh, old *ent.APIKey }
	result, err := inTx(ctx, func(tx *ent.Tx) (rolled, error) {
		fresh, err := tx.APIKey.Create().
			SetKind(old.Kind).SetPublicID(generated.PublicID).SetSecretHash(generated.Hash).
			SetName(old.Name).SetEnvironmentID(env.ID).SetCreatedBy(who).
			Save(ctx.Context())
		if err != nil {
			return rolled{}, core.AsHTTPError(err)
		}
		update := tx.APIKey.UpdateOne(old).SetRolledAt(now).SetReplacedBy(fresh.UID).SetExpiresAt(stops)
		if in.GraceHours == 0 {
			update = update.SetRevokedAt(now).SetRevokedBy(who)
		}
		retired, err := update.Save(ctx.Context())
		if err != nil {
			return rolled{}, core.AsHTTPError(err)
		}
		return rolled{fresh, retired}, auditFor(ctx).record(ctx.Context(), tx, audit.KeyRolled, "api_key", old.UID.String(),
			map[string]any{
				"kind": string(old.Kind), "environment": string(env.Kind), "project": proj.Slug,
				"public_id": old.PublicID, "replaced_by": generated.PublicID, "grace_hours": in.GraceHours,
			})
	})
	if err != nil {
		return schemas.RolledAPIKeyOut{}, err
	}
	// Stopped now: the cached entries go with it, as a revocation's do.
	if in.GraceHours == 0 {
		if resolver, found := muzak.TryFrom[*core.KeyResolver](ctx); found {
			if err := resolver.EvictPublicID(ctx.Context(), old.PublicID, core.RevocationTombstoneTTL); err != nil {
				ctx.Logger().Warn("could not evict a rolled key from the cache", "error", err)
			}
		}
	}
	ctx.SetStatus(201)
	return schemas.RolledAPIKeyOut{
		Key:      schemas.APIKeyCreatedOut{APIKeyOut: apiKeyOut(result.fresh, string(env.Kind)), Secret: generated.Secret},
		Replaced: apiKeyOut(result.old, string(env.Kind)),
	}, nil
}

// ReadKeyUsage is one key's requests by hour, up to two days.
func ReadKeyUsage(ctx *muzak.Context, in schemas.KeyUsageParams) (schemas.KeyUsageOut, error) {
	key, err := projectKey(ctx, schemas.RollAPIKeyIn{KeyID: in.KeyID})
	if err != nil {
		return schemas.KeyUsageOut{}, err
	}
	now := nowUTC()
	series, last, err := core.ReadKeyUsage(ctx.Context(), muzak.From[*valkey.Client](ctx), []string{key.PublicID}, in.Hours, now)
	if err != nil {
		return schemas.KeyUsageOut{}, muzak.ServiceUnavailable("key usage could not be read").Wrap(err)
	}
	start := now.Truncate(time.Hour).Add(-time.Duration(in.Hours-1) * time.Hour)
	out := schemas.KeyUsageOut{KeyID: key.UID, From: start, Hours: make([]schemas.KeyHourOut, in.Hours), LastUsedAt: key.LastUsedAt}
	for i, n := range series[key.PublicID] {
		out.Hours[i] = schemas.KeyHourOut{Hour: start.Add(time.Duration(i) * time.Hour), Requests: n}
		out.Total += n
	}
	if at, found := last[key.PublicID]; found && (out.LastUsedAt == nil || at.After(*out.LastUsedAt)) {
		out.LastUsedAt = &at
	}
	return out, nil
}
