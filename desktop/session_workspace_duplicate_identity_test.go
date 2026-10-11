package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/config"
)

// The repair removes the losing record, while a saved tab can still name it.
// That tab must be restored against the directory's surviving owner instead of
// being dropped as a stale presentation of a workspace that no longer exists.
func TestSavedTabNamingAbsorbedWorkspaceIsRestored(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	finishSavedTabRecovery(app)
	root, err := ensureGlobalWorkspaceRoot()
	if err != nil {
		t.Fatal(err)
	}
	// Registered while Global has no record: the project record takes the
	// directory, the way a build without physical identity left it.
	ref, duplicate := createCurrentRegistrySession(t, app, root, "kept-session", true)
	if duplicate == workspacestate.GlobalWorkspaceID {
		t.Fatalf("fixture did not register a separate project record: %q", duplicate)
	}
	path := config.DesktopWorkspaceStatePath()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	document["workspaceIds"] = append(document["workspaceIds"].([]any), workspacestate.GlobalWorkspaceID)
	document["workspaces"].(map[string]any)[workspacestate.GlobalWorkspaceID] = map[string]any{
		"id": workspacestate.GlobalWorkspaceID, "root": root,
		"title": "Global", "visible": true, "sessionIds": []string{},
	}
	if body, err = json.Marshal(document); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	if owner, err := app.ensureDesktopWorkspace(t.Context(), "global", ""); err != nil || owner != workspacestate.GlobalWorkspaceID {
		t.Fatalf("repair resolved %q: %v", owner, err)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, stranded := state.Workspaces[duplicate]; stranded || !slices.Contains(state.Workspaces[workspacestate.GlobalWorkspaceID].SessionIDs, ref.SessionID) {
		t.Fatalf("session did not move to the surviving owner: %+v", state.Workspaces)
	}
	file := desktopTabsFile{Tabs: []desktopTabEntry{savedProjectTab("kept", ref.SessionID, "", root, duplicate)}, ActiveTab: "kept"}
	got, _ := app.reconcileSavedTabs(t.Context(), file)
	if len(got.Tabs) != 1 || got.Tabs[0].SessionID != ref.SessionID {
		t.Fatalf("saved tab naming the absorbed workspace was dropped: %+v", got.Tabs)
	}
}
