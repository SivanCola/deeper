package sessioninbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFreezeSnapshotKeepsMissingInboxAbsentAndExcludesTransactions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	release, err := FreezeSnapshot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read created a missing inbox: %v", err)
	}
	store, err := OpenAt("session-id:current", dir, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	release, err = FreezeSnapshot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if unlock, err := FreezeSnapshot(ctx, dir); err == nil {
		unlock()
		t.Fatal("competing snapshot did not join the queue transaction lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended snapshot = %v", err)
	}
}
