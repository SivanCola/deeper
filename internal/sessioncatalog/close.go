package sessioncatalog

import (
	"context"

	"deepzora/internal/projectiondb"
)

// Stopping closes when Close starts shutting down the discovery workers.
// Queued reconcile signals may remain unsettled when discovery is paused or
// interrupted, so callers waiting on them must also observe this signal.
func (c *Catalog) Stopping() <-chan struct{} { return c.stop }

func (c *Catalog) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.stopOnce.Do(func() {
		if c.workerCancel != nil {
			c.workerCancel()
		}
		close(c.stop)
		go func() {
			c.closeReadLeases()
			c.workers.Wait()
			projectiondb.CheckpointBeforeClose(context.Background(), c.db)
			c.closeErr = c.db.Close()
			c.statusMu.Lock()
			c.status.State = StateClosed
			c.statusMu.Unlock()
			close(c.closeDone)
		}()
	})
	select {
	case <-c.closeDone:
		return c.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
