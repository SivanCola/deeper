package control

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestSubmitClearDeletesCurrentSourceAndPublishesFreshIdentity(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "old context"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	clearResult := make(chan string, 1)
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind != event.Notice || (e.Text != "context cleared" && !strings.HasPrefix(e.Text, "clear context failed: ")) {
			return
		}
		select {
		case clearResult <- e.Text:
		default:
		}
	})
	c := newCurrentOwnedTestController(t, Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, Label: "test", Sink: sink})
	if err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	before, _ := c.SessionRef()
	oldDir, err := c.SessionService().SessionDir(t.Context(), before)
	if err != nil {
		t.Fatal(err)
	}
	ckpt := filepath.Join(oldDir, "file-checkpoints")
	if err := os.MkdirAll(ckpt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ckpt, "turn-0.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	c.submit("/clear", "", "")
	select {
	case result := <-clearResult:
		if result != "context cleared" {
			t.Fatal(result)
		}
	case <-time.After(30 * time.Second):
		stack := make([]byte, 2<<20)
		n := runtime.Stack(stack, true)
		t.Fatalf("/clear did not finish\n%s", stack[:n])
	}
	after, bound := c.SessionRef()
	if !bound || after == before {
		t.Fatal("clear did not rotate current identity")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("cleared directory still exists: %v", err)
	}
	if _, err := c.SessionService().Query().Stat(t.Context(), after); err != nil {
		t.Fatalf("fresh current identity not durable: %v", err)
	}
	current := exec.Session().Snapshot()
	if len(current) != 1 || current[0].Role != provider.RoleSystem || current[0].Content != "sys" {
		t.Fatalf("cleared context = %+v, want only system prompt", current)
	}
}
