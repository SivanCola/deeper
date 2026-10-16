package main

import (
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/memory"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestSubmitToTabHistoryDisplaysRawInputAfterStandingMemoryCompose(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	service := app.desktopSessionService("")
	mem := memory.Load(memory.Options{CWD: t.TempDir()})
	sess := agent.NewSession("sys")
	model := &capturingProvider{}
	exec := agent.New(model, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{
		Runner: exec, Executor: exec, Sink: event.Discard,
		SessionService: service, SessionCreateService: service, Label: "test", Memory: mem,
	})
	defer ctrl.Close()

	app.setTestCtrl(ctrl, "deepseek/test")
	ref, workspaceID, err := app.bindFreshDesktopSessionWithIDs(t.Context(), "global", "", ctrl, "memory-display", "")
	if err != nil {
		t.Fatal(err)
	}
	app.tabs["test"].SessionID = ref.SessionID
	app.tabs["test"].SessionWorkspace.ID = workspaceID
	if _, err := ctrl.QuickAdd(memory.ScopeProject, "contribution count updated"); err != nil {
		t.Fatal(err)
	}

	const prompt = "不要，删了"
	if err := app.SubmitToTabWithID("test", prompt, newSessionRuntimeID("test-submit")); err != nil {
		t.Fatal(err)
	}
	waitNotRunning(t, ctrl)
	var composed string
	for _, message := range model.lastRequestMessages(t) {
		if message.Role == provider.RoleUser {
			composed = message.Content
		}
	}
	if !strings.Contains(composed, "<memory-update>") || !strings.HasSuffix(composed, prompt) {
		t.Fatalf("model input should include memory update followed by prompt, got %q", composed)
	}
	var users []HistoryMessage
	for _, message := range currentTranscriptMessages(t, ctrl) {
		if message.Role == "user" {
			users = append(users, message)
		}
	}
	if len(users) != 1 || users[0].Content != prompt || strings.Contains(users[0].Content, "<memory-update>") {
		t.Fatalf("displayed user content = %+v, want raw prompt %q", users, prompt)
	}
}
