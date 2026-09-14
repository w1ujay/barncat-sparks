package hearth

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeHearth is an httptest server that records every request and serves
// canned responses keyed by "METHOD /path".
type fakeHearth struct {
	*httptest.Server
	mu     sync.Mutex
	hits   []string
	auth   []string
	bodies map[string]string
}

func newFakeHearth(t *testing.T, routes map[string]http.HandlerFunc) *fakeHearth {
	t.Helper()
	f := &fakeHearth{bodies: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.hits = append(f.hits, key)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.bodies[key] = string(body)
		f.mu.Unlock()
		if h, ok := routes[key]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeHearth) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

// sawHit reports whether key ("METHOD /path") was ever recorded.
func (f *fakeHearth) sawHit(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.hits {
		if h == key {
			return true
		}
	}
	return false
}

func (f *fakeHearth) authHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

func (f *fakeHearth) body(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func jsonReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

func errorReply(status int, msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, msg, status)
	}
}

// hijackReply hijacks the connection and closes it without writing a
// response, simulating a transport-level failure (a dropped connection)
// rather than an HTTP error status.
func hijackReply() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		conn.Close()
	}
}

// redirectReply answers with a 3xx and a Location header.
func redirectReply(status int, location string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", location)
		w.WriteHeader(status)
	}
}

// deadURL returns an http:// URL that nothing is listening on.
func deadURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}
