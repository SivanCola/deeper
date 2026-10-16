package main

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"deepzora/internal/session"
)

func TestForkForTabCreatesIndependentCurrentSession(t *testing.T) {
	app, tab, ctrl, _ := currentClearFixture(t, workspaceContextProvider{}, nil)
	t.Cleanup(func() { app.shutdown(context.Background()) })
	tab.TopicTitle = "Source topic"
	tab.setPinnedFiles([]string{"README.md", "docs/plan.md"})
	if err := ctrl.RunTurn(t.Context(), "first needle"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RunTurn(t.Context(), "second needle"); err != nil {
		t.Fatal(err)
	}
	parent, _ := ctrl.SessionRef()
	service := ctrl.SessionService()
	before, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := app.ForkForTab(tab.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID == "" || meta.ID == tab.ID || !meta.Active || meta.SessionID == "" || meta.SessionID == parent.SessionID || meta.SessionPath != "" {
		t.Fatalf("fork meta = %+v, want an independent current identity", meta)
	}
	if meta.TopicTitle != "Source topic (1)" {
		t.Fatalf("fork title = %q", meta.TopicTitle)
	}
	waitForForkHistoryForTest(t, app, meta.ID)
	app.mu.RLock()
	childTab := app.tabs[meta.ID]
	app.mu.RUnlock()
	if childTab == nil || !reflect.DeepEqual(childTab.pinnedFilesState(), tab.pinnedFilesState()) {
		t.Fatalf("fork lost pinned context: child=%v source=%v", childTab.pinnedFilesState(), tab.pinnedFilesState())
	}
	pins, err := loadPinnedContextState(desktopPinnedContextPath(meta.SessionID))
	if err != nil || !reflect.DeepEqual(pins.Files, tab.pinnedFilesState()) {
		t.Fatalf("fork pins are not durable under the child identity: %+v, %v", pins, err)
	}
	if got, _ := ctrl.SessionRef(); got != parent || len(app.tabs) != 2 {
		t.Fatal("fork replaced parent tab")
	}
	after, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil || after.EventSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatalf("fork changed parent history: %v", err)
	}
	child := session.SessionRef{HostID: service.HostID(), SessionID: meta.SessionID}
	forked, err := service.Query().Snapshot(t.Context(), child)
	if err != nil {
		t.Fatal(err)
	}
	if len(forked.Projection.Turns) != 1 || forked.Projection.Turns[0].TurnID != before.Projection.Turns[0].TurnID {
		t.Fatal("fork did not retain the exact selected turn")
	}
	info, err := service.Query().Stat(t.Context(), child)
	if err != nil || info.ParentSessionID != parent.SessionID {
		t.Fatalf("child lineage: %+v, %v", info, err)
	}
	parentReader, err := app.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer app.ReleaseSessionHistoryRead(parentReader.ID)
	childReader, err := app.BeginSessionHistoryReadForTab(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer app.ReleaseSessionHistoryRead(childReader.ID)
	search := func(id, cursor string) session.SearchHistoryPage {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			page, err := app.SearchSessionHistoryRead(id, "needle", cursor, 1)
			if err != nil {
				t.Fatal(err)
			}
			if page.Status != "preparing" {
				return page
			}
			select {
			case <-deadline:
				t.Fatal("current history search did not finish")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	parentPage := search(parentReader.ID, "")
	if parentPage.Status != "ready" || len(parentPage.Hits) != 1 || parentPage.Hits[0].Preview != "second needle" || !parentPage.HasMore {
		t.Fatalf("parent search = %+v", parentPage)
	}
	childPage := search(childReader.ID, "")
	if childPage.Status != "ready" || len(childPage.Hits) != 1 || childPage.Hits[0].Preview != "first needle" || childPage.HasMore {
		t.Fatalf("child search escaped its selected turn = %+v", childPage)
	}
	if page := search(childReader.ID, parentPage.NextCursor); page.Status != "stale_cursor" || len(page.Hits) != 0 {
		t.Fatalf("child accepted parent search cursor = %+v", page)
	}
}

func TestCommitRewindForTabOpensIndependentChild(t *testing.T) {
	fx := newCurrentRewindFixture(t)
	before := fx.snapshot(t, fx.ref)
	plan := fx.app.PreviewRewindForTab("test", 1, "both")
	if !plan.OK || !plan.CanFiles || !plan.CanConversation {
		t.Fatalf("preview = %+v", plan)
	}
	result := fx.app.CommitRewindForTab("test", plan.PlanID, 1, "both")
	if !result.OK || !result.ConversationForked || result.Branch == "" || result.Branch == fx.ref.SessionID {
		t.Fatalf("commit = %+v", result)
	}
	if result.TabID == "" || result.TabID == "test" || result.Tab == nil || result.Tab.ID != result.TabID || result.Tab.SessionID != result.Branch || result.Tab.SessionPath != "" {
		t.Fatalf("child tab = %+v", result)
	}
	if got, err := os.ReadFile(fx.filePath); err != nil || string(got) != "before" {
		t.Fatalf("file after commit = %q, %v", got, err)
	}
	after := fx.snapshot(t, fx.ref)
	if after.EventSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatal("rewind altered parent history")
	}
	if got, _ := fx.ctrl.SessionRef(); got != fx.ref || len(fx.app.tabs) != 2 {
		t.Fatal("rewind replaced parent identity")
	}
	child := fx.snapshot(t, session.SessionRef{HostID: fx.ref.HostID, SessionID: result.Branch})
	if len(child.Projection.Turns) != 1 || child.Projection.Turns[0].TurnID != before.Projection.Turns[0].TurnID {
		t.Fatal("child did not stop before edit turn")
	}
	if undo := fx.app.UndoRewindForTab("test", result.TransactionID); !undo.OK {
		t.Fatalf("undo = %+v", undo)
	}
	if got, err := os.ReadFile(fx.filePath); err != nil || string(got) != "after" {
		t.Fatalf("file after undo = %q, %v", got, err)
	}
	afterUndo := fx.snapshot(t, fx.ref)
	if !reflect.DeepEqual(afterUndo.Projection.Messages, before.Projection.Messages) {
		t.Fatal("undo changed parent messages")
	}
}
