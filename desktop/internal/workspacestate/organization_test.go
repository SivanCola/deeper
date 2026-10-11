package workspacestate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOrganizationLegacyMoveUpdatesAuthoritativeOrder(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "registry.json"))
	if err := store.EnsureWorkspace(t.Context(), Workspace{ID: GlobalWorkspaceID}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := store.AttachSession(t.Context(), "", GlobalWorkspaceID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := store.UpdateOrganization(t.Context(), GlobalWorkspaceID, nil, func(o *Organization) error {
		o.Order = []string{SessionKey("a"), SessionKey("b")}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveSession(t.Context(), GlobalWorkspaceID, "b", "a"); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	o := state.Workspaces[GlobalWorkspaceID].Organization
	if !o.ManualOrderEnabled || !reflect.DeepEqual(o.Order, []string{SessionKey("b"), SessionKey("a")}) {
		t.Fatalf("organization did not follow move: %+v", o)
	}
}

func TestOrganizationCASRejectsStaleWriterWithoutOverwritingWinner(t *testing.T) {
	store := organizationTestStore(t)
	revision := uint64(0)
	winner, applied, err := store.UpdateOrganization(t.Context(), GlobalWorkspaceID, &revision, func(o *Organization) error {
		o.Groups = []OrganizationGroup{{ID: "winner", Title: "Other window", Members: []string{}}}
		return nil
	})
	if err != nil || !applied {
		t.Fatalf("first writer: applied=%v err=%v", applied, err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	invoked := false
	latest, applied, err := NewStore(store.Path()).UpdateOrganization(t.Context(), GlobalWorkspaceID, &revision, func(o *Organization) error {
		invoked = true
		o.Groups = []OrganizationGroup{{ID: "loser", Title: "Stale window"}}
		return nil
	})
	if err != nil || applied || invoked {
		t.Fatalf("stale writer: applied=%v invoked=%v err=%v", applied, invoked, err)
	}
	latestJSON, _ := json.Marshal(latest)
	winnerJSON, _ := json.Marshal(winner)
	if !bytes.Equal(latestJSON, winnerJSON) {
		t.Fatalf("conflict snapshot=%#v, want winner=%#v", latest, winner)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("conflicting organization update changed the registry")
	}
}

func TestOrganizationForkAttachmentInheritsGroupAndFollowsParent(t *testing.T) {
	store := organizationTestStore(t)
	ctx := t.Context()
	for _, id := range []string{"parent", "sibling"} {
		if err := store.AttachSession(ctx, "", GlobalWorkspaceID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := store.UpdateOrganization(ctx, GlobalWorkspaceID, nil, func(o *Organization) error {
		o.ManualOrderEnabled = true
		o.Order = []string{SessionKey("parent"), SessionKey("sibling")}
		o.Groups = []OrganizationGroup{{ID: "feature", Title: "Feature", Members: []string{SessionKey("parent")}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parentGeneration := state.SessionStates["parent"].Generation
	if err := store.BeginCreate(ctx, PendingCreate{OperationID: "fork", WorkspaceID: GlobalWorkspaceID, SessionID: "child"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachSessionFromSourceIfUnchanged(ctx, "fork", GlobalWorkspaceID, "child", "sibling", "parent", parentGeneration); err != nil {
		t.Fatal(err)
	}
	state, err = NewStore(store.Path()).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[GlobalWorkspaceID]
	assertStrings(t, workspace.Organization.Order, []string{SessionKey("parent"), SessionKey("child"), SessionKey("sibling")})
	assertStrings(t, workspace.Organization.Groups[0].Members, []string{SessionKey("parent"), SessionKey("child")})
	assertStrings(t, workspace.SessionIDs, []string{"parent", "child", "sibling"})
	revision := workspace.Organization.Revision
	if err := store.AttachSessionFromSourceIfUnchanged(ctx, "fork", GlobalWorkspaceID, "child", "sibling", "parent", parentGeneration); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Workspaces[GlobalWorkspaceID].Organization.Revision != revision {
		t.Fatal("fork retry changed organization or duplicated its child")
	}
}

func TestOrganizationFailedMutationLeavesOrderAndRevisionUnchanged(t *testing.T) {
	store := organizationTestStore(t)
	ctx := t.Context()
	if _, _, err := store.UpdateOrganization(ctx, GlobalWorkspaceID, nil, func(o *Organization) error {
		o.Order = []string{"source\x00local\x00before"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("failed validation")
	if _, applied, err := store.UpdateOrganization(ctx, GlobalWorkspaceID, nil, func(o *Organization) error {
		o.Order = []string{"source\x00local\x00after"}
		return failure
	}); !errors.Is(err, failure) || applied {
		t.Fatalf("failed mutation: applied=%v err=%v", applied, err)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed callback partially published organization")
	}
}

func TestOrganizationForkPreservesActivitySortWithoutEnablingManualOrder(t *testing.T) {
	store := organizationTestStore(t)
	ctx := t.Context()
	if err := store.AttachSession(ctx, "", GlobalWorkspaceID, "parent", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateOrganization(ctx, GlobalWorkspaceID, nil, func(o *Organization) error {
		o.ManualOrderEnabled = false
		o.Order = []string{SessionKey("parent")}
		o.Groups = []OrganizationGroup{{ID: "feature", Title: "Feature", Members: []string{SessionKey("parent")}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginCreate(ctx, PendingCreate{OperationID: "fork-activity", WorkspaceID: GlobalWorkspaceID, SessionID: "child"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachSessionFromSourceIfUnchanged(ctx, "fork-activity", GlobalWorkspaceID, "child", "", "parent", state.SessionStates["parent"].Generation); err != nil {
		t.Fatal(err)
	}
	state, err = NewStore(store.Path()).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o := state.Workspaces[GlobalWorkspaceID].Organization
	if o.ManualOrderEnabled {
		t.Fatal("forking implicitly changed activity sorting to manual order")
	}
	assertStrings(t, o.Groups[0].Members, []string{SessionKey("parent"), SessionKey("child")})
}

func TestManualOrderPlacesNewSessionAtTheHead(t *testing.T) {
	store := organizationTestStore(t)
	ctx := t.Context()
	if _, _, err := store.UpdateOrganization(ctx, GlobalWorkspaceID, nil, func(*Organization) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"older", "newer"} {
		if err := store.AttachSession(ctx, "", GlobalWorkspaceID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MoveSession(ctx, GlobalWorkspaceID, "newer", "older"); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachSession(ctx, "", GlobalWorkspaceID, "fresh", ""); err != nil {
		t.Fatal(err)
	}
	state, err := NewStore(store.Path()).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o := state.Workspaces[GlobalWorkspaceID].Organization
	assertStrings(t, o.Order, []string{SessionKey("fresh"), SessionKey("newer"), SessionKey("older")})
	assertStrings(t, state.Workspaces[GlobalWorkspaceID].SessionIDs, []string{"fresh", "newer", "older"})
}

func organizationTestStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "registry.json"))
	if err := store.EnsureWorkspace(t.Context(), Workspace{ID: GlobalWorkspaceID, Visible: true}); err != nil {
		t.Fatal(err)
	}
	return store
}
