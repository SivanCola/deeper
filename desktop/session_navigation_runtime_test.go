package main

import (
	"path/filepath"
	"testing"

	"deepzora/internal/session"
)

func TestForkRuntimeLookupDoesNotRegisterSourcePathAlias(t *testing.T) {
	app, tab, ctrl, _ := currentClearFixture(t, nil, nil)
	service, parent, bound := ctrl.SessionBinding()
	if !bound {
		t.Fatal("missing source runtime")
	}
	parentDir, err := service.SessionDir(t.Context(), parent.Ref())
	if err != nil {
		t.Fatal(err)
	}
	childID := "lookup-child"
	manifest, err := parent.Session().Fork(t.Context(), filepath.Join(filepath.Dir(parentDir), childID), childID, parent.Session().ExecutionSnapshot().EventSequence)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Source == nil || manifest.Source.Path != parentDir {
		t.Fatal("fork fixture lost its source provenance")
	}
	child := session.SessionRef{HostID: service.HostID(), SessionID: childID}
	if _, err := ctrl.OpenSession(t.Context(), child); err != nil {
		t.Fatal(err)
	}
	tab.SessionID = childID
	if !sessionRuntimeKeysOverlap(tab, sessionRoute(childID)) {
		t.Fatal("child runtime is not addressable by its current identity")
	}
	for _, unrelated := range []string{parentDir, sessionRoute(parent.Ref().SessionID)} {
		if sessionRuntimeKeysOverlap(tab, unrelated) {
			t.Fatalf("child runtime still accepts source alias %q", unrelated)
		}
	}
	app.mu.Lock()
	app.registerDetachedRuntimeLocked(tab)
	aliased := app.liveRuntimeTabMatchingLocked(nil, parentDir)
	app.mu.Unlock()
	if aliased != nil {
		t.Fatal("detached child was registered under its source directory")
	}
}
