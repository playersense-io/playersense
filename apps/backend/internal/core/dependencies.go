package core

import (
	"errors"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/internal/pgdb"
)

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
	case errors.Is(err, ErrNotFound), ent.IsNotFound(err):
		return muzak.NotFound("not found").Wrap(err)
	case ent.IsConstraintError(err):
		return muzak.Conflict("that already exists").Wrap(err)
	case ent.IsValidationError(err):
		return muzak.UnprocessableEntity("the request could not be stored as given").Wrap(err)
	default:
		return err
	}
}

// DB is the database, published to handlers with WithSingleton.
//
// An alias rather than a re-export, so a handler names the thing it wants without
// importing the package that knows how to dial it.
type DB = pgdb.DB

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
