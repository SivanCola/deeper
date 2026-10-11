package workspacestate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionTopicSurvivesReopenWithoutOverwritingPresentation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStore(path)
	ctx := t.Context()
	if err := store.EnsureWorkspace(ctx, Workspace{ID: "global", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachSession(ctx, "", "global", "session", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSessionTopic(ctx, "session", "topic", "Initial"); err != nil {
		t.Fatal(err)
	}
	title, pinned := "Edited", true
	if err := store.UpdatePresentation(ctx, []string{"session"}, &title, &pinned); err != nil {
		t.Fatal(err)
	}
	reopened := NewStore(path)
	if err := reopened.EnsureSessionTopic(ctx, "session", "stale-tab-topic", "Stale"); err != nil {
		t.Fatal(err)
	}
	state, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	value := state.Presentation["session"]
	if value.TopicID != "topic" || value.Title != title || !value.Pinned {
		t.Fatalf("presentation overwritten: %+v", value)
	}
}

func TestPurgeTombstoneAndRestoreAreOrderedByDurableCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	first, second := NewStore(path), NewStore(path)
	ctx := t.Context()
	if err := first.EnsureWorkspace(ctx, Workspace{ID: GlobalWorkspaceID, Visible: true}); err != nil {
		t.Fatal(err)
	}
	if err := first.AttachSession(ctx, "", GlobalWorkspaceID, "victim", ""); err != nil {
		t.Fatal(err)
	}
	if err := first.ArchiveSession(ctx, "victim"); err != nil {
		t.Fatal(err)
	}
	state, err := first.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.BeginPurge(ctx, "victim", state.Generation); err != nil {
		t.Fatal(err)
	}
	state, err = second.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionStates["victim"].Lifecycle != Deleted || ClassifyPurge(state, "victim") != PurgeTombstoned {
		t.Fatalf("purge commit was not atomic: lifecycle=%+v purge=%v", state.SessionStates["victim"], ClassifyPurge(state, "victim"))
	}
	if err := second.RestoreSession(ctx, "victim"); !errors.Is(err, ErrMutationConflict) {
		t.Fatalf("restore after tombstone = %v, want mutation conflict", err)
	}
}

func TestPurgeIgnoresUnrelatedSessionGeneration(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	ctx := t.Context()
	if err := store.EnsureWorkspace(ctx, Workspace{ID: GlobalWorkspaceID, Visible: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"victim", "other"} {
		if err := store.AttachSession(ctx, "", GlobalWorkspaceID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ArchiveSession(ctx, "victim"); err != nil {
		t.Fatal(err)
	}
	observed, _ := store.Load(ctx)
	if err := store.ArchiveSession(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPurge(ctx, "victim", observed.Generation); err != nil {
		t.Fatalf("unrelated lifecycle change blocked purge: %v", err)
	}
}

func TestV2ArchiveBatchFailureDoesNotChangeOtherSessions(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.EnsureWorkspace(t.Context(), Workspace{ID: "global"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachSession(t.Context(), "", "global", "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLifecycle(t.Context(), []string{"a", "missing"}, Archived); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("error=%v", err)
	}
	state, _ := store.Load(t.Context())
	if state.SessionStates["a"].Lifecycle != Active {
		t.Fatal("partial archive committed")
	}
}

func TestV2RejectsUnknownLifecycleWithoutRewriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	body := []byte(`{"version":2,"workspaceIds":["global"],"workspaces":{"global":{"id":"global","sessionIds":["a"]}},"sessionStates":{"a":{"lifecycle":"future"}}}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).Load(t.Context()); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("error=%v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(body) {
		t.Fatal("unknown lifecycle rewritten")
	}
}

func TestV2MissingLifecycleIsNotReinterpretedAsActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"workspaceIds":["global"],"workspaces":{"global":{"id":"global","sessionIds":["a"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).Load(t.Context()); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("missing state=%v", err)
	}
}
