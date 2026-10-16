package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCopyRequiresResumeTarget(t *testing.T) {
	isolateCLIConfigHome(t)

	errOut := captureStderr(t, func() {
		if rc := runAgent([]string{"--copy", "do things"}, "dev"); rc != 2 {
			t.Fatalf("run --copy without target rc = %d, want 2", rc)
		}
	})
	if !strings.Contains(errOut, "--copy requires --resume or --continue") {
		t.Fatalf("run --copy stderr = %q, want usage error", errOut)
	}
}

func TestRunResumeRejectsTranscriptPath(t *testing.T) {
	isolateCLIConfigHome(t)
	pinProviderOffline(t)
	path := filepath.Join(t.TempDir(), "history.jsonl")
	writeOldTranscriptFixture(t, path, "old transcript")
	out := captureStderr(t, func() {
		if rc := runAgent([]string{"--resume", path, "continue task"}, "dev"); rc != 1 {
			t.Fatalf("resume path rc = %d", rc)
		}
	})
	if !strings.Contains(out, "no session matches") {
		t.Fatalf("unexpected refusal: %s", out)
	}
}

func TestChatNewSessionOwnsFreshCanonicalIdentity(t *testing.T) {
	m, ctrl, service, _ := newCanonicalTakeoverTUI(t)
	previous, _ := ctrl.SessionRef()
	if cmd := m.runSlashCommand("/new"); cmd != nil {
		t.Fatal("/new should complete synchronously")
	}
	next, bound := ctrl.SessionRef()
	if !bound || next == previous {
		t.Fatalf("new identity = %+v, previous = %+v", next, previous)
	}
	if _, err := service.Query().Snapshot(t.Context(), next); err != nil {
		t.Fatalf("new identity not persisted: %v", err)
	}
	if _, err := service.Query().Snapshot(t.Context(), previous); err != nil {
		t.Fatalf("previous conversation lost: %v", err)
	}
}
