package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
	"deepzora/internal/tool/builtin"
)

type currentRewindFixture struct {
	app      *App
	ctrl     *control.Controller
	ref      session.SessionRef
	filePath string
}

// Capture checkpoints through real tool execution so rewind exercises the
// current mutation observer and durable turn identities together.
func newCurrentRewindFixture(t *testing.T) currentRewindFixture {
	t.Helper()
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"path": path})
	writeArgs, _ := json.Marshal(map[string]string{"path": path, "content": "after"})
	prov := testutil.NewMock("rewind-test",
		testutil.Turn{Text: "answer"},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "read", Name: "read_file", Arguments: string(readArgs)}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: string(writeArgs)}}},
		testutil.Turn{Text: "done"},
		testutil.Turn{Text: "parent continued answer"},
	)
	registry := tool.NewRegistry()
	read, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("missing read_file")
	}
	registry.Add(read)
	for _, writer := range builtin.ConfineWriters([]string{root}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}) {
		if writer.Name() == "write_file" {
			registry.Add(writer)
		}
	}
	exec := agent.New(prov, registry, agent.NewSession("system"), agent.Options{WriteWorkspaceRoot: root}, event.Discard)
	app := NewApp()
	app.ctx = t.Context()
	ctrl := newCurrentRebuildController(t, app, control.Options{Runner: exec, Executor: exec, Sink: event.Discard, WorkspaceRoot: root})
	app.setTestCtrl(ctrl, "test")
	tab := app.tabs["test"]
	tab.Scope, tab.WorkspaceRoot, tab.TopicTitle = "project", root, "Source topic"
	setCurrentRebuildTabIdentity(t, app, tab, ctrl)
	// These regressions inspect publication and parent ownership. Cancel and
	// join child builders before they can acquire unrelated provider runtimes.
	release := make(chan struct{})
	app.tabBuildStartHook = func(string) { <-release }
	t.Cleanup(func() {
		app.mu.Lock()
		var builds []<-chan struct{}
		for _, tab := range app.tabs {
			if tab.buildDone != nil {
				builds = append(builds, tab.buildDone)
			}
			app.supersedeTabBuildLocked(tab)
		}
		app.mu.Unlock()
		close(release)
		for _, done := range builds {
			if !waitChannelBefore(done, time.Now().Add(5*time.Second)) {
				t.Error("rewind child builder did not stop")
			}
		}
	})
	for _, prompt := range []string{"first", "edit"} {
		if err := ctrl.RunTurn(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "after" {
		t.Fatalf("tool write = %q, %v", got, err)
	}
	checkpoints := ctrl.Checkpoints()
	if len(checkpoints) != 2 || len(checkpoints[1].Paths) == 0 {
		t.Fatalf("real tool mutation was not captured: %+v", checkpoints)
	}
	ref, _ := ctrl.SessionRef()
	return currentRewindFixture{app: app, ctrl: ctrl, ref: ref, filePath: path}
}

func (f currentRewindFixture) snapshot(t *testing.T, ref session.SessionRef) session.Snapshot {
	t.Helper()
	snapshot, err := f.ctrl.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
