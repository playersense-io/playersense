package handlers

import (
	"sort"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/fx"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/schemas"
)

// How an organization's net revenue is estimated: each store's share, the usual one until the
// organization sets its own, because only the studio knows which programs it is in.

func revenueSettingsOut(ctx *muzak.Context, org *ent.Organization) schemas.RevenueSettingsOut {
	out := schemas.RevenueSettingsOut{Note: query.NetNote, Rates: schemas.RatesStatusOut{Base: "USD"}}
	stores := map[string]bool{}
	for s := range query.DefaultStoreFees {
		stores[s] = true
	}
	for s := range org.StoreFees {
		stores[s] = true
	}
	for s := range stores {
		_, custom := org.StoreFees[s]
		out.Stores = append(out.Stores, schemas.StoreFeeOut{Store: s, Percent: query.FeeOf(org.StoreFees, s) * 100,
			DefaultPercent: query.DefaultStoreFees[s], Custom: custom})
	}
	sort.Slice(out.Stores, func(i, j int) bool { return out.Stores[i].Store < out.Stores[j].Store })
	if svc, found := muzak.TryFrom[*fx.Service](ctx); found {
		if latest, err := svc.Latest(ctx.Context()); err == nil {
			at := latest.At
			out.Rates.Available, out.Rates.AsOf, out.Rates.Currencies = true, &at, len(latest.PerUSD)
		}
	}
	return out
}

// ReadRevenueSettings is the organization's store shares, and whether revenue can be shown in
// dollars.
func ReadRevenueSettings(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.RevenueSettingsOut, error) {
	return revenueSettingsOut(ctx, muzak.From[*core.Principal](ctx).Organization), nil
}

// UpdateRevenueSettings sets stores' shares; a store sent with null goes back to the usual rate.
func UpdateRevenueSettings(ctx *muzak.Context, in schemas.RevenueSettingsIn) (schemas.RevenueSettingsOut, error) {
	org := muzak.From[*core.Principal](ctx).Organization
	fees := map[string]float64{}
	for s, v := range org.StoreFees {
		fees[s] = v
	}
	changed := map[string]any{}
	for s, pct := range in.StoreFees {
		if pct == nil {
			delete(fees, s)
			changed[s] = nil
			continue
		}
		fees[s] = *pct
		changed[s] = *pct
	}
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Organization, error) {
		updated, err := tx.Organization.UpdateOne(org).SetStoreFees(fees).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.RevenueSettingsChanged, "organization", org.UID.String(),
			map[string]any{"store_fees": changed})
	})
	if err != nil {
		return schemas.RevenueSettingsOut{}, err
	}
	return revenueSettingsOut(ctx, updated), nil
}
