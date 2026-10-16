package main

import "testing"

func TestNormalizeTopicStatusStillRejectsUnknown(t *testing.T) {
	if got := normalizeTopicStatus("not_a_status"); got != "" {
		t.Fatalf("normalizeTopicStatus(unknown) = %q, want empty", got)
	}
}
