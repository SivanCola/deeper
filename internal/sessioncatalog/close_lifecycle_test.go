package sessioncatalog

import (
	"context"
	"testing"

	"deepzora/internal/testutil"
)

func TestCloseCanBeAwaitedAfterCanceledCaller(t *testing.T) {
	catalog, err := Open(context.Background(), Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	testutil.AssertCloseAwaitedAfterCanceledCaller(t, catalog.Close, func() bool { return catalog.db.Ping() != nil })
}
