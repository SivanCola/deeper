package usagecatalog

import (
	"context"
	"path/filepath"
	"testing"

	"deepzora/internal/testutil"
)

func TestCloseCanBeAwaitedAfterCanceledCaller(t *testing.T) {
	catalog, err := Open(context.Background(), filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	testutil.AssertCloseAwaitedAfterCanceledCaller(t, catalog.Close, func() bool { return catalog.db.Ping() != nil })
}
