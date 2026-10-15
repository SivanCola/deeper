package agent

import (
	"testing"
	"time"
)

// Recovery watermarks intentionally deduplicate identical observation times.
// Instant mock turns can finish inside one Windows wall-clock tick, unlike
// the distinct observations required by the healthy-streak fixtures. Establish
// that precondition before invoking the subject once; do not retry its result.
func waitForDistinctReasoningObservation(t *testing.T) {
	t.Helper()
	start := time.Now()
	for time.Now().UnixNano() <= start.UnixNano() {
		if time.Since(start) >= time.Second {
			t.Fatal("wall clock did not advance for a distinct reasoning observation")
		}
		time.Sleep(time.Millisecond)
	}
}
