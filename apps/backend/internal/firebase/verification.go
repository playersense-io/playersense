package firebase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// IdentityToolkitURL is Firebase's own authentication API, the one the browser SDK calls.
const IdentityToolkitURL = "https://identitytoolkit.googleapis.com/v1"

// Sender asks Firebase to email somebody a verification link.
//
// # Why not the Admin SDK
//
// The Admin SDK has `EmailVerificationLink`, and it does less than its name suggests: it
// *generates* a link and leaves the sending to you. Using it would mean owning an SMTP or ESP
// integration, the message templates, the bounce handling and the deliverability — all to
// reproduce something Firebase already does, configurable in the same console where the rest
// of the sign-in lives.
//
// It would also mean holding a **service account private key**, which is a credential with
// far more authority than this one task needs and one more secret to rotate and protect. The
// REST call below authenticates with the Web API key, which is public by design: it ships in
// every browser bundle that uses Firebase, and it authorises nothing on its own — the caller
// still has to present a valid ID token for the account it is acting on.
//
// So this is `accounts:sendOobCode`, the exact call the browser SDK's
// `sendEmailVerification()` makes, with no new secret and no mail infrastructure.
type Sender struct {
	// APIKey is the Firebase Web API key.
	APIKey string

	// URL is the Identity Toolkit base. Overridden by tests, and by a deployment behind a
	// proxy that must see the request.
	URL string

	HTTP *http.Client
}

// NewSender returns one pointed at Firebase.
func NewSender(apiKey string) *Sender {
	return &Sender{APIKey: apiKey, URL: IdentityToolkitURL}
}

func (s *Sender) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	// Short, because this sits in front of a response somebody is waiting for and the
	// request has already been refused. A slow send must not also be a slow refusal.
	return &http.Client{Timeout: 8 * time.Second}
}

func (s *Sender) url() string {
	if s.URL != "" {
		return s.URL
	}
	return IdentityToolkitURL
}

// ErrSentRecently reports that Firebase declined because one went out a moment ago.
//
// Not a failure. Firebase applies its own limit per account and answers
// TOO_MANY_ATTEMPTS_TRY_LATER to the second request within a short window — measured against
// a real project, the very next call after a successful send. A caller that treated it as an
// error would show somebody a red banner for the one thing that did work.
var ErrSentRecently = errors.New("a verification email was sent recently")

// ErrNotConfigured reports that no API key was given, so nothing could be sent.
var ErrNotConfigured = errors.New("no Firebase API key is configured")

// SendVerification emails the account behind an ID token a link that verifies its address.
//
// The ID token is the authorisation: Firebase decides which account to mail from the token
// itself, so there is no way for this to be aimed at an address the caller does not already
// hold a valid token for. That is also why it takes the raw token rather than an email
// address — an email parameter would be a way to make Firebase send mail to anybody.
func (s *Sender) SendVerification(ctx context.Context, idToken string) error {
	if s.APIKey == "" {
		return ErrNotConfigured
	}

	body, err := json.Marshal(map[string]string{
		"requestType": "VERIFY_EMAIL",
		"idToken":     idToken,
	})
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.url()+"/accounts:sendOobCode?key="+s.APIKey, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := s.client().Do(request)
	if err != nil {
		return fmt.Errorf("ask Firebase to send a verification email: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusOK {
		return nil
	}

	// The body is small and its `error.message` is the only thing worth reading. Bounded
	// anyway: an endpoint that answered a gigabyte would otherwise be read into memory
	// while somebody waits.
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	var failure struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &failure)

	switch failure.Error.Message {
	case "TOO_MANY_ATTEMPTS_TRY_LATER":
		return ErrSentRecently
	case "":
		return fmt.Errorf("Firebase answered %s", response.Status)
	default:
		return fmt.Errorf("Firebase refused to send a verification email: %s",
			failure.Error.Message)
	}
}
