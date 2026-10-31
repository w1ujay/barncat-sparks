//go:build windows

package midi

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLearnOnceDeviceMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := LearnOnce(ctx, []string{"no such controller"})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("LearnOnce = %v, want ErrNotConnected", err)
	}
}
