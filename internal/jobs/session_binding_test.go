package jobs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"deepzora/internal/event"
)

func TestBindSessionMigratesRunningJobArtifacts(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	m := NewManager(event.Discard)
	defer m.Close()

	wroteBefore := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	j := m.StartForSession("session", "bash", "migrate", func(_ context.Context, out io.Writer) (string, error) {
		_, _ = io.WriteString(out, "before\n")
		close(wroteBefore)
		<-release
		_, _ = io.WriteString(out, "after\n")
		return "", nil
	})
	<-wroteBefore
	j.mu.Lock()
	oldPath := j.artifactPath
	j.mu.Unlock()

	m.BindSession("session", sessionDir)
	j.mu.Lock()
	gotPath := j.artifactPath
	j.mu.Unlock()
	if gotPath != oldPath {
		t.Fatalf("running artifact path = %q, want unchanged %q before completion", gotPath, oldPath)
	}

	releaseOnce.Do(func() { close(release) })
	<-j.done
	j.mu.Lock()
	donePath := j.artifactPath
	j.mu.Unlock()
	if !strings.HasPrefix(donePath, ArtifactDir(sessionDir)+string(filepath.Separator)) {
		t.Fatalf("completed artifact path = %q, want under %q", donePath, ArtifactDir(sessionDir))
	}
	res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "before\n") || !strings.Contains(res[0].Output, "after\n") {
		t.Fatalf("wait after migration = %+v, want before and after output", res)
	}
}

func TestBindSessionAdoptsUnscopedTemporaryJobs(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.StartForSession("", "task", "temporary", func(context.Context, io.Writer) (string, error) {
		return "temporary answer", nil
	})
	<-j.done

	m.BindSession("session", sessionDir)
	res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "temporary answer") {
		t.Fatalf("adopted wait = %+v, want temporary answer", res)
	}
	if _, _, ok := m.OutputForSession("", j.ID); !ok {
		t.Fatalf("unscoped lookup should still find adopted job %s", j.ID)
	}
	if _, err := os.Stat(filepath.Join(ArtifactDir(sessionDir), j.ID+jobLogExt)); err != nil {
		t.Fatalf("adopted artifact should be under session directory: %v", err)
	}
}

func TestBindSessionAdoptsUnscopedJobsOnMigrationFailure(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ArtifactDir(sessionDir), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.StartForSession("", "task", "temporary", func(context.Context, io.Writer) (string, error) {
		return "temporary answer", nil
	})
	<-j.done

	m.BindSession("session", sessionDir)

	out, status, ok := m.OutputForSession("session", j.ID)
	if !ok || status != Done {
		t.Fatalf("scoped output ok/status = %v/%s, want true/done", ok, status)
	}
	if !strings.Contains(out, "temporary answer") || !strings.Contains(out, "job artifact incomplete: migration:") {
		t.Fatalf("scoped output = %q, want answer and migration error", out)
	}
	res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "job artifact incomplete: migration:") {
		t.Fatalf("scoped wait = %+v, want migration error", res)
	}
	if note := m.DrainCompletedNoteForSession("session"); !strings.Contains(note, j.ID) {
		t.Fatalf("adopted completion note = %q, want job id %s", note, j.ID)
	}
}

func TestBindSessionReportsMigrationFailure(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ArtifactDir(sessionDir), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	var noticesMu sync.Mutex
	var notices []event.Event
	m := NewManager(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			noticesMu.Lock()
			notices = append(notices, e)
			noticesMu.Unlock()
		}
	}))
	defer m.Close()

	j := m.StartForSession("session", "task", "migrate fail", func(context.Context, io.Writer) (string, error) {
		return "answer", nil
	})
	m.BindSession("session", sessionDir)
	<-j.done

	res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "job artifact incomplete: migration:") {
		t.Fatalf("wait after migration failure = %+v, want artifact error", res)
	}
	noticesMu.Lock()
	capturedNotices := append([]event.Event(nil), notices...)
	noticesMu.Unlock()
	found := false
	for _, notice := range capturedNotices {
		if notice.Text == "Job artifact migration failed." && strings.Contains(notice.Detail, "job artifact migration failed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("migration failure notice not emitted, got %+v", capturedNotices)
	}
}
