package main

import (
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestListProjectTopicsPaginatesCustomGroupsIndependently(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Grouped project"); err != nil {
		t.Fatal(err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	for _, id := range []string{"group-a", "group-b", "ungrouped", "pinned"} {
		ref, _ := createCurrentRegistrySession(t, app, root, id, true)
		if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), ref.SessionID, id, id); err != nil {
			t.Fatal(err)
		}
		if err := app.desktopSessionService("").SetTitle(t.Context(), ref, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.SetTopicPinned("pinned", true); err != nil {
		t.Fatal(err)
	}
	if err := setSessionGroupsForTest(t, app, "project", root, []desktopGroup{
		{ID: "feature", Title: "Feature", SessionKeys: []string{workspacestate.SessionKey("group-a"), workspacestate.SessionKey("group-b"), workspacestate.SessionKey("pinned")}},
		{ID: "empty", Title: "Empty", SessionKeys: []string{workspacestate.SessionKey("missing")}},
	}); err != nil {
		t.Fatal(err)
	}

	first, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 1,
		GroupFilter: "group", GroupID: "feature", ExcludePinned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].TopicID != "group-b" || first.NextCursor == "" {
		t.Fatalf("first group page = %#v, want group-b and a next cursor", first)
	}
	second, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 1, Cursor: first.NextCursor,
		GroupFilter: "group", GroupID: "feature", ExcludePinned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].TopicID != "group-a" || second.NextCursor != "" {
		t.Fatalf("second group page = %#v, want group-a as final row", second)
	}

	ungrouped, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 10, GroupFilter: "ungrouped", ExcludePinned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ungrouped.Items) != 1 || ungrouped.Items[0].TopicID != "ungrouped" {
		t.Fatalf("ungrouped page = %#v, want only ungrouped", ungrouped)
	}
	empty, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 10, GroupFilter: "group", GroupID: "empty",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty group page = %#v, want empty", empty)
	}
	if _, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, GroupFilter: "group", GroupID: "deleted",
	}); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("missing group error = %v, want explicit invalid-group error", err)
	}

	legacy, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Items) != 4 {
		t.Fatalf("legacy unfiltered page has %d rows, want 4", len(legacy.Items))
	}

	if err := setSessionGroupsForTest(t, app, "project", root, []desktopGroup{{
		ID: "feature", Title: "Feature", SessionKeys: []string{workspacestate.SessionKey("group-a")},
	}}); err != nil {
		t.Fatal(err)
	}
	if frozen, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 1, Cursor: first.NextCursor,
		GroupFilter: "group", GroupID: "feature", ExcludePinned: true,
	}); err != nil || frozen.SnapshotID != first.SnapshotID || len(frozen.Items) != 1 {
		t.Fatalf("group edit disturbed the frozen read: %+v %v", frozen, err)
	}
}

func TestOrganizationGroupFilterSupportsMaximumSizedGroup(t *testing.T) {
	members := make([]string, maxSessionGroupTopics)
	for index := range members {
		members[index] = workspacestate.SessionKey(fmt.Sprintf("session-%05d", index))
	}
	org := workspacestate.Organization{Groups: []workspacestate.OrganizationGroup{{ID: "large", Title: "Large", Members: members}}}
	req := ProjectTopicPageRequest{GroupFilter: "group", GroupID: "large"}
	if err := applyOrganizationGroupFilter(&req, org); err != nil {
		t.Fatal(err)
	}
	if len(req.groupSelected.SessionKeys) != maxSessionGroupTopics {
		t.Fatal("group members were truncated")
	}
	for _, id := range []string{"session-00000", "session-09999"} {
		if !projectNodeRequestAllows(req, ProjectNode{Session: &session.SessionRef{HostID: "local", SessionID: id}}) {
			t.Fatalf("member %s is missing", id)
		}
	}
}

func TestProjectTopicCursorRejectsDifferentListIdentity(t *testing.T) {
	app, root, _ := canonicalOrganizationFixture(t, "a", "b")
	base := ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 1}
	first, err := app.ListProjectTopics(base)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	base.Cursor = first.NextCursor
	variants := []ProjectTopicPageRequest{base, base, base}
	variants[0].Query = "other"
	variants[1].SortMode = "created"
	variants[2].GroupFilter = "ungrouped"
	for _, req := range variants {
		if _, err := app.ListProjectTopics(req); err == nil {
			t.Fatalf("cursor accepted different list identity: %+v", req)
		}
	}
	if _, err := app.ListProjectTopics(base); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataFallbackBindsGroupCursorToMembershipRevision(t *testing.T) {
	app, root, _ := canonicalOrganizationFixture(t, "a", "b", "c")
	if err := setSessionGroupsForTest(t, app, "project", root, []desktopGroup{{
		ID: "feature", Title: "Feature", SessionKeys: []string{workspacestate.SessionKey("a"), workspacestate.SessionKey("b")},
	}}); err != nil {
		t.Fatal(err)
	}
	first, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 1, GroupFilter: "group", GroupID: "feature",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("metadata first page = %#v, want one row and bound cursor", first)
	}
	if err := setSessionGroupsForTest(t, app, "project", root, []desktopGroup{{
		ID: "feature", Title: "Feature", SessionKeys: []string{workspacestate.SessionKey("a")},
	}}); err != nil {
		t.Fatal(err)
	}
	if frozen, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 1, Cursor: first.NextCursor,
		GroupFilter: "group", GroupID: "feature",
	}); err != nil || frozen.SnapshotID != first.SnapshotID || len(frozen.Items) != 1 {
		t.Fatalf("metadata group edit disturbed the frozen read: %+v %v", frozen, err)
	}
}

func TestSessionGroupCanSplitRowsSharingTopicID(t *testing.T) {
	app, root, refs := canonicalOrganizationFixtureWithTopic(t, "shared", "a", "b")
	aRef := refs["a"]
	a := ProjectNode{Key: "a", Kind: "topic", TopicID: "shared", Session: &aRef}
	group := desktopGroup{
		ID: "one", Title: "One", SessionKeys: []string{projectNodeSessionKey(a)},
	}
	if err := setSessionGroupsForTest(t, app, "project", root, []desktopGroup{group}); err != nil {
		t.Fatal(err)
	}
	grouped, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, GroupFilter: "group", GroupID: "one", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Items) != 1 || grouped.Items[0].Session == nil || grouped.Items[0].Session.SessionID != "a" {
		t.Fatalf("group membership did not isolate A: %#v", grouped.Items)
	}
	ungrouped, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, GroupFilter: "ungrouped", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(ungrouped.Items) != 1 || ungrouped.Items[0].Session == nil || ungrouped.Items[0].Session.SessionID != "b" {
		t.Fatalf("ungrouped membership did not isolate B: %#v", ungrouped.Items)
	}
}
