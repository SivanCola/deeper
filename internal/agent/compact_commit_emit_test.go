package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

// reentrantSnapshotSink re-enters ContextMaintenanceSnapshot on every emit,
// which takes compactionMu. commitSummaryProjection must unlock before Emit.
type reentrantSnapshotSink struct {
	agent *Agent
	mu    sync.Mutex
	n     int
}

type modelContextRecorderStub struct {
	result SessionModelContextCommitResult
	err    error
	commit SessionModelContextCommit
}

type modelContextRecorderStep struct {
	result SessionModelContextCommitResult
	err    error
}

type sequencedModelContextRecorder struct {
	steps   []modelContextRecorderStep
	commits []SessionModelContextCommit
}

func (*modelContextRecorderStub) CheckpointSession(context.Context, SessionCheckpointBoundary) error {
	return nil
}

func (r *modelContextRecorderStub) RecordSessionModelContext(_ context.Context, commit SessionModelContextCommit) (SessionModelContextCommitResult, error) {
	r.commit = commit
	return r.result, r.err
}

func (*sequencedModelContextRecorder) CheckpointSession(context.Context, SessionCheckpointBoundary) error {
	return nil
}

func (r *sequencedModelContextRecorder) RecordSessionModelContext(_ context.Context, commit SessionModelContextCommit) (SessionModelContextCommitResult, error) {
	r.commits = append(r.commits, cloneSessionModelContextCommit(commit))
	if len(r.steps) == 0 {
		return SessionModelContextCommitResult{}, errors.New("unexpected model context commit")
	}
	step := r.steps[0]
	r.steps = r.steps[1:]
	return step.result, step.err
}

func (s *reentrantSnapshotSink) Emit(e event.Event) {
	if e.Kind != event.ContextMaintenanceEvent {
		return
	}
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	if s.agent != nil {
		_ = s.agent.ContextMaintenanceSnapshot()
	}
}

func TestCommitSummaryEmitsOutsideCompactionLock(t *testing.T) {
	prov := &fakeProvider{reply: "digest for reentrant emit"}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("work line\n", 800)},
		{Role: provider.RoleUser, Content: "continue"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("more work\n", 800)},
		{Role: provider.RoleUser, Content: "tail"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}
	sink := &reentrantSnapshotSink{}
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 20_000, CompactRatio: 0.5, WorkspaceID: "ws", ModelRef: "p/m",
	}, sink)
	sink.agent = a
	if err := a.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	sink.mu.Lock()
	n := sink.n
	sink.mu.Unlock()
	if n == 0 {
		t.Fatal("expected context_maintenance emit after checkpoint install")
	}
	if got := a.currentProjectionVersion(); got != 1 {
		t.Fatalf("projection version = %d, want 1", got)
	}
}

func TestCommitSummaryRetainsAcceptedProjectionOnDurabilityFailure(t *testing.T) {
	recorder := &modelContextRecorderStub{
		result: SessionModelContextCommitResult{Accepted: true},
		err:    errors.New("injected flush failure"),
	}
	a := New(&fakeProvider{reply: "durability failure digest"}, tool.NewRegistry(), &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("work line\n", 800)},
		{Role: provider.RoleUser, Content: "continue"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("more work\n", 800)},
		{Role: provider.RoleUser, Content: "tail"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}, Options{ContextWindow: 20_000, CompactRatio: 0.5, SessionCheckpointer: recorder}, event.Discard)

	if err := a.CompactNow(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "injected flush failure") {
		t.Fatalf("CompactNow error = %v, want durability failure", err)
	}
	if got := a.currentProjectionVersion(); got != 1 {
		t.Fatalf("accepted projection version = %d, want 1", got)
	}
	if len(recorder.commit.Messages) == 0 || recorder.commit.OperationID == "" {
		t.Fatalf("recorder received incomplete commit: %+v", recorder.commit)
	}
	if a.sess.checkpointState != "pending" {
		t.Fatalf("checkpoint state = %q, want pending", a.sess.checkpointState)
	}
}

func TestPendingProjectionBlocksModelUntilExactCommitIsDurable(t *testing.T) {
	recorder := &sequencedModelContextRecorder{steps: []modelContextRecorderStep{
		{result: SessionModelContextCommitResult{Accepted: true}, err: errors.New("initial flush failure")},
		{result: SessionModelContextCommitResult{Accepted: true}, err: errors.New("retry flush failure")},
		{result: SessionModelContextCommitResult{Accepted: true, Durable: true}},
	}}
	prov := &scriptedProvider{name: "model", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: "durable retry digest"}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "continued"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, tool.NewRegistry(), &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("work line\n", 800)},
		{Role: provider.RoleUser, Content: "continue"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("more work\n", 800)},
		{Role: provider.RoleUser, Content: "tail"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}, Options{ContextWindow: 20_000, CompactRatio: 0.5, SessionCheckpointer: recorder}, event.Discard)

	if err := a.CompactNow(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "initial flush failure") {
		t.Fatalf("CompactNow error = %v, want initial durability failure", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider calls after summary = %d, want 1", prov.call)
	}
	if err := a.Run(t.Context(), "must stay blocked"); err == nil || !strings.Contains(err.Error(), "retry flush failure") {
		t.Fatalf("blocked Run error = %v, want retry durability failure", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider dispatched with pending durability: calls=%d, want 1", prov.call)
	}
	if err := a.Run(t.Context(), "continue after durable"); err != nil {
		t.Fatalf("Run after durable retry: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls after recovery = %d, want 2", prov.call)
	}
	if len(recorder.commits) != 3 {
		t.Fatalf("model context commits = %d, want 3", len(recorder.commits))
	}
	for i := 1; i < len(recorder.commits); i++ {
		if !reflect.DeepEqual(recorder.commits[0], recorder.commits[i]) {
			t.Fatalf("retry %d changed accepted commit\nfirst=%+v\nretry=%+v", i, recorder.commits[0], recorder.commits[i])
		}
	}
	if a.sess.pendingModelContextCommit != nil || a.sess.checkpointState != "applied" {
		t.Fatalf("pending checkpoint not cleared: pending=%+v state=%q", a.sess.pendingModelContextCommit, a.sess.checkpointState)
	}
}

func TestCommitSummaryRollsBackRejectedProjection(t *testing.T) {
	recorder := &modelContextRecorderStub{err: errors.New("injected prepare failure")}
	a := New(&fakeProvider{reply: "rejected projection digest"}, tool.NewRegistry(), &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("work line\n", 800)},
		{Role: provider.RoleUser, Content: "continue"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("more work\n", 800)},
		{Role: provider.RoleUser, Content: "tail"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}, Options{ContextWindow: 20_000, CompactRatio: 0.5, SessionCheckpointer: recorder}, event.Discard)

	if err := a.CompactNow(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "injected prepare failure") {
		t.Fatalf("CompactNow error = %v, want prepare failure", err)
	}
	if got := a.currentProjectionVersion(); got != 0 {
		t.Fatalf("rejected projection version = %d, want 0", got)
	}
}
