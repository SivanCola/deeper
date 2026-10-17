package main

import (
	"errors"
	"strings"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

func TestAgentPresetAuthoringWithoutRuntime(t *testing.T) {
	a := newManualSessionTestApp(t)
	list, err := a.ListAgentPresets("")
	if err != nil || list.Current != "default" || list.Locked || len(list.Presets) != 1 {
		t.Fatalf("initial list = %+v, %v", list, err)
	}
	text := "---\nname: Reviewer\ntools: []\n---\nOnly review changes.\n"
	saved, err := a.SaveAgentPreset("", "user", "review", text)
	if err != nil || saved.ID != "review" || saved.Tools == nil {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	if _, err := a.SaveAgentPreset("", "user", "default", text); !errors.Is(err, agentpreset.ErrBuiltin) {
		t.Fatalf("builtin write = %v", err)
	}
	list, err = a.ListAgentPresets("")
	if err != nil || len(list.Presets) != 2 || list.Presets[1].Body != "Only review changes." {
		t.Fatalf("saved list = %+v, %v", list, err)
	}
	if err := a.DeleteAgentPreset("", "user", "review"); err != nil {
		t.Fatal(err)
	}
}

func manualAgentPresetTab(t *testing.T, a *App, id string) (*WorkspaceTab, ManualSessionCreationView) {
	t.Helper()
	view, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "agent-preset-" + id, Scope: "global", AgentPreset: id})
	if err != nil {
		t.Fatal(err)
	}
	a.manualCreationTasks.Wait()
	view, err = a.GetManualSessionCreation(view.OperationID)
	if err != nil || view.Phase != "ready" {
		t.Fatalf("creation = %+v, %v", view, err)
	}
	meta := a.metaForSession(view.Ref.SessionID)
	if meta == nil {
		t.Fatal("created session has no tab")
	}
	tab, _ := a.tabAndCtrlByID(meta.ID)
	return tab, view
}

func TestManualCreationAgentPresetSelectionAndFailureAtomicity(t *testing.T) {
	a := newManualSessionTestApp(t)
	if _, err := a.SaveAgentPreset("", "user", "review", "---\nname: Review\ntools: [read_file]\n---\nPERSONA-REVIEW"); err != nil {
		t.Fatal(err)
	}
	tab, view := manualAgentPresetTab(t, a, "review")
	ctrl := a.controllerForTab(tab).(*control.Controller)
	if ctrl.AgentPreset() != "review" || !strings.Contains(ctrl.SystemPrompt(), "PERSONA-REVIEW") {
		t.Fatalf("preset was not assembled: %q", ctrl.AgentPreset())
	}
	list, err := a.ListAgentPresets(tab.ID)
	if err != nil || list.Current != "review" || list.Locked {
		t.Fatalf("blank list = %+v, %v", list, err)
	}
	if _, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: view.OperationID, Scope: "global", AgentPreset: "default"}); err == nil {
		t.Fatal("operation identity accepted different preset")
	}
	before := ctrl.History()
	if err := a.SetAgentPresetForTab(tab.ID, "missing"); err == nil {
		t.Fatal("missing preset accepted")
	}
	if a.controllerForTab(tab) != ctrl || ctrl.AgentPreset() != "review" || len(ctrl.History()) != len(before) {
		t.Fatal("failed selection changed current runtime")
	}
	if err := a.SetAgentPresetForTab(tab.ID, "default"); err != nil {
		t.Fatal(err)
	}
	next := a.controllerForTab(tab).(*control.Controller)
	if next == ctrl || next.AgentPreset() != "" || strings.Contains(next.SystemPrompt(), "PERSONA-REVIEW") {
		t.Fatal("blank selection did not replace runtime")
	}
	snapshot, err := a.desktopSessionService("").Query().Snapshot(t.Context(), view.Ref)
	if err != nil || snapshot.Projection.AgentPreset != "" {
		t.Fatalf("selection not recorded: %+v, %v", snapshot.Projection, err)
	}
}

func TestDesktopAgentPresetLocksAfterFirstTurn(t *testing.T) {
	a := newManualSessionTestApp(t)
	tab, view := manualAgentPresetTab(t, a, "default")
	ctrl := a.controllerForTab(tab).(*control.Controller)
	_, runtime, _ := ctrl.SessionBinding()
	if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "preset-test-turn", TurnID: "preset-turn", Events: []session.Event{{Kind: "turn/start"}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAgentPresetForTab(tab.ID, "default"); !errors.Is(err, control.ErrAgentPresetLocked) {
		t.Fatalf("locked selection = %v", err)
	}
	list, err := a.ListAgentPresets(tab.ID)
	if err != nil || !list.Locked {
		t.Fatalf("locked list = %+v, %v", list, err)
	}
	meta := a.metaForSession(view.Ref.SessionID)
	if meta == nil || !meta.AgentPresetLocked || meta.AgentPreset != "default" {
		t.Fatalf("locked metadata = %+v", meta)
	}
}

func TestDesktopAgentPresetColdRestoreReadsLatestDefinition(t *testing.T) {
	a := newManualSessionTestApp(t)
	if _, err := a.SaveAgentPreset("", "user", "review", "PERSONA-FIRST"); err != nil {
		t.Fatal(err)
	}
	tab, view := manualAgentPresetTab(t, a, "review")
	old := a.controllerForTab(tab)
	if _, err := a.SaveAgentPreset(tab.ID, "user", "review", "PERSONA-LATEST"); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if done, ok := old.(interface{ Closed() <-chan struct{} }); ok {
		<-done.Closed()
	}
	a.mu.Lock()
	tab.Ctrl, tab.Ready = nil, false
	a.mu.Unlock()
	a.buildTabController(tab)
	next, ok := a.controllerForTab(tab).(*control.Controller)
	if !ok {
		t.Fatalf("restore failed: %s", tab.StartupErr)
	}
	if next.AgentPreset() != "review" || !strings.Contains(next.SystemPrompt(), "PERSONA-LATEST") || strings.Contains(next.SystemPrompt(), "PERSONA-FIRST") {
		t.Fatal("restore did not load latest definition")
	}
	snapshot, err := a.desktopSessionService("").Query().Snapshot(t.Context(), view.Ref)
	if err != nil || snapshot.Projection.AgentPreset != "review" {
		t.Fatalf("restore identity = %q, %v", snapshot.Projection.AgentPreset, err)
	}
}
