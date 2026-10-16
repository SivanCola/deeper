package subagentpersist

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestCurrentChildForeignWriterExclusion(t *testing.T) {
	root := t.TempDir()
	store, _ := testStore(t, root)
	handle, err := store.Create(t.Context(), agent.SubagentMeta{Ref: "sa_foreign"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCurrentChildWriterHelper$")
	cmd.Env = append(os.Environ(), "DEEPZORA_CHILD_WRITER_ROOT="+root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child readiness = %q, %v", line, err)
	}
	if _, err := store.Open(t.Context(), "sa_foreign"); err == nil {
		t.Fatal("foreign writer allowed competing continuation")
	}
	if _, err := store.Read(t.Context(), "sa_foreign"); err != nil {
		t.Fatalf("cold read failed under foreign writer: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Open(t.Context(), "sa_foreign")
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentChildWriterHelper(t *testing.T) {
	root := os.Getenv("DEEPZORA_CHILD_WRITER_ROOT")
	if root == "" {
		return
	}
	store, _ := testStore(t, root)
	handle, err := store.Open(t.Context(), "sa_foreign")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	var released [1]byte
	_, _ = os.Stdin.Read(released[:])
}

func testStore(t *testing.T, root string) (*Store, *session.Service) {
	t.Helper()
	parent, err := session.NewService("host", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Shutdown(context.Background()) })
	child, err := parent.Subagents()
	if err != nil {
		t.Fatal(err)
	}
	return New(child), parent
}

func TestDeleteParentRemovesOnlyItsCurrentChildrenAndRefusesLiveChild(t *testing.T) {
	persistence, parent := testStore(t, t.TempDir())
	runtime, err := parent.Create(t.Context(), session.CreateOptions{SessionID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	store := agent.NewSubagentStore(persistence)
	prepare := func(owner string) *agent.SubagentRun {
		run, err := store.PrepareFresh(agent.SubagentSpec{ParentSession: owner, SystemPrompt: "system"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkRunning(run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	owned, other := prepare("parent"), prepare("other")
	defer other.Release()
	if err := parent.Delete(t.Context(), runtime.Ref()); err == nil {
		t.Fatal("parent deleted while child still owns writer")
	}
	if _, err := parent.Query().Stat(t.Context(), runtime.Ref()); err != nil {
		t.Fatalf("failed child cleanup deleted parent: %v", err)
	}
	if err := owned.Release(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Delete(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.Read(t.Context(), owned.Ref); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("deleted child readable: %v", err)
	}
	if _, err := store.LoadMeta(owned.Ref); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child metadata survived: %v", err)
	}
	if _, err := store.LoadMeta(other.Ref); err != nil {
		t.Fatalf("foreign child removed: %v", err)
	}
	if err := other.Persistence.Flush(t.Context()); err != nil {
		t.Fatalf("foreign writer disturbed: %v", err)
	}
}

func TestCurrentChildTranscriptPreservesHistoryAcrossCompactionAndColdCopy(t *testing.T) {
	root := t.TempDir()
	store, parent := testStore(t, root)
	initial := []provider.Message{{ID: "sys", Role: provider.RoleSystem, Content: "system"}}
	handle, err := store.Create(t.Context(), agent.SubagentMeta{Ref: "sa_original", WorkspaceRoot: root}, initial)
	if err != nil {
		t.Fatal(err)
	}
	messages := []provider.Message{{ID: "user", Role: provider.RoleUser, Content: "question"}, {ID: "answer", Role: provider.RoleAssistant, Content: "answer"}}
	if err := handle.RecordSessionMessages(t.Context(), "response", messages); err != nil {
		t.Fatal(err)
	}
	// Exact message retries are idempotent even across independent checkpoints.
	if err := handle.RecordSessionMessages(t.Context(), "response", messages); err != nil {
		t.Fatal(err)
	}
	messages[1].Content = "corrected answer"
	if err := handle.RecordSessionMessageUpsert(t.Context(), "correction", messages[1]); err != nil {
		t.Fatal(err)
	}
	model := []provider.Message{initial[0], {ID: "summary", Role: provider.RoleUser, Content: "compressed context"}}
	result, err := handle.RecordSessionModelContext(t.Context(), agent.SessionModelContextCommit{OperationID: "compact-1", Reason: "compaction", Messages: model})
	if err != nil || !result.Accepted || !result.Durable {
		t.Fatalf("context commit = %+v, %v", result, err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := handle.RecordSessionMessages(t.Context(), "late", messages); !errors.Is(err, session.ErrStaleExecution) {
		t.Fatalf("late commit = %v", err)
	}
	if err := parent.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	cold, _ := testStore(t, root)
	full, err := cold.Read(t.Context(), "sa_original")
	want := append(initial, messages...)
	if err != nil || !reflect.DeepEqual(full, want) {
		t.Fatalf("full history = %+v, %v", full, err)
	}
	copy, err := cold.Copy(t.Context(), "sa_original", agent.SubagentMeta{Ref: "sa_copy", WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := copy.Messages(); !reflect.DeepEqual(got, model) {
		t.Fatalf("copy model context = %+v", got)
	}
	if err := copy.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	fullCopy, err := cold.Read(t.Context(), "sa_copy")
	if err != nil || !reflect.DeepEqual(fullCopy, full) {
		t.Fatalf("copy lost full history: %+v, %v", fullCopy, err)
	}
	continued, err := cold.Open(t.Context(), "sa_original")
	if err != nil {
		t.Fatal(err)
	}
	if got := continued.Messages(); !reflect.DeepEqual(got, model) {
		t.Fatalf("continued model context = %+v", got)
	}
	if err := continued.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentChildWriterExcludesOtherStoresAndDeletion(t *testing.T) {
	store, _ := testStore(t, t.TempDir())
	handle, err := store.Create(t.Context(), agent.SubagentMeta{Ref: "sa_busy"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := New(store.service)
	if _, err := other.Open(t.Context(), "sa_busy"); !errors.Is(err, session.ErrRuntimeBusy) {
		t.Fatalf("competing continuation = %v", err)
	}
	if err := other.Delete(t.Context(), "sa_busy"); err == nil {
		t.Fatal("deleted a live child")
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	continued, err := other.Open(t.Context(), "sa_busy")
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Flush(t.Context()); !errors.Is(err, session.ErrStaleExecution) {
		t.Fatalf("old writer flush = %v", err)
	}
	if err := continued.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := other.Delete(t.Context(), "sa_busy"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Read(t.Context(), "sa_busy"); err == nil {
		t.Fatal("deleted transcript still readable")
	}
}
