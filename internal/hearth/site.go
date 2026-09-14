package hearth

import "net/url"

// sitePath adds ?site= to a status or power-save path when Client.Site is set.
// A multi-site hub (the hosted replica) requires it; a single-site Pi ignores
// it. whoami, the K4 routes and the WoL routes never take a site.
func (c *Client) sitePath(path string) string {
	if c.Site == "" {
		return path
	}
	return path + "?" + url.Values{"site": {c.Site}}.Encode()
}
