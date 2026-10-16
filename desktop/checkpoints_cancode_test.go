package main

import (
	"deepzora/internal/session"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestDesktopRewindCommitAndUndoUseAuthoritativeControllerState(t *testing.T) {
	fx := newCurrentRewindFixture(t)
	app, ctrl := fx.app, fx.ctrl
	before := fx.snapshot(t, fx.ref)
	plan := app.PreviewRewindForTab("test", 1, "both")
	if !plan.OK || !plan.CanFiles || !plan.CanConversation {
		t.Fatalf("preview = %+v", plan)
	}
	result := app.CommitRewindForTab("test", plan.PlanID, 1, "both")
	if !result.OK || !result.UndoAvailable || result.TransactionID == "" || !result.ConversationForked || result.Tab == nil {
		t.Fatalf("commit = %+v", result)
	}
	if got, err := os.ReadFile(fx.filePath); err != nil || string(got) != "before" {
		t.Fatalf("file after commit = %q, %v", got, err)
	}
	after := fx.snapshot(t, fx.ref)
	if !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatal("commit changed parent history")
	}
	sourceView := currentHistoryMessagesForTab(t, app, "test")
	if len(sourceView) == 0 {
		t.Fatalf("parent history = %+v", sourceView)
	}
	forkTab := app.tabs[result.TabID]
	if forkTab == nil || forkTab.SessionID != result.Branch || app.activeTabID != result.TabID {
		t.Fatalf("fork tab = %+v", forkTab)
	}
	child := fx.snapshot(t, session.SessionRef{HostID: fx.ref.HostID, SessionID: result.Branch})
	var contents []string
	for _, message := range child.Projection.Messages {
		contents = append(contents, message.Content)
	}
	if !slices.Contains(contents, "first") || !slices.Contains(contents, "answer") || slices.Contains(contents, "edit") || slices.Contains(contents, "done") {
		t.Fatalf("child history = %q", contents)
	}
	if err := ctrl.RunTurn(t.Context(), "parent continued"); err != nil {
		t.Fatal(err)
	}
	continued := fx.snapshot(t, fx.ref)
	if len(continued.Projection.Turns) != 3 {
		t.Fatal("parent continuation did not persist")
	}
	viewBeforeUndo := currentHistoryMessagesForTab(t, app, "test")
	if len(viewBeforeUndo) <= len(sourceView) {
		t.Fatalf("parent continuation view = %+v", viewBeforeUndo)
	}
	// A new turn invalidates the undo slot. The stale request must neither
	// replay old conversation bytes nor change the rewound file.
	if undo := app.UndoRewindForTab("test", result.TransactionID); undo.OK || undo.Error != "undo not available" {
		t.Fatalf("stale undo = %+v", undo)
	}
	if got, err := os.ReadFile(fx.filePath); err != nil || string(got) != "before" {
		t.Fatalf("file after undo = %q, %v", got, err)
	}
	afterUndo := fx.snapshot(t, fx.ref)
	if !reflect.DeepEqual(afterUndo.Projection.Messages, continued.Projection.Messages) {
		t.Fatal("undo overwrote continued parent history")
	}
	viewAfterUndo := currentHistoryMessagesForTab(t, app, "test")
	if !reflect.DeepEqual(viewAfterUndo, viewBeforeUndo) {
		t.Fatalf("undo changed parent history view: %+v", viewAfterUndo)
	}
}

func TestAttachForkedRewindTabFailsClosedWhenSourceIsGone(t *testing.T) {
	app := NewApp()
	source := &WorkspaceTab{ID: "removed"}
	result := app.attachForkedRewindTab(source, RewindResultView{
		OK:                 true,
		ConversationForked: true,
		Branch:             filepath.Join(t.TempDir(), "fork.jsonl"),
	})
	if result.OK || !result.Partial {
		t.Fatalf("result = %+v, want failed partial result", result)
	}
	if result.Error != rewindForkAttachError {
		t.Fatalf("error = %q, want stable path-free error", result.Error)
	}
	if result.TabID != "" || result.Tab != nil {
		t.Fatalf("failed attach exposed target tab: %+v", result)
	}
}

func assertCheckpointFilesEncodeAsArray(t *testing.T, metas []CheckpointMeta) {
	t.Helper()
	raw, err := json.Marshal(metas)
	if err != nil {
		t.Fatal(err)
	}
	var payload []struct {
		Files json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for i, item := range payload {
		if string(item.Files) == "null" {
			t.Fatalf("checkpoint %d files encoded as null; frontend expects []", i)
		}
		if len(item.Files) == 0 || item.Files[0] != '[' {
			t.Fatalf("checkpoint %d files encoded as %s, want JSON array", i, item.Files)
		}
	}
}

// TestCheckpointsCanCodePropagatesToEarlierTurns covers #3438: RestoreCode(turn)
// reverts files touched in that turn or any later one, so a turn with no file
// changes of its own can still rewind code when a later turn changed files. The
// desktop CanCode flag must reflect that suffix capability, not just the turn's
// own paths.
func TestCheckpointsCanCodePropagatesToEarlierTurns(t *testing.T) {
	fx := newCurrentRewindFixture(t)
	app, ctrl := fx.app, fx.ctrl
	if err := ctrl.RunTurn(t.Context(), "ask again"); err != nil {
		t.Fatal(err)
	}

	metas := app.CheckpointsForTab("test")
	if len(metas) != 3 {
		t.Fatalf("checkpoints = %d, want 3", len(metas))
	}
	got := map[int]bool{}
	for _, m := range metas {
		got[m.Turn] = m.CanCode
	}
	if !got[0] {
		t.Error("turn 0 (no files of its own) should allow code rewind — turn 1 changed files")
	}
	if !got[1] {
		t.Error("turn 1 changed files, should allow code rewind")
	}
	if got[2] {
		t.Error("turn 2 is after the last file-bearing turn, should NOT allow code rewind")
	}
	if metas[0].TurnFileCount != 0 {
		t.Fatalf("turn 0 file count = %d, want 0 for this turn", metas[0].TurnFileCount)
	}
	if metas[1].TurnFileCount != 1 {
		t.Fatalf("turn 1 file count = %d, want 1 for this turn", metas[1].TurnFileCount)
	}
	if len(metas[0].Files) != 1 || metas[0].Files[0] != fx.filePath {
		t.Fatalf("turn 0 cumulative files = %#v, want [%s]", metas[0].Files, fx.filePath)
	}
	if metas[0].FileCount != 1 || metas[0].FilesTruncated {
		t.Fatalf("turn 0 file summary = count %d truncated %v, want count 1 truncated false", metas[0].FileCount, metas[0].FilesTruncated)
	}
	if len(metas[2].Files) != 0 {
		t.Fatalf("turn 2 cumulative files = %#v, want empty", metas[2].Files)
	}
	assertCheckpointFilesEncodeAsArray(t, metas)
}

func TestCheckpointsForTabLimitsCumulativeFilePreview(t *testing.T) {
	app := currentCheckpointFileFixture(t, checkpointFilePreviewLimit+5)

	metas := app.CheckpointsForTab("test")
	if len(metas) != 2 {
		t.Fatalf("checkpoints = %d, want 2", len(metas))
	}
	if metas[0].FileCount != checkpointFilePreviewLimit+5 {
		t.Fatalf("turn 0 cumulative file count = %d, want %d", metas[0].FileCount, checkpointFilePreviewLimit+5)
	}
	if len(metas[0].Files) != checkpointFilePreviewLimit {
		t.Fatalf("turn 0 preview files = %d, want %d", len(metas[0].Files), checkpointFilePreviewLimit)
	}
	if !metas[0].FilesTruncated {
		t.Fatal("turn 0 should mark file preview as truncated")
	}
	if metas[0].TurnFileCount != 0 {
		t.Fatalf("turn 0 file count = %d, want 0 for this turn", metas[0].TurnFileCount)
	}
	if metas[1].TurnFileCount != checkpointFilePreviewLimit+5 {
		t.Fatalf("turn 1 file count = %d, want %d", metas[1].TurnFileCount, checkpointFilePreviewLimit+5)
	}
	assertCheckpointFilesEncodeAsArray(t, metas)
}
