package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

type crashAfterEffectTool struct{ path string }

func (crashAfterEffectTool) Name() string            { return "crash_after_effect" }
func (crashAfterEffectTool) Description() string     { return "test fixture" }
func (crashAfterEffectTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (crashAfterEffectTool) ReadOnly() bool          { return false }
func (t crashAfterEffectTool) Execute(context.Context, json.RawMessage) (string, error) {
	f, err := os.OpenFile(t.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return "", err
	}
	if _, err = f.WriteString("effect\n"); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	_ = f.Close()
	os.Exit(73) // A committed external effect, with no local result receipt.
	return "", nil
}

func TestToolRecoveryCrashAfterEffect(t *testing.T) {
	if root := os.Getenv("DEEPZORA_RECOVERY_CRASH_FIXTURE"); root != "" {
		reg := tool.NewRegistry()
		reg.Add(crashAfterEffectTool{path: filepath.Join(root, "effects")})
		p := &recordingProvider{streams: [][]provider.Chunk{{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "crash", Name: "crash_after_effect", Arguments: `{}`}}, {Type: provider.ChunkDone}}}}
		a := agent.New(p, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
		service, err := session.NewService("crash-test", session.NewFilesystemPersistence(filepath.Join(root, "sessions")))
		if err != nil {
			t.Fatal(err)
		}
		c := newOwnedTestController(t, Options{Executor: a, Runner: a, SessionService: service, ExclusiveSession: true, Sink: event.Discard})
		if _, err := c.BindFreshSession(t.Context(), "crash"); err != nil {
			t.Fatal(err)
		}
		if err := c.RunTurn(context.Background(), "perform effect"); err != nil {
			t.Fatal(err)
		}
		t.Fatal("fixture failed to crash")
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestToolRecoveryCrashAfterEffect$")
	cmd.Env = append(os.Environ(), "DEEPZORA_RECOVERY_CRASH_FIXTURE="+root)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("crash helper: %v %s", err, out)
	}
	service, err := session.NewService("crash-test", session.NewFilesystemPersistence(filepath.Join(root, "sessions")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	a := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: a, SessionService: service, ExclusiveSession: true, Sink: event.Discard})
	defer c.Close()
	ref, err := c.OpenSession(t.Context(), session.SessionRef{HostID: "crash-test", SessionID: "crash"})
	if err != nil {
		t.Fatal(err)
	}
	view := c.ToolRecoverySnapshot()
	// The exact started record survives the process crash. The host closes the
	// interrupted tool with an unknown-result fact, without claiming the external
	// effect did not happen or discarding its inspection identity.
	if len(view.Calls) != 1 || view.Calls[0].Identity.CallID != "crash" ||
		view.Calls[0].State != provider.ToolRunStarted || view.Statistics.Unknown != 1 {
		t.Fatalf("unresolved crash effects=%+v", view)
	}
	snapshot, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	projection := snapshot.Projection
	if len(projection.ActiveTools) != 0 || projection.TurnStatus != event.TurnInterrupted {
		t.Fatal("current restart did not close the interrupted turn and tools")
	}
	info, err := service.Query().Stat(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := session.Replay(info.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	unknown := false
	for _, commit := range commits {
		for _, recorded := range commit.Events {
			if recorded.Kind != "tool/result" {
				continue
			}
			var body struct {
				ID       string                `json:"id"`
				RunState provider.ToolRunState `json:"runState"`
			}
			if json.Unmarshal(recorded.Payload, &body) == nil && body.ID == "crash" && body.RunState == provider.ToolRunUnknown {
				unknown = true
			}
		}
	}
	if !unknown {
		t.Fatal("restart did not preserve the unknown external result as a typed v3 fact")
	}
	effects, err := os.ReadFile(filepath.Join(root, "effects"))
	if err != nil || string(effects) != "effect\n" {
		t.Fatalf("effect repeated: %q %v", effects, err)
	}
}
