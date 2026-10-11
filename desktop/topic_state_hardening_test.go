package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/topicstate"
)

func TestCompoundTopicMutationsCommitOneRevision(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspaceRoot := t.TempDir()
	if err := createTopicState(workspaceRoot, "topic-1", defaultTopicTitle, topicTitleSourceAuto, 123); err != nil {
		t.Fatal(err)
	}
	created, err := desktopTopicState.snapshot(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if created.State.Revision != 1 {
		t.Fatalf("create revision = %d, want 1", created.State.Revision)
	}
	record := created.Records["topic-1"]
	if record.Title != defaultTopicTitle || record.TitleSource != topicTitleSourceAuto || record.CreatedAtMS != 123 {
		t.Fatalf("created record is partial: %+v", record)
	}
	proposal := autoTopicTitleProposal{Title: "Generated title", Stage: 2, UserTurns: 3, BasisHash: "basis"}
	if applied, err := applyAutoTopicTitle(workspaceRoot, "topic-1", proposal.Title, proposal); err != nil {
		t.Fatal(err)
	} else if !applied {
		t.Fatal("automatic title was not applied")
	}
	updated, err := desktopTopicState.snapshot(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State.Revision != 2 {
		t.Fatalf("auto-title revision = %d, want 2", updated.State.Revision)
	}
	record = updated.Records["topic-1"]
	var meta topicAutoTitleMeta
	if err := json.Unmarshal(record.AutoMeta, &meta); err != nil {
		t.Fatal(err)
	}
	if record.Title != proposal.Title || record.TitleSource != topicTitleSourceAuto || meta.Stage != proposal.Stage || meta.BasisHash != proposal.BasisHash {
		t.Fatalf("auto-title record is partial: record=%+v meta=%+v", record, meta)
	}
}

func TestAutomaticTitleDoesNotOverwriteConcurrentManualRename(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspaceRoot := t.TempDir()
	if err := createTopicState(workspaceRoot, "topic-1", defaultTopicTitle, topicTitleSourceAuto, 123); err != nil {
		t.Fatal(err)
	}
	proposal := autoTopicTitleProposal{Title: "Stale automatic title", Stage: 2, UserTurns: 3, BasisHash: "basis"}
	if err := setTopicTitle(workspaceRoot, "topic-1", "Manual rename wins"); err != nil {
		t.Fatal(err)
	}
	if applied, err := applyAutoTopicTitle(workspaceRoot, "topic-1", proposal.Title, proposal); err != nil {
		t.Fatal(err)
	} else if applied {
		t.Fatal("stale automatic title overwrote a manual rename")
	}
	snapshot, err := desktopTopicState.snapshot(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	record := snapshot.Records["topic-1"]
	if record.Title != "Manual rename wins" || record.TitleSource != topicTitleSourceManual || len(record.AutoMeta) != 0 {
		t.Fatalf("manual record changed: %+v", record)
	}
}

func TestFutureTopicSchemaWithoutLegacyReturnsVisibleReadError(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspaceRoot := t.TempDir()
	path := config.DesktopTopicStatePath(workspaceRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(999, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTopicTitlesForUpdate(workspaceRoot); err == nil {
		t.Fatal("future schema without legacy unexpectedly became an empty title map")
	} else {
		var future *topicstate.FutureSchemaError
		if !errors.As(err, &future) {
			t.Fatalf("update read error = %v, want FutureSchemaError", err)
		}
	}
	_, err = NewApp().ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: workspaceRoot})
	if err == nil || !strings.Contains(err.Error(), "newer Deepzora version") {
		t.Fatalf("bridge read error = %v", err)
	}
	if strings.Contains(err.Error(), workspaceRoot) {
		t.Fatalf("bridge read error leaked workspace path: %v", err)
	}
}
