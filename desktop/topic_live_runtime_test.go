package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/provider"
)

func TestStartTopicActivationReadyDoesNotWaitForRebuildMutex(t *testing.T) {
	app, tab, _, _, _ := canonicalWorkspaceOpenFixture(t)
	installNoopRuntimeEvents(app)
	app.readyHook = func() {}
	t.Cleanup(func() { app.shutdown(context.Background()) })
	events := newActivationEventRecorder(app)
	request := TopicActivationRequest{Scope: tab.Scope, WorkspaceRoot: tab.WorkspaceRoot, SessionPath: sessionRoute(tab.SessionID), RequestID: "warmup"}
	// Establish a real ready-open state: the fixture only binds a controller.
	// First-open project registration can exceed one second in Windows
	// CreateFile without waiting for the rebuild lock.
	controller := tab.Ctrl
	if _, err := app.StartTopicActivation(request); err != nil {
		t.Fatal(err)
	}
	events.waitFor(t, activationEventFor("warmup", "ready"))
	app.flushTabLayoutWrites()
	app.runtimeRebuildMu.Lock()
	unlock := sync.OnceFunc(app.runtimeRebuildMu.Unlock)
	defer unlock()
	done := make(chan error, 1)
	request.RequestID = "current"
	go func() {
		_, err := app.StartTopicActivation(request)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		stacks := make([]byte, 2<<20)
		t.Logf("activation timeout goroutines:\n%s", stacks[:runtime.Stack(stacks, true)])
		unlock()
		<-done // Do not let a failed activation outlive its fixture directories.
		t.Fatal("ready activation waited for the rebuild mutex")
	}
	if app.controllerForTab(tab) != controller {
		t.Fatal("ready activation replaced its controller")
	}
	unlock()
	events.waitFor(t, activationEventFor("current", "ready"))
}

func installCoveringLeafTopicCatalog(t *testing.T, app *App) (parent, leaf, live string) {
	t.Helper()
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	q := provider.Message{Role: provider.RoleUser, Content: "question"}
	a := provider.Message{Role: provider.RoleAssistant, Content: "answer"}
	save := func(path, topic string, messages ...provider.Message) {
		t.Helper()
		session := agent.NewSession("sys")
		for _, message := range messages {
			session.Add(message)
		}
		if err := session.Save(path); err != nil {
			t.Fatal(err)
		}
		if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
			ID: agent.BranchID(path), Scope: "global", TopicID: topic, TopicTitle: "Upgraded",
		}); err != nil {
			t.Fatal(err)
		}
	}
	parent = filepath.Join(dir, "root.jsonl")
	leaf = filepath.Join(dir, "leaf.jsonl")
	live = filepath.Join(dir, "live.jsonl")
	save(parent, "conversation", q, a)
	save(leaf, "legacy-leaf-topic", q, a,
		provider.Message{Role: provider.RoleUser, Content: "next"},
		provider.Message{Role: provider.RoleAssistant, Content: "done"})
	leafSession, err := agent.LoadSession(leaf)
	if err != nil {
		t.Fatal(err)
	}
	leafDigest, err := agent.ContentDigestForMessages(leafSession.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(leaf, agent.BranchMeta{
		ID: "leaf", Scope: "global", TopicID: "legacy-leaf-topic",
		Recovered: true, ParentID: "root", RecoveryDepth: 1,
		Revision: 1, ContentDigest: leafDigest, RecoveryDigest: leafDigest,
	}); err != nil {
		t.Fatal(err)
	}
	save(live, "conversation",
		provider.Message{Role: provider.RoleUser, Content: "today live turn"},
		provider.Message{Role: provider.RoleAssistant, Content: "paused here"})
	installSessionCatalogForTest(t, app, dir, "global", "")
	return parent, leaf, live
}

func TestResolveOpenTopicSessionPathKeepsPausedLiveOnCoveringParent(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	parent, leaf, _ := installCoveringLeafTopicCatalog(t, app)
	paused := &WorkspaceTab{
		ID:             "paused",
		Scope:          "global",
		TopicID:        "conversation",
		SessionPath:    parent,
		Ctrl:           &retargetRuntimeController{status: control.RuntimeStatus{}, path: parent},
		ActivityStatus: topicStatusPaused,
	}
	app.detachedSessions[sessionRuntimeKey(parent)] = paused

	_, resolved := app.resolveOpenTopicSessionPath("global", "", parent)
	if resolved != parent {
		t.Fatalf("resolve paused parent = %q, want keep live parent %q (not covering leaf %q)", resolved, parent, leaf)
	}
}
