package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/session"
)

func TestSessionSummaryUsesDirectoryProjectionWithoutCatalog(t *testing.T) {
	app, root, refs := canonicalOrganizationFixtureWithTopic(t, "shared", "a", "b")
	for id, ref := range refs {
		if err := app.desktopSessionService("").SetTitle(t.Context(), ref, "Title "+id); err != nil {
			t.Fatal(err)
		}
	}
	ref := refs["b"]
	if err := app.SetSessionPinned(SessionSelector{Ref: &ref}, true); err != nil {
		t.Fatal(err)
	}
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		summary, err := app.GetSessionSummary(*row.Session)
		if err != nil {
			t.Fatal(err)
		}
		summaryJSON, _ := json.Marshal(summary)
		rowJSON, _ := json.Marshal(row)
		if !bytes.Equal(summaryJSON, rowJSON) {
			t.Fatalf("summary differs from directory:\n%+v\n%+v", summary, row)
		}
	}
	if len(page.Items) != 2 {
		t.Fatalf("shared topic collapsed sessions: %+v", page.Items)
	}
}

func TestSessionSummaryIgnoresStaleTopicCatalogAndPreservesData(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "global", "")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{SessionID: "global-summary", CWD: globalWorkspaceRoot(), Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	ref := runtime.Ref()
	if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, ref.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	if err := app.desktopSessionService("").Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), ref.SessionID, "manual-topic", ""); err != nil {
		t.Fatal(err)
	}
	if err := app.desktopSessionService("").SetTitle(t.Context(), ref, "你是谁"); err != nil {
		t.Fatal(err)
	}
	if err := setTopicTitle("", "manual-topic", defaultTopicTitle); err != nil {
		t.Fatal(err)
	}
	installSessionCatalogForTest(t, app, desktopSessionDir(globalWorkspaceRoot()), "global", "")
	paths := []string{config.DesktopWorkspaceStatePath(), filepath.Join(app.desktopSessions.root, ref.SessionID, "header.json"), filepath.Join(app.desktopSessions.root, ref.SessionID, "events.frames")}
	before := map[string]string{}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = string(b)
	}
	for range 3 {
		summary, err := app.GetSessionSummary(ref)
		if err != nil {
			t.Fatal(err)
		}
		if summary.Session == nil || *summary.Session != ref || summary.Label != "你是谁" || summary.SessionPath != sessionRoute(ref.SessionID) || summary.LifecycleGeneration == 0 {
			t.Fatalf("stale topic changed canonical summary: %+v", summary)
		}
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != before[path] {
			t.Fatalf("summary mutated %s: %v", path, err)
		}
	}
}

func TestSessionSummaryRejectsMissingArchivedAndRemoteIdentities(t *testing.T) {
	app, _, refs := canonicalOrganizationFixtureWithTopic(t, "shared", "a")
	ref := refs["a"]
	if _, err := app.GetSessionSummary(session.SessionRef{HostID: "local", SessionID: "missing"}); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("missing summary: %v", err)
	}
	if _, err := app.GetSessionSummary(session.SessionRef{HostID: "other-host", SessionID: ref.SessionID}); err == nil {
		t.Fatal("remote identity was resolved locally")
	}
	if err := app.ArchiveCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetSessionSummary(ref); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("archive resurrected summary: %v", err)
	}
}
