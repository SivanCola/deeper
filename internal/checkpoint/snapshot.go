package checkpoint

import "fmt"

// FreezeSnapshot excludes both mutations/rewinds and checkpoint metadata/GC
// while a session export copies preimages and transaction records. Never wait
// for a writer here: it may itself be waiting for a session durability flush.
func (s *Store) FreezeSnapshot(fn func() error) error {
	if !s.barrier.TryEnterExclusive() {
		return fmt.Errorf("file checkpoints have an active writer or rewind")
	}
	defer s.barrier.ExitExclusive()
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn()
}

// TurnIDs connects persisted file checkpoints to immutable session turn IDs.
// Message indexes are executor offsets and cannot identify a turn after reopen.
func (s *Store) TurnIDs() map[int]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make(map[int]string)
	for _, c := range s.all() {
		ids[c.Turn] = c.TurnID
	}
	return ids
}
