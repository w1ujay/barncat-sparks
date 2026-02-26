package lp100a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Data
	}{
		{
			name:  "normal response",
			input: ";100.5,50.2,3.1,50.0,1.2,3,2,1,W3LPL",
			want: Data{
				Power: 100.5, Impedance: 50.2, Phase: 3.1,
				PowerDBm: 50.0, SWR: 1.2,
				SWRAlarmPoint: 3, PowerRange: 2, PeakHoldMode: 1,
				Callsign: "W3LPL",
			},
		},
		{
			name:  "zero values",
			input: "0,0,0,0,0,0,0,0,N0CALL",
			want: Data{
				Callsign: "N0CALL",
			},
		},
		{
			name:  "whitespace trimming",
			input: " ; 100.5 , 50.2 , 3.1 , 50.0 , 1.2 , 3 , 2 , 1 , W3LPL ",
			want: Data{
				Power: 100.5, Impedance: 50.2, Phase: 3.1,
				PowerDBm: 50.0, SWR: 1.2,
				SWRAlarmPoint: 3, PowerRange: 2, PeakHoldMode: 1,
				Callsign: "W3LPL",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseResponse(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Power != tt.want.Power {
				t.Errorf("Power: got %v, want %v", got.Power, tt.want.Power)
			}
			if got.Impedance != tt.want.Impedance {
				t.Errorf("Impedance: got %v, want %v", got.Impedance, tt.want.Impedance)
			}
			if got.Phase != tt.want.Phase {
				t.Errorf("Phase: got %v, want %v", got.Phase, tt.want.Phase)
			}
			if got.PowerDBm != tt.want.PowerDBm {
				t.Errorf("PowerDBm: got %v, want %v", got.PowerDBm, tt.want.PowerDBm)
			}
			if got.SWR != tt.want.SWR {
				t.Errorf("SWR: got %v, want %v", got.SWR, tt.want.SWR)
			}
			if got.SWRAlarmPoint != tt.want.SWRAlarmPoint {
				t.Errorf("SWRAlarmPoint: got %v, want %v", got.SWRAlarmPoint, tt.want.SWRAlarmPoint)
			}
			if got.PowerRange != tt.want.PowerRange {
				t.Errorf("PowerRange: got %v, want %v", got.PowerRange, tt.want.PowerRange)
			}
			if got.PeakHoldMode != tt.want.PeakHoldMode {
				t.Errorf("PeakHoldMode: got %v, want %v", got.PeakHoldMode, tt.want.PeakHoldMode)
			}
			if got.Callsign != tt.want.Callsign {
				t.Errorf("Callsign: got %q, want %q", got.Callsign, tt.want.Callsign)
			}
		})
	}
}

func TestParseResponseErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"only semicolons", ";;;"},
		{"too few fields", "100,50,3"},
		{"bad power", "abc,50,3,50,1.2,3,2,1,CALL"},
		{"bad impedance", "100,abc,3,50,1.2,3,2,1,CALL"},
		{"bad phase", "100,50,abc,50,1.2,3,2,1,CALL"},
		{"bad dbm", "100,50,3,abc,1.2,3,2,1,CALL"},
		{"bad swr", "100,50,3,50,abc,3,2,1,CALL"},
		{"bad alarm", "100,50,3,50,1.2,abc,2,1,CALL"},
		{"bad range", "100,50,3,50,1.2,3,abc,1,CALL"},
		{"bad peakMode", "100,50,3,50,1.2,3,2,abc,CALL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseResponse(tt.input)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestPeakSWRTimeout(t *testing.T) {
	d := &Driver{
		portName: "/dev/null",
		state:    State{PortName: "/dev/null", Connected: true},
	}

	// Set peak SWR as if it happened in the past
	d.peakSWR = 2.5
	d.peakSWRTime = time.Now().Add(-5 * time.Second) // 5s ago, exceeds 4s timeout
	d.state.Data.PeakSWR = 2.5

	state := d.GetState()
	if state.Data.PeakSWR != 0 {
		t.Errorf("PeakSWR should be 0 after timeout, got %v", state.Data.PeakSWR)
	}

	// Set peak SWR as if it happened recently
	d.mu.Lock()
	d.peakSWR = 3.0
	d.peakSWRTime = time.Now()
	d.state.Data.PeakSWR = 3.0
	d.mu.Unlock()

	state = d.GetState()
	if state.Data.PeakSWR != 3.0 {
		t.Errorf("PeakSWR should be 3.0 within timeout, got %v", state.Data.PeakSWR)
	}
}

func TestHTTPGet(t *testing.T) {
	d := &Driver{
		portName: "/dev/ttyUSB0",
		state: State{
			PortName:  "/dev/ttyUSB0",
			Connected: true,
			Data: Data{
				Power: 100.0,
				SWR:   1.5,
			},
		},
	}

	handler := d.HTTPHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/lp100a", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}

	var state State
	if err := json.NewDecoder(rec.Body).Decode(&state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state.PortName != "/dev/ttyUSB0" {
		t.Errorf("PortName: got %q, want /dev/ttyUSB0", state.PortName)
	}
	if !state.Connected {
		t.Error("Connected should be true")
	}
	if state.Data.Power != 100.0 {
		t.Errorf("Power: got %v, want 100.0", state.Data.Power)
	}
	if state.Data.SWR != 1.5 {
		t.Errorf("SWR: got %v, want 1.5", state.Data.SWR)
	}
}

func TestHTTPCommand(t *testing.T) {
	d := &Driver{
		portName: "/dev/ttyUSB0",
		state:    State{PortName: "/dev/ttyUSB0"},
	}

	handler := d.HTTPHandler()

	tests := []struct {
		name    string
		body    string
		wantOK  bool
		wantMsg string
	}{
		{
			name:    "reset_peak",
			body:    `{"command":"reset_peak"}`,
			wantOK:  true,
			wantMsg: "peak SWR reset",
		},
		{
			name:    "unknown command",
			body:    `{"command":"bogus"}`,
			wantOK:  false,
			wantMsg: "unknown command: bogus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/lp100a/command",
				strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200", rec.Code)
			}

			var resp struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Success != tt.wantOK {
				t.Errorf("success: got %v, want %v", resp.Success, tt.wantOK)
			}
			if resp.Message != tt.wantMsg {
				t.Errorf("message: got %q, want %q", resp.Message, tt.wantMsg)
			}
		})
	}
}

func TestHTTPMethodNotAllowed(t *testing.T) {
	d := &Driver{portName: "/dev/null", state: State{PortName: "/dev/null"}}
	handler := d.HTTPHandler()

	// POST to GET-only endpoint
	req := httptest.NewRequest(http.MethodPost, "/api/lp100a", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/lp100a: got %d, want 405", rec.Code)
	}

	// GET to POST-only endpoint
	req = httptest.NewRequest(http.MethodGet, "/api/lp100a/command", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/lp100a/command: got %d, want 405", rec.Code)
	}
}
