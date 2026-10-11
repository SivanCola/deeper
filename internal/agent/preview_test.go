package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"deepzora/internal/provider"
)

// realContract mirrors the shape compileExecutionContract emits: the
// source_event lives under planner_ir, and the block replaces the whole user
// turn.
func realContract(sourceEvent string) string {
	return "<memory-compiler-execution>\n" +
		`{"type":"memory_v5_execution_contract","instruction":"Execute source_event through planner_ir.",` +
		`"ir_explanation":{},"planner_ir":{"version":5,"goal":"g","source_event":` + jsonString(sourceEvent) + `}}` +
		"\n</memory-compiler-execution>"
}

func jsonString(s string) string {
	// minimal JSON string quoting for test fixtures (no control chars used here)
	return `"` + s + `"`
}

func assertNoContractLeak(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, "<memory-compiler-execution>") || strings.Contains(got, "</memory-compiler-execution>") {
		t.Fatalf("preview leaked a contract tag (raw JSON shown to the user):\n%q", got)
	}
	if strings.Contains(got, "planner_ir") || strings.Contains(got, "memory_v5_execution_contract") {
		t.Fatalf("preview leaked contract JSON:\n%q", got)
	}
	if !strings.Contains(got, want) {
		t.Fatalf("preview lost the user's actual text %q, got:\n%q", want, got)
	}
}

// mcContract builds a <memory-compiler-execution> block whose
// planner_ir.source_event is the given text, matching the real contract shape.
func mcContract(t *testing.T, sourceEvent string) string {
	t.Helper()
	body, err := json.Marshal(struct {
		Type      string `json:"type"`
		PlannerIR struct {
			Version     int    `json:"version"`
			SourceEvent string `json:"source_event"`
		} `json:"planner_ir"`
	}{Type: "memory_v5_execution_contract", PlannerIR: struct {
		Version     int    `json:"version"`
		SourceEvent string `json:"source_event"`
	}{Version: 5, SourceEvent: sourceEvent}})
	if err != nil {
		t.Fatal(err)
	}
	return "<memory-compiler-execution>\n" + string(body) + "\n</memory-compiler-execution>"
}

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
