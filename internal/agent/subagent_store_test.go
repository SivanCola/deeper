package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestSubagentStoreContinueLoadsSavedTranscript(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "review diff"})
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleAssistant, Content: "finding A"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	continued, err := store.PrepareContinue(run.Ref, spec)
	if err != nil {
		t.Fatalf("PrepareContinue: %v", err)
	}
	defer continued.Release()
	if continued.Ref != run.Ref {
		t.Fatalf("continued ref = %q, want %q", continued.Ref, run.Ref)
	}
	if got := continued.Session.Snapshot(); len(got) != 3 || got[2].Content != "finding A" {
		t.Fatalf("continued transcript = %+v, want saved messages", got)
	}
}

func TestSubagentStoreTerminalSaveKeepsStartTime(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	run, err := store.PrepareFresh(testSubagentSpec(t, "explore"))
	if err != nil {
		t.Fatal(err)
	}
	defer run.Release()
	created := run.Meta.CreatedAt
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleAssistant, Content: "done"})
	before := time.Now().UTC()
	if err := store.SaveCompleted(run); err != nil {
		t.Fatal(err)
	}
	meta, err := store.LoadMeta(run.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.CreatedAt.Equal(created) || meta.UpdatedAt.Before(before) {
		t.Fatalf("terminal times = %+v", meta)
	}
}

func TestSubagentStoreSaveFailedPersistsTerminalMetaWhenFlushFails(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	run, err := store.PrepareFresh(testSubagentSpec(t, "explore"))
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	defer run.Release()
	if err := store.MarkRunning(run); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	run.Persistence.(*testSubagentTranscript).flushErr = errors.New("flush failed")

	if err := store.SaveFailed(run); err == nil {
		t.Fatal("SaveFailed unexpectedly succeeded despite flush failure")
	}
	meta, err := store.LoadMeta(run.Ref)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Status != SubagentFailed {
		t.Fatalf("persisted status = %q, want %q", meta.Status, SubagentFailed)
	}
	if run.terminalPersisted {
		t.Fatal("flush failure recorded successful terminal durability")
	}
	if err := store.SaveFailed(run); err == nil {
		t.Fatal("retry skipped the failed flush")
	}
	run.Persistence.(*testSubagentTranscript).flushErr = nil
	if err := store.SaveFailed(run); err != nil {
		t.Fatal(err)
	}
	if !run.terminalPersisted {
		t.Fatal("successful retry did not confirm durability")
	}
}

func TestSubagentStoreForkCreatesIndependentReference(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "review diff"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	forked, err := store.prepareFork(run.Ref, spec)
	if err != nil {
		t.Fatalf("PrepareFork: %v", err)
	}
	defer forked.Release()
	if forked.Ref == run.Ref {
		t.Fatalf("fork ref should be new, got %q", forked.Ref)
	}
	if got := forked.Session.Snapshot(); len(got) != 2 || got[1].Content != "review diff" {
		t.Fatalf("fork transcript = %+v, want copied messages", got)
	}
	if forked.Meta.ParentSession != spec.ParentSession {
		t.Fatalf("fork parent session = %q, want %q", forked.Meta.ParentSession, spec.ParentSession)
	}
}

func TestSubagentStoreRejectsContinueFromSiblingSession(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "left"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "left", "root")
	saveTestParentSession(t, sessionDir, "right", "root")
	other := spec
	other.ParentSession = "right"
	if _, err := store.PrepareContinue(run.Ref, other); err == nil || !strings.Contains(err.Error(), "not in current parent session") {
		t.Fatalf("PrepareContinue error = %v, want lineage rejection", err)
	}
}

func TestSubagentStoreContinueFromAncestorCopiesIntoCurrentSession(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "root"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "review diff"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "child", "root")
	child := spec
	child.ParentSession = "child"
	continued, err := store.PrepareContinue(run.Ref, child)
	if err != nil {
		t.Fatalf("PrepareContinue: %v", err)
	}
	defer continued.Release()
	if continued.Ref == run.Ref {
		t.Fatalf("continued ref should be copied into child session, got source ref %q", continued.Ref)
	}
	if continued.Meta.ParentSession != "child" {
		t.Fatalf("continued parent session = %q, want child", continued.Meta.ParentSession)
	}
	if continued.Meta.ForkedFrom != run.Ref {
		t.Fatalf("forkedFrom = %q, want %q", continued.Meta.ForkedFrom, run.Ref)
	}
	if got := continued.Session.Snapshot(); len(got) != 2 || got[1].Content != "review diff" {
		t.Fatalf("continued transcript = %+v, want copied source transcript", got)
	}
	sourceMeta, err := store.LoadMeta(run.Ref)
	if err != nil {
		t.Fatalf("LoadMeta source: %v", err)
	}
	if sourceMeta.ParentSession != "root" {
		t.Fatalf("source parent session = %q, want root", sourceMeta.ParentSession)
	}
}

func TestSubagentStoreContinueFromAncestorReusesCurrentSessionCopy(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "root"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "review diff"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "child", "root")
	child := spec
	child.ParentSession = "child"
	first, err := store.PrepareContinue(run.Ref, child)
	if err != nil {
		t.Fatalf("first PrepareContinue: %v", err)
	}
	firstRef := first.Ref
	if err := store.SaveCompleted(first); err != nil {
		t.Fatalf("SaveCompleted first: %v", err)
	}
	first.Release()

	second, err := store.PrepareContinue(run.Ref, child)
	if err != nil {
		t.Fatalf("second PrepareContinue: %v", err)
	}
	defer second.Release()
	if second.Ref != firstRef {
		t.Fatalf("second continuation ref = %q, want existing child copy %q", second.Ref, firstRef)
	}
}

func TestSubagentStoreContinueFromOlderAncestorUsesNearestLineageCopy(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "root"
	rootRun, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh root: %v", err)
	}
	appendSubagentMessages(t, rootRun, provider.Message{Role: provider.RoleUser, Content: "root task"})
	if err := store.SaveCompleted(rootRun); err != nil {
		t.Fatalf("SaveCompleted root: %v", err)
	}
	rootRun.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "child", "root")
	saveTestParentSession(t, sessionDir, "grandchild", "child")

	child := spec
	child.ParentSession = "child"
	childRun, err := store.PrepareContinue(rootRun.Ref, child)
	if err != nil {
		t.Fatalf("PrepareContinue child: %v", err)
	}
	appendSubagentMessages(t, childRun, provider.Message{Role: provider.RoleAssistant, Content: "child finding"})
	childRef := childRun.Ref
	if err := store.SaveCompleted(childRun); err != nil {
		t.Fatalf("SaveCompleted child: %v", err)
	}
	childRun.Release()

	grandchild := spec
	grandchild.ParentSession = "grandchild"
	fromRoot, err := store.PrepareContinue(rootRun.Ref, grandchild)
	if err != nil {
		t.Fatalf("PrepareContinue grandchild from root: %v", err)
	}
	grandchildRef := fromRoot.Ref
	if fromRoot.Meta.ForkedFrom != childRef {
		t.Fatalf("grandchild forkedFrom = %q, want nearest child copy %q", fromRoot.Meta.ForkedFrom, childRef)
	}
	if got := fromRoot.Session.Snapshot(); len(got) != 3 || got[2].Content != "child finding" {
		t.Fatalf("grandchild transcript = %+v, want child copy transcript", got)
	}
	if err := store.SaveCompleted(fromRoot); err != nil {
		t.Fatalf("SaveCompleted grandchild: %v", err)
	}
	fromRoot.Release()

	fromChild, err := store.PrepareContinue(childRef, grandchild)
	if err != nil {
		t.Fatalf("PrepareContinue grandchild from child: %v", err)
	}
	defer fromChild.Release()
	if fromChild.Ref != grandchildRef {
		t.Fatalf("grandchild ref from child copy = %q, want existing copy %q", fromChild.Ref, grandchildRef)
	}
}

func TestSubagentStoreRejectsAncestorContinuationWhenCurrentCopyFailed(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "root"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted root: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "child", "root")
	child := spec
	child.ParentSession = "child"
	copyRun, err := store.PrepareContinue(run.Ref, child)
	if err != nil {
		t.Fatalf("PrepareContinue child: %v", err)
	}
	if err := store.SaveFailed(copyRun); err != nil {
		t.Fatalf("SaveFailed child copy: %v", err)
	}
	copyRun.Release()

	if _, err := store.PrepareContinue(run.Ref, child); err == nil || !strings.Contains(err.Error(), "failed and cannot be continued") {
		t.Fatalf("PrepareContinue error = %v, want failed current copy rejection", err)
	}
}

func TestSubagentStoreRejectsAncestorContinuationWithMultipleCurrentCopies(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "root"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted root: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "child", "root")
	child := spec
	child.ParentSession = "child"
	first, err := store.PrepareContinue(run.Ref, child)
	if err != nil {
		t.Fatalf("PrepareContinue first: %v", err)
	}
	if err := store.SaveCompleted(first); err != nil {
		t.Fatalf("SaveCompleted first: %v", err)
	}
	first.Release()

	second, err := store.PrepareFresh(child)
	if err != nil {
		t.Fatalf("PrepareFresh second: %v", err)
	}
	second.Meta.ForkedFrom = run.Ref
	if err := store.SaveCompleted(second); err != nil {
		t.Fatalf("SaveCompleted second: %v", err)
	}
	second.Release()

	if _, err := store.PrepareContinue(run.Ref, child); err == nil || !strings.Contains(err.Error(), "multiple copied transcripts") {
		t.Fatalf("PrepareContinue error = %v, want multiple-copy rejection", err)
	}
}

func TestSubagentStoreForkFromAncestorSessionCreatesCurrentOwner(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "root"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "review diff"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "child", "root")
	other := spec
	other.ParentSession = "child"
	forked, err := store.prepareFork(run.Ref, other)
	if err != nil {
		t.Fatalf("PrepareFork: %v", err)
	}
	defer forked.Release()
	if forked.Ref == run.Ref {
		t.Fatalf("fork ref should be new, got %q", forked.Ref)
	}
	if forked.Meta.ParentSession != "child" {
		t.Fatalf("fork parent session = %q, want child", forked.Meta.ParentSession)
	}
	sourceMeta, err := store.LoadMeta(run.Ref)
	if err != nil {
		t.Fatalf("LoadMeta source: %v", err)
	}
	if sourceMeta.ParentSession != spec.ParentSession {
		t.Fatalf("source parent session = %q, want %q", sourceMeta.ParentSession, spec.ParentSession)
	}
}

func TestSubagentStoreRejectsForkWhenSourceOwnerMetaMissing(t *testing.T) {
	sessionDir, store, ref, spec := prepareCompletedSubagentForLineageTest(t, "root")
	saveTestParentSession(t, sessionDir, "child", "root")

	other := spec
	other.ParentSession = "child"
	if _, err := store.prepareFork(ref, other); err == nil || !strings.Contains(err.Error(), "lineage could not be verified") {
		t.Fatalf("PrepareFork error = %v, want unverified lineage rejection", err)
	}
}

func TestSubagentStoreRejectsForkWhenSourceOwnerMetaCorrupt(t *testing.T) {
	sessionDir, store, ref, spec := prepareCompletedSubagentForLineageTest(t, "root")
	saveTestParentSession(t, sessionDir, "child", "root")
	setTestParentError(t, sessionDir, "root", errors.New("corrupt current session header"))

	other := spec
	other.ParentSession = "child"
	if _, err := store.prepareFork(ref, other); err == nil || !strings.Contains(err.Error(), "lineage could not be verified") {
		t.Fatalf("PrepareFork error = %v, want unverified lineage rejection", err)
	}
}

func TestSubagentStoreRejectsForkWhenSourceOwnerMetaIDDiffers(t *testing.T) {
	sessionDir, store, ref, spec := prepareCompletedSubagentForLineageTest(t, "root")
	saveTestParentSession(t, sessionDir, "child", "root")
	saveTestParentSession(t, sessionDir, "root", "")
	rewriteTestParentHeader(t, sessionDir, "root", "sessionId", "other-root")

	other := spec
	other.ParentSession = "child"
	if _, err := store.prepareFork(ref, other); err == nil || !strings.Contains(err.Error(), "lineage could not be verified") {
		t.Fatalf("PrepareFork error = %v, want unverified lineage rejection", err)
	}
}

func TestSubagentStoreRejectsForkFromSiblingSession(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "left"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "left", "root")
	saveTestParentSession(t, sessionDir, "right", "root")
	other := spec
	other.ParentSession = "right"
	if _, err := store.prepareFork(run.Ref, other); err == nil || !strings.Contains(err.Error(), "not in current parent session") {
		t.Fatalf("PrepareFork error = %v, want lineage rejection", err)
	}
}

func TestSubagentStoreRejectsForkFromUnrelatedSession(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "source"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	saveTestParentSession(t, sessionDir, "root", "")
	saveTestParentSession(t, sessionDir, "current", "root")
	other := spec
	other.ParentSession = "current"
	if _, err := store.prepareFork(run.Ref, other); err == nil || !strings.Contains(err.Error(), "not in current parent session") {
		t.Fatalf("PrepareFork error = %v, want unrelated session rejection", err)
	}
}

func TestSubagentStoreRejectsForkWhenLineageCannotBeProven(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "root"
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	other := spec
	other.ParentSession = "child"
	if _, err := store.prepareFork(run.Ref, other); err == nil || !strings.Contains(err.Error(), "lineage could not be verified") {
		t.Fatalf("PrepareFork error = %v, want unverified lineage rejection", err)
	}
}

func TestSubagentStoreForkReleasesSourceLockAfterCopy(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "review diff"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	forked, err := store.prepareFork(run.Ref, spec)
	if err != nil {
		t.Fatalf("PrepareFork: %v", err)
	}
	defer forked.Release()
	continued, err := store.PrepareContinue(run.Ref, spec)
	if err != nil {
		t.Fatalf("source should not stay locked by fork run: %v", err)
	}
	continued.Release()
}

func TestSubagentStoreRejectsIncompatibleTranscript(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	other := spec
	other.Name = "security-review"
	if _, err := store.PrepareContinue(run.Ref, other); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("PrepareContinue error = %v, want incompatible name", err)
	}
}

func TestSubagentStoreRejectsConcurrentContinue(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()

	first, err := store.PrepareContinue(run.Ref, spec)
	if err != nil {
		t.Fatalf("first PrepareContinue: %v", err)
	}
	defer first.Release()
	if _, err := store.PrepareContinue(run.Ref, spec); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second PrepareContinue error = %v, want lock error", err)
	}
}

func TestSubagentStoreSaveFailedPersistsTranscriptAndRejectsReuse(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "failed continuation"})
	if err := store.SaveFailed(run); err != nil {
		t.Fatalf("SaveFailed: %v", err)
	}
	run.Release()

	loaded, err := store.persistence.Read(t.Context(), run.Ref)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded; len(got) != 2 || got[1].Content != "failed continuation" {
		t.Fatalf("failed transcript = %+v, want persisted failed prompt", got)
	}
	meta, err := store.LoadMeta(run.Ref)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Status != SubagentFailed {
		t.Fatalf("status = %q, want failed", meta.Status)
	}
	if _, err := store.PrepareContinue(run.Ref, spec); err == nil || !strings.Contains(err.Error(), "failed and cannot be continued") {
		t.Fatalf("PrepareContinue error = %v, want failed ref rejection", err)
	}
	if _, err := store.prepareFork(run.Ref, spec); err == nil || !strings.Contains(err.Error(), "failed and cannot be continued") {
		t.Fatalf("PrepareFork error = %v, want failed ref rejection", err)
	}
}

func TestSubagentStoreCleanupStaleRunningMarksInterrupted(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	appendSubagentMessages(t, run, provider.Message{Role: provider.RoleUser, Content: "interrupted prompt"})
	if err := store.MarkRunning(run); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	ref := run.Ref
	run.Release()

	cleaned, err := store.CleanupStaleRunning()
	if err != nil {
		t.Fatalf("CleanupStaleRunning: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("cleaned = %d, want 1", cleaned)
	}
	meta, err := store.LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Status != SubagentInterrupted {
		t.Fatalf("status = %q, want interrupted", meta.Status)
	}
	if _, err := store.PrepareContinue(ref, spec); err == nil || !strings.Contains(err.Error(), "interrupted by a previous shutdown or crash") {
		t.Fatalf("PrepareContinue error = %v, want interrupted rejection", err)
	}
	if _, err := store.prepareFork(ref, spec); err == nil || !strings.Contains(err.Error(), "cannot be continued or forked") {
		t.Fatalf("PrepareFork error = %v, want interrupted fork rejection", err)
	}
}

func TestSubagentStoreCleanupStaleRunningSkipsMissingParentProof(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.MarkRunning(run); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	ref := run.Ref
	run.Release()
	meta, err := store.LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	meta.ParentSession = ""
	if err := store.saveMeta(meta); err != nil {
		t.Fatalf("saveMeta without parent: %v", err)
	}

	cleaned, err := store.CleanupStaleRunning()
	if err != nil {
		t.Fatalf("CleanupStaleRunning: %v", err)
	}
	if cleaned != 0 {
		t.Fatalf("cleaned = %d without parent proof, want 0", cleaned)
	}
	meta, err = store.LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta after cleanup: %v", err)
	}
	if meta.Status != SubagentRunning {
		t.Fatalf("status = %q without parent proof, want running", meta.Status)
	}
}

func TestSubagentStoreCleanupStaleRunningSkipsCorruptMeta(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir())
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.MarkRunning(run); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	ref := run.Ref
	run.Release()

	// Corrupt metadata files (truncated JSON, empty, and invalid custom field
	// values) must be skipped, not abort the whole startup cleanup.
	for i, corrupt := range []string{
		`{"status":"running"`,
		"",
		`{"createdAt":"not-a-time"}`,
	} {
		corruptRef := fmt.Sprintf("sa_corrupt_%d", i)
		if err := os.WriteFile(filepath.Join(store.dir, corruptRef+".meta.json"), []byte(corrupt), 0o600); err != nil {
			t.Fatalf("write corrupt meta %d: %v", i, err)
		}
	}

	cleaned, err := store.CleanupStaleRunning()
	if err != nil {
		t.Fatalf("CleanupStaleRunning should skip corrupt meta: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("cleaned = %d, want 1 (corrupt metas skipped, running meta interrupted)", cleaned)
	}
	meta, err := store.LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Status != SubagentInterrupted {
		t.Fatalf("status = %q, want interrupted", meta.Status)
	}
}

func TestSubagentStoreCleanupStaleRunningKeepsParentGuardAfterCorruptReread(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = "lease-parent"

	refs := make([]string, 0, 2)
	for range 2 {
		run, err := store.PrepareFresh(spec)
		if err != nil {
			t.Fatalf("PrepareFresh: %v", err)
		}
		if err := store.MarkRunning(run); err != nil {
			t.Fatalf("MarkRunning: %v", err)
		}
		refs = append(refs, run.Ref)
		run.Release()
	}
	sort.Strings(refs)

	held := false
	store.WithParentIdleGuard(func(_ context.Context, id string, work func() error) (bool, error) {
		held = true
		defer func() { held = false }()
		return true, work()
	})
	var guardedReread bool
	store.cleanupBeforeReread = func(parentSession, ref string) {
		switch ref {
		case refs[0]:
			if err := os.WriteFile(store.metaPath(ref), []byte(`{"createdAt":"not-a-time"}`), 0o600); err != nil {
				t.Fatalf("corrupt first metadata reread: %v", err)
			}
		case refs[1]:
			guardedReread = held
		}
	}

	cleaned, err := store.CleanupStaleRunning()
	if err != nil {
		t.Fatalf("CleanupStaleRunning: %v", err)
	}
	if !guardedReread || held {
		t.Fatal("guard must span every reread and release after cleanup")
	}
	if cleaned != 1 {
		t.Fatalf("cleaned = %d, want 1", cleaned)
	}
	meta, err := store.LoadMeta(refs[1])
	if err != nil {
		t.Fatalf("LoadMeta second ref: %v", err)
	}
	if meta.Status != SubagentInterrupted {
		t.Fatalf("second ref status = %q, want interrupted", meta.Status)
	}
}

func TestSubagentStoreRefusesDestroyedParent(t *testing.T) {
	store := newTestSubagentStore(t, t.TempDir()).WithDestroyedChecker(func(string) bool { return true })
	if _, err := store.PrepareFresh(testSubagentSpec(t, "review")); err == nil {
		t.Fatal("created a child for destroyed parent")
	}
	if len(store.persistence.(*testSubagentPersistence).records) != 0 {
		t.Fatal("destroyed parent created artifacts")
	}
}

func testSubagentSpec(t *testing.T, name string) SubagentSpec {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	return SubagentSpec{
		Kind:          "skill",
		Name:          name,
		WorkspaceRoot: t.TempDir(),
		ParentSession: "parent-session",
		SystemPrompt:  "review persona",
		Registry:      reg,
		Model:         "deepseek",
		Effort:        "max",
	}
}

func prepareCompletedSubagentForLineageTest(t *testing.T, parentSession string) (string, *SubagentStore, string, SubagentSpec) {
	t.Helper()
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
	spec := testSubagentSpec(t, "review")
	spec.ParentSession = parentSession
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	run.Release()
	return sessionDir, store, run.Ref, spec
}
