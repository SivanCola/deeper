package main

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
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
