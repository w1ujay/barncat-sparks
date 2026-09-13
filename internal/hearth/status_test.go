package hearth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// piStatusJSON is a trimmed /api/status as hearth serves it on the Pi.
const piStatusJSON = `{
 "k4":{"card_state":"on","reachable":true,"on":true,"stale":false,"armed":false,
       "idle_sec":0,"shutoff_after_sec":0,"power_relay":"k4_power",
       "freq_hz":14074000,"mode":"DATA","vfo":"A","tx":false,"reading_age_sec":3},
 "powersave":{"enabled":true,"armed":true,"phase":"up","window":"day",
       "next_change":"2026-09-13T14:15:00Z","next_up_at":"2026-09-13T16:00:00Z",
       "schedule_summary":"night 22:00-06:00","relay":"starlink_mains",
       "actual_at":"2026-09-13T08:00:00Z"}
}`

// replicaStatusJSON is the pushed blob as the replica serves it.
const replicaStatusJSON = `{"replica":true,"push_age_sec":2520,
 "k4":{"card_state":"unreachable","power_relay":"k4_power"},
 "powersave":{"enabled":true,"armed":true,"phase":"down",
       "next_change":"2026-09-13T22:00:00Z","next_up_at":"2026-09-13T16:00:00Z"}}`

const replicaPowerSaveJSON = `{"enabled":true,"replica":true,
 "desired":"off","desired_at":"2026-09-13T15:10:00Z","desired_by":"K1ABC",
 "next_expected_report":"2026-09-13T16:05:00Z","overdue_sec":0}`

func TestStatusFromPi(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status": jsonReply(piStatusJSON),
	})
	st := (&Client{PiURL: pi.URL}).Status(context.Background())

	if st.Source != SourcePi || st.Err != "" {
		t.Fatalf("source=%q err=%q, want pi and no error", st.Source, st.Err)
	}
	if st.K4 == nil || st.K4.State != "on" || st.K4.FreqHz != 14074000 || st.K4.Mode != "DATA" || st.K4.PowerRelay != "k4_power" {
		t.Errorf("k4 = %+v", st.K4)
	}
	if st.PowerSave == nil || !st.PowerSave.Enabled || !st.PowerSave.Armed || st.PowerSave.Phase != "up" {
		t.Fatalf("powersave = %+v", st.PowerSave)
	}
	if want := time.Date(2026, 9, 13, 16, 0, 0, 0, time.UTC); !st.PowerSave.NextUpAt.Equal(want) {
		t.Errorf("next_up_at = %v, want %v", st.PowerSave.NextUpAt, want)
	}
}

func TestStatusFallsBackToReplica(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status":    jsonReply(replicaStatusJSON),
		"GET /api/powersave": jsonReply(replicaPowerSaveJSON),
	})
	st := (&Client{PiURL: deadURL(t), ReplicaURL: replica.URL}).Status(context.Background())

	if st.Source != SourceReplica {
		t.Fatalf("source = %q (err %q), want replica", st.Source, st.Err)
	}
	if st.PushAgeSec != 2520 {
		t.Errorf("push_age_sec = %d, want 2520", st.PushAgeSec)
	}
	ps := st.PowerSave
	if ps == nil || !ps.Armed || ps.Phase != "down" {
		t.Fatalf("powersave = %+v, want armed/down from the pushed blob", ps)
	}
	if ps.Desired != "off" || ps.DesiredBy != "K1ABC" || ps.DesiredAt.IsZero() || ps.NextExpectedReport.IsZero() {
		t.Errorf("replica fields not merged: %+v", ps)
	}
}

func TestStatusPiHTTPErrorDoesNotFallBack(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status": errorReply(http.StatusInternalServerError, "boom"),
	})
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status": jsonReply(replicaStatusJSON),
	})
	st := (&Client{PiURL: pi.URL, ReplicaURL: replica.URL}).Status(context.Background())

	if st.Source != SourceUnreachable || !strings.Contains(st.Err, "boom") {
		t.Errorf("source=%q err=%q, want unreachable with hearth's message", st.Source, st.Err)
	}
	if n := replica.hitCount(); n != 0 {
		t.Errorf("replica called %d times after a Pi HTTP error", n)
	}
}

func TestStatusBothDown(t *testing.T) {
	st := (&Client{PiURL: deadURL(t), ReplicaURL: deadURL(t)}).Status(context.Background())
	if st.Source != SourceUnreachable || !strings.Contains(st.Err, "replica") {
		t.Errorf("source=%q err=%q, want unreachable mentioning the replica", st.Source, st.Err)
	}
}

func TestStatusReplicaAwaitingFirstPush(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status":    jsonReply(`{"replica":true,"awaiting_first_push":true}`),
		"GET /api/powersave": jsonReply(`{"enabled":true,"replica":true}`),
	})
	st := (&Client{PiURL: deadURL(t), ReplicaURL: replica.URL}).Status(context.Background())
	if st.Source != SourceReplica || st.Err == "" || st.K4 != nil {
		t.Errorf("status = %+v, want replica source with an explanatory error and no k4", st)
	}
}

func TestStatusNotConfigured(t *testing.T) {
	st := (&Client{}).Status(context.Background())
	if st.Source != SourceUnreachable || !strings.Contains(st.Err, "hearth_url") {
		t.Errorf("status = %+v, want unreachable naming hearth_url", st)
	}
}
