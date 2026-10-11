package main

import (
	"os"
	"strings"
	"testing"

	"deepzora/desktop/internal/instanceidentity"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/config"
	"deepzora/internal/provider"
)

func TestGlobalWorkspaceSurvivesWindowsUpdateEnvironment(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPZORA_HOME", "")
	t.Setenv("DEEPZORA_STATE_HOME", "")
	normalHome := config.DeepzoraHomeDir()
	updateHome := ""
	for _, entry := range instanceidentity.UpdateEnvironment(os.Environ(), normalHome) {
		if value, ok := strings.CutPrefix(entry, "DEEPZORA_HOME="); ok {
			updateHome = value
		}
	}
	if updateHome != instanceidentity.AccessHome(normalHome) {
		t.Fatalf("updater rewrote the access spelling: %q -> %q", normalHome, updateHome)
	}

	// v1.38.10 wrote the lowercase comparison key into DEEPZORA_HOME. Seed that
	// historical state, then verify the fixed ordinary launch can reuse it.
	legacyUpdateHome := strings.ToLower(updateHome)
	if legacyUpdateHome == normalHome || !strings.EqualFold(legacyUpdateHome, normalHome) {
		t.Fatalf("fixture must exercise legacy update/normal casing: %q -> %q", normalHome, legacyUpdateHome)
	}
	t.Setenv("DEEPZORA_HOME", legacyUpdateHome)
	first := NewApp()
	t.Cleanup(first.closeSessionServices)
	service := first.desktopSessionService("")
	ref, workspaceID, err := first.bindFreshDesktopSession(t.Context(), "global", "", registrySessionCreator{service})
	if err != nil {
		t.Fatal(err)
	}
	live, ok := service.Runtime(ref)
	if !ok {
		t.Fatal("new session runtime missing")
	}
	appendSessionTestMessage(t, live, "saved-turn", provider.Message{ID: "saved-turn", Role: provider.RoleUser, Content: "history before ordinary relaunch"})
	first.closeSessionServices()

	t.Setenv("DEEPZORA_HOME", "")
	second := NewApp()
	t.Cleanup(second.closeSessionServices)
	if id, err := second.attachDesktopSession(t.Context(), "global", "", ref); err != nil || id != workspaceID {
		t.Fatalf("reopen after update: workspace=%q, err=%v", id, err)
	}
	page, err := second.ReadSessionHistory(ref, "", 10)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Content != "history before ordinary relaunch" {
		t.Fatalf("reopened history=%+v, err=%v", page, err)
	}
	if _, err := second.canonicalSessionWorkspace(t.Context(), ref); err != nil {
		t.Fatalf("reopened workspace/header membership: %v", err)
	}
	newRef, newWorkspaceID, err := second.bindFreshDesktopSession(t.Context(), "global", "", registrySessionCreator{second.desktopSessionService("")})
	if err != nil || newWorkspaceID != workspaceID || newRef == ref {
		t.Fatalf("new session after update: ref=%v, workspace=%q, err=%v", newRef, newWorkspaceID, err)
	}
	state, err := second.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[workspacestate.GlobalWorkspaceID]
	if len(workspace.SessionIDs) != 2 || len(state.PendingCreates) != 0 {
		t.Fatalf("relaunch lost sessions or left pending creates: %+v", state)
	}
}
