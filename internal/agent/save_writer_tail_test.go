package agent

import (
	"errors"
	"testing"
	"time"

	"deepzora/internal/provider"
)

func bindSessionWriter(t *testing.T, s *Session, path string) *SessionWriter {
	t.Helper()
	w, err := AcquireSessionWriter(path)
	if err != nil {
		t.Fatalf("AcquireSessionWriter: %v", err)
	}
	t.Cleanup(w.Release)
	if err := w.Bind(s, NextSessionWriteGeneration()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return w
}

func TestWriterBoundSaveHonorsCurrentFileLock(t *testing.T) {
	path := currentDAGSessionPath(t, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	bindSessionWriter(t, s, path)
	held, err := lockSessionFile(path)
	if err != nil {
		t.Fatalf("lockSessionFile: %v", err)
	}
	defer held()
	prevWait, prevPoll := sessionFileLockWait, sessionFileLockPollInterval
	sessionFileLockWait = 40 * time.Millisecond
	sessionFileLockPollInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		sessionFileLockWait = prevWait
		sessionFileLockPollInterval = prevPoll
	})
	if err := s.SaveSnapshot(path); !errors.Is(err, ErrSessionFileLockHeld) {
		t.Fatalf("writer-bound save error = %v, want ErrSessionFileLockHeld", err)
	}
}
