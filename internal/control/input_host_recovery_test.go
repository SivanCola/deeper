package control

import (
	"testing"

	"deepzora/internal/agent"
)

func TestIsSyntheticUserMessageHostRecovery(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "plain guidance", input: agent.HostRecoveryGuidanceToolFailedPrefix + ", continue unrelated work automatically."},
		{name: "persisted steer", input: agent.MidTurnSteerPrefix + "\n" + agent.HostRecoveryGuidanceToolFailedPrefix + ", continue unrelated work automatically."},
	}
	for _, tc := range cases {
		if got := IsSyntheticUserMessage(tc.input); got != (tc.name == "plain guidance") {
			t.Errorf("%s: guidance classification must not infer origin from persisted steer text", tc.name)
		}
	}
}
