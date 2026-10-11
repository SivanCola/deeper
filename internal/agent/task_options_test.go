package agent

import (
	"testing"

	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestNewTaskToolWithOptionsEmptySysPromptUsesDefault(t *testing.T) {
	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       &mockProvider{name: "sub"},
		ParentRegistry: tool.NewRegistry(),
		MaxSteps:       5,
		SysPrompt:      "",
	})
	if task.sysPrompt != DefaultTaskSystemPrompt {
		t.Fatalf("sysPrompt = %q, want DefaultTaskSystemPrompt", task.sysPrompt)
	}
}

func TestNewTaskToolWithOptionsProviderResolverAndOverrides(t *testing.T) {
	base := &mockProvider{name: "base"}
	resolved := &mockProvider{name: "resolved-child"}
	pricing := &provider.Pricing{Input: 3}
	var sawModel, sawEffort string
	resolve := func(model, effort string) (provider.Provider, *provider.Pricing, int, error) {
		sawModel, sawEffort = model, effort
		return resolved, pricing, 4096, nil
	}
	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:        base,
		ParentRegistry:  tool.NewRegistry(),
		MaxSteps:        8,
		SubagentModel:   "child-model",
		SubagentEffort:  "max",
		ResolveProvider: resolve,
	})
	gotProv, gotPrice, gotWin, err := task.resolveSubSessionRuntime("child-model", "max")
	if err != nil {
		t.Fatalf("resolveSubSessionRuntime: %v", err)
	}
	if gotProv != resolved || gotPrice != pricing || gotWin != 4096 {
		t.Fatalf("resolver result = (%v,%v,%d), want resolved pricing/window", gotProv.Name(), gotPrice, gotWin)
	}
	if sawModel != "child-model" || sawEffort != "max" {
		t.Fatalf("resolver args = (%q,%q), want child-model/max", sawModel, sawEffort)
	}
	if task.subagentModel != "child-model" || task.subagentEffort != "max" {
		t.Fatalf("stored overrides = (%q,%q)", task.subagentModel, task.subagentEffort)
	}
}
