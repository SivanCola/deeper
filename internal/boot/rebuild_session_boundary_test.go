package boot

import (
	"strings"
	"testing"

	"deepzora/internal/agent"
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
