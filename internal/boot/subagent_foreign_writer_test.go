package boot

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/session"
)

func TestSubagentCleanupSkipsForeignCurrentWriter(t *testing.T) {
	sessionDir := t.TempDir()
	parents := newCleanupParent(t, sessionDir)
	ref := prepareRunningSubagent(t, sessionDir)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSubagentCurrentWriterHelper$")
	cmd.Env = append(os.Environ(), "DEEPZORA_CURRENT_PARENT_HELPER="+sessionDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("foreign readiness = %q, %v", line, err)
	}
	if _, err := newSubagentStore(parents, nil); err != nil {
		t.Fatal(err)
	}
	requireSubagentStatus(t, sessionDir, ref, agent.SubagentRunning)
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := newSubagentStore(parents, nil); err != nil {
		t.Fatal(err)
	}
	requireSubagentStatus(t, sessionDir, ref, agent.SubagentInterrupted)
}

func TestSubagentCurrentWriterHelper(t *testing.T) {
	dir := os.Getenv("DEEPZORA_CURRENT_PARENT_HELPER")
	if dir == "" {
		return
	}
	service, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(dir)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown(context.Background())
	if _, err := service.Open(t.Context(), session.SessionRef{HostID: "local", SessionID: "parent-session"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	var release [1]byte
	_, _ = os.Stdin.Read(release[:])
}
