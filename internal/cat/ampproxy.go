package cat

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// newAmpProxy returns a reverse proxy handler that forwards all requests to
// the given amplifier base URL. Used by RadioServer to proxy RF-Kit amp HTTP
// API requests through the same TCP port as CAT commands.
func newAmpProxy(targetURL string) (http.Handler, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	return httputil.NewSingleHostReverseProxy(u), nil
}
