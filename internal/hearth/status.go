package hearth

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Source says where a Status came from.
type Source string

const (
	SourcePi          Source = "pi"          // live, from the cabin Pi
	SourceReplica     Source = "replica"     // the replica's copy of the last push
	SourceUnreachable Source = "unreachable" // no usable status; see Status.Err
)

// K4 is the k4 block of hearth's /api/status.
type K4 struct {
	State      string `json:"card_state"`        // on | off | unreachable | unknown
	FreqHz     int64  `json:"freq_hz,omitempty"` // 0 when hearth has no reading
	Mode       string `json:"mode,omitempty"`
	PowerRelay string `json:"power_relay"` // empty: the rig cannot be powered on remotely
}

// PowerSave is the powersave block of /api/status, plus the replica-only
// fields of the replica's GET /api/powersave.
type PowerSave struct {
	Enabled    bool      `json:"enabled"`
	Armed      bool      `json:"armed"`
	Phase      string    `json:"phase"` // up | down
	NextChange time.Time `json:"next_change,omitzero"`
	NextUpAt   time.Time `json:"next_up_at,omitzero"`

	// Replica only.
	Desired            string    `json:"desired,omitempty"` // queued "on" | "off"
	DesiredBy          string    `json:"desired_by,omitempty"`
	DesiredAt          time.Time `json:"desired_at,omitzero"`
	NextExpectedReport time.Time `json:"next_expected_report,omitzero"`
	OverdueSec         int       `json:"overdue_sec,omitempty"`
	SilentSec          int       `json:"silent_sec,omitempty"`
}

// Status is what the Station panel shows.
type Status struct {
	Source     Source     `json:"source"`
	PushAgeSec int        `json:"push_age_sec,omitempty"` // replica only
	K4         *K4        `json:"k4,omitempty"`
	PowerSave  *PowerSave `json:"powersave,omitempty"`
	Err        string     `json:"error,omitempty"`
}

// statusBlob is the subset of hearth's /api/status that Sparks reads.
type statusBlob struct {
	PushAgeSec        int        `json:"push_age_sec"`
	AwaitingFirstPush bool       `json:"awaiting_first_push"`
	K4                *K4        `json:"k4"`
	PowerSave         *PowerSave `json:"powersave"`
}

// Status reads K4 and power-save state from the Pi, or from the replica's
// last push when the Pi cannot be reached at all. It never returns an error:
// failures are reported in Err with Source unreachable.
func (c *Client) Status(ctx context.Context) Status {
	if c.PiURL == "" {
		return Status{Source: SourceUnreachable, Err: ErrNotConfigured.Error()}
	}
	var blob statusBlob
	err := c.do(ctx, c.PiURL, http.MethodGet, "/api/status", nil, &blob, piTimeout)
	if err == nil {
		return Status{Source: SourcePi, K4: blob.K4, PowerSave: blob.PowerSave}
	}
	if !isTransport(err) || c.ReplicaURL == "" {
		return Status{Source: SourceUnreachable, Err: err.Error()}
	}
	return c.replicaStatus(ctx, err)
}

// replicaStatus reads the pushed blob and the replica's power-save view in
// parallel and merges the replica-only fields into the blob's power-save.
func (c *Client) replicaStatus(ctx context.Context, piErr error) Status {
	var (
		wg             sync.WaitGroup
		blob           statusBlob
		ps             PowerSave
		blobErr, psErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		blobErr = c.do(ctx, c.ReplicaURL, http.MethodGet, "/api/status", nil, &blob, replicaTimeout)
	}()
	go func() {
		defer wg.Done()
		psErr = c.do(ctx, c.ReplicaURL, http.MethodGet, "/api/powersave", nil, &ps, replicaTimeout)
	}()
	wg.Wait()

	if blobErr != nil {
		return Status{Source: SourceUnreachable, Err: fmt.Sprintf("pi unreachable (%v); replica: %v", piErr, blobErr)}
	}
	st := Status{Source: SourceReplica, PushAgeSec: blob.PushAgeSec, K4: blob.K4, PowerSave: blob.PowerSave}
	if blob.AwaitingFirstPush {
		st.Err = "the replica has not heard from the cabin yet"
	}
	if psErr == nil && st.PowerSave != nil {
		st.PowerSave.Desired = ps.Desired
		st.PowerSave.DesiredBy = ps.DesiredBy
		st.PowerSave.DesiredAt = ps.DesiredAt
		st.PowerSave.NextExpectedReport = ps.NextExpectedReport
		st.PowerSave.OverdueSec = ps.OverdueSec
		st.PowerSave.SilentSec = ps.SilentSec
	}
	return st
}
