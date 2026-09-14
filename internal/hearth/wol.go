package hearth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// wolStatusTimeout bounds one GET /api/wol/{name}/status. Hearth answers it by
// running `tailscale status` and then `ping -c 1 -W 2`, which can outlast
// piTimeout.
const wolStatusTimeout = 8 * time.Second

// WolTarget is one hearth wake-on-LAN target and its last-known liveness.
type WolTarget struct {
	Name   string `json:"name"`
	Online *bool  `json:"online"` // nil: unknown
	Source string `json:"source"` // tailscale | ping | none
}

// WolTargets lists hearth's wake-on-LAN targets and reads each one's status in
// parallel. It only ever talks to the Pi: the magic packet is a cabin-LAN
// broadcast and the replica has no WoL. A status that cannot be read leaves
// that target unknown rather than failing the list, because a failed probe is
// never "asleep".
func (c *Client) WolTargets(ctx context.Context) ([]WolTarget, error) {
	if c.PiURL == "" {
		return nil, ErrNotConfigured
	}
	var list struct {
		Targets []struct {
			Name string `json:"name"`
		} `json:"targets"`
	}
	err := c.do(ctx, c.PiURL, http.MethodGet, "/api/wol", nil, &list, piTimeout)
	if isTransport(err) {
		return nil, fmt.Errorf("%w: %v", ErrPiUnreachable, err)
	}
	if err != nil {
		return nil, err
	}

	targets := make([]WolTarget, len(list.Targets))
	var wg sync.WaitGroup
	for i, t := range list.Targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := WolTarget{Name: t.Name, Source: "none"}
			var st struct {
				Online *bool  `json:"online"`
				Source string `json:"source"`
			}
			if c.do(ctx, c.PiURL, http.MethodGet, wolPath(t.Name)+"/status", nil, &st, wolStatusTimeout) == nil {
				target.Online, target.Source = st.Online, st.Source
			}
			targets[i] = target
		}()
	}
	wg.Wait()
	return targets, nil
}

// Wake asks hearth to broadcast the named target's magic packet. Pi only, like
// K4Power. Success means the packet went out, not that the machine woke.
func (c *Client) Wake(ctx context.Context, name string) error {
	if c.PiURL == "" {
		return ErrNotConfigured
	}
	err := c.do(ctx, c.PiURL, http.MethodPost, wolPath(name), nil, nil, piTimeout)
	if isTransport(err) {
		return fmt.Errorf("%w: %v", ErrPiUnreachable, err)
	}
	return err
}

// wolPath is /api/wol/<name>, with the name escaped as one path segment.
func wolPath(name string) string {
	return "/api/wol/" + url.PathEscape(name)
}
