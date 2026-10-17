package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestRebuildBeforeFirstSessionPreservesHostService(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeRuntimeFixture(t, dir)
	old, err := BuildRuntime(t.Context(), withTestSession(t, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(old.Controller.Close)
	service := old.Controller.SessionService()
	if _, bound := old.Controller.SessionRef(); bound {
		t.Fatal("fixture unexpectedly allocated a session")
	}
	next, err := Rebuild(t.Context(), old.Controller, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Controller.Close)
	if !next.Controller.UsesExclusiveSession() || next.Controller.SessionService() != service {
		t.Fatal("rebuild lost the host's current session service")
	}
	if _, bound := next.Controller.SessionRef(); bound {
		t.Fatal("rebuild allocated an unsolicited session")
	}
	if systemMessage(next.Controller.History()) == "" {
		t.Fatal("rebuild lost its assembled system prompt")
	}
}

func TestModelReplacementPreservesPromptThroughAssembly(t *testing.T) {
	for _, preset := range []string{"default", "ordinary", "complete"} {
		t.Run(preset, func(t *testing.T) { testModelReplacementPrompt(t, preset) })
	}
}

func testModelReplacementPrompt(t *testing.T, preset string) {
	t.Helper()
	opts, rec := presetEffectSetup(t)
	configPath := filepath.Join(opts.WorkspaceRoot, "deepzora.toml")
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	configText := strings.ReplaceAll(string(body), `models = ["x"]`, `models = ["x", "y"]`)
	writeFile(t, opts.WorkspaceRoot, "deepzora.toml", configText)
	approveWorkspace(t, opts.WorkspaceRoot)
	if preset != "default" {
		complete := preset == "complete"
		content := fmt.Sprintf("---\ncomplete: %t\ntools: [read_file]\n---\nORIGINAL PERSONA", complete)
		if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, preset, content); err != nil {
			t.Fatal(err)
		}
		opts.AgentPreset = preset
	}
	first := buildPresetEffect(t, opts)
	if _, err := first.Controller.BindFreshSession(t.Context(), "model-prompt"); err != nil {
		t.Fatal(err)
	}
	if preset == "default" {
		carried := first.Controller.History()
		carried[0].Content = "CURRENT PERSISTED PROMPT"
		if err := first.Controller.AdoptRebuiltModelContext(carried); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Controller.Run(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	before := first.Controller.History()[0]
	configText = strings.ReplaceAll(configText, "BASE CODING", "FRESH CONFIG PROMPT")
	writeFile(t, opts.WorkspaceRoot, "deepzora.toml", configText)
	approveWorkspace(t, opts.WorkspaceRoot)
	if preset != "default" {
		content := fmt.Sprintf("---\ncomplete: %t\ntools: []\n---\nLATEST PERSONA", preset != "complete")
		if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, preset, content); err != nil {
			t.Fatal(err)
		}
	}
	next, err := RebuildFrom(t.Context(), first, Options{
		WorkspaceRoot: opts.WorkspaceRoot, Model: "test-model/y", Sink: event.Discard,
		RuntimeReload: RuntimeReload{SystemPromptPolicy: PreserveSystemPrompt},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Controller.Close)
	if next.Controller.SystemPrompt() != before.Content || next.Snapshot.SystemPrompt() != before.Content {
		t.Fatal("model replacement changed controller or snapshot system prompt")
	}
	if err := control.ActivateControllerReplacement(first.Controller, next.Controller); err != nil {
		t.Fatal(err)
	}
	after := next.Controller.History()[0]
	if !reflect.DeepEqual(after, before) {
		t.Fatal("model replacement changed carried system message identity or content")
	}
	if err := next.Controller.Run(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	requests := rec.requests()
	if len(requests) != 2 || systemMessage(requests[1].Messages) != before.Content || next.Controller.ModelRef() != "test-model/y" {
		t.Fatal("replacement provider did not receive the selected model and carried prompt")
	}
	if preset != "default" && len(requests[1].Tools) != 1 {
		t.Fatal("prompt preservation blocked the rebuilt tool admission")
	}
	ref, _ := next.Controller.SessionRef()
	snapshot, err := next.Controller.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil || len(snapshot.Projection.ModelMessages) == 0 || !reflect.DeepEqual(snapshot.Projection.ModelMessages[0], before) {
		t.Fatalf("durable model context changed its carried system prompt: %v", err)
	}
	refreshed, err := RebuildFrom(t.Context(), next, Options{WorkspaceRoot: opts.WorkspaceRoot, Model: "test-model/y", Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(refreshed.Controller.Close)
	if refreshed.ReusedController || refreshed.Controller.SystemPrompt() == before.Content || refreshed.Snapshot.SystemPrompt() != refreshed.Controller.SystemPrompt() {
		t.Fatal("explicit reload reused a preserved prompt instead of refreshing assembly")
	}
}

func TestRebuildRejectsConversationWithoutCurrentSession(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeRuntimeFixture(t, dir)
	conversation := agent.NewSession("original prompt")
	conversation.Add(provider.Message{Role: provider.RoleUser, Content: "keep this conversation"})
	old := control.New(control.Options{Executor: agent.New(nil, nil, conversation, agent.Options{}, event.Discard)})
	t.Cleanup(old.Close)
	if next, err := Rebuild(t.Context(), old, Options{}); err == nil {
		next.Controller.Close()
		t.Fatal("rebuild accepted an unbound conversation")
	} else if !strings.Contains(err.Error(), "requires a host-owned session service") {
		t.Fatalf("rebuild failed for another reason: %v", err)
	}
	history := old.History()
	if len(history) != 2 || history[1].Content != "keep this conversation" {
		t.Fatalf("rejected rebuild altered its source: %+v", history)
	}
}
