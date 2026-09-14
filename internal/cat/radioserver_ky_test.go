package cat

import (
	"testing"
	"time"
)

// TestRadioServerLongKYIsRTTY: a remote Sparks sends a whole RTTY message as
// one KY (SendRTTYText is unchunked), while CW senders never put more than 24
// characters in a KY. RadioServer must pass longer text to SendRTTYText, which
// doesn't truncate, instead of SendKY, which keeps only the first 24.
func TestRadioServerLongKYIsRTTY(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"24 characters stays CW", "CQ CQ DE NZ1U NZ1U K ABC", "SendKY(CQ CQ DE NZ1U NZ1U K ABC)"},
		{"25 characters is RTTY", "CQ CQ DE NZ1U NZ1U K ABCD", "SendRTTYText(CQ CQ DE NZ1U NZ1U K ABCD)"},
		{"40-character RTTY message", "CQ CQ CQ DE NZ1U NZ1U NZ1U PSE K RYRY 73", "SendRTTYText(CQ CQ CQ DE NZ1U NZ1U NZ1U PSE K RYRY 73)"},
	}

	mock := &mockRadio{}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.reset()
			c.send(t, "KY "+tt.text+";")

			deadline := time.Now().Add(time.Second)
			for len(mock.getCalls()) == 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			got := mock.getCalls()
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("radio calls = %v, want [%s]", got, tt.want)
			}
		})
	}
}
