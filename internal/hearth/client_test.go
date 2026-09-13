package hearth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const operatorJSON = `{"name":"K1ABC","owner":false,"scopes":["k4","powersave"],"replica":false}`

func TestWhoAmIFromPi(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/whoami": jsonReply(operatorJSON),
	})
	c := &Client{PiURL: pi.URL, Token: "tok"}

	id, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if id.Name != "K1ABC" || id.Owner || id.Replica {
		t.Errorf("identity = %+v", id)
	}
	if len(id.Scopes) != 2 || id.Scopes[0] != "k4" || id.Scopes[1] != "powersave" {
		t.Errorf("scopes = %v", id.Scopes)
	}
	if got := pi.authHeaders(); len(got) != 1 || got[0] != "Bearer tok" {
		t.Errorf("Authorization headers = %v, want [Bearer tok]", got)
	}
}

func TestWhoAmIUnauthorizedDoesNotFallBack(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/whoami": errorReply(http.StatusUnauthorized, "unauthorized"),
	})
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/whoami": jsonReply(operatorJSON),
	})
	c := &Client{PiURL: pi.URL, ReplicaURL: replica.URL, Token: "bad"}

	_, err := c.WhoAmI(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want HTTPError 401", err)
	}
	if err.Error() != "token rejected" {
		t.Errorf("Error() = %q, want %q", err.Error(), "token rejected")
	}
	if n := replica.hitCount(); n != 0 {
		t.Errorf("replica called %d times after a Pi HTTP error", n)
	}
}

func TestWhoAmIFallsBackToReplicaWhenPiDown(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/whoami": jsonReply(`{"name":"K1ABC","owner":false,"scopes":["powersave"],"replica":true}`),
	})
	c := &Client{PiURL: deadURL(t), ReplicaURL: replica.URL, Token: "tok"}

	id, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if !id.Replica || id.Name != "K1ABC" {
		t.Errorf("identity = %+v, want replica K1ABC", id)
	}
}

func TestWhoAmINotConfigured(t *testing.T) {
	_, err := (&Client{}).WhoAmI(context.Background())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestWhoAmIRedirectNamesLocation(t *testing.T) {
	const location = "https://example.invalid/api/whoami"
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/whoami": redirectReply(http.StatusFound, location),
	})
	c := &Client{PiURL: pi.URL, Token: "tok"}

	_, err := c.WhoAmI(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusFound {
		t.Fatalf("err = %v, want HTTPError 302", err)
	}
	if !strings.Contains(err.Error(), location) {
		t.Errorf("Error() = %q, want it to name %s", err.Error(), location)
	}
}

func TestURLWithoutSchemeIsNotATransportFailure(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/whoami": jsonReply(operatorJSON),
	})
	c := &Client{PiURL: "hearth.example:8123", ReplicaURL: replica.URL}

	_, err := c.WhoAmI(context.Background())
	if err == nil || !strings.Contains(err.Error(), "http://") {
		t.Fatalf("err = %v, want a scheme error", err)
	}
	if n := replica.hitCount(); n != 0 {
		t.Errorf("replica called %d times for a misconfigured Pi URL", n)
	}
}
