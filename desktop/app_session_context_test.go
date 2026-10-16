package main

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/sessioncontext"
	"deepzora/internal/tool"
)

type workspaceContextProvider struct{}

func (workspaceContextProvider) Name() string { return "workspace-context" }

func (workspaceContextProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func installStubControllerWithCurrentPrompt(t *testing.T, app *App, tab *WorkspaceTab) *control.Controller {
	t.Helper()
	if tab == nil || tab.Ctrl == nil {
		t.Fatal("tab controller is required")
	}
	sys := tab.Ctrl.SystemPrompt()
	if strings.TrimSpace(sys) == "" {
		t.Fatal("tab controller did not expose a system prompt")
	}
	sessionDir := tab.Ctrl.SessionDir()
	workspaceRoot := tab.Ctrl.WorkspaceRoot()
	label := tab.Ctrl.Label()
	previous, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatal("current controller required")
	}
	service, runtime, bound := previous.SessionBinding()
	if !bound {
		t.Fatal("current session binding required")
	}

	sess := agent.NewSession(sys)
	ag := agent.New(workspaceContextProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{
		Runner:               ag,
		Executor:             ag,
		SessionDir:           sessionDir,
		SessionService:       service,
		SessionRuntime:       runtime,
		ExclusiveSession:     true,
		WorkspaceRoot:        workspaceRoot,
		Label:                label,
		SystemPrompt:         sys,
		SessionContextStatic: sessioncontext.Sections{Workspace: "Current workspace: " + strconv.Quote(workspaceRoot)},
		Sink:                 event.Discard,
	})
	if err := control.ActivateControllerReplacement(previous, ctrl); err != nil {
		ctrl.Close()
		t.Fatal(err)
	}
	previous.Close()
	<-previous.Closed()
	t.Cleanup(func() { ctrl.Close(); <-ctrl.Closed() })
	app.mu.Lock()
	tab.Ctrl = ctrl
	app.mu.Unlock()

	return ctrl
}

func assertWorkspaceSessionContext(t *testing.T, ctrl *control.Controller, want, unwanted string) {
	t.Helper()
	ref, _ := ctrl.SessionRef()
	messages, err := ctrl.SessionService().Query().History(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for i := range slices.Backward(messages) {
		snapshot, ok := sessioncontext.Parse(messages[i].Content)
		if !ok || messages[i].Origin != provider.MessageOriginHost {
			continue
		}
		if !strings.Contains(snapshot.Sections.Workspace, "Current workspace: "+strconv.Quote(want)) {
			t.Fatalf("session-context missing workspace %q:\n%s", want, snapshot.Content)
		}
		if strings.Contains(snapshot.Sections.Workspace, "Current workspace: "+strconv.Quote(unwanted)) {
			t.Fatalf("session-context retained workspace %q:\n%s", unwanted, snapshot.Content)
		}
		return
	}
	t.Fatalf("history contains no valid host session-context: %+v", messages)
}
