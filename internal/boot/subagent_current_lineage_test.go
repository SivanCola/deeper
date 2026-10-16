package boot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func TestSubagentContinuationUsesCurrentParentCatalog(t *testing.T) {
	sessionDir := t.TempDir()
	root := session.RootForSessionDir(sessionDir)
	parents, err := session.NewService("local", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parents.Shutdown(context.Background()) })
	for _, item := range []struct{ id, parent string }{{"root", ""}, {"child", "root"}, {"unrelated", ""}} {
		runtime, err := parents.Create(t.Context(), session.CreateOptions{SessionID: item.id, ParentSessionID: item.parent, Origin: session.SessionOriginNew})
		if err != nil {
			t.Fatal(err)
		}
		if err := parents.Close(t.Context(), runtime.Ref()); err != nil {
			t.Fatal(err)
		}
	}
	store, err := newSubagentStore(parents, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := agent.SubagentSpec{Kind: "task", Name: "review", WorkspaceRoot: sessionDir, ParentSession: "root", SystemPrompt: "review", Registry: tool.NewRegistry(), Model: "test"}
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatal(err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleAssistant, Content: "parent result"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatal(err)
	}
	run.Release()
	spec.ParentSession = "child"
	child, err := store.PrepareContinue(run.Ref, spec)
	if err != nil {
		t.Fatal(err)
	}
	if child.Ref == run.Ref || child.Meta.ParentSession != "child" || child.ForkedFrom != run.Ref {
		t.Fatalf("wrong continuation identity: %+v", child.Meta)
	}
	child.Release()
	// A valid-looking old sidecar must not grant ancestry absent from the
	// current catalog, even when it claims the exact desired parent.
	oldPath := filepath.Join(sessionDir, "unrelated.jsonl.meta")
	oldBody := []byte(`{"id":"unrelated","parent_id":"root"}`)
	if err := os.WriteFile(oldPath, oldBody, 0o600); err != nil {
		t.Fatal(err)
	}
	spec.ParentSession = "unrelated"
	if _, err := store.PrepareContinue(run.Ref, spec); err == nil {
		t.Fatal("old sidecar authorized a foreign parent")
	}
	for _, id := range []string{"root", "child", "unrelated"} {
		if _, live := parents.Runtime(session.SessionRef{HostID: "local", SessionID: id}); live {
			t.Fatalf("lineage read acquired parent writer %s", id)
		}
	}
	body, err := os.ReadFile(oldPath)
	if err != nil || string(body) != string(oldBody) {
		t.Fatalf("old metadata was changed: %q %v", body, err)
	}
	// Broken current identity must fail closed, not fall back to that sidecar.
	headerPath := filepath.Join(root, "unrelated", "header.json")
	body, err = os.ReadFile(headerPath)
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]any
	if err := json.Unmarshal(body, &header); err != nil {
		t.Fatal(err)
	}
	header["parentSessionId"] = "../escape"
	body, _ = json.Marshal(header)
	if err := os.WriteFile(headerPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareContinue(run.Ref, spec); err == nil {
		t.Fatalf("invalid current parent accepted: %v", err)
	}
}
