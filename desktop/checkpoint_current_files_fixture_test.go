package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/checkpoint"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
	"deepzora/internal/tool/builtin"
)

// Capture actual current-runtime mutation receipts; no old checkpoint sidecar
// is manufactured or loaded to make the presentation assertions pass.
func currentFileMutationController(t *testing.T, app *App, root, id, name, before, after, prompt string) *control.Controller {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"path": path})
	writeArgs, _ := json.Marshal(map[string]string{"path": path, "content": after})
	prov := testutil.NewMock("checkpoint-test",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "read", Name: "read_file", Arguments: string(readArgs)}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: string(writeArgs)}}},
		testutil.Turn{Text: "done"},
	)
	registry := tool.NewRegistry()
	reader, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("read_file missing")
	}
	registry.Add(reader)
	for _, writer := range builtin.ConfineWriters([]string{root}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}) {
		if writer.Name() == "write_file" {
			registry.Add(writer)
		}
	}
	exec := agent.New(prov, registry, agent.NewSession("system"), agent.Options{WriteWorkspaceRoot: root}, event.Discard)
	ctrl := newNamedCurrentRebuildController(t, app, id, control.Options{Runner: exec, Executor: exec, Sink: event.Discard, WorkspaceRoot: root})
	if err := ctrl.RunTurn(t.Context(), prompt); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != after {
		t.Fatalf("tool write did not produce expected content: got %d bytes, want %d bytes; err=%v", len(got), len(after), err)
	}
	return ctrl
}

// The preview-limit regression exercises the current checkpoint store directly.
// Actual tool-to-checkpoint integration is covered by currentFileMutationController
// and the rewind tests, without repeating a full provider turn for every file.
func currentCheckpointFileFixture(t *testing.T, count int) *App {
	t.Helper()
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	app := NewApp()
	app.ctx = t.Context()
	ctrl := newCurrentRebuildController(t, app, control.Options{WorkspaceRoot: root})
	app.setTestCtrl(ctrl, "test")
	tab := app.tabs["test"]
	tab.Scope, tab.WorkspaceRoot = "project", root
	setCurrentRebuildTabIdentity(t, app, tab, ctrl)
	_, runtime, _ := ctrl.SessionBinding()
	store := runtime.Session().FileCheckpoints(root)
	store.Begin(0, "ask only", 0, "ask")
	store.Begin(1, "edit files", 0, "edit")
	observer := checkpoint.NewMutationObserver(checkpoint.ObserverOptions{Store: store, OwnershipTurn: 1, WriterID: "preview-test"})
	for i := range count {
		path := filepath.Join(root, "file-"+strconv.Itoa(1000+i)+".txt")
		if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
		observer.BeforeMutation(path, "write_file", checkpoint.CaptureBeforeMutation)
		if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
			t.Fatal(err)
		}
		if !observer.AfterMutation(path, "write_file") {
			t.Fatal("checkpoint missed a real file change")
		}
	}
	return app
}
