package cli

import (
	"deepzora/internal/session"
	"strings"
	"testing"
)

func TestSessionPickerLabelPrefersCurrentTitle(t *testing.T) {
	info := session.SessionInfo{Turns: 5, Preview: "first user message here"}
	if got := sessionPickerLabel(info); !strings.Contains(got, info.Preview) {
		t.Fatalf("label %q lost the preview", got)
	}
	info.Title = "My Custom Name"
	for name, format := range map[string]func(session.SessionInfo) string{"picker": sessionPickerLabel, "summary": sessionSummary} {
		got := format(info)
		if !strings.Contains(got, info.Title) || strings.Contains(got, info.Preview) {
			t.Fatalf("%s label %q must prefer the current title", name, got)
		}
	}
}
