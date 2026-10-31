// Package update finds the latest GitHub release of Sparks and installs it.
// It imports only the standard library.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Repo is the GitHub repository releases come from.
const Repo = "w1ujay/barncat-sparks"

const (
	defaultAPIBase = "https://api.github.com"
	maxAPIBody     = 2 << 20
)

// Client talks to the GitHub releases API. The zero value uses the real API.
type Client struct {
	HTTP    *http.Client // nil means http.DefaultClient
	APIBase string       // "" means api.github.com
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	URL  string
}

// Release is a published, stable release.
type Release struct {
	Version string // no leading v
	URL     string // the release's page
	Assets  []Asset
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "barncat-sparks") // GitHub rejects requests without one
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

// Latest returns the newest stable release. GitHub's releases/latest already
// skips drafts and pre-releases; they are refused here too, since an update
// must never come from either.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	base := c.APIBase
	if base == "" {
		base = defaultAPIBase
	}
	resp, err := c.get(ctx, base+"/repos/"+Repo+"/releases/latest")
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	var body struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBody)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("reading release: %w", err)
	}
	version := strings.TrimPrefix(body.TagName, "v")
	if _, ok := parseVersion(version); !ok {
		return Release{}, fmt.Errorf("release tag %q is not a version", body.TagName)
	}
	if body.Prerelease || body.Draft || strings.Contains(version, "-") {
		return Release{}, errors.New("latest release is not a stable release")
	}
	// The page opens this link in the browser, so it must stay on the repo.
	if !strings.HasPrefix(body.HTMLURL, "https://github.com/"+Repo+"/") {
		return Release{}, fmt.Errorf("release link %q is not on %s", body.HTMLURL, Repo)
	}

	rel := Release{Version: version, URL: body.HTMLURL}
	for _, a := range body.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL})
	}
	return rel, nil
}

// version is a parsed major.minor.patch[-prerelease].
type version struct {
	core [3]int
	pre  []string
}

func parseVersion(s string) (version, bool) {
	coreText, preText, hasPre := strings.Cut(s, "-")
	parts := strings.Split(coreText, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(preText, ".")
	}
	return v, true
}

// less orders two versions by semver precedence.
func (a version) less(b version) bool {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			return a.core[i] < b.core[i]
		}
	}
	switch {
	case len(a.pre) == 0:
		return false // a release is never below its own pre-release
	case len(b.pre) == 0:
		return true
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if a.pre[i] == b.pre[i] {
			continue
		}
		an, aErr := strconv.Atoi(a.pre[i])
		bn, bErr := strconv.Atoi(b.pre[i])
		switch {
		case aErr == nil && bErr == nil:
			return an < bn
		case aErr == nil:
			return true // numeric identifiers sort below text
		case bErr == nil:
			return false
		}
		return a.pre[i] < b.pre[i]
	}
	return len(a.pre) < len(b.pre)
}

// Newer reports whether latest is a newer version than current. A version
// that can't be read is never newer, so a dev build isn't offered an update.
func Newer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	return c.less(l)
}
