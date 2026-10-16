package boot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func newCleanupParent(t *testing.T, sessionDir string) *session.Service {
	t.Helper()
	parents, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(sessionDir)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parents.Shutdown(context.Background()) })
	runtime, err := parents.Create(t.Context(), session.CreateOptions{SessionID: "parent-session", Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	if err := parents.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	return parents
}

func prepareRunningSubagent(t *testing.T, sessionDir string) string {
	t.Helper()
	store := agent.NewSubagentStore(testSubagentPersistence(t, sessionDir))
	spec := agent.SubagentSpec{
		Kind:          "task",
		Name:          "task",
		WorkspaceRoot: t.TempDir(),
		ParentSession: "parent-session",
		SystemPrompt:  "sys",
		Registry:      tool.NewRegistry(),
		Model:         "base-model",
	}
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.MarkRunning(run); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	ref := run.Ref
	run.Release()
	return ref
}

func requireSubagentStatus(t *testing.T, sessionDir, ref string, want agent.SubagentStatus) {
	t.Helper()
	meta, err := agent.NewSubagentStore(testSubagentPersistence(t, sessionDir)).LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta(%s): %v", ref, err)
	}
	if meta.Status != want {
		t.Fatalf("status = %q, want %q", meta.Status, want)
	}
}

func TestNewSubagentStoreCleansStaleRunningOnEveryBuild(t *testing.T) {
	sessionDir := t.TempDir()

	parents := newCleanupParent(t, sessionDir)
	firstRef := prepareRunningSubagent(t, sessionDir)
	if _, err := newSubagentStore(parents, nil); err != nil {
		t.Fatalf("newSubagentStore (first): %v", err)
	}
	requireSubagentStatus(t, sessionDir, firstRef, agent.SubagentInterrupted)

	secondRef := prepareRunningSubagent(t, sessionDir)
	if _, err := newSubagentStore(parents, nil); err != nil {
		t.Fatalf("newSubagentStore (second): %v", err)
	}
	requireSubagentStatus(t, sessionDir, secondRef, agent.SubagentInterrupted)
}

func TestNewSubagentStoreCurrentWriterDefersThenRecovers(t *testing.T) {
	sessionDir := t.TempDir()
	parents := newCleanupParent(t, sessionDir)
	ref := prepareRunningSubagent(t, sessionDir)
	parentRef := session.SessionRef{HostID: "local", SessionID: "parent-session"}
	binding, err := parents.Open(t.Context(), parentRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newSubagentStore(parents, nil); err != nil {
		t.Fatal(err)
	}
	requireSubagentStatus(t, sessionDir, ref, agent.SubagentRunning)
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := parents.Close(t.Context(), parentRef); err != nil {
		t.Fatal(err)
	}
	if _, err := newSubagentStore(parents, nil); err != nil {
		t.Fatal(err)
	}
	requireSubagentStatus(t, sessionDir, ref, agent.SubagentInterrupted)
}

func TestNewSubagentStoreNeverCachesCleanupError(t *testing.T) {
	sessionDir := t.TempDir()
	parents := newCleanupParent(t, sessionDir)
	if err := os.WriteFile(filepath.Join(parents.Query().StorageRoot(), ".subagents"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := newSubagentStore(parents, nil); err == nil {
			t.Fatalf("newSubagentStore attempt %d unexpectedly succeeded", attempt)
		}
	}
}

func TestNewSubagentStoreCleansNewlyOpenedParentBeforeExecution(t *testing.T) {
	sessionDir := t.TempDir()
	parents := newCleanupParent(t, sessionDir)
	ref := prepareRunningSubagent(t, sessionDir)
	binding, err := parents.Open(t.Context(), session.SessionRef{HostID: "local", SessionID: "parent-session"})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release(context.Background())
	if _, err := newSubagentStore(parents, binding.Runtime()); err != nil {
		t.Fatal(err)
	}
	requireSubagentStatus(t, sessionDir, ref, agent.SubagentInterrupted)
}
