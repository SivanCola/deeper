package main

import (
	"context"
	"sync"
	"testing"
)

func TestSwitchWorkspaceReusesInitialConversation(t *testing.T) {
	isolateDesktopUserDirs(t)
	a := NewApp()
	installNoopRuntimeEvents(a)
	root, other := t.TempDir(), t.TempDir()
	t.Cleanup(func() { a.shutdown(context.Background()) })
	for _, dir := range []string{root, root, other, root} {
		if _, err := a.SwitchWorkspace(dir); err != nil {
			t.Fatal(err)
		}
	}
	page, err := a.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 50})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("opening/retrying the same workspace: %+v, %v", page, err)
	}
	ref := *page.Items[0].Session
	a.shutdown(context.Background())
	reopened := NewApp()
	installNoopRuntimeEvents(reopened)
	t.Cleanup(func() { reopened.shutdown(context.Background()) })
	if _, err := reopened.SwitchWorkspace(root); err != nil {
		t.Fatal(err)
	}
	page, err = reopened.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].Session == nil || *page.Items[0].Session != ref {
		t.Fatalf("reopening after restart changed conversation: %+v, %v", page, err)
	}
}

func TestSwitchWorkspaceConcurrentOpenCreatesOneConversation(t *testing.T) {
	isolateDesktopUserDirs(t)
	a := NewApp()
	installNoopRuntimeEvents(a)
	root := t.TempDir()
	t.Cleanup(func() { a.shutdown(context.Background()) })
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, _ = a.SwitchWorkspace(root) })
	}
	wg.Wait()
	page, err := a.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].Session == nil {
		t.Fatalf("concurrent workspace open: %+v, %v", page, err)
	}
}
