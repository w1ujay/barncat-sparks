package hearth

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
)

// onlineString renders a WolTarget.Online for comparison: "true", "false" or "nil".
func onlineString(b *bool) string {
	if b == nil {
		return "nil"
	}
	if *b {
		return "true"
	}
	return "false"
}

func describeTargets(ts []WolTarget) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Name+" "+onlineString(t.Online)+" "+t.Source)
	}
	return out
}

func TestWolTargetsReadsEachStatus(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/wol":                    jsonReply(`{"targets":[{"name":"awake-box"},{"name":"asleep-box"},{"name":"unknown-box"}]}`),
		"GET /api/wol/awake-box/status":   jsonReply(`{"online":true,"source":"tailscale"}`),
		"GET /api/wol/asleep-box/status":  jsonReply(`{"online":false,"source":"ping"}`),
		"GET /api/wol/unknown-box/status": jsonReply(`{"online":null,"source":"none"}`),
	})
	got, err := (&Client{PiURL: pi.URL, Token: "tok"}).WolTargets(context.Background())
	if err != nil {
		t.Fatalf("WolTargets: %v", err)
	}
	want := []string{"awake-box true tailscale", "asleep-box false ping", "unknown-box nil none"}
	if d := describeTargets(got); !reflect.DeepEqual(d, want) {
		t.Errorf("targets = %q, want %q", d, want)
	}
	for _, h := range pi.authHeaders() {
		if h != "Bearer tok" {
			t.Errorf("Authorization = %q, want Bearer tok on every request", h)
		}
	}
}

func TestWolTargetsEmptyListMakesNoStatusCalls(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/wol": jsonReply(`{"targets":[]}`),
	})
	got, err := (&Client{PiURL: pi.URL}).WolTargets(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("WolTargets = %v, %v; want an empty non-nil slice", got, err)
	}
	if n := pi.hitCount(); n != 1 {
		t.Errorf("hearth saw %d requests, want only GET /api/wol", n)
	}
}

// A status that can't be read (404, 500) leaves that target unknown and never
// fails the list or the other targets.
func TestWolTargetsFailedStatusIsUnknown(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/wol":                   jsonReply(`{"targets":[{"name":"gone-box"},{"name":"broken-box"},{"name":"good-box"}]}`),
		"GET /api/wol/broken-box/status": errorReply(http.StatusInternalServerError, "tailscale exploded"),
		"GET /api/wol/good-box/status":   jsonReply(`{"online":true,"source":"ping"}`),
		// gone-box/status is unrouted: fakeHearth answers 404.
	})
	got, err := (&Client{PiURL: pi.URL}).WolTargets(context.Background())
	if err != nil {
		t.Fatalf("WolTargets: %v", err)
	}
	want := []string{"gone-box nil none", "broken-box nil none", "good-box true ping"}
	if d := describeTargets(got); !reflect.DeepEqual(d, want) {
		t.Errorf("targets = %q, want %q", d, want)
	}
}

// A status call that fails at the transport level (a dropped connection, not
// merely an HTTP error status) also leaves that target unknown, and the call
// as a whole still succeeds — same contract as the 404/500 cases above.
func TestWolTargetsTransportFailureStatusIsUnknown(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/wol":                   jsonReply(`{"targets":[{"name":"broken-box"},{"name":"good-box"}]}`),
		"GET /api/wol/broken-box/status": hijackReply(),
		"GET /api/wol/good-box/status":   jsonReply(`{"online":true,"source":"ping"}`),
	})
	got, err := (&Client{PiURL: pi.URL}).WolTargets(context.Background())
	if err != nil {
		t.Fatalf("WolTargets: %v", err)
	}
	want := []string{"broken-box nil none", "good-box true ping"}
	if d := describeTargets(got); !reflect.DeepEqual(d, want) {
		t.Errorf("targets = %q, want %q", d, want)
	}
}

func TestWolTargetsListErrorIsReturned(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/wol": errorReply(http.StatusUnauthorized, "unauthorized"),
	})
	_, err := (&Client{PiURL: pi.URL}).WolTargets(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want HTTPError 401", err)
	}
}

func TestWolPiDownNeverTriesReplica(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/wol":                  jsonReply(`{"targets":[]}`),
		"POST /api/wol/shack-laptop": jsonReply(`{"sent":true}`),
	})
	c := &Client{PiURL: deadURL(t), ReplicaURL: replica.URL}
	if _, err := c.WolTargets(context.Background()); !errors.Is(err, ErrPiUnreachable) {
		t.Errorf("WolTargets err = %v, want ErrPiUnreachable", err)
	}
	if err := c.Wake(context.Background(), "shack-laptop"); !errors.Is(err, ErrPiUnreachable) {
		t.Errorf("Wake err = %v, want ErrPiUnreachable", err)
	}
	if n := replica.hitCount(); n != 0 {
		t.Errorf("replica called %d times for WoL", n)
	}
}

func TestWolNotConfigured(t *testing.T) {
	if _, err := (&Client{}).WolTargets(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("WolTargets err = %v, want ErrNotConfigured", err)
	}
	if err := (&Client{}).Wake(context.Background(), "x"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Wake err = %v, want ErrNotConfigured", err)
	}
}

func TestWakePostsEscapedName(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		// fakeHearth keys on the decoded path; the recorder checks the wire form.
		"POST /api/wol/shack laptop": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.EscapedPath())
			mu.Unlock()
			jsonReply(`{"sent":true,"name":"shack laptop"}`)(w, r)
		},
	})
	if err := (&Client{PiURL: pi.URL, Token: "tok"}).Wake(context.Background(), "shack laptop"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"/api/wol/shack%20laptop"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %q, want %q", paths, want)
	}
	if got := pi.authHeaders(); len(got) != 1 || got[0] != "Bearer tok" {
		t.Errorf("Authorization headers = %v", got)
	}
}

func TestWakeForbiddenPassesThrough(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/wol/shack-laptop": errorReply(http.StatusForbidden, `operator sparks-test lacks scope "wol"`),
	})
	err := (&Client{PiURL: pi.URL}).Wake(context.Background(), "shack-laptop")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want HTTPError 403", err)
	}
	if want := `not permitted: operator sparks-test lacks scope "wol"`; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// WoL lives on the Pi alone, so no WoL request carries ?site=, even with a
// hub site configured.
func TestWolCallsNeverSendSite(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	record := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			queries = append(queries, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
			mu.Unlock()
			next(w, r)
		}
	}
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/wol":                        record(jsonReply(`{"targets":[{"name":"shack-laptop"}]}`)),
		"GET /api/wol/shack-laptop/status": record(jsonReply(`{"online":false,"source":"ping"}`)),
		"POST /api/wol/shack-laptop":       record(jsonReply(`{"sent":true}`)),
	})
	c := &Client{PiURL: pi.URL, Site: "cabin"}
	if _, err := c.WolTargets(context.Background()); err != nil {
		t.Fatalf("WolTargets: %v", err)
	}
	if err := c.Wake(context.Background(), "shack-laptop"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"GET /api/wol?",
		"GET /api/wol/shack-laptop/status?",
		"POST /api/wol/shack-laptop?",
	}
	if !reflect.DeepEqual(queries, want) {
		t.Errorf("requests = %q, want %q", queries, want)
	}
}
