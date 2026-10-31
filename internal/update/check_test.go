package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"0.3.2", "0.3.3", true},
		{"0.3.2", "0.4.0", true},
		{"0.9.0", "1.0.0", true},
		{"0.9.9", "0.10.0", true}, // numeric, not string, order
		{"0.3.2", "0.3.2", false},
		{"0.3.3", "0.3.2", false},
		// A pre-release sorts below its own release, and above the one before.
		{"0.4.0-rc.2", "0.4.0", true},
		{"0.4.0-rc.2", "0.3.2", false},
		{"0.4.0", "0.4.0-rc.2", false},
		{"0.4.0-rc.2", "0.4.0-rc.10", true},
		// A version we can't read never offers an update.
		{"dev", "0.4.0", false},
		{"0.4.0", "garbage", false},
		{"0.4", "0.5", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if got := Newer(tt.current, tt.latest); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

// apiServer serves one fixed reply as the releases/latest endpoint.
func apiServer(t *testing.T, code int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+Repo+"/releases/latest" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("GitHub rejects requests with no User-Agent")
		}
		rw.WriteHeader(code)
		rw.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), APIBase: srv.URL}
}

func TestLatest(t *testing.T) {
	c := apiServer(t, http.StatusOK, `{
		"tag_name": "v0.5.0", "html_url": "https://github.com/w1ujay/barncat-sparks/releases/tag/v0.5.0",
		"prerelease": false, "draft": false,
		"assets": [{"name": "checksums.txt", "browser_download_url": "https://example.test/checksums.txt"}]
	}`)
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.5.0" {
		t.Errorf("Version = %q, want 0.5.0 with no leading v", rel.Version)
	}
	if rel.URL != "https://github.com/w1ujay/barncat-sparks/releases/tag/v0.5.0" {
		t.Errorf("URL = %q", rel.URL)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != "checksums.txt" || rel.Assets[0].URL != "https://example.test/checksums.txt" {
		t.Errorf("Assets = %+v", rel.Assets)
	}
}

func TestLatestRefusesWhatItShouldNotOffer(t *testing.T) {
	const page = `"html_url": "https://github.com/w1ujay/barncat-sparks/releases/tag/v0.5.0"`
	tests := []struct {
		name string
		code int
		body string
	}{
		{"pre-release flag", 200, `{"tag_name": "v0.5.0", ` + page + `, "prerelease": true}`},
		{"pre-release tag", 200, `{"tag_name": "v0.5.0-rc.1", ` + page + `}`},
		{"draft", 200, `{"tag_name": "v0.5.0", ` + page + `, "draft": true}`},
		{"no tag", 200, `{` + page + `}`},
		{"link off the repo", 200, `{"tag_name": "v0.5.0", "html_url": "https://evil.example/x"}`},
		{"not json", 200, `<html>`},
		{"no releases", 404, `{"message": "Not Found"}`},
		{"rate limited", 403, `{"message": "rate limit"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rel, err := apiServer(t, tt.code, tt.body).Latest(context.Background()); err == nil {
				t.Fatalf("got %+v, want an error", rel)
			}
		})
	}
}

func TestLatestWithNoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // nothing is listening now
	c := &Client{HTTP: http.DefaultClient, APIBase: base}
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("want an error")
	}
}
