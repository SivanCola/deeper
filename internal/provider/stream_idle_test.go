package provider

import (
	"testing"
	"time"
)

func TestDefaultStreamIdleTimeoutIsFiveMinutes(t *testing.T) {
	if DefaultStreamIdleTimeout != 5*time.Minute {
		t.Fatalf("DefaultStreamIdleTimeout = %s, want 5m", DefaultStreamIdleTimeout)
	}
}
