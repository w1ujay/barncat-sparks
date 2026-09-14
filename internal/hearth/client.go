// Package hearth is a client for lempster-hearth's control API: the cabin K4's
// power and Starlink power-save. It talks to the Pi over the tailnet and, when
// the Pi cannot be reached at all, falls back to the hosted replica for status
// and for queueing a power-save change. See
// docs/plans/2026-09-13-hearth-control-design.md.
package hearth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	piTimeout      = 4 * time.Second  // Pi reads
	controlTimeout = 15 * time.Second // Pi control POSTs (PS0 over CAT, relay pulse)
	replicaTimeout = 6 * time.Second  // hosted replica, over the internet
)

var (
	ErrNotConfigured = errors.New("hearth: no hearth_url configured")
	ErrPiUnreachable = errors.New("hearth: Pi unreachable")
)

// HTTPError is a non-2xx response from hearth. Message is hearth's plain-text
// body, trimmed. Location is set from the response's Location header when
// Status is a redirect (3xx); redirects are never followed (see
// defaultHTTPClient), so the caller sees the redirect itself rather than
// whatever it points to.
type HTTPError struct {
	Status   int
	Message  string
	Location string
}

func (e *HTTPError) Error() string {
	switch {
	case e.Status >= 300 && e.Status < 400:
		if e.Location != "" {
			return "hearth redirected to " + e.Location + "; use that URL"
		}
		return fmt.Sprintf("hearth redirected (HTTP %d); check the URL", e.Status)
	case e.Status == http.StatusUnauthorized:
		return "token rejected"
	case e.Status == http.StatusForbidden && e.Message != "":
		return "not permitted: " + e.Message
	case e.Status == http.StatusForbidden:
		return "not permitted"
	case e.Message != "":
		return e.Message
	}
	return fmt.Sprintf("hearth returned HTTP %d", e.Status)
}

// transportError marks a failure to get any HTTP response at all: refused
// dial, timeout, reset, TLS. Only these trigger the replica fallback.
type transportError struct{ err error }

func (e *transportError) Error() string { return "hearth: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func isTransport(err error) bool {
	var te *transportError
	return errors.As(err, &te)
}

// Client talks to one hearth site. A nil HTTP uses defaultHTTPClient; every
// request is bounded by its own timeout.
type Client struct {
	PiURL      string
	ReplicaURL string
	Token      string
	Site       string // hub site name sent as ?site= on status and power-save calls; empty sends none
	HTTP       *http.Client
}

// defaultHTTPClient never follows redirects: a 3xx response is returned as-is
// so do() turns it into an *HTTPError instead of Go silently re-issuing a
// POST as a GET (or dropping the Authorization header on a cross-host hop).
var defaultHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Identity is GET /api/whoami: who the token authenticates as on that host.
type Identity struct {
	Name    string   `json:"name"`
	Owner   bool     `json:"owner"`
	Scopes  []string `json:"scopes"`
	Replica bool     `json:"replica"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTPClient
}

// do sends one request to base+path and decodes a 2xx JSON body into out
// (nil out discards the body). A failure to get any response is returned as
// a *transportError; a non-2xx response as an *HTTPError.
func (c *Client) do(ctx context.Context, base, method, path string, body, out any, timeout time.Duration) error {
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("hearth: URL %q must start with http:// or https://", base)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return &transportError{err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		he := &HTTPError{Status: resp.StatusCode, Message: strings.TrimSpace(string(msg))}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			he.Location = resp.Header.Get("Location")
		}
		return he
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("hearth: decode %s: %w", path, err)
	}
	return nil
}

// piThenReplica runs fn against the Pi and, only if that failed at the
// transport level and a replica is configured, against the replica.
// usedReplica reports whether the replica was tried.
func (c *Client) piThenReplica(ctx context.Context, piTO time.Duration, fn func(base string, timeout time.Duration) error) (usedReplica bool, err error) {
	if c.PiURL == "" {
		return false, ErrNotConfigured
	}
	err = fn(c.PiURL, piTO)
	if err == nil || !isTransport(err) || c.ReplicaURL == "" {
		return false, err
	}
	if rerr := fn(c.ReplicaURL, replicaTimeout); rerr != nil {
		return true, fmt.Errorf("pi unreachable (%v); replica: %w", err, rerr)
	}
	return true, nil
}

// WhoAmI reports who the token authenticates as, asking the Pi and falling
// back to the replica when the Pi is unreachable.
func (c *Client) WhoAmI(ctx context.Context) (Identity, error) {
	var id Identity
	_, err := c.piThenReplica(ctx, piTimeout, func(base string, timeout time.Duration) error {
		id = Identity{}
		return c.do(ctx, base, http.MethodGet, "/api/whoami", nil, &id, timeout)
	})
	return id, err
}
