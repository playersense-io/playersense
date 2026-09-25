package webhooks

import (
	"time"

	"github.com/google/uuid"
)

// Sample is a made-up event's data, for a test send: the shape the real event has, with values
// nobody will mistake for a real player.
func Sample(kind, env string) map[string]any {
	now := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	purchase := map[string]any{
		"id": uuid.NewString(), "status": "verified", "duplicate": false,
		"player":      map[string]any{"id": "test_player"},
		"product":     map[string]any{"id": "gems_500", "kind": "consumable", "quantity": 1},
		"price":       map[string]any{"amount_micros": 4_990_000, "currency": "USD", "from_store": true},
		"store":       map[string]any{"name": "app_store", "transaction_id": "2000000000000000"},
		"environment": "sandbox", "country": "US", "first_purchase": false, "purchased_at": at(-time.Minute),
	}
	switch kind {
	case "purchase.verified":
		return map[string]any{"purchase": purchase}
	case "purchase.rejected":
		purchase["status"] = "rejected"
		delete(purchase, "id")
		purchase["rejection"] = map[string]any{"code": "invalid_receipt", "message": "the store knows no such purchase"}
		return map[string]any{"purchase": purchase}
	case "purchase.refunded":
		purchase["status"] = "refunded"
		purchase["refund"] = map[string]any{"at": at(0), "reason": "customer_request", "source": "notification"}
		return map[string]any{"purchase": purchase}
	case "config.published":
		return map[string]any{"environment": env, "version": 42, "published_by": "test@example.com",
			"changes": map[string]any{"flags": 2, "experiments": 1, "liveops_events": 1}, "note": "A test publish"}
	case "config.rolled_back":
		return map[string]any{"environment": env, "from_version": 42, "to_version": 41, "rolled_back_by": "test@example.com"}
	case "experiment.started", "experiment.concluded":
		return map[string]any{"experiment": map[string]any{"id": uuid.NewString(), "key": "test_experiment",
			"flag": "shop_layout", "arms": []string{"control", "grid"}, "status": map[bool]string{true: "running", false: "concluded"}[kind == "experiment.started"]}}
	case "export.completed", "export.failed":
		return map[string]any{"export": map[string]any{"id": uuid.NewString(), "player": map[string]any{"id": "test_player"},
			"status": map[bool]string{true: "completed", false: "failed"}[kind == "export.completed"]}}
	case "player.deleted":
		return map[string]any{"player": map[string]any{"id": "test_player"}, "requested_at": at(-26 * time.Hour), "completed_at": at(0)}
	case "member.added", "member.removed":
		return map[string]any{"member": map[string]any{"email": "test@example.com", "role": "developer"}}
	case "api_key.created", "api_key.revoked":
		return map[string]any{"key": map[string]any{"id": uuid.NewString(), "kind": "project", "environment": env,
			"name": "A test key", "prefix": "ps_project_live_test"}}
	}
	return map[string]any{"test": true}
}
