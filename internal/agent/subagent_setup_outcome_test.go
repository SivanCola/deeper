package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestTaskSetupFailurePreservesAllocatedSubagentOutcome(t *testing.T) {
	workspace := t.TempDir()
	store := NewSubagentStore(t.TempDir())
	task := NewTaskToolWithOptions(TaskToolOptions{Provider: nil, Pricing: nil, ParentRegistry: tool.NewRegistry(), MaxSteps: 20, ContextWindow: 0, CompactRatio: 0, Temperature: 0, ArchiveDir: "", SysPrompt: "sys", Gate: nil, SubagentModel: "", SubagentEffort: "", ResolveProvider: nil}).
		WithTranscripts(store, workspace, "model", "effort")
	task.resolveProvider = func(string, string) (provider.Provider, *provider.Pricing, int, error) {
		return nil, nil, 0, errors.New("profile unavailable")
	}
	out, err := task.Execute(WithParentSession(context.Background(), "parent"), []byte(`{"prompt":"inspect","model":"child-model"}`))
	if err == nil || !strings.Contains(err.Error(), "profile unavailable") {
		t.Fatalf("Execute error = %v, want setup failure", err)
	}
	outcome, ok := ParseSubagentOutcome(out)
	if !ok || outcome.Ref == "" || outcome.Status != SubagentOutcomeFailed {
		t.Fatalf("outcome = %+v, output=%q, want failed ref", outcome, out)
	}
	meta, metaErr := store.LoadMeta(outcome.Ref)
	if metaErr != nil || meta.Status != SubagentFailed || meta.ErrorCode != "subagent_error" {
		t.Fatalf("metadata = %+v/%v, want retained failed outcome", meta, metaErr)
	}
}
