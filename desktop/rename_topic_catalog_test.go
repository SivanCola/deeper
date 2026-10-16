package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameTopicDoesNotDiscoverHistoricalCatalog(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := robustTempDir(t)
	if err := addProject(root, "Catalog Only Project"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_catalog_only"
	// The session exists in the project session dir with branch metadata, but
	// no topic title index entry and no open tab: the catalog is the only
	// place that knows the topic.
	writeTopicSessionWithPrompt(t, dir, "catalog-only.jsonl", topicID, "旧标题", root, "catalog only prompt", time.Now())

	app := NewApp()
	waitForInitialCatalogReconcile(t, app)

	path := filepath.Join(dir, "catalog-only.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeMeta, err := os.ReadFile(path + ".meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RenameTopic(topicID, "新标题"); err == nil {
		t.Fatal("historical catalog must not provide a current topic identity")
	}
	if afterMeta, err := os.ReadFile(path + ".meta"); err != nil || string(afterMeta) != string(beforeMeta) {
		t.Fatalf("historical metadata changed: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("historical bytes changed: %v", err)
	}

}

func TestRenameTopicUnknownTopicStillFails(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	if err := app.RenameTopic("topic_does_not_exist", "标题"); err == nil {
		t.Fatal("rename of an unknown topic should fail")
	}
}
