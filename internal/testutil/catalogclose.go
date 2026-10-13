package testutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

// AssertCloseAwaitedAfterCanceledCaller pins the close contract shared by the
// projection catalogs (session, history, task, usage): a Close with an
// already-canceled caller context must not wedge or report a spurious failure,
// a later Close must be able to await the background shutdown, and the database
// must be closed once that await returns.
func AssertCloseAwaitedAfterCanceledCaller(t *testing.T, closeCatalog func(context.Context) error, databaseClosed func() bool) {
	t.Helper()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := closeCatalog(canceled); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first close: %v", err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := closeCatalog(ctx); err != nil {
		t.Fatalf("await eventual close: %v", err)
	}
	if !databaseClosed() {
		t.Fatal("database remained open after close completion")
	}
}
