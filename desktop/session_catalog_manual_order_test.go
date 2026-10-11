package main

import (
	"slices"
	"testing"
)

func TestManualTopicOrderIsAuthoritativeAcrossCatalogPages(t *testing.T) {
	app, root, refs := canonicalOrganizationFixture(t, "a", "b", "c")
	workspace := SessionOrganizationWorkspace{Scope: "project", WorkspaceRoot: root}
	org, err := app.GetSessionOrganization(workspace)
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := refs["a"], refs["b"], refs["c"]
	target, anchor := SessionSelector{Ref: &c}, SessionSelector{Ref: &a}
	org, err = app.UpdateSessionOrganization(workspace, org.Revision, SessionOrganizationMutation{Kind: "move", Target: &target, Anchor: &anchor, Position: "before"})
	if err != nil {
		t.Fatal(err)
	}
	target, anchor = SessionSelector{Ref: &b}, SessionSelector{Ref: &a}
	if _, err = app.UpdateSessionOrganization(workspace, org.Revision, SessionOrganizationMutation{Kind: "move", Target: &target, Anchor: &anchor, Position: "before"}); err != nil {
		t.Fatal(err)
	}
	first, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || len(second.Items) != 1 || first.NextCursor == "" || second.NextCursor != "" {
		t.Fatalf("pages = %+v %+v", first, second)
	}
	got := []string{first.Items[0].Session.SessionID, first.Items[1].Session.SessionID, second.Items[0].Session.SessionID}
	if !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Fatalf("manual order = %v", got)
	}
}
