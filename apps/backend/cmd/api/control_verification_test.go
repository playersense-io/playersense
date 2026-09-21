package main

import (
	"net/http"
	"testing"
)

// A new unverified account can open its dashboard repeatedly. Authentication does not
// trigger email sending or attach an email throttle's Retry-After to successful reads.
func TestAnUnverifiedAccountCanSignIn(t *testing.T) {
	s := newStack(t)
	who := person(t)
	who.EmailVerified = false
	client := s.as(t, who)

	for range 2 {
		res := client.Get("/v1/organizations").AssertStatus(http.StatusOK)
		if got := res.Header.Get("Retry-After"); got != "" {
			t.Errorf("a dashboard read has Retry-After %q", got)
		}
	}
}
