package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestHiddenTabPruneRejectsInvalidCurrentIdentity(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	root := t.TempDir()
	_, workspaceID := createCurrentRegistrySession(t, app, root, "session-A", true)
	before, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bad/id", "..", `bad\id`, "CON"} {
		t.Run(id, func(t *testing.T) {
			tab := &WorkspaceTab{ID: "conflict", Scope: "project", WorkspaceRoot: root, SessionWorkspace: desktopTabWorkspace{ID: workspaceID}, SessionID: id}
			app.tabs[tab.ID] = tab
			for _, save := range []func() error{
				func() error { return app.persistHiddenTabBeforePrune(tab.ID, tab) },
			} {
				var identityErr *sessionLocatorError
				if err := save(); !errors.As(err, &identityErr) {
					t.Fatalf("conflicting identity accepted: %v", err)
				}
			}
			if tab.SessionID != id {
				t.Fatal("failed persistence changed tab identity")
			}
		})
	}
	after, err := app.workspaceRegistry().Load(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed prune mutated registry: %v", err)
	}
}
