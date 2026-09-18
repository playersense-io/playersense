// Package ratelimit counts a request against the budgets it belongs to.
//
// The framework's own limiter runs **before** any dependency resolves, so it can only key on
// the client's address — it cannot know which project, which device or which person is
// asking, because none of that has been established yet. That is the right shape for a wall
// against an unauthenticated flood and the wrong shape for everything else: an address is not
// a player. Behind carrier-grade NAT tens of thousands of subscribers share one, and a budget
// keyed on that punishes a whole country for one bad actor.
//
// So the meaningful limits are counted here instead, after the credential has been resolved
// and the request has been bound, where the caller is known by name.
//
// # The rule this package exists to keep
//
// **One player's behaviour must not cost another player their data.** A device stuck in a
// retry loop has to be stopped without the well-behaved devices beside it noticing, which is
// only possible if the budget that stops it is the device's own. Everything below follows
// from that: the device budget is the one meant to bite, and the wider budgets are sized so
// that only genuine abuse reaches them.
//
// Full documentation, including the numbers and why each is what it is:
// `docs/rate-limits.md`.
package ratelimit

import (
	"fmt"
	"strconv"
	"time"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/valkey"
)

// Budget is one limit and whose it is.
type Budget struct {
	// Quota names the limit — "device-burst", "project-sustained". It is part of the
	// counter's key, so two budgets of the same name for the same subject share a counter
	// and two different names never collide.
	Quota string

	// Subject is who the budget belongs to: a project, a device, an address inside a
	// project. Derived from client-supplied data and therefore **never
	// logged** — it routinely carries an installation identifier.
	Subject string

	Window time.Duration
	Limit  int
}

// Limiter counts requests against budgets.
//
// Backed by the same Valkey storage and the same Lua script the framework's limiter uses, so
// there is one definition of what a window means rather than two that drift.
type Limiter struct {
	storage *valkey.RateLimitStorage

	// FailOpen decides what happens when Valkey cannot be reached.
	//
	// True on both SDK paths: ingestion and configuration. §65's rule is that losing
	// Valkey costs speed rather than data. False reports a storage error as a 503; no
	// service currently registers a limiter with that policy.
	FailOpen bool
}

// New returns a limiter over the given storage, or a nil one when `enabled` is false.
//
// Nil rather than a flag inside, because [Limiter.Allow] already tolerates a nil receiver —
// so "limits are off" is the same code path as "this service publishes no limiter", and there
// is one state to reason about instead of two that must agree.
//
// Off is a test convenience and nothing else: it is how one suite's counters stop spending the
// next one's budget. No deployment should set it.
func New(storage *valkey.RateLimitStorage, failOpen, enabled bool) *Limiter {
	if !enabled {
		return nil
	}
	return &Limiter{storage: storage, FailOpen: failOpen}
}

// Allow counts one request against every budget and refuses if any is over.
//
// All of them are counted even when the first is already over, because a counter that stops
// being incremented stops being a measurement: a device over its own limit must still
// contribute to its project's, or the project's number becomes a number about something else.
//
// The refusal names the quota and carries `Retry-After`, so an SDK waits the right amount
// rather than guessing — and so a person reading a log can tell "this device is looping" from
// "this project is over its ceiling", which are different problems with different fixes.
func (l *Limiter) Allow(ctx *muzak.Context, budgets ...Budget) error {
	if l == nil || len(budgets) == 0 {
		return nil
	}

	quotas := make([]string, len(budgets))
	subjects := make([]string, len(budgets))
	windows := make([]time.Duration, len(budgets))
	for i, budget := range budgets {
		quotas[i] = budget.Quota
		subjects[i] = budget.Subject
		windows[i] = budget.Window
	}

	counted, err := l.storage.IncrementMany(ctx.Context(), quotas, subjects, windows)
	if err != nil {
		if l.FailOpen {
			return nil
		}
		return muzak.ServiceUnavailable("cannot check the rate limit right now").Wrap(err)
	}

	// The first *breach* in declaration order, not the first counted. Callers put the
	// narrowest budget first, so a looping device is told it is the device rather than
	// being told its project is busy — which would send a developer to look at the wrong
	// thing.
	for i, result := range counted {
		if result.Count <= budgets[i].Limit {
			continue
		}
		return refuse(ctx, budgets[i], result.Reset)
	}
	return nil
}

// AllowOne is the single-budget case, which is most of them.
func (l *Limiter) AllowOne(ctx *muzak.Context, budget Budget) error {
	return l.Allow(ctx, budget)
}

func refuse(ctx *muzak.Context, budget Budget, reset time.Duration) error {
	seconds := int(reset.Seconds())
	if seconds < 1 {
		// Never zero. A client told to retry in no time retries immediately, which is the
		// request that was just refused.
		seconds = 1
	}
	// The real header, because that is what an SDK reads. The message is for a person
	// reading a log; `Retry-After` is for the code that has to decide when to come back.
	ctx.SetHeader(muzak.HeaderRetryAfter, strconv.Itoa(seconds))

	return muzak.TooManyRequests(fmt.Sprintf(
		"over the %s limit of %d per %s; try again in %ds",
		budget.Quota, budget.Limit, budget.Window, seconds)).
		WithCode("rate_limited")
}
