package sessioncatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRebuildFailureKeepsExistingCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	catalog, err := Open(ctx, Options{Path: path, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	session := filepath.Join(dir, "keep.jsonl")
	if err := os.WriteFile(session, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertSession(ctx, SessionRecord{
		Path: session, Directory: dir, Scope: "global", TopicID: "keep",
		Turns: 1, TurnsState: TurnsValid, Health: HealthOK, Preview: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// ListSessionOrder on a regular file fails; Rebuild must keep the old DB.
	fileTarget := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileTarget, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(ctx, path, []DirectoryTarget{{Path: fileTarget, Scope: "global"}}); err == nil {
		t.Fatal("expected rebuild failure for file path")
	}
	restored, err := Open(ctx, Options{Path: path, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close(context.Background()) })
	topic, ok, err := restored.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "keep"})
	if err != nil || !ok || len(topic.Sessions) != 1 {
		t.Fatalf("rebuild failure lost catalog: ok=%v topic=%#v err=%v", ok, topic, err)
	}
}
