package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"deepzora/internal/filelock"
	"deepzora/internal/fileutil"
)

func TestSessionSidecarSavesUseDurableAtomicWrite(t *testing.T) {
	tests := []struct {
		name string
		path func(string) string
		save func(string) error
	}{
		{
			name: "displays",
			path: sessionDisplayPath,
			save: func(dir string) error {
				return saveSessionDisplays(dir, sessionDisplayMap{
					"session.jsonl": {messageDisplayKey("expanded prompt"): "visible prompt"},
				})
			},
		},
		{
			name: "planner displays",
			path: sessionPlannerDisplayPath,
			save: func(dir string) error {
				return saveSessionPlannerDisplays(dir, sessionPlannerDisplayMap{
					"session.jsonl": {{UserHash: "prompt-digest", Messages: []HistoryMessage{}}},
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tt.path(dir)
			previousCrashPoint := fileutil.CrashPoint
			t.Cleanup(func() { fileutil.CrashPoint = previousCrashPoint })
			usedDurableWrite := false
			fileutil.CrashPoint = func(op, gotPath string) {
				if op == "atomic-write" && gotPath == path {
					usedDurableWrite = true
				}
			}

			if err := tt.save(dir); err != nil {
				t.Fatalf("save %s sidecar: %v", tt.name, err)
			}
			if !usedDurableWrite {
				t.Fatalf("%s sidecar bypassed fileutil.AtomicWriteFile", tt.name)
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatalf("stat %s sidecar: %v", tt.name, err)
				}
				if got := info.Mode().Perm(); got != 0o600 {
					t.Fatalf("%s sidecar mode = %o, want 600", tt.name, got)
				}
			}
		})
	}
}

func TestRecordSessionDisplayExternalLockUsesShortBudget(t *testing.T) {
	if os.Getenv("DEEPZORA_DISPLAY_LOCK_HELPER") != "" {
		dir := os.Getenv("DEEPZORA_DISPLAY_LOCK_DIR")
		release, err := filelock.Acquire(context.Background(), sessionDisplayPath(dir)+".lock")
		if err != nil {
			t.Fatalf("acquire external display lock: %v", err)
		}
		defer release()
		if err := os.WriteFile(filepath.Join(dir, "display-lock.ready"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		if !waitForPlannerDisplayTestFile(filepath.Join(dir, "display-lock.release"), 10*time.Second) {
			t.Fatal("timed out waiting to release external display lock")
		}
		return
	}

	dir := t.TempDir()
	var output strings.Builder
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecordSessionDisplayExternalLockUsesShortBudget$")
	cmd.Env = append(os.Environ(),
		"DEEPZORA_DISPLAY_LOCK_HELPER=1",
		"DEEPZORA_DISPLAY_LOCK_DIR="+dir,
	)
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start external display-lock helper: %v", err)
	}
	releasePath := filepath.Join(dir, "display-lock.release")
	releaseHelper := func() {
		_ = os.WriteFile(releasePath, []byte("release"), 0o600)
	}
	if !waitForPlannerDisplayTestFile(filepath.Join(dir, "display-lock.ready"), 5*time.Second) {
		releaseHelper()
		_ = cmd.Wait()
		t.Fatalf("external display-lock helper did not start: %s", output.String())
	}

	previousTimeout := sessionDisplayExternalLockTimeout
	sessionDisplayExternalLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { sessionDisplayExternalLockTimeout = previousTimeout })
	started := time.Now()
	err := recordSessionDisplay(dir, filepath.Join(dir, "session.jsonl"), "expanded prompt", "visible prompt")
	elapsed := time.Since(started)
	releaseHelper()
	if waitErr := cmd.Wait(); waitErr != nil {
		t.Fatalf("external display-lock helper failed: %v\n%s", waitErr, output.String())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("record display error = %v, want external lock deadline exceeded", err)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("record display waited %v, want the short external lock budget", elapsed)
	}
}
