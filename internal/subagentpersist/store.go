// Package subagentpersist connects child Agents to the host's current session
// service. It owns neither a global runtime nor a legacy transcript mirror.
package subagentpersist

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

type Store struct{ service *session.Service }

// New accepts the child catalog from the parent service's Subagents method.
// The parent owns shutdown; replacement Agents share this same catalog.
func New(service *session.Service) *Store { return &Store{service: service} }

func (s *Store) Root() string { return s.service.Query().StorageRoot() }

func (s *Store) Create(ctx context.Context, meta agent.SubagentMeta, messages []provider.Message) (agent.SubagentTranscript, error) {
	runtime, err := s.service.Create(ctx, session.CreateOptions{SessionID: meta.Ref, CWD: meta.WorkspaceRoot})
	if err != nil {
		return nil, err
	}
	owner, err := s.service.Owner(runtime)
	if err != nil {
		return nil, err
	}
	binding, err := owner.Bind()
	if err != nil {
		return nil, errors.Join(err, owner.Close(context.Background()))
	}
	handle, err := s.bind(binding)
	if err != nil {
		return nil, err
	}
	if err := handle.RecordSessionMessages(ctx, "initial-prompt", messages); err != nil {
		return nil, errors.Join(err, handle.Close(context.Background()))
	}
	return handle, nil
}

func (s *Store) Open(ctx context.Context, id string) (agent.SubagentTranscript, error) {
	ref, err := s.service.Query().ResolveSessionID(ctx, id)
	if err != nil {
		return nil, err
	}
	binding, err := s.service.Open(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s.bind(binding)
}

func (s *Store) Copy(ctx context.Context, source string, meta agent.SubagentMeta) (agent.SubagentTranscript, error) {
	writer, err := s.Open(ctx, source)
	if err != nil {
		return nil, err
	}
	defer writer.Close(context.Background())
	if err := writer.Flush(ctx); err != nil {
		return nil, err
	}
	ref, err := s.service.Query().ResolveSessionID(ctx, source)
	if err != nil {
		return nil, err
	}
	result, err := s.service.CopySession(ctx, session.CopyRequest{Source: ref, ChildID: meta.Ref, OperationID: "subagent-copy:" + meta.Ref, CWD: meta.WorkspaceRoot})
	if err != nil {
		return nil, err
	}
	return s.Open(ctx, result.Child.SessionID)
}

func (s *Store) Read(ctx context.Context, id string) ([]provider.Message, error) {
	ref, err := s.service.Query().ResolveSessionID(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.service.Query().Snapshot(ctx, ref)
	if err != nil {
		return nil, err
	}
	return snapshot.Projection.Messages, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	ref, err := s.service.Query().ResolveSessionID(ctx, id)
	if errors.Is(err, session.ErrSessionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.service.Delete(ctx, ref)
}

func (s *Store) bind(binding *session.ClientBinding) (*transcript, error) {
	runtime := binding.Runtime()
	owner, err := s.service.Owner(runtime)
	if err != nil {
		return nil, errors.Join(err, binding.Release(context.Background()))
	}
	handle := &transcript{runtime: runtime, binding: binding, owner: owner}
	handle.generation = runtime.BindExecution(handle)
	if handle.generation == 0 {
		return nil, errors.Join(fmt.Errorf("subagent transcript is already running: %w", session.ErrRuntimeBusy), binding.Release(context.Background()))
	}
	runtime.NoteExecution(handle.generation, session.RuntimeRunning, "subagent")
	return handle, nil
}

type transcript struct {
	mu         sync.Mutex
	runtime    *session.Runtime
	binding    *session.ClientBinding
	owner      *session.RuntimeOwner
	generation uint64
	closed     bool
	closeErr   error
}

// Parent job lifecycle owns cancellation. This record-only execution binding
// exists to fence competing continuations, not to introduce another task loop.
func (t *transcript) Cancel() bool { return false }
func (t *transcript) Snapshot() session.RuntimeSnapshot {
	return session.RuntimeSnapshot{Ref: t.runtime.Ref(), Phase: session.RuntimeRunning, Activity: "subagent"}
}
func (t *transcript) Messages() []provider.Message {
	return t.runtime.Session().ExecutionSnapshot().Projection.ModelMessages
}
func (t *transcript) Flush(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return session.ErrStaleExecution
	}
	_, err := t.runtime.Session().Flush(ctx)
	return err
}
func (t *transcript) CheckpointSession(ctx context.Context, _ agent.SessionCheckpointBoundary) error {
	return t.Flush(ctx)
}
func (t *transcript) Close(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return t.closeErr
	}
	t.closed = true
	_, flushErr := t.runtime.Session().Flush(ctx)
	t.runtime.NoteExecution(t.generation, session.RuntimeIdle, "")
	t.runtime.UnbindExecution(t.generation)
	releaseErr := t.binding.Release(ctx)
	closeErr := t.owner.Close(ctx)
	if errors.Is(closeErr, session.ErrRuntimeBound) || errors.Is(closeErr, session.ErrSessionNotRunning) {
		// Another continuation may have acquired the shared runtime after our
		// binding was released. Its host retains that exact writer.
		closeErr = nil
	}
	t.closeErr = errors.Join(flushErr, releaseErr, closeErr)
	return t.closeErr
}

var _ agent.SubagentPersistence = (*Store)(nil)
var _ agent.SubagentTranscript = (*transcript)(nil)
