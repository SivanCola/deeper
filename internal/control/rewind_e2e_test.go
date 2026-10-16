package control

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/checkpoint"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func TestRewindCommandRequiresConfirmationForPartialCoverage(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	path := filepath.Join(root, "partial.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("sys")
	ag := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{
		Runner:     ag,
		Executor:   ag,
		SessionDir: dir,

		WorkspaceRoot: root,
		Sink:          event.Discard,
	})
	c.beginCheckpoint(context.Background(), "edit partial.txt")
	c.mutationObserver.BeforeMutation("partial.txt", "write_file", checkpoint.CaptureBeforeMutation)
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.mutationObserver.AfterMutation("partial.txt", "write_file")
	c.mutationObserver.RecordGap(checkpoint.CoverageGap{Reason: checkpoint.GapBashSideEffect, Tool: "bash"})

	plan, err := c.PrepareRewind(0, RewindCode)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.CanFiles || !RewindPlanRequiresConfirmation(plan) {
		t.Fatalf("partial plan = %+v, want restorable files with explicit confirmation", plan)
	}
	if err := c.executeRewindCommand(0, RewindCode); !errors.Is(err, ErrRewindCoverageConfirmationRequired) {
		t.Fatalf("compatibility Rewind error = %v, want confirmation-required", err)
	}
	if got := string(mustReadFile(t, path)); got != "after" {
		t.Fatalf("unconfirmed rewind changed file to %q", got)
	}

	result, err := c.CommitRewind(plan.PlanID)
	if err != nil || !result.OK {
		t.Fatalf("confirmed CommitRewind result=%+v err=%v", result, err)
	}
	if got := string(mustReadFile(t, path)); got != "before" {
		t.Fatalf("confirmed rewind left file at %q, want before", got)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func runTwoTurns(t *testing.T) (*Controller, *agent.Agent, *[]event.Event) {
	t.Helper()
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		textTurn("first answer"),
		textTurn("second answer"),
		textTurn("edited answer"),
	}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	var events []event.Event
	c, _ := bindCurrentBranchController(t, Options{
		Runner:   ag,
		Executor: ag,
		Label:    "test",
		Sink:     event.FuncSink(func(e event.Event) { events = append(events, e) }),
	})
	if err := c.RunTurn(t.Context(), "first prompt"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := c.RunTurn(t.Context(), "second prompt"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	return c, ag, &events
}

// Current rewind resolves the checkpoint's stable turn identity in the event
// log, even when later compaction shortened the executor's model context.
func TestRewindAfterCompactionRestoresCurrentTurnPrefix(t *testing.T) {
	c, ag, _ := runTwoTurns(t)
	parent, _ := c.SessionRef()
	service := c.SessionService()
	c.checkpoints.mu.Lock()
	lastTurn := c.checkpoints.turn - 1
	boundary := c.checkpoints.bound[lastTurn]
	c.checkpoints.mu.Unlock()
	original := ag.Session().Snapshot()
	if boundary <= 1 {
		t.Fatalf("expected a nontrivial checkpoint, got %d", boundary)
	}
	compacted := []provider.Message{{ID: "summary", Role: provider.RoleSystem, Content: "compressed context"}}
	commit, err := c.RecordSessionModelContext(t.Context(), agent.SessionModelContextCommit{
		OperationID: "compact-before-rewind", Reason: "summary", Messages: compacted,
	})
	if err != nil || !commit.Durable {
		t.Fatalf("context commit = %+v, %v", commit, err)
	}
	ag.Session().Replace(compacted)
	before, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.executeRewindCommand(lastTurn, RewindBoth); err != nil {
		t.Fatal(err)
	}
	child, _ := c.SessionRef()
	if child == parent || !reflect.DeepEqual(ag.Session().Snapshot(), provider.ModelMessages(original[:boundary])) {
		t.Fatal("rewind did not restore the selected turn's model context")
	}
	after, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil || before.EventSequence != after.EventSequence || !reflect.DeepEqual(before.Projection, after.Projection) {
		t.Fatalf("rewind mutated the compacted parent: %v", err)
	}
}

// TestRewindConversationSucceedsWithLiveBoundary is the companion happy path: a
// boundary still within the log truncates the conversation and reports success.
func TestRewindConversationSucceedsWithLiveBoundary(t *testing.T) {
	c, ag, events := runTwoTurns(t)

	c.checkpoints.mu.Lock()
	lastTurn := c.checkpoints.turn - 1
	boundary := c.checkpoints.bound[lastTurn]
	c.checkpoints.mu.Unlock()

	*events = nil
	if err := c.executeRewindCommand(lastTurn, RewindConversation); err != nil {
		t.Fatalf("Rewind with a live boundary: %v", err)
	}
	if got := len(ag.Session().Messages); got != boundary {
		t.Fatalf("switched session = %d messages, want boundary %d", got, boundary)
	}
	ok := false
	for _, e := range *events {
		if e.Kind == event.Notice && strings.Contains(e.Text, "forked conversation") {
			ok = true
		}
	}
	if !ok {
		t.Fatal("expected a conversation-fork success notice")
	}
}

func TestCurrentRewindTransfersExecutionOwnershipBeforeSwitch(t *testing.T) {
	c, _, _ := runTwoTurns(t)
	service := c.SessionService()
	parent, _ := c.SessionRef()
	_, oldRuntime, _ := c.SessionBinding()
	if err := c.executeRewindCommand(1, RewindConversation); err != nil {
		t.Fatal(err)
	}
	child, ok := c.SessionRef()
	if !ok || child == parent {
		t.Fatalf("child identity = %+v", child)
	}
	_, runtime, _ := c.SessionBinding()
	if runtime == oldRuntime || runtime.Ref() != child {
		t.Fatal("rewind retained the parent's execution runtime")
	}
	// The host can close the now-unbound parent, while the child's controller
	// binding still prevents another service from acquiring its writer.
	if err := service.Close(t.Context(), parent); err != nil {
		t.Fatalf("parent retained a client binding: %v", err)
	}
	parentDir, err := service.SessionDir(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	other, err := session.NewService(service.HostID(), session.NewFilesystemPersistence(filepath.Dir(parentDir)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.CloseAll(context.Background()) })
	parentBinding, err := other.Open(t.Context(), parent)
	if err != nil {
		t.Fatalf("parent writer was not released: %v", err)
	}
	t.Cleanup(func() { _ = parentBinding.Release(context.Background()) })
	if _, err := other.Open(t.Context(), child); err == nil {
		t.Fatal("active child writer was not exclusive")
	}
}

func TestPositionalCompressionPreservesCheckpointLineage(t *testing.T) {
	c, ag, _ := runTwoTurns(t)
	sess := ag.Session()
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("large completed output ", 240)})
	beforeMessages := sess.Snapshot()
	beforeRewrite := sess.RewriteVersion()
	beforeRevision := atomic.LoadInt64(&c.sessionRevision)
	c.checkpoints.mu.Lock()
	beforeBounds := make(map[int]int, len(c.checkpoints.bound))
	maps.Copy(beforeBounds, c.checkpoints.bound)
	c.checkpoints.mu.Unlock()

	if err := c.SummarizeFrom(context.Background(), 0); err != nil {
		t.Fatalf("SummarizeFrom: %v", err)
	}
	if !reflect.DeepEqual(sess.Snapshot(), beforeMessages) {
		t.Fatal("positional compression changed canonical history")
	}
	if got := sess.RewriteVersion(); got != beforeRewrite {
		t.Fatalf("rewrite version = %d, want unchanged %d", got, beforeRewrite)
	}
	if got := atomic.LoadInt64(&c.sessionRevision); got != beforeRevision {
		t.Fatalf("controller session revision = %d, want unchanged %d", got, beforeRevision)
	}
	c.checkpoints.mu.Lock()
	afterBounds := make(map[int]int, len(c.checkpoints.bound))
	maps.Copy(afterBounds, c.checkpoints.bound)
	c.checkpoints.mu.Unlock()
	if !reflect.DeepEqual(afterBounds, beforeBounds) {
		t.Fatalf("checkpoint boundaries changed: before=%v after=%v", beforeBounds, afterBounds)
	}
	state := ag.ContextMaintenanceSnapshot()
	if state.LastReceipt == nil || state.LastReceipt.Trigger != agent.CompactionTriggerManual || state.ProjectionVersion == 0 {
		t.Fatalf("projection state = %+v", state)
	}
	ref, _ := c.SessionRef()
	durable, err := c.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil || reflect.DeepEqual(durable.Projection.ModelMessages, durable.Projection.Messages) {
		t.Fatalf("current compaction was not persisted: %v", err)
	}
	if _, ok := c.checkpoints.boundary(1); !ok {
		t.Fatal("conversation rewind boundary disappeared after compression")
	}
	plan, err := c.PrepareRewind(1, RewindConversation)
	if err != nil || !plan.CanConversation {
		t.Fatalf("conversation rewind unavailable after compression: plan=%+v err=%v", plan, err)
	}
	if err := c.SummarizeFrom(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "no longer present in the model context") {
		t.Fatalf("second positional compression error = %v, want folded-boundary explanation", err)
	}
}

// A tail rewind retains the model-context compaction committed before the
// selected turn, while keeping the complete display transcript independently.
func TestTailRewindKeepsCompactionProjection(t *testing.T) {
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		textTurn("first answer"),
		textTurn("compact summary of the first exchange"),
		textTurn("second answer"),
	}}
	service, err := session.NewService("rewind-compact", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(prov, tool.NewRegistry(), compactionPersistenceHistory(false), agent.Options{ContextWindow: 100000, CompactRatio: 0.80}, event.Discard)
	c := newOwnedTestController(t, Options{Runner: ag, Executor: ag, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	if err := c.RunTurn(t.Context(), "first prompt"); err != nil {
		t.Fatal(err)
	}
	if err := ag.CompactNow(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	parent, _ := c.SessionRef()
	compacted, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(compacted.Projection.ModelMessages, compacted.Projection.Messages) {
		t.Fatal("fixture did not compact model context")
	}
	if err := c.RunTurn(t.Context(), "second prompt"); err != nil {
		t.Fatal(err)
	}
	before, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.executeRewindCommand(1, RewindConversation); err != nil {
		t.Fatal(err)
	}
	child, _ := c.SessionRef()
	forked, err := service.Query().Snapshot(t.Context(), child)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forked.Projection.ModelMessages, compacted.Projection.ModelMessages) || !reflect.DeepEqual(forked.Projection.Messages, compacted.Projection.Messages) {
		t.Fatal("tail rewind lost the compressed prefix or display history")
	}
	after, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil || after.EventSequence != before.EventSequence {
		t.Fatalf("rewind changed parent: %v", err)
	}
	if !reflect.DeepEqual(c.History(), forked.Projection.ModelMessages) {
		t.Fatal("child executor did not restore the compressed model context")
	}
}

func TestEditPromptPersistsOriginalPrompt(t *testing.T) {
	c, ag, _ := runTwoTurns(t)

	if err := c.executeRewindCommand(1, RewindConversation); err != nil {
		t.Fatal(err)
	}
	c.SubmitEditedDisplay("edited prompt", "edited prompt", "second prompt")
	// Join the controller's completion and persistence owners before reading
	// disk; a fixed polling deadline measures CI filesystem speed instead.
	waitIdle(t, c)
	c.autosaveWG.Wait()
	ref, _ := c.SessionRef()
	loaded, err := c.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatalf("load completed edited turn: %v", err)
	}
	msgs := loaded.Projection.Messages
	if len(msgs) < 2 {
		t.Fatalf("completed edited turn has too few messages: %+v", msgs)
	}
	last := msgs[len(msgs)-2]
	if last.Role != provider.RoleUser || agent.StripTransientUserBlocks(last.Content) != "edited prompt" {
		t.Fatalf("last user message = %+v, want edited prompt", last)
	}
	if !last.Edited || last.Original != "second prompt" {
		t.Fatalf("edit metadata = edited:%v original:%q, want edited:true original:%q", last.Edited, last.Original, "second prompt")
	}
	for _, m := range ag.Session().Snapshot() {
		if m.Role == provider.RoleUser && m.Content == "second prompt" {
			t.Fatalf("original prompt stayed as an active model turn: %+v", ag.Session().Snapshot())
		}
	}
}
