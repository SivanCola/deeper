package main

import (
	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/tool"
	"testing"
)

func TestRestoredTabGoalFollowsCurrentSessionIdentity(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	tab := &WorkspaceTab{ID: "goal", Scope: "global", WorkspaceRoot: globalTabWorkspaceRoot()}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	executor := agent.New(stubProvider{}, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	ctrl := bindHistoryRuntime(t, app, tab, executor, executor)
	first, _ := ctrl.SessionRef()
	if err := ctrl.SetGoalDurable("finish the work"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.NewSession(); err != nil {
		t.Fatal(err)
	}
	second, ok := ctrl.SessionRef()
	if !ok || second == first || ctrl.Goal() != "" {
		t.Fatal("new session inherited old goal")
	}
	if _, err := ctrl.OpenSession(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if ctrl.Goal() != "finish the work" {
		t.Fatalf("old session lost goal: %q", ctrl.Goal())
	}
	if _, err := ctrl.OpenSession(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if ctrl.Goal() != "" {
		t.Fatal("reopened new session resurrected old goal")
	}
}
