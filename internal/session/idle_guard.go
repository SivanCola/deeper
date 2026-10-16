package session

import (
	"context"
	"errors"
	"fmt"

	"deepzora/internal/filelock"
)

// BeforeFirstExecution grants startup maintenance under an already-owned
// writer. The query must belong to that runtime's service. Holding runtime.mu
// prevents execution binding or closure while work runs; a runtime that ever
// bound execution cannot use this entry, even after its controller detaches.
// Work must not call back into the runtime or its owner.
func (q *Query) BeforeFirstExecution(ctx context.Context, runtime *Runtime, work func() error) (bool, error) {
	if q == nil || runtime == nil || runtime.owner == nil || runtime.owner.Query() != q {
		return false, fmt.Errorf("session: startup maintenance requires the owning catalog")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if runtime.phase != RuntimeIdle || runtime.bindGen.Load() != 0 || runtime.execution.Load() != nil {
		return false, nil
	}
	if work == nil {
		return true, nil
	}
	return true, work()
}

// WhileIdle runs maintenance only while the observed session has no writer.
// It holds the same exclusion as runtime admission without opening, replaying,
// repairing, or advancing the parent's store. Missing parents grant no work.
func (q *Query) WhileIdle(ctx context.Context, id string, work func() error) (bool, error) {
	ref, err := q.ResolveSessionID(ctx, id)
	if errors.Is(err, ErrSessionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return false, fmt.Errorf("session: writer exclusion is unavailable")
	}
	dir, err := filesystem.sessionDir(ref.SessionID, true)
	if err != nil {
		return false, err
	}
	release, err := acquireSessionWriter(dir)
	if errors.Is(err, filelock.ErrHeld) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Recheck identity after acquisition: deletion/replacement could have won
	// between discovery and writer exclusion.
	if _, err := q.Stat(ctx, ref); err != nil {
		return false, err
	}
	if work == nil {
		return true, nil
	}
	return true, work()
}
