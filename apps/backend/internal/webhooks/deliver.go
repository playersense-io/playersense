package webhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/schema"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/signing"
)

// Timeout is how long one try may take, answer included.
const Timeout = 10 * time.Second

// ResponseLimit is how much of an answer's body is kept.
const ResponseLimit = 4 << 10

// UserAgent names PlayerSense on every delivery.
const UserAgent = "PlayerSense-Webhooks/1.0"

// Reserved are the headers PlayerSense sets, which an endpoint's own headers may not.
var Reserved = []string{"content-type", "user-agent", "host", "content-length",
	strings.ToLower(signing.HeaderID), strings.ToLower(signing.HeaderTimestamp), strings.ToLower(signing.HeaderSignature)}

// Target is an endpoint as a delivery needs it: where, with what, signed with what.
type Target struct {
	EndpointID  int
	EndpointUID uuid.UUID
	ProjectID   int
	URL         string
	Headers     []schema.WebhookHeader
	// Secrets sign each try: the current secret, and the previous one while a roll is honoured.
	Secrets []string
}

// TargetOf reads an endpoint as a target, as of now.
func TargetOf(e *ent.WebhookEndpoint, now time.Time) Target {
	t := Target{EndpointID: e.ID, EndpointUID: e.UID, ProjectID: e.ProjectID, URL: e.URL, Secrets: []string{e.Secret}}
	if e.PreviousSecret != "" && e.PreviousExpiresAt != nil && now.Before(*e.PreviousExpiresAt) {
		t.Secrets = append(t.Secrets, e.PreviousSecret)
	}
	for _, h := range e.Headers {
		if h.Secret {
			h.Value = e.HeaderSecrets[h.Name]
		}
		t.Headers = append(t.Headers, h)
	}
	return t
}

// Result is one try.
type Result struct {
	At time.Time
	// Code is the HTTP status; 0 when no answer came.
	Code     int
	Error    string
	Ms       int
	Response string
}

// OK is a 2xx answer.
func (r Result) OK() bool { return r.Code >= 200 && r.Code < 300 }

// Deliverer POSTs events.
type Deliverer struct {
	HTTP *http.Client
}

// NewDeliverer is a deliverer whose client refuses private addresses (unless allowPrivate, for a
// local stack and tests), follows no redirect, and keeps connections to each host open, which is
// what makes a steady stream to one endpoint cheap.
func NewDeliverer(allowPrivate bool) *Deliverer {
	client := notify.NewClient(Timeout, allowPrivate)
	if t, ok := client.Transport.(*http.Transport); ok {
		// As many idle connections to one host as the pool has workers: several endpoints often
		// share a host (a studio's gateway), and a connection closed after each try is a new TCP and
		// TLS handshake for the next, and a socket left in TIME_WAIT.
		t.MaxIdleConns = 4096
		t.MaxIdleConnsPerHost = 256
		t.MaxConnsPerHost = 0
	}
	return &Deliverer{HTTP: client}
}

// Send makes one try.
func (d *Deliverer) Send(ctx context.Context, t Target, eventID uuid.UUID, body []byte) Result {
	start := time.Now()
	res := Result{At: start.UTC()}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		res.Error = "The URL cannot be requested"
		return res
	}
	for _, h := range t.Headers {
		if h.Name != "" {
			req.Header.Set(h.Name, h.Value)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set(signing.HeaderID, eventID.String())
	req.Header.Set(signing.HeaderTimestamp, strconv.FormatInt(start.Unix(), 10))
	req.Header.Set(signing.HeaderSignature, signing.Sign(start, body, t.Secrets...))

	resp, err := d.HTTP.Do(req)
	res.Ms = int(time.Since(start).Milliseconds())
	if err != nil {
		res.Error = errorWords(err)
		return res
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, ResponseLimit))
	// Whatever is left is read away, so the connection goes back to the pool.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	res.Code, res.Response = resp.StatusCode, string(raw)
	res.Ms = int(time.Since(start).Milliseconds())
	return res
}

// errorWords is what went wrong before an answer, in words the delivery log shows.
func errorWords(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() || errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("Timed out after %d s", int(Timeout.Seconds()))
	}
	if errors.Is(err, notify.ErrPrivateAddress) {
		return "Refused: the address is private"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "The host does not resolve"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Connection refused"
	case strings.Contains(msg, "connection reset"):
		return "Connection reset"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls:"):
		return "TLS failed: " + clip(msg, 120)
	}
	return clip(msg, 200)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// CheckURL says why a URL cannot be an endpoint, or nil: https on a public host, no credentials in
// it. allowPrivate admits http and private addresses, for a local stack and tests; the client
// checks the address again when it connects, so a name that resolves somewhere private later is
// refused then.
func CheckURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("not a URL: it starts with https://")
	}
	if u.User != nil {
		return errors.New("no user name or password in the URL; send one as a header instead")
	}
	if allowPrivate {
		if u.Scheme != "https" && u.Scheme != "http" {
			return errors.New("only http and https URLs")
		}
		return nil
	}
	if u.Scheme != "https" {
		return errors.New("only https URLs: events carry player ids and purchases")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") {
		return errors.New("a public address: PlayerSense cannot reach localhost or a private network")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !notify.Public(addr) {
			return errors.New("a public address: PlayerSense cannot reach localhost or a private network")
		}
	}
	return nil
}
