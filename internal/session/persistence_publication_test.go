package session

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestFlushSkipsUnchangedDurablePublications(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	scheduler := &manualScheduler{}
	var syncs atomic.Int64
	s, err := CreateWithOptions(dir, "session", OpenOptions{
		AfterFunc: scheduler.after,
		Sync: func(file *os.File) error {
			syncs.Add(1)
			return file.Sync()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := s.RecentSnapshot().DurableSequence; got != 0 {
		t.Fatalf("empty publication sequence = %d", got)
	}
	appendRecoveryTestMessage(t, s, "one", "first")
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := s.binding.handle.(*Store)
	recentPath := store.recovery.recent
	metadataPath := catalogMetadataPath(filepath.Join(filepath.Dir(dir), ".query-cache", "session"))
	recent, metadata := publicationFileInfo(t, recentPath), publicationFileInfo(t, metadataPath)
	transaction, beforeSyncs := recoveryTransactionID(t, store.recovery), syncs.Load()
	results := make(chan error, 8)
	for range cap(results) {
		go func() {
			_, err := s.Flush(t.Context())
			results <- err
		}()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := recoveryTransactionID(t, store.recovery); got != transaction {
		t.Fatalf("unchanged flush wrote recovery transaction: %d -> %d", transaction, got)
	}
	if got := syncs.Load(); got != beforeSyncs {
		t.Fatalf("unchanged flush synced the event log: %d -> %d", beforeSyncs, got)
	}
	assertPublicationUnchanged(t, recentPath, recent)
	assertPublicationUnchanged(t, metadataPath, metadata)

	appendRecoveryTestMessage(t, s, "two", "second")
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := store.recovery.loadCheckpoints()
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 2 || checkpoints[0].DurableSequence != 2 || checkpoints[1].DurableSequence != 1 {
		t.Fatalf("distinct recovery checkpoints = %+v", checkpoints)
	}
	if recent := s.RecentSnapshot(); recent.DurableSequence != 2 || len(recent.Entries) != 2 {
		t.Fatalf("advanced recent publication = %+v", recent)
	}
	revision, err := revisionOfLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := readCatalogMetadata(filepath.Dir(metadataPath), s.Manifest(), revision)
	if err != nil || catalog.Sequence != 2 {
		t.Fatalf("advanced catalog publication = %+v, %v", catalog, err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(dir, "session", OpenOptions{AfterFunc: scheduler.after})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if got := reopened.DeriveMessages(); len(got) != 2 || got[0].ID != "one" || got[1].ID != "two" {
		t.Fatalf("recovered messages = %+v", got)
	}
	appendRecoveryTestMessage(t, reopened, "one", "first")
	if got := reopened.EventSequence(); got != 2 {
		t.Fatalf("recovered duplicate operation advanced sequence to %d", got)
	}
}

func TestFlushRetriesOnlyFailedOrReplacedPublication(t *testing.T) {
	for _, target := range []string{"recovery", "catalog"} {
		t.Run(target, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "session")
			scheduler := &manualScheduler{}
			s, err := CreateWithOptions(dir, "session", OpenOptions{AfterFunc: scheduler.after})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close(context.Background()) })
			store := s.binding.handle.(*Store)
			recentPath := store.recovery.recent
			metadataPath := catalogMetadataPath(filepath.Join(filepath.Dir(dir), ".query-cache", "session"))
			failed, healthy := recentPath, metadataPath
			if target == "catalog" {
				failed, healthy = metadataPath, recentPath
			}
			if err := os.Remove(failed); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.MkdirAll(failed, 0o700); err != nil {
				t.Fatal(err)
			}
			appendRecoveryTestMessage(t, s, "one", "first")
			if _, err := s.Flush(t.Context()); err != nil {
				t.Fatalf("derived publication failure changed log durability: %v", err)
			}
			if got := s.StateSnapshot().DurableSequence; got != 1 {
				t.Fatalf("durable sequence after cache failure = %d", got)
			}
			before := publicationFileInfo(t, healthy)
			transaction := recoveryTransactionID(t, store.recovery)
			if err := os.Remove(failed); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			publicationFileInfo(t, failed)
			assertPublicationUnchanged(t, healthy, before)
			if target == "catalog" && recoveryTransactionID(t, store.recovery) != transaction {
				t.Fatal("catalog retry rewrote a successful recovery checkpoint")
			}
			if err := os.Remove(failed); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(failed, []byte("stale replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(failed)
			if err != nil || string(data) == "stale replacement" {
				t.Fatalf("same-sequence replacement was not repaired: %q, %v", data, err)
			}
			assertPublicationUnchanged(t, healthy, before)
		})
	}
}

func TestPublicationWitnessRejectsStaleReplacement(t *testing.T) {
	for _, field := range []string{"sequence", "durableSequence"} {
		t.Run(field, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "publication.json")
			if err := os.WriteFile(path, []byte(`{"`+field+`":1}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var witness durablePublication
			witness.record(path, 2)
			if witness.current(path, 2) {
				t.Fatal("older publication was certified at a newer durable sequence")
			}
			witness.record(path, 1)
			if !witness.current(path, 1) {
				t.Fatal("matching publication was not recorded")
			}
		})
	}
}

func recoveryTransactionID(t *testing.T, store *recoveryStore) int {
	t.Helper()
	var id int
	if err := store.db.View(func(tx *bolt.Tx) error { id = tx.ID(); return nil }); err != nil {
		t.Fatal(err)
	}
	return id
}

func publicationFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("publication %s is not a regular file", path)
	}
	return info
}

func assertPublicationUnchanged(t *testing.T, path string, before os.FileInfo) {
	t.Helper()
	after := publicationFileInfo(t, path)
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("unchanged durable prefix rewrote %s", path)
	}
}
