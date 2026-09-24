package handlers

import (
	"slices"
	"strings"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/segment"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/targeting"
)

// reachSample is how many installs a reach estimate is drawn from: a share within about a
// percent, read in one pass that stops at the limit.
const reachSample = 20_000

// estimable reports whether a key can be read from what is stored about an install. Platform
// and country are read as they were at install, which is almost always what they are now.
func estimable(key string) bool {
	switch {
	case key == "device.platform", key == "geo.country":
		return true
	case strings.HasPrefix(key, "commerce.install."), strings.HasPrefix(key, "activity.install."):
		return true
	}
	return false
}

func keysOfRule(r targeting.Rule, into map[string]bool) {
	if r.Key != "" {
		into[r.Key] = true
	}
	for _, c := range r.All {
		keysOfRule(c, into)
	}
	for _, c := range r.Any {
		keysOfRule(c, into)
	}
	if r.Not != nil {
		keysOfRule(*r.Not, into)
	}
}

// SegmentReach estimates how many of the last week's players each of a project's segments holds,
// every segment against one sample, with the same evaluator config-api answers players with.
func SegmentReach(ctx *muzak.Context, in schemas.SegmentReachParams) (schemas.SegmentReachOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	engine, scope, _, err := explore(ctx, in.Environment, schemas.AnalyticsRange{})
	if err != nil {
		return schemas.SegmentReachOut{}, err
	}
	now := nowUTC()
	since := now.AddDate(0, 0, -7)
	out := schemas.SegmentReachOut{Since: since, Segments: []schemas.SegmentReachRow{},
		SuppressionThreshold: engine.Threshold()}

	rows, err := db.Client.Segment.Query().Where(segment.ProjectID(proj.ID), segment.Archived(false)).
		Order(ent.Asc(segment.FieldKey)).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	segments, err := projectSegments(ctx.Context(), db, proj.ID)
	if err != nil {
		return out, err
	}

	sample, err := engine.ReachSample(ctx.Context(), scope, since, reachSample, now)
	if err != nil {
		return out, exploreFailed(err, "players")
	}
	out.Sampled, out.Exact = len(sample), len(sample) < reachSample
	if out.Exact {
		out.WeeklyPlayers = uint64(len(sample))
	} else if out.WeeklyPlayers, err = engine.PlayersSince(ctx.Context(), scope, since); err != nil {
		return out, exploreFailed(err, "players")
	}

	contexts := make([]targeting.Context, len(sample))
	for i, p := range sample {
		c := targeting.Context{"device.platform": p.Platform, "geo.country": p.Country}
		record := facts.Record{
			SpendMicros: p.RevenueMicros, Purchases: int64(p.Purchases), DaysActive: int64(p.ActiveDays),
			Sessions: int64(p.Sessions), PlaytimeSeconds: int64(p.PlaytimeMs / 1000),
			DaysSinceSeen: int64(now.Sub(p.LastSeen).Hours() / 24), DaysSinceFirst: int64(now.Sub(p.FirstSeen).Hours() / 24),
		}
		if p.FirstPurchaseAt != nil {
			days := int64(now.Sub(*p.FirstPurchaseAt).Hours() / 24)
			record.DaysSinceFirstPurchase = &days
		}
		if p.LastPurchaseAt != nil {
			days := int64(now.Sub(*p.LastPurchaseAt).Hours() / 24)
			record.DaysSinceLastPurchase = &days
		}
		record.Context(facts.SubjectInstall, c)
		contexts[i] = c
	}

	k := uint64(engine.Threshold())
	for _, row := range rows {
		one := schemas.SegmentReachRow{Key: row.Key, Estimable: true}
		rule, err := ruleFrom(row.Rule)
		if err == nil {
			rule, err = segments.Inline(rule)
		}
		if err != nil {
			one.Estimable, one.Unestimable = false, []string{"the rule does not resolve: " + err.Error()}
			out.Segments = append(out.Segments, one)
			continue
		}
		keys := map[string]bool{}
		keysOfRule(rule, keys)
		for key := range keys {
			if !estimable(key) {
				one.Unestimable = append(one.Unestimable, key)
			}
		}
		if len(one.Unestimable) > 0 {
			slices.Sort(one.Unestimable)
			one.Estimable = false
			out.Segments = append(out.Segments, one)
			continue
		}
		if len(sample) > 0 {
			matched := 0
			for _, c := range contexts {
				if rule.Matches(c) {
					matched++
				}
			}
			share := float64(matched) / float64(len(sample))
			players := uint64(share*float64(out.WeeklyPlayers) + 0.5)
			one.Share = &share
			if players >= k || players == 0 {
				one.Players = &players
			}
		}
		out.Segments = append(out.Segments, one)
	}
	return out, nil
}
