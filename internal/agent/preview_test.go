package agent

import (
	"testing"

	"deepzora/internal/provider"
)

func TestIsHostRecoveryGuidance(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "tool failed", in: HostRecoveryGuidanceToolFailedPrefix + ", continue unrelated work automatically.", want: true},
		{name: "transient", in: HostRecoveryGuidanceTransientPrefix + " Inspect its current state.", want: true},
		{name: "steer notice prefix", in: "↪ " + HostRecoveryGuidanceToolFailedPrefix + ", continue.", want: true},
		{name: "user quoting failure", in: "A tool failed yesterday, please retry the install.", want: false},
		{name: "user steer", in: "改用 Pillow 10 验证", want: false},
	}
	for _, tc := range cases {
		if got := IsHostRecoveryGuidance(tc.in); got != tc.want {
			t.Errorf("%s: IsHostRecoveryGuidance(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
	if text, ok := VisibleSteerText(MidTurnSteerPrefix + "\n改用 Pillow 10 验证"); !ok || text != "改用 Pillow 10 验证" {
		t.Fatalf("VisibleSteerText user steer = %q %v", text, ok)
	}
	if text, ok := VisibleSteerText(MidTurnSteerPrefix + "\n" + HostRecoveryGuidanceToolFailedPrefix + ", continue."); ok {
		t.Fatalf("VisibleSteerText host recovery = %q, want hidden", text)
	}
}

func TestRawSteerRemainsGuidanceNotAUserTurn(t *testing.T) {
	msg := provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: midTurnSteerMessage("use the smaller patch"), RawContent: "use the smaller patch",
	}
	if IsUserAuthoredTurnMessage(msg) {
		t.Fatal("a real steer with RawContent must not start a new user turn")
	}
}
