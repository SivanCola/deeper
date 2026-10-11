package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/store"
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

func TestRunResumeReleasesLeaseOnExit(t *testing.T) {
	isolateCLIConfigHome(t)
	pinProviderOffline(t)

	path := filepath.Join(t.TempDir(), "release-run.jsonl")
	saveTestSession(t, path, "resume me")

	// No provider config in the isolated home: the run fails after the lease
	// was taken, and the deferred release must still run.
	_ = captureStderr(t, func() {
		if rc := runAgent([]string{"--resume", path, "continue task"}, "dev"); rc != 1 {
			t.Fatalf("run --resume rc = %d, want 1 (setup fails in isolated home)", rc)
		}
	})
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("lease not released after run exit: %v", err)
	}
	lease.Release()
}

func TestCopySessionForWritingDuplicatesTranscript(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jsonl")
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "question"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer"})
	if err := s.Save(src); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMeta(src, agent.BranchMeta{
		CustomTitle:   "My debugging session",
		Model:         "deepseek/deepseek-chat",
		SchemaVersion: agent.BranchMetaCountsVersion,
	}); err != nil {
		t.Fatal(err)
	}
	srcBytes, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	copyPath, err := copySessionForWriting(src)
	if err != nil {
		t.Fatalf("copySessionForWriting: %v", err)
	}
	if filepath.Dir(copyPath) != dir {
		t.Fatalf("copy landed in %q, want beside the source in %q", filepath.Dir(copyPath), dir)
	}

	loaded, err := agent.LoadSession(copyPath)
	if err != nil {
		t.Fatalf("load copy: %v", err)
	}
	got := loaded.Snapshot()
	want := s.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("copy has %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("copy message %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	meta, ok, err := agent.LoadBranchMeta(copyPath)
	if err != nil || !ok {
		t.Fatalf("copy branch meta: ok=%v err=%v", ok, err)
	}
	if meta.ParentID != agent.BranchID(src) {
		t.Fatalf("copy ParentID = %q, want %q", meta.ParentID, agent.BranchID(src))
	}
	if meta.CustomTitle != "My debugging session (copy)" {
		t.Fatalf("copy CustomTitle = %q", meta.CustomTitle)
	}
	if meta.Model != "deepseek/deepseek-chat" {
		t.Fatalf("copy Model = %q", meta.Model)
	}

	// The copy starts unowned and without lease/lock sidecars of its own.
	for _, sidecar := range []string{
		store.SessionLeaseLock(agent.CanonicalSessionPath(copyPath)),
	} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("copy has lease sidecar %s (err=%v)", sidecar, err)
		}
	}
	// The source transcript is only read.
	srcAfter, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(srcAfter) != string(srcBytes) {
		t.Fatalf("source transcript was modified by the copy")
	}
}

// chatLeaseFixture builds a TUI over a temp session dir with two saved
// sessions: older (the active one) and newer (the /resume 1 target).
func chatLeaseFixture(t *testing.T) (m chatTUI, active, target string) {
	t.Helper()
	dir := t.TempDir()
	active = filepath.Join(dir, "a-active.jsonl")
	target = filepath.Join(dir, "b-target.jsonl")
	saveTestSession(t, active, "active session")
	saveTestSession(t, target, "target session")
	pinNewer(t, active, target)

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m = newTestChatTUI()
	m.width = 80
	m.ctrl = newOwnedTestController(t, control.Options{Executor: exec, SessionDir: dir, SessionPath: active, Label: "test"})
	m.leases = control.NewSessionLeaseKeeper()
	t.Cleanup(m.leases.Release)
	if err := m.leases.Rebind(active); err != nil {
		t.Fatalf("seed lease on active: %v", err)
	}
	return m, active, target
}

// pinNewer gives newer a strictly later mtime than older so ListSessions
// ordering is deterministic across filesystems with coarse mtimes.
func pinNewer(t *testing.T, older, newer string) {
	t.Helper()
	info, err := os.Stat(newer)
	if err != nil {
		t.Fatal(err)
	}
	old := info.ModTime().Add(-2 * time.Second)
	if err := os.Chtimes(older, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestChatNewSessionTakesFreshLease(t *testing.T) {
	m, active, _ := chatLeaseFixture(t)

	if cmd := m.runSlashCommand("/new"); cmd != nil {
		t.Fatal("/new should not return a tea.Cmd")
	}

	fresh := m.ctrl.SessionPath()
	if fresh == active {
		t.Fatalf("/new did not rotate the session path")
	}
	if got, want := m.leases.HeldPath(), agent.CanonicalSessionPath(fresh); got != want {
		t.Fatalf("lease after /new = %q, want %q", got, want)
	}
	lease, err := agent.TryAcquireSessionLease(active)
	if err != nil {
		t.Fatalf("old session lease not released by /new: %v", err)
	}
	lease.Release()
}
