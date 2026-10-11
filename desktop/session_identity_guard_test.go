package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHiddenTabPruneRejectsCanonicalIdentityConflict(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	root := t.TempDir()
	ref, workspaceID := createCurrentRegistrySession(t, app, root, "session-A", true)
	before, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"session-id:session-B", "session-id:bad/id", filepath.Join(t.TempDir(), "session-id:session-B"), filepath.Join(t.TempDir(), "legacy.jsonl")} {
		t.Run(path, func(t *testing.T) {
			tab := &WorkspaceTab{ID: "conflict", Scope: "project", WorkspaceRoot: root, SessionWorkspace: desktopTabWorkspace{ID: workspaceID}, SessionID: ref.SessionID, SessionPath: path}
			app.tabs[tab.ID] = tab
			for _, save := range []func() error{
				func() error { return app.persistHiddenTabBeforePrune(tab.ID, tab) },
				func() error { return app.saveTabSessionMeta(tab, sessionRoute(ref.SessionID)) },
			} {
				var identityErr *sessionLocatorError
				if err := save(); !errors.As(err, &identityErr) {
					t.Fatalf("conflicting identity accepted: %v", err)
				}
			}
			if tab.SessionID != ref.SessionID || tab.SessionPath != path {
				t.Fatal("failed persistence changed tab identity")
			}
		})
	}
	after, err := app.workspaceRegistry().Load(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed prune mutated registry: %v", err)
	}
}
