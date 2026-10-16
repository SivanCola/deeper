package agent

import (
	"context"
	"testing"
)

// The external test package installs the real current store without making
// Agent import Session (Session already uses Agent's message projection).
type CurrentPersistenceHarness struct {
	Store SubagentPersistence
	Copy  func(context.Context, string, string) error
	Close func() error
}

var CurrentPersistenceFactoryForTest func(string) (CurrentPersistenceHarness, error)

type currentPersistenceSession struct {
	store  SubagentPersistence
	copy   func(context.Context, string, string) error
	ref    string
	handle SubagentTranscript
}

func newCurrentPersistenceSession(t *testing.T, seed *Session) *currentPersistenceSession {
	t.Helper()
	if CurrentPersistenceFactoryForTest == nil {
		t.Fatal("current persistence test factory is unavailable")
	}
	harness, err := CurrentPersistenceFactoryForTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Error(err)
		}
	})
	state := &currentPersistenceSession{store: harness.Store, copy: harness.Copy, ref: "sa_current"}
	messages := seed.Snapshot()
	for i := range messages {
		if messages[i].ID == "" {
			messages[i].ID = NewMessageID()
		}
	}
	state.handle, err = state.store.Create(t.Context(), SubagentMeta{Ref: state.ref}, messages)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if state.handle != nil {
			if err := state.handle.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	return state
}

func (s *currentPersistenceSession) conversation() *Session {
	conversation := NewSession("")
	conversation.Replace(s.handle.Messages())
	return conversation
}

func (s *currentPersistenceSession) reopen(t *testing.T, ref string) *Session {
	t.Helper()
	if err := s.handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	handle, err := s.store.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	s.ref, s.handle = ref, handle
	return s.conversation()
}

// history reads full durable messages, including host-only display metadata.
// The execution workset returned by conversation intentionally omits some of it.
func (s *currentPersistenceSession) history(t *testing.T) *Session {
	t.Helper()
	messages, err := s.store.Read(t.Context(), s.ref)
	if err != nil {
		t.Fatal(err)
	}
	conversation := NewSession("")
	conversation.Replace(messages)
	return conversation
}

func roundTripCurrentSession(t *testing.T, seed *Session) *Session {
	t.Helper()
	state := newCurrentPersistenceSession(t, seed)
	state.reopen(t, state.ref)
	return state.history(t)
}
