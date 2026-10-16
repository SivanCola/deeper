package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/goal"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestCollectQualityProducesPublicSafeSummary(t *testing.T) {
	dir := t.TempDir()
	const secret = "private-source-and-prompt-secret"
	messages := []string{
		`{"role":"system","content":"` + secret + `"}`,
		`{"role":"user","content":"fix /private/example/repo and keep ` + secret + `"}`,
		`{"role":"assistant","tool_calls":[{"id":"edit-1","name":"edit_file","arguments":"{\"path\":\"/private/example/repo/app.go\"}"}]}`,
		`{"role":"tool","tool_call_id":"edit-1","name":"edit_file","content":"updated ` + secret + `"}`,
		`{"role":"assistant","reasoning_content":"verify the change","reasoning_state":"complete","tool_calls":[{"id":"test-1","name":"bash","arguments":"{\"command\":\"go test ./...\"}"}]}`,
		`{"role":"tool","tool_call_id":"test-1","name":"bash","content":"ok"}`,
		`{"role":"user","content":"<compaction-summary>\nprivate summary ` + secret + `"}`,
		`{"role":"assistant","content":"done"}`,
	}
	goals := goal.NewMachine(nil, nil)
	if _, err := goals.Create(goal.CreateRequest{Objective: "finish " + secret}); err != nil {
		t.Fatal(err)
	}
	goalState, err := goals.Encode()
	if err != nil {
		t.Fatal(err)
	}
	service, ref := createQualityTestSession(t, dir, messages, []session.Event{
		{Kind: "session/config", Payload: json.RawMessage(`{"modelRef":"corp.private.example/supersecret-model"}`)},
		{Kind: "plan/state", Payload: json.RawMessage(`{"enabled":true}`)},
		{Kind: "session/permission-preset", Payload: json.RawMessage(`{"preset":"workspace-write"}`)},
		{Kind: "goal/state", Payload: goalState},
	})
	telemetry := `{
		"version":3,
		"usage":{
			"promptTokens":1000,
			"completionTokens":200,
			"reasoningTokens":80,
			"cacheHitTokens":750,
			"cacheMissTokens":250,
			"requestCount":5,
			"sources":{
				"executor":{"requestCount":2},
				"planner":{"requestCount":1},
				"subagent":{"requestCount":1},
				"` + secret + `":{"requestCount":1}
			}
		}
	}`
	telemetryPath := filepath.Join(t.TempDir(), "current.telemetry.json")
	if err := os.WriteFile(telemetryPath, []byte(telemetry), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := CollectQuality(QualityOptions{Version: "v-test", Query: service.Query(), Ref: ref, TelemetryPath: telemetryPath})
	if err != nil {
		t.Fatalf("CollectQuality: %v", err)
	}
	if report.Profile.ModelFamily != "custom/unknown" {
		t.Fatalf("profile = %+v", report.Profile)
	}
	if report.Profile.CollaborationMode != "plan" || report.Profile.ToolApprovalMode != "workspace-write" || !report.Profile.GoalActive || !report.Profile.Interrupted {
		t.Fatalf("profile modes = %+v", report.Profile)
	}
	if report.Transcript.ToolCalls != 2 || report.Transcript.WriterCalls != 1 || report.Transcript.VerificationCalls != 1 || report.Transcript.CompactionSummaries != 1 {
		t.Fatalf("transcript = %+v", report.Transcript)
	}
	if report.Transcript.ToolCallTurnsWithoutReasoning != 1 {
		t.Fatalf("tool-call turns without reasoning = %d", report.Transcript.ToolCallTurnsWithoutReasoning)
	}
	if report.Usage.CacheHitPercent == nil || *report.Usage.CacheHitPercent != 75 {
		t.Fatalf("usage = %+v", report.Usage)
	}
	if report.Signals.ExecutorRequests != 2 || report.Signals.PlannerRequests != 1 || report.Signals.SubagentRequests != 1 || report.Signals.OtherRequests != 1 {
		t.Fatalf("signals = %+v", report.Signals)
	}

	jsonData, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := RenderQualityText(report)
	for _, output := range []string{string(jsonData), text} {
		for _, forbidden := range []string{secret, dir, "/private/example", "private-session-id", "supersecret-model", "corp.private.example"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("quality summary leaked %q:\n%s", forbidden, output)
			}
		}
	}
	if !strings.Contains(text, "privacy: transcript text, paths") {
		t.Fatalf("text missing privacy statement:\n%s", text)
	}
}

func TestCollectQualityWithoutTelemetryStaysUseful(t *testing.T) {
	service, ref := createQualityTestSession(t, t.TempDir(), []string{`{"role":"user","content":"hello"}`}, nil)
	report, err := CollectQuality(QualityOptions{Version: "v-test", Query: service.Query(), Ref: ref})
	if err != nil {
		t.Fatalf("CollectQuality: %v", err)
	}
	if report.Usage.Available {
		t.Fatal("usage should be unavailable without desktop telemetry")
	}
	if report.Profile.ToolApprovalMode != "unknown" {
		t.Fatalf("unrecorded permission was guessed: %+v", report.Profile)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "telemetry") {
		t.Fatalf("warnings = %v", report.Warnings)
	}
}

func TestQualityTranscriptExcludesPinnedContextRevisions(t *testing.T) {
	report := summarizeQualityTranscript([]provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "<pinned_context_revision>private pinned body</pinned_context_revision>"},
		{Role: provider.RoleUser, Content: "visible question"},
	})
	if report.Messages != 1 || report.UserMessages != 1 {
		t.Fatalf("quality transcript = %+v, want one visible user message", report)
	}
}
