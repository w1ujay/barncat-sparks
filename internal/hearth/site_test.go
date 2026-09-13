package hearth

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"testing"
)

// hubOnly mimics a multi-site hub such as the hosted replica: without
// ?site=<want> it answers 400 "site required".
func hubOnly(want string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("site") != want {
			http.Error(w, "site required: ?site=cabin|k3-site", http.StatusBadRequest)
			return
		}
		next(w, r)
	}
}

func TestStatusSendsSiteToHubReplica(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status":    hubOnly("cabin", jsonReply(replicaStatusJSON)),
		"GET /api/powersave": hubOnly("cabin", jsonReply(replicaPowerSaveJSON)),
	})
	st := (&Client{PiURL: deadURL(t), ReplicaURL: replica.URL, Site: "cabin"}).Status(context.Background())

	if st.Source != SourceReplica || st.Err != "" {
		t.Fatalf("source=%q err=%q, want replica with no error", st.Source, st.Err)
	}
	if st.PowerSave == nil || st.PowerSave.Desired != "off" {
		t.Errorf("replica power-save not merged: %+v", st.PowerSave)
	}
}

func TestStatusSendsSiteToPi(t *testing.T) {
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status": hubOnly("cabin", jsonReply(piStatusJSON)),
	})
	st := (&Client{PiURL: pi.URL, Site: "cabin"}).Status(context.Background())

	if st.Source != SourcePi {
		t.Fatalf("source=%q err=%q, want pi", st.Source, st.Err)
	}
}

func TestSetPowerSaveSendsSiteToHubReplica(t *testing.T) {
	replica := newFakeHearth(t, map[string]http.HandlerFunc{
		"POST /api/powersave": hubOnly("cabin", jsonReply(`{"enabled":true,"replica":true,"desired":"on"}`)),
	})
	queued, err := (&Client{PiURL: deadURL(t), ReplicaURL: replica.URL, Site: "cabin"}).SetPowerSave(context.Background(), true)

	if err != nil || !queued {
		t.Fatalf("SetPowerSave = queued %v, err %v; want queued on the hub", queued, err)
	}
}

// TestSiteQueryEscapedOmittedAndNotOnWhoAmI: an empty site sends no query, a
// site is URL-escaped, and whoami never carries one.
func TestSiteQueryEscapedOmittedAndNotOnWhoAmI(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	record := func(label string, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, label+"?"+r.URL.RawQuery)
			mu.Unlock()
			next(w, r)
		}
	}
	pi := newFakeHearth(t, map[string]http.HandlerFunc{
		"GET /api/status": record("status", jsonReply(piStatusJSON)),
		"GET /api/whoami": record("whoami", jsonReply(operatorJSON)),
	})

	(&Client{PiURL: pi.URL}).Status(context.Background())
	(&Client{PiURL: pi.URL, Site: "a b&c"}).Status(context.Background())
	if _, err := (&Client{PiURL: pi.URL, Site: "cabin"}).WhoAmI(context.Background()); err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"status?", "status?site=a+b%26c", "whoami?"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}
