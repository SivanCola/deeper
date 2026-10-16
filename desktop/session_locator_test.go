package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionLocatorAcceptsOnlyCurrentRoutes(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "session-id:real.jsonl")
	tests := []struct {
		name string
		raw  string
		kind sessionLocatorKind
		id   string
	}{
		{name: "empty", raw: "  ", kind: sessionLocatorEmpty},
		{name: "canonical", raw: " session-id:valid-id ", kind: sessionLocatorCanonical, id: "valid-id"},
		{name: "empty canonical id", raw: "session-id:", kind: sessionLocatorInvalid},
		{name: "canonical separator", raw: "session-id:bad/id", kind: sessionLocatorInvalid},
		{name: "canonical reserved name", raw: "session-id:CON", kind: sessionLocatorInvalid},
		{name: "canonical control", raw: "session-id:bad\x01id", kind: sessionLocatorInvalid},
		{name: "canonical windows punctuation", raw: `session-id:bad<id>`, kind: sessionLocatorInvalid},
		{name: "canonical question mark", raw: `session-id:bad?id`, kind: sessionLocatorInvalid},
		{name: "canonical too long", raw: "session-id:" + strings.Repeat("x", 256), kind: sessionLocatorInvalid},
		{name: "legacy colon filename", raw: legacy, kind: sessionLocatorInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifySessionLocator(test.raw)
			if got.kind != test.kind || got.ref.SessionID != test.id {
				t.Fatalf("locator = kind:%v id:%q reason:%q", got.kind, got.ref.SessionID, got.reason)
			}
		})
	}
}

func TestSessionLocatorNeverCanonicalizesRouteAsPath(t *testing.T) {
	if got := sessionRuntimeKey("session-id:valid-id"); got != "session-id:valid-id" {
		t.Fatalf("canonical runtime key = %q", got)
	}
	if got := sessionRuntimeKey("session-id:bad/id"); got != "" {
		t.Fatalf("invalid route runtime key = %q", got)
	}
	if got := sessionRuntimeKey(filepath.Join(t.TempDir(), "not-a-transcript.meta")); got != "" {
		t.Fatalf("invalid legacy runtime key = %q", got)
	}
}

func TestCanonicalHiddenTabPruneSkipsLegacyMetadataWriter(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	root := t.TempDir()
	ref, workspaceID := createCurrentRegistrySession(t, app, root, "hidden-canonical", true)
	tab := &WorkspaceTab{
		ID: "hidden", Scope: "project", WorkspaceRoot: root, SessionWorkspace: desktopTabWorkspace{ID: workspaceID},
		SessionID: ref.SessionID,
	}
	app.tabs[tab.ID] = tab
	if err := app.persistHiddenTabBeforePrune(tab.ID, tab); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{"session-id:hidden-canonical.meta", "session-id:hidden-canonical.lock"} {
		if _, err := os.Lstat(artifact); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canonical hidden-tab prune created %q: %v", artifact, err)
		}
	}
}

func TestCanonicalDetachCloneAndReattachKeepExclusiveIdentity(t *testing.T) {
	source := &WorkspaceTab{ID: "source", Scope: "global", SessionID: "canonical-transfer"}
	detached := cloneDetachedRuntimeTab(source, sessionRoute("canonical-transfer"), source.currentSessionIdentity())
	if detached == nil || detached.SessionID != "canonical-transfer" {
		t.Fatalf("detached identity = id:%q path:%q", detached.SessionID, sessionRoute(detached.SessionID))
	}
	target := &WorkspaceTab{ID: "target", Scope: "global"}
	applyRuntimeTab(target, detached, sessionRoute("canonical-transfer"), context.Background(), nil)
	if target.SessionID != "canonical-transfer" {
		t.Fatalf("reattached identity = id:%q path:%q", target.SessionID, sessionRoute(target.SessionID))
	}
}
