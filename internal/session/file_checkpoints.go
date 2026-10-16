package session

import (
	"path/filepath"
	"sync"

	"deepzora/internal/checkpoint"
)

// File checkpoints belong to the current session, not to a replaceable UI
// controller or a historical transcript filename.
type fileCheckpointOwner struct {
	mu    sync.Mutex
	store *checkpoint.Store
}

func (s *Session) FileCheckpoints(root string, opts ...checkpoint.Option) *checkpoint.Store {
	s.fileCheckpoints.mu.Lock()
	defer s.fileCheckpoints.mu.Unlock()
	if s.fileCheckpoints.store == nil {
		dir := s.dir()
		if dir != "" {
			dir = filepath.Join(dir, "file-checkpoints")
		}
		s.fileCheckpoints.store = checkpoint.New(dir, root, opts...)
		s.fileCheckpoints.store.SetSessionID(s.ID())
	}
	return s.fileCheckpoints.store
}

func (s *Session) freezeFileCheckpoints(fn func() error) error {
	s.fileCheckpoints.mu.Lock()
	defer s.fileCheckpoints.mu.Unlock()
	if s.fileCheckpoints.store == nil {
		return fn()
	}
	return s.fileCheckpoints.store.FreezeSnapshot(fn)
}
