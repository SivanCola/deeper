package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
	"deepzora/internal/tool/sessiontool"
)

func TestSessionToolsFollowWorkspaceMembershipAndArchive(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.ctx = t.Context()
	app.desktopSessions.root = filepath.Join(root, "current-sessions")
	app.desktopSessions.workspaceState = workspacestate.NewStore(filepath.Join(root, "workspace.json"))
	t.Cleanup(app.closeSessionServices)
	service := app.desktopSessionService("")
	project := filepath.Join(root, "project")
	for _, item := range []struct{ id, workspace string }{
		{"visible", project}, {"foreign", filepath.Join(root, "other")},
	} {
		runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: item.id, CWD: item.workspace, Origin: session.SessionOriginNew})
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"message": map[string]string{"id": "user-1", "role": "user", "content": item.id + " content"}})
		if _, err := runtime.Session().AppendBatch(t.Context(), "seed", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Session().Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "project", item.workspace)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, item.id, ""); err != nil {
			t.Fatal(err)
		}
	}
	scope := app.sessionToolScope(desktopSessionDir(project))
	globalSources := app.globalHistorySources(desktopSessionDir(project))
	globalIDs, err := globalSources[0].Membership(t.Context())
	if err != nil || globalIDs["foreign"] || globalIDs["visible"] {
		t.Fatalf("global workspace widened to unrelated projects: %v, %v", globalIDs, err)
	}
	list := sessiontool.NewListSessionsTool(service.Query(), scope)
	read := sessiontool.NewReadSessionTool(service.Query(), scope)
	out, err := list.Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out, "visible") || strings.Contains(out, "foreign") {
		t.Fatalf("workspace listing: %q, %v", out, err)
	}
	out, err = read.Execute(t.Context(), json.RawMessage(`{"session":"visible"}`))
	if err != nil || !strings.Contains(out, "visible content") {
		t.Fatalf("current read: %q, %v", out, err)
	}
	if _, err := read.Execute(t.Context(), json.RawMessage(`{"session":"foreign"}`)); err == nil {
		t.Fatal("foreign workspace read accepted")
	}
	ref := session.SessionRef{HostID: service.HostID(), SessionID: "visible"}
	if err := app.ArchiveCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	archiveIDs, err := globalSources[1].Membership(t.Context())
	if err != nil || !archiveIDs["visible"] || archiveIDs["foreign"] {
		t.Fatalf("archive history membership: %v, %v", archiveIDs, err)
	}
	out, err = list.Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || strings.Contains(out, "visible") {
		t.Fatalf("archived session listed: %q, %v", out, err)
	}
	if _, err := read.Execute(t.Context(), json.RawMessage(`{"session":"visible"}`)); err == nil {
		t.Fatal("old tool instance read archived session")
	}
	if err := app.RestoreCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	archiveIDs, err = globalSources[1].Membership(t.Context())
	if err != nil || archiveIDs["visible"] {
		t.Fatalf("restored session remains in archive scope: %v, %v", archiveIDs, err)
	}
	if _, err := read.Execute(t.Context(), json.RawMessage(`{"session":"visible"}`)); err != nil {
		t.Fatalf("restored session remained unavailable: %v", err)
	}
}
