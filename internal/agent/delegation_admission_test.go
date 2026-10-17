package agent

import (
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestDelegationAdmissionDoesNotClassifyPromptText(t *testing.T) {
	cases := []struct {
		name, input, args, verdict, reason string
	}{
		{"local fix, plain query", "fix the config serializer bug in parser.go",
			`{"prompt":"how does the serializer format keys"}`, "allow", "model_decides"},
		{"user asked for research", "research the best TOML library and fix the loader",
			`{"prompt":"compare toml libraries"}`, "allow", "model_decides"},
		{"external source cited", "fix the retry logic to match the upstream spec",
			`{"prompt":"read https://example.com/spec and summarize backoff rules"}`, "allow", "model_decides"},
		{"advisory turn", "how does our retry budget compare to industry practice?",
			`{"prompt":"survey retry budget conventions"}`, "allow", "model_decides"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &admissionSink{}
			a := &Agent{svc: agentServices{sink: sink}}
			a.turn.recoveryTaskSummary = c.input
			a.observeDelegationAdmission([]provider.ToolCall{{Name: "research", Arguments: c.args}})
			if len(sink.audits) != 1 || sink.audits[0].Tool != "research" || sink.audits[0].Verdict != c.verdict || sink.audits[0].Reason != c.reason {
				t.Fatalf("audits = %+v, want research/%s/%s", sink.audits, c.verdict, c.reason)
			}
		})
	}
}

type admissionSink struct {
	audits []event.DelegationAdmissionAudit
}

func (s *admissionSink) Emit(event.Event) {}
func (s *admissionSink) RecordDelegationAdmission(a event.DelegationAdmissionAudit) {
	s.audits = append(s.audits, a)
}

func TestObserveDelegationAdmissionRecordsOnlyGatedTools(t *testing.T) {
	sink := &admissionSink{}
	a := &Agent{svc: agentServices{sink: sink}}
	a.turn.recoveryTaskSummary = "fix the failing date parser"
	a.observeDelegationAdmission([]provider.ToolCall{
		{Name: "read_file", Arguments: `{"path":"a.go"}`},
		{Name: "research", Arguments: `{"prompt":"date formats"}`},
		{Name: "task", Arguments: `{"prompt":"sub work"}`},
		{Name: "explore", Arguments: `{"query":"nearby code"}`},
		{Name: "fleet", Arguments: `{"prompt":"parallel work"}`},
	})
	// Audit each supported delegation, while ordinary reads stay outside it.
	if len(sink.audits) != 4 {
		t.Fatalf("got %d audits, want research + task + explore + fleet", len(sink.audits))
	}
	for _, got := range sink.audits {
		if got.Tool == "read_file" {
			t.Fatalf("a non-delegation tool was audited: %+v", got)
		}
		if got.Verdict != "allow" || got.Reason != "model_decides" {
			t.Fatalf("audit = %+v, want allow/model_decides", got)
		}
	}
}
