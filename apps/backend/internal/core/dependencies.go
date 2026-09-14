package core

import (
	"errors"
	"time"

	"muzak.dev/framework"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/pgdb"
)

// RequireAdminToken guards the whole control API with a shared secret.
//
// This is the shared-secret case the framework's own guard is meant for: an internal
// console talking to its own backend. Per-user credentials want a provider that resolves
// the user instead, and that arrives with the dashboard's own sign-in — §52 already
// distinguishes dashboard tokens from the key classes minted here, so this guard is the
// placeholder for that surface rather than the design for it.
//
// The comparison is constant time, so response timing does not reveal how much of the
// supplied token was correct.
func RequireAdminToken(settings config.Settings) muzak.Guard {
	return muzak.RequireBearerToken(settings.AdminToken)
}

// ErrNotFound reports a lookup that matched nothing.
var ErrNotFound = errors.New("not found")

// AsHTTPError translates a store error into the response it deserves.
//
// The translation lives at the edge rather than in the store, which keeps the store free
// of any knowledge about HTTP, and it is exhaustive rather than a fall-through: an error
// nothing recognises becomes an opaque 500 with the real cause logged and never sent.
func AsHTTPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), global.IsNotFound(err):
		return muzak.NotFound("not found").Wrap(err)
	case global.IsConstraintError(err):
		return muzak.Conflict("that already exists").Wrap(err)
	case global.IsValidationError(err):
		return muzak.UnprocessableEntity("the request could not be stored as given").Wrap(err)
	default:
		return err
	}
}

// Control is the control plane, published to handlers with muzak.WithSingleton.
type Control = pgdb.Control

// RateLimitPolicy is the application-wide budget every route inherits.
//
// Three windows rather than one, because a single number cannot tell a person clicking
// from a script that never stops. Every quota is counted for every request, so pausing
// between bursts launders nothing.
func RateLimitPolicy(storage muzak.RateLimitStorage) muzak.RateLimitOptions {
	return muzak.RateLimitOptions{
		// Counters live in Valkey so every instance shares one budget. The in-process
		// default the framework ships is right for a single process and wrong for
		// several: a limit of three hundred a minute silently becomes three hundred a
		// minute per instance.
		Storage: storage,
		// An IPv6 /64 is the block most providers hand out, so keying on the exact
		// address gives a client holding one a fresh budget on every request.
		Tracker: muzak.IPPrefixTracker(32, 64),
		Quotas: []muzak.Quota{
			{Name: "burst", Window: time.Second, Limit: 10},
			{Name: "sustained", Window: time.Minute, Limit: 300},
		},
	}
}
