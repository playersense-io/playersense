package firebase

import (
	"errors"
	"fmt"
)

// TokenError is a token that is not acceptable. The caller's problem: a 401.
//
// Separated from a key-source failure on purpose. Both stop a request, but one means "sign in
// again" and the other means "we cannot check right now" — and answering the second with the
// first tells a person to do something that cannot work, during an outage, while they watch
// their dashboard log itself out.
type TokenError struct {
	Reason string
	Err    error
}

func (e *TokenError) Error() string {
	if e.Err != nil {
		return e.Reason + ": " + e.Err.Error()
	}
	return e.Reason
}

func (e *TokenError) Unwrap() error { return e.Err }

// UnknownKeyError is a token signed with a key Google does not publish.
//
// The caller's problem rather than an outage, and the distinction is earned: [KeySource.Key]
// refreshes before it returns this, so a `kid` that is still unknown afterwards is a forged or
// foreign token rather than a rotation we had not caught up with.
type UnknownKeyError struct{ KeyID string }

func (e *UnknownKeyError) Error() string {
	return fmt.Sprintf("no Google signing key with id %q", e.KeyID)
}

// Rejected reports whether an error means "this token is not acceptable" as opposed to "we
// could not check".
//
// The one place the two are told apart, so that no call site has to remember the list. A
// refusal is a 401 and the person signs in again; anything else is a 503, because telling
// somebody their session is invalid when Google is unreachable sends them to do the one thing
// that cannot help.
func Rejected(err error) bool {
	var token *TokenError
	var key *UnknownKeyError
	return errors.As(err, &token) || errors.As(err, &key)
}
