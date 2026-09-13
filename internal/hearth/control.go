package hearth

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// K4Power powers the cabin K4 on (hearth pulses its power relay) or off
// (hearth sends PS0;). It only ever talks to the Pi: the replica has no path
// to the rig, and with the Pi unreachable the rig's CAT port is too.
func (c *Client) K4Power(ctx context.Context, on bool) error {
	if c.PiURL == "" {
		return ErrNotConfigured
	}
	path := "/api/k4/power_off"
	if on {
		path = "/api/k4/power_on"
	}
	err := c.do(ctx, c.PiURL, http.MethodPost, path, nil, nil, controlTimeout)
	if isTransport(err) {
		return fmt.Errorf("%w: %v", ErrPiUnreachable, err)
	}
	return err
}

// SetPowerSave arms or disarms Starlink power-save. When the Pi is
// unreachable the request is parked on the replica, which the Pi collects at
// its next up window; queued reports that.
func (c *Client) SetPowerSave(ctx context.Context, armed bool) (queued bool, err error) {
	body := map[string]string{"desired": "off"}
	if armed {
		body["desired"] = "on"
	}
	return c.piThenReplica(ctx, controlTimeout, func(base string, timeout time.Duration) error {
		return c.do(ctx, base, http.MethodPost, c.sitePath("/api/powersave"), body, nil, timeout)
	})
}
