package main

import "testing"

func TestRenameSessionResolvesVersionOutsideActiveProject(t *testing.T) {
	app, tab, target, rootB, _ := canonicalWorkspaceOpenFixture(t)
	sourceTitle := tab.TopicTitle
	if err := app.RenameSession(sessionRoute(target.Ref().SessionID), "Project B version note"); err != nil {
		t.Fatal(err)
	}
	info, err := app.desktopSessionService("").Query().Stat(t.Context(), target.Ref())
	if err != nil || info.Title != "Project B version note" {
		t.Fatalf("target title: %+v %v", info, err)
	}
	if tab.TopicTitle != sourceTitle {
		t.Fatal("active project title changed")
	}
	if got := sidebarLabelForSession(t, app, rootB, target.Ref()); got != "Project B version note" {
		t.Fatalf("target sidebar: %q", got)
	}
}
