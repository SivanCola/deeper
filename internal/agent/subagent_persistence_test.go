package agent

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"deepzora/internal/provider"
)

// The Agent tests exercise invocation policy through an explicit recorder.
// Filesystem/current-session durability is tested by subagentpersist.
type testSubagentPersistence struct {
	dir     string
	mu      sync.Mutex
	records map[string]*testSubagentTranscript
}
type testSubagentTranscript struct {
	parent      *testSubagentPersistence
	id          string
	full, model []provider.Message
	active      bool
	flushErr    error
}

func (s *testSubagentPersistence) Root() string { return s.dir }
func (s *testSubagentPersistence) Create(_ context.Context, meta SubagentMeta, messages []provider.Message) (SubagentTranscript, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := &testSubagentTranscript{parent: s, id: meta.Ref, full: append([]provider.Message(nil), messages...), model: append([]provider.Message(nil), messages...), active: true}
	s.records[meta.Ref] = h
	return h, nil
}
func (s *testSubagentPersistence) Open(_ context.Context, id string) (SubagentTranscript, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.records[id]
	if h == nil {
		return nil, os.ErrNotExist
	}
	if h.active {
		return nil, errors.New("already running")
	}
	h.active = true
	return h, nil
}
func (s *testSubagentPersistence) Copy(ctx context.Context, source string, meta SubagentMeta) (SubagentTranscript, error) {
	s.mu.Lock()
	old := s.records[source]
	if old == nil {
		s.mu.Unlock()
		return nil, os.ErrNotExist
	}
	full, model := append([]provider.Message(nil), old.full...), append([]provider.Message(nil), old.model...)
	s.mu.Unlock()
	h, err := s.Create(ctx, meta, full)
	if err == nil {
		h.(*testSubagentTranscript).model = model
	}
	return h, err
}
func (s *testSubagentPersistence) Read(_ context.Context, id string) ([]provider.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.records[id]
	if h == nil {
		return nil, os.ErrNotExist
	}
	return append([]provider.Message(nil), h.full...), nil
}
func (s *testSubagentPersistence) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.records[id]; h != nil && h.active {
		return errors.New("already running")
	}
	delete(s.records, id)
	return nil
}
func (h *testSubagentTranscript) Messages() []provider.Message {
	return append([]provider.Message(nil), h.model...)
}
func (h *testSubagentTranscript) Flush(context.Context) error { return h.flushErr }
func (h *testSubagentTranscript) Close(context.Context) error {
	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()
	h.active = false
	return h.flushErr
}
func (h *testSubagentTranscript) CheckpointSession(ctx context.Context, _ SessionCheckpointBoundary) error {
	return h.Flush(ctx)
}
func (h *testSubagentTranscript) RecordSessionMessages(_ context.Context, _ string, messages []provider.Message) error {
	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()
	h.full = append(h.full, messages...)
	h.model = append(h.model, messages...)
	return nil
}
func (h *testSubagentTranscript) RecordSessionMessageUpsert(_ context.Context, _ string, message provider.Message) error {
	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()
	for _, messages := range [][]provider.Message{h.full, h.model} {
		for i := range messages {
			if messages[i].ID == message.ID {
				messages[i] = message
			}
		}
	}
	return nil
}
func (h *testSubagentTranscript) RecordSessionModelContext(_ context.Context, request SessionModelContextCommit) (SessionModelContextCommitResult, error) {
	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()
	h.model = append([]provider.Message(nil), request.Messages...)
	return SessionModelContextCommitResult{Accepted: true, Durable: true}, nil
}
func appendSubagentMessages(t *testing.T, run *SubagentRun, messages ...provider.Message) {
	t.Helper()
	for i := range messages {
		if messages[i].ID == "" {
			messages[i].ID = NewMessageID()
		}
	}
	if run.Persistence != nil {
		if err := run.Persistence.RecordSessionMessages(t.Context(), "fixture", messages); err != nil {
			t.Fatal(err)
		}
	}
	run.Session.AddBatch(messages...)
}
