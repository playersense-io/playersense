package handlers

import (
	"time"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/internal/schemas"
)

func nowUTC() time.Time { return time.Now().UTC() }

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// retentionOrDefault applies §24's default tier when the client chose none.
//
// The field is a pointer precisely so that "not chosen" is distinguishable here; a zero
// would have been rejected by validation before reaching this.
func retentionOrDefault(v *uint16) uint16 {
	if v == nil {
		return 30
	}
	return *v
}

func organizationOut(org *global.Organization) schemas.OrganizationOut {
	return schemas.OrganizationOut{
		ID:        org.UID,
		Name:      org.Name,
		Slug:      org.Slug,
		CreatedAt: org.CreatedAt,
	}
}

// policyNotes says what a tier costs, at the point a studio can still choose another one.
//
// kids_strict is the one that matters: it has no persistent identifier, so recognising
// the same player on two days is impossible and cross-day retention cannot be computed
// (§98). Selling a Kids Category studio an analytics product and letting them find an
// empty retention page after integrating is a worse outcome than saying so here.
func policyNotes(tier string) []string {
	switch tier {
	case "kids_strict":
		return []string{
			"kids_strict uses a session-scoped identifier only, so cross-day retention " +
				"and all cohort metrics are unavailable for this project. Same-day " +
				"aggregates — sessions, events, revenue, playtime — are unaffected.",
			"Acquisition attribution is disabled.",
		}
	case "child_directed":
		return []string{
			"child_directed disables acquisition attribution and caps raw retention. " +
				"Retention and cohort metrics are unaffected.",
		}
	default:
		return nil
	}
}

func projectOut(proj *global.Project, envs []*global.Environment) schemas.ProjectOut {
	out := schemas.ProjectOut{
		ID:            proj.UID,
		Name:          proj.Name,
		Slug:          proj.Slug,
		HomeRegion:    proj.HomeRegion,
		PolicyTier:    string(proj.PolicyTier),
		RetentionDays: proj.RetentionDays,
		CreatedAt:     proj.CreatedAt,
		Notes:         policyNotes(string(proj.PolicyTier)),
	}
	for _, env := range envs {
		out.Environments = append(out.Environments, schemas.EnvironmentOut{
			ID:   env.UID,
			Kind: string(env.Kind),
		})
	}
	return out
}

func apiKeyOut(key *global.APIKey, environment string) schemas.APIKeyOut {
	return schemas.APIKeyOut{
		ID:          key.UID,
		Kind:        string(key.Kind),
		Prefix:      key.Prefix,
		Name:        key.Name,
		Environment: environment,
		CreatedAt:   key.CreatedAt,
		LastUsedAt:  key.LastUsedAt,
		RevokedAt:   key.RevokedAt,
	}
}
