package hearth

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const k4BlockJSON = `{"card_state":"off","power_relay":"k4_power"}`

func TestK4PowerOnPostsPowerOn(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/k4/power_on": jsonReply(k4BlockJSON),
	})
	if err := (&Client{PiURL: pi.URL, Token: "tok"}).K4Power(context.Background(), true); err != nil {
		t.Fatalf("K4Power(on): %v", err)
	}
	if got := pi.authHeaders(); len(got) != 1 || got[0] != "Bearer tok" {
		t.Errorf("Authorization headers = %v", got)
	}
}

func TestK4PowerOffPostsPowerOff(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/k4/power_off": jsonReply(k4BlockJSON),
	})
	if err := (&Client{PiURL: pi.URL}).K4Power(context.Background(), false); err != nil {
		t.Fatalf("K4Power(off): %v", err)
	}
}

func TestK4PowerConflictPassesMessageThrough(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/k4/power_on": errorReply(http.StatusConflict, "the K4 is already on"),
	})
	err := (&Client{PiURL: pi.URL}).K4Power(context.Background(), true)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusConflict {
		t.Fatalf("err = %v, want HTTPError 409", err)
	}
	if err.Error() != "the K4 is already on" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestK4PowerPiDownNeverTriesReplica(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/k4/power_on": jsonReply(k4BlockJSON),
	})
	err := (&Client{PiURL: deadURL(t), ReplicaURL: replica.URL}).K4Power(context.Background(), true)
	if !errors.Is(err, ErrPiUnreachable) {
		t.Fatalf("err = %v, want ErrPiUnreachable", err)
	}
	if n := replica.hitCount(); n != 0 {
		t.Errorf("replica called %d times for K4 power", n)
	}
}

func TestK4PowerNotConfigured(t *testing.T) {
	if err := (&Client{}).K4Power(context.Background(), true); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestSetPowerSaveOnPi(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/powersave": jsonReply(`{"enabled":true,"armed":true}`),
	})
	queued, err := (&Client{PiURL: pi.URL}).SetPowerSave(context.Background(), true)
	if err != nil || queued {
		t.Fatalf("SetPowerSave(true) = queued %v, err %v; want not queued, no error", queued, err)
	}
	if got := pi.body("POST /api/powersave"); got != `{"desired":"on"}` {
		t.Errorf("body = %s, want {\"desired\":\"on\"}", got)
	}
}

func TestSetPowerSaveQueuesOnReplicaWhenPiDown(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/powersave": jsonReply(`{"enabled":true,"replica":true,"desired":"off"}`),
	})
	queued, err := (&Client{PiURL: deadURL(t), ReplicaURL: replica.URL}).SetPowerSave(context.Background(), false)
	if err != nil || !queued {
		t.Fatalf("SetPowerSave(false) = queued %v, err %v; want queued", queued, err)
	}
	if got := replica.body("POST /api/powersave"); got != `{"desired":"off"}` {
		t.Errorf("replica body = %s, want {\"desired\":\"off\"}", got)
	}
}

func TestSetPowerSaveForbiddenDoesNotQueue(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/powersave": errorReply(http.StatusForbidden, `operator K1ABC lacks scope "powersave"`),
	})
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/powersave": jsonReply(`{}`),
	})
	queued, err := (&Client{PiURL: pi.URL, ReplicaURL: replica.URL}).SetPowerSave(context.Background(), true)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusForbidden || queued {
		t.Fatalf("queued %v, err %v; want HTTPError 403, not queued", queued, err)
	}
	if n := replica.hitCount(); n != 0 {
		t.Errorf("replica called %d times after a Pi 403", n)
	}
}
