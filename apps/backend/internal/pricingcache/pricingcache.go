// Package pricingcache names the shared pricing entries charging keeps in Valkey (ADR 0041), and
// deletes them.
//
// Its own package because three unrelated callers change what those entries hold: the operator
// catalogue, Stripe's subscription webhooks and the charger itself. None of them should import
// another to say so.
package pricingcache

import (
	"context"
	"strconv"

	"playersense.dev/backend/internal/valkey"
)

// OrgKey and PlanKey name the shared entries. Every writer that changes what they hold deletes them.
func OrgKey(cache *valkey.Client, orgSeq int) string {
	if cache == nil {
		return ""
	}
	return cache.Key("billing", "pricing", "org", strconv.Itoa(orgSeq))
}

func PlanKey(cache *valkey.Client, planID int) string {
	if cache == nil {
		return ""
	}
	return cache.Key("billing", "pricing", "plan", strconv.Itoa(planID))
}

// InvalidateOrganization is called after an organization moves plans or its overrides change.
func InvalidateOrganization(ctx context.Context, cache *valkey.Client, orgSeq int) error {
	if cache == nil {
		return nil
	}
	return cache.Del(ctx, OrgKey(cache, orgSeq))
}

// InvalidatePlan is called after a plan's allowances change or a card is published for it.
func InvalidatePlan(ctx context.Context, cache *valkey.Client, planID int) error {
	if cache == nil {
		return nil
	}
	return cache.Del(ctx, PlanKey(cache, planID))
}
