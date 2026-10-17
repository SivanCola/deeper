package control

import (
	"reflect"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/sessioncontext"
	"deepzora/internal/skill"
	"deepzora/internal/tool"
)

func TestPresetRuntimeContextPolicyKeepsRequiredWorkspace(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "included"
		if disabled {
			name = "cwd-only"
		}
		t.Run(name, func(t *testing.T) {
			executor := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
			c := newOwnedTestController(t, Options{
				Executor: executor, DisableRuntimeContext: disabled,
				SessionContextStatic: sessioncontext.Sections{
					Workspace: "Current workspace: fixture", Environment: "OPTIONAL ENVIRONMENT",
					BackgroundMemory: "OPTIONAL BACKGROUND", SkillsCatalog: "OPTIONAL STATIC SKILLS",
				},
				Skills: []skill.Skill{{Name: "optional-skill", Description: "OPTIONAL LIVE SKILLS"}},
			})
			for _, real := range []bool{false, true} {
				ctx := c.withTurnContext(t.Context(), real)
				// The public Agent admission path validates the envelope and records
				// exactly the same executor snapshot used by real controller turns.
				fresh := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
				if !fresh.AppendTurnContext(ctx) {
					t.Fatal("required cwd was not published")
				}
				snapshot, ok := latestControlSessionContext(fresh.Session().Snapshot())
				if !ok || snapshot.Sections.Workspace != "Current workspace: fixture" {
					t.Fatalf("runtime snapshot=%+v", snapshot)
				}
				if disabled {
					if !reflect.DeepEqual(snapshot.Sections, sessioncontext.Sections{Workspace: "Current workspace: fixture"}) {
						t.Fatalf("optional context escaped=%+v", snapshot.Sections)
					}
				} else if snapshot.Sections.Environment != "OPTIONAL ENVIRONMENT" || snapshot.Sections.BackgroundMemory != "OPTIONAL BACKGROUND" || snapshot.Sections.SkillsCatalog == "" {
					t.Fatalf("default context changed=%+v", snapshot.Sections)
				}
				if snapshot.Digest == "" {
					t.Fatal("filtered context has no validated digest")
				}
			}
			if len(c.skills.list()) != 1 {
				t.Fatal("context preference unloaded the skills service")
			}
		})
	}
}

func TestCompletePresetSurvivesControllerPromptReplacement(t *testing.T) {
	const body = "You are a travel writer."
	executor := agent.New(nil, tool.NewRegistry(), agent.NewSession("HOST BASE"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: executor, SystemPrompt: "HOST BASE", CompleteSystemPrompt: body})
	c.ApplyExtensionSystemPrompt("EXTENSION REPLACEMENT")
	if c.SystemPrompt() != body || executor.Session().Snapshot()[0].Content != body {
		t.Fatal("extension replaced the complete prompt")
	}
	for _, message := range c.History() {
		if message.Role == provider.RoleSystem && message.Content != body {
			t.Fatalf("unexpected system prompt=%q", message.Content)
		}
	}
}
