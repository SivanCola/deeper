package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/jobs"
	"deepzora/internal/store"
)

func occupyReadFileWithTimeoutSlots(t *testing.T) func() {
	t.Helper()
	filled := 0
	for filled < cap(readFileWithTimeoutSlots) {
		readFileWithTimeoutSlots <- struct{}{}
		filled++
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		for range filled {
			<-readFileWithTimeoutSlots
		}
	}
	t.Cleanup(release)
	return release
}

// deleteSessionFile

func TestRemoveDesktopSessionArtifactsRemovesOwnedSidecars(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	for _, p := range []string{
		sessionPath,
		store.SessionMeta(sessionPath),
		store.SessionGoalState(sessionPath),
		store.SessionEventLog(sessionPath),
		store.SessionEventIndex(sessionPath),
		store.SessionConflictLog(sessionPath),
		sessionTelemetryPath(sessionPath),
		store.SessionLockFile(sessionPath),
		store.SessionLeaseLock(sessionPath),
		store.SessionLeaseLock(sessionPath),
	} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	if err := recordSessionDisplay(dir, sessionPath, "expanded prompt", "[Pasted text #1 · 2 lines]"); err != nil {
		t.Fatalf("record display: %v", err)
	}
	ckptDir := store.SessionCheckpointDir(sessionPath)
	if err := os.MkdirAll(ckptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ckptDir, "1.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	jobsDir := jobs.ArtifactDir(sessionPath)
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobsDir, "job.log"), []byte("output"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeDesktopSessionArtifacts(sessionPath); err != nil {
		t.Fatalf("removeDesktopSessionArtifacts: %v", err)
	}

	for _, p := range []string{
		sessionPath,
		store.SessionMeta(sessionPath),
		store.SessionGoalState(sessionPath),
		store.SessionEventLog(sessionPath),
		store.SessionEventIndex(sessionPath),
		store.SessionConflictLog(sessionPath),
		sessionTelemetryPath(sessionPath),
		store.SessionLockFile(sessionPath),
		store.SessionLeaseLock(sessionPath),
		store.SessionLeaseLock(sessionPath),
		ckptDir,
		jobsDir,
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed, stat err = %v", p, err)
		}
	}
	if got := resolveSessionDisplay(dir, sessionPath, "expanded prompt"); got != "expanded prompt" {
		t.Fatalf("session display key should be removed, got %q", got)
	}
}

func TestSessionDisplayRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	content := "prefix\n--- Begin [Pasted text #1 · 5 lines] ---\nfull text\n--- End [Pasted text #1 · 5 lines] ---"
	display := "[Pasted text #1 · 5 lines]"
	if err := recordSessionDisplay(dir, sessionPath, content, display); err != nil {
		t.Fatalf("record display: %v", err)
	}
	if got := resolveSessionDisplay(dir, sessionPath, content); got != display {
		t.Fatalf("display = %q, want %q", got, display)
	}
	if got := resolveSessionDisplay(dir, sessionPath, "other"); got != "other" {
		t.Fatalf("unknown content should pass through, got %q", got)
	}
}

func TestRecordSessionPlannerDisplayConcurrentPreservesEverySession(t *testing.T) {
	dir := t.TempDir()
	const writers = 32
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			path := filepath.Join(dir, fmt.Sprintf("session-%02d.jsonl", i))
			// A burst of durable writes can exhaust the bounded local queue on
			// slow disks. The caller retains these writes for retry; this test
			// checks that completed updates preserve every session, rather than
			// requiring all 32 fsyncs to fit into one queue budget. Lock deadline
			// errors occur before mutation, so retrying cannot duplicate a record.
			var err error
			for range 1 + displayPersistRetryLimit {
				err = recordSessionPlannerDisplay(dir, path, fmt.Sprintf("prompt-%02d", i), []HistoryMessage{{
					Role: "assistant", Content: fmt.Sprintf("answer-%02d", i),
				}})
				if !errors.Is(err, context.DeadlineExceeded) {
					break
				}
			}
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("record planner display: %v", err)
		}
	}

	got := loadSessionPlannerDisplays(dir)
	if len(got) != writers {
		t.Fatalf("planner display sessions = %d, want %d", len(got), writers)
	}
	for i := range writers {
		key := fmt.Sprintf("session-%02d.jsonl", i)
		if len(got[key]) != 1 || len(got[key][0].Messages) != 1 || got[key][0].Messages[0].Content != fmt.Sprintf("answer-%02d", i) {
			t.Fatalf("planner display %s = %+v", key, got[key])
		}
	}
}

func TestRecordSessionPlannerDisplayCrossProcessPreservesEverySession(t *testing.T) {
	if role := os.Getenv("DEEPZORA_PLANNER_DISPLAY_HELPER"); role != "" {
		dir := os.Getenv("DEEPZORA_PLANNER_DISPLAY_DIR")
		sessionPlannerDisplayExternalLockTimeout = 5 * time.Second
		attempted := filepath.Join(dir, role+".attempted")
		loaded := filepath.Join(dir, role+".loaded")
		release := filepath.Join(dir, role+".release")
		if err := os.WriteFile(attempted, []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		if role == "second" && !waitForPlannerDisplayTestFile(filepath.Join(dir, "second.begin"), 10*time.Second) {
			t.Fatal("timed out waiting to begin second update")
		}
		sessionPlannerDisplayUpdateAfterLoad = func() {
			if err := os.WriteFile(loaded, []byte("loaded"), 0o600); err != nil {
				t.Fatal(err)
			}
			if !waitForPlannerDisplayTestFile(release, 10*time.Second) {
				t.Fatalf("timed out waiting for %s release", role)
			}
		}
		err := recordSessionPlannerDisplay(dir, filepath.Join(dir, role+".jsonl"), role+" prompt", []HistoryMessage{{
			Role: "assistant", Content: role + " answer",
		}})
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	dir := t.TempDir()
	startHelper := func(role string, output *strings.Builder) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordSessionPlannerDisplayCrossProcessPreservesEverySession$")
		cmd.Env = append(os.Environ(),
			"DEEPZORA_PLANNER_DISPLAY_HELPER="+role,
			"DEEPZORA_PLANNER_DISPLAY_DIR="+dir,
		)
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %s helper: %v", role, err)
		}
		return cmd
	}
	releaseHelper := func(role string) {
		if err := os.WriteFile(filepath.Join(dir, role+".release"), []byte("release"), 0o600); err != nil {
			t.Fatalf("release %s helper: %v", role, err)
		}
	}

	var firstOutput, secondOutput strings.Builder
	first := startHelper("first", &firstOutput)
	if !waitForPlannerDisplayTestFile(filepath.Join(dir, "first.loaded"), 5*time.Second) {
		releaseHelper("first")
		_ = first.Wait()
		t.Fatalf("first helper did not load sidecar: %s", firstOutput.String())
	}
	second := startHelper("second", &secondOutput)
	if !waitForPlannerDisplayTestFile(filepath.Join(dir, "second.attempted"), 5*time.Second) {
		releaseHelper("first")
		_ = os.WriteFile(filepath.Join(dir, "second.begin"), []byte("begin"), 0o600)
		releaseHelper("second")
		_ = first.Wait()
		_ = second.Wait()
		t.Fatalf("second helper did not attempt update: %s", secondOutput.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "second.begin"), []byte("begin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if waitForPlannerDisplayTestFile(filepath.Join(dir, "second.loaded"), time.Second) {
		releaseHelper("first")
		releaseHelper("second")
		_ = first.Wait()
		_ = second.Wait()
		t.Fatal("second process loaded the stale sidecar while the first update still held its transaction lock")
	}

	releaseHelper("first")
	if err := first.Wait(); err != nil {
		releaseHelper("second")
		_ = second.Wait()
		t.Fatalf("first helper failed: %v\n%s", err, firstOutput.String())
	}
	if !waitForPlannerDisplayTestFile(filepath.Join(dir, "second.loaded"), 5*time.Second) {
		releaseHelper("second")
		_ = second.Wait()
		t.Fatalf("second helper did not enter transaction after release: %s", secondOutput.String())
	}
	releaseHelper("second")
	if err := second.Wait(); err != nil {
		t.Fatalf("second helper failed: %v\n%s", err, secondOutput.String())
	}

	got := loadSessionPlannerDisplays(dir)
	for _, role := range []string{"first", "second"} {
		turns := got[role+".jsonl"]
		if len(turns) != 1 || len(turns[0].Messages) != 1 || turns[0].Messages[0].Content != role+" answer" {
			t.Fatalf("%s planner display lost after cross-process updates: %#v", role, got)
		}
	}
}

func TestRecordSessionPlannerDisplayDoesNotOverwriteCorruptSidecar(t *testing.T) {
	dir := t.TempDir()
	corrupt := []byte(`{"session.jsonl":[`)
	if err := os.WriteFile(sessionPlannerDisplayPath(dir), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	err := recordSessionPlannerDisplay(dir, filepath.Join(dir, "new.jsonl"), "prompt", []HistoryMessage{{Role: "assistant", Content: "answer"}})
	if err == nil {
		t.Fatal("record should reject a corrupt sidecar instead of replacing it with an empty map")
	}
	got, readErr := os.ReadFile(sessionPlannerDisplayPath(dir))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(corrupt) {
		t.Fatalf("corrupt sidecar was overwritten: %q", got)
	}
}

func TestRemoveSessionPlannerDisplayRetiresCorruptSidecar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(sessionPlannerDisplayPath(dir), []byte(`{"session.jsonl":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionPlannerDisplay(dir, filepath.Join(dir, "session.jsonl")); err != nil {
		t.Fatalf("remove display from corrupt sidecar: %v", err)
	}
	if _, err := os.Stat(sessionPlannerDisplayPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("corrupt sidecar should be retired during destructive cleanup, stat err = %v", err)
	}
}

func waitForPlannerDisplayTestFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestRemoveDesktopSessionArtifactsPrunesPlannerDisplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recordSessionPlannerDisplay(dir, path, "prompt", []HistoryMessage{{
		Role: "tool", ToolName: "read_file", Content: "sensitive cancelled output",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := removeDesktopSessionArtifacts(path); err != nil {
		t.Fatal(err)
	}
	if got := sessionPlannerDisplayTurns(dir, path); len(got) != 0 {
		t.Fatalf("planner display retained after permanent session removal: %+v", got)
	}
	if _, err := os.Stat(sessionPlannerDisplayPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("empty planner display sidecar should be removed, stat err = %v", err)
	}
}

func TestRecordSessionPlannerDisplayForTurnIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	first := []HistoryMessage{{Role: "assistant", Content: "partial"}}
	final := []HistoryMessage{{Role: "assistant", Content: "recovered partial"}, {Role: "notice", Content: "interrupted"}}
	if err := recordSessionPlannerDisplayForTurn(dir, path, "turn-1", "prompt", first); err != nil {
		t.Fatalf("record first projection: %v", err)
	}
	if err := recordSessionPlannerDisplayForTurn(dir, path, "turn-1", "prompt", final); err != nil {
		t.Fatalf("upsert recovered projection: %v", err)
	}

	got := sessionPlannerDisplayTurns(dir, path)
	if len(got) != 1 || got[0].TurnID != "turn-1" || len(got[0].Messages) != 2 || got[0].Messages[0].Content != "recovered partial" {
		t.Fatalf("turn-id upsert = %+v, want one updated projection", got)
	}
}

func TestRecordSessionDisplaySkipsNoop(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	if err := recordSessionDisplay(dir, sessionPath, "same", "same"); err != nil {
		t.Fatalf("record display: %v", err)
	}
	if _, err := os.Stat(sessionDisplayPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("noop display should not create sidecar, stat err = %v", err)
	}
}

func TestRecordSessionDisplaySerializesConcurrentTabs(t *testing.T) {
	dir := t.TempDir()
	const tabs = 8
	errs := make(chan error, tabs)
	var wg sync.WaitGroup
	for i := range tabs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := filepath.Join(dir, fmt.Sprintf("tab-%02d.jsonl", i))
			content := fmt.Sprintf("expanded-%02d", i)
			display := fmt.Sprintf("display-%02d", i)
			errs <- recordSessionDisplay(dir, path, content, display)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("recordSessionDisplay: %v", err)
		}
	}

	got := loadSessionDisplays(dir)
	for i := range tabs {
		key := fmt.Sprintf("tab-%02d.jsonl", i)
		content := fmt.Sprintf("expanded-%02d", i)
		if display := got[key][messageDisplayKey(content)]; display != fmt.Sprintf("display-%02d", i) {
			t.Fatalf("%s display = %q, want retained concurrent value", key, display)
		}
	}
}
