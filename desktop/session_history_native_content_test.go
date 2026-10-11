package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
)

func TestCompatibilityNativeBorrowCancellationKeepsOtherReader(t *testing.T) {
	a := historySliceTestApp(t)
	t.Cleanup(a.closeHistoryReaders)
	tab := newColdHistoryTab(t, a)
	dir := tabSessionDir(tab)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	tab.SessionPath = filepath.Join(dir, "shared.jsonl")
	if err := os.WriteFile(tab.SessionPath, []byte("{\"role\":\"user\",\"content\":\"question\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	handle, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.ReleaseSessionHistoryRead(handle.ID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = a.withNativeHistoryPager(ctx, tab.SessionPath, "", func(ctx context.Context, pager *agent.DisplayPager, _ string) error {
		cancel()
		_, err := pager.Entries(0, 1)
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled compatibility reader continued: %v", err)
	}
	page, err := a.ReadSessionHistorySlice(handle.ID, HistorySliceRequest{Entries: 1})
	if err != nil || page.Status != "ready" || len(page.Page.Entries) != 1 {
		t.Fatalf("borrower cancelled its peer: %+v %v", page, err)
	}
}
