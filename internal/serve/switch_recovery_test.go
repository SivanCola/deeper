package serve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

type switchAskProvider struct {
	turn int
}

func (*switchAskProvider) Name() string { return "switch-ask" }

func (p *switchAskProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	if p.turn == 0 {
		ch <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{
			ID:        "ask-after-switch",
			Name:      "ask",
			Arguments: `{"questions":[{"header":"Direction","question":"Which path?","options":[{"label":"A"},{"label":"B"}]}]}`,
		}}
	} else {
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "done"}
	}
	p.turn++
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func TestSwitchModelKeepsAskInteractive(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	dir := t.TempDir()

	bc := NewBroadcaster()
	old := control.New(control.Options{
		Executor:   agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard),
		SessionDir: dir,
		Label:      "old",
		Sink:       bc,
	})
	old.EnableInteractiveApproval()

	askCh := make(chan event.Ask, 1)
	s := &Server{ctrl: old, bc: bc}
	defer s.Close()
	s.buildController = func(_ context.Context, _ string) (*control.Controller, error) {
		reg := tool.NewRegistry()
		reg.Add(agent.NewAskTool())
		exec := agent.New(&switchAskProvider{}, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
		return control.New(control.Options{
			Executor:   exec,
			SessionDir: dir,
			Label:      "new",
			Sink: event.FuncSink(func(e event.Event) {
				if e.Kind == event.AskRequest {
					askCh <- e.Ask
				}
			}),
		}), nil
	}

	if err := s.switchModel(context.Background(), "next-model"); err != nil {
		t.Fatalf("switchModel: %v", err)
	}

	newCtrl := s.ctl().(*control.Controller)
	runDone := make(chan error, 1)
	go func() { runDone <- newCtrl.Executor().Run(context.Background(), "ask the user") }()

	select {
	case ask := <-askCh:
		newCtrl.AnswerQuestion(ask.ID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"A"}}})
	case err := <-runDone:
		t.Fatalf("ask tool returned without an ask_request after model switch: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("ask tool did not emit ask_request after model switch")
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run after answering ask_request: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run stayed blocked after answering ask_request")
	}
}

// TestSwitchModelRefreshesLeadingSystemPrompt pins the fix for the bug where
// switchModel rebuilt the controller with the target model/profile's own
// system prompt, only for AdoptHistory to immediately overwrite it with the
// carried history's leading message — the outgoing controller's system
// prompt. The user-visible symptom was that the model kept following the
// previous system prompt after every /model switch.
func TestSwitchModelRefreshesLeadingSystemPrompt(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	dir := t.TempDir()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})

	bc := NewBroadcaster()
	old := control.New(control.Options{
		Executor:   agent.New(nil, nil, oldSession, agent.Options{}, event.Discard),
		SessionDir: dir,
		Label:      "old",
		Sink:       bc,
	})
	s := &Server{ctrl: old, bc: bc}
	defer s.Close()
	s.buildController = func(_ context.Context, _ string) (*control.Controller, error) {
		return control.New(control.Options{
			Executor:   agent.New(nil, nil, agent.NewSession("new system prompt"), agent.Options{}, event.Discard),
			SessionDir: dir,
			Label:      "new",
			Sink:       bc,
		}), nil
	}

	if err := s.switchModel(context.Background(), "next-model"); err != nil {
		t.Fatalf("switchModel: %v", err)
	}

	history := s.ctl().History()
	if len(history) != 3 || history[0].Role != provider.RoleSystem {
		t.Fatalf("history = %+v, want a leading system message", history)
	}
	if got, want := history[0].Content, "new system prompt"; got != want {
		t.Fatalf("leading system message = %q, want %q (stale outgoing prompt carried forward)", got, want)
	}
	if history[1].Content != "hello" || history[2].Content != "hi" {
		t.Fatalf("history after switch = %+v, want carried user/assistant turns preserved", history)
	}
}

// TestSwitchModelRestoresSessionAuthorizations pins the fix for switchModel
// dropping same-session "Allow for this session" tool grants and Plan-mode
// read-only command trust on every /model switch, forcing the user to
// re-approve something already granted this session.
func TestSwitchModelRestoresSessionAuthorizations(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	dir := t.TempDir()

	bc := NewBroadcaster()
	old := control.New(control.Options{
		Executor:   agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard),
		SessionDir: dir,
		Label:      "old",
		Sink:       bc,
	})
	old.RestoreSessionAuthorizations(control.SessionAuthorizations{
		Grants: []string{"bash|go test ./..."},
	})

	s := &Server{ctrl: old, bc: bc}
	defer s.Close()
	s.buildController = func(_ context.Context, _ string) (*control.Controller, error) {
		return control.New(control.Options{
			Executor:   agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard),
			SessionDir: dir,
			Label:      "new",
			Sink:       bc,
		}), nil
	}

	if err := s.switchModel(context.Background(), "next-model"); err != nil {
		t.Fatalf("switchModel: %v", err)
	}

	newCtrl, ok := s.ctl().(*control.Controller)
	if !ok {
		t.Fatalf("s.ctl() = %T, want *control.Controller", s.ctl())
	}
	got := newCtrl.SessionAuthorizations()
	if len(got.Grants) != 1 || got.Grants[0] != "bash|go test ./..." {
		t.Fatalf("restored grants = %+v, want [\"bash|go test ./...\"]", got.Grants)
	}

}

// TestSwitchModelPersistsRefreshedSystemPromptToDisk pins the disk half of the
// system-prompt splice: switchModel refreshes the leading system message in
// the new controller's memory, and nothing snapshots an idle session again, so
// the switch itself must persist the adopted history or a restart + /resume
// revives the outgoing controller's contract from disk.
func TestSwitchModelPersistsRefreshedSystemPromptToDisk(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "switch-persist.jsonl")

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := oldSession.Save(path); err != nil {
		t.Fatalf("save base session: %v", err)
	}

	bc := NewBroadcaster()
	old := control.New(control.Options{
		Executor:    agent.New(nil, nil, oldSession, agent.Options{}, event.Discard),
		SessionDir:  dir,
		SessionPath: path,
		Label:       "old",
		Sink:        bc,
	})
	s := &Server{ctrl: old, bc: bc}
	defer s.Close()
	s.buildController = func(_ context.Context, _ string) (*control.Controller, error) {
		return control.New(control.Options{
			Executor:   agent.New(nil, nil, agent.NewSession("new system prompt"), agent.Options{}, event.Discard),
			SessionDir: dir,
			Label:      "new",
			Sink:       bc,
		}), nil
	}

	if err := s.switchModel(context.Background(), "next-model"); err != nil {
		t.Fatalf("switchModel: %v", err)
	}

	loaded, err := agent.LoadSession(s.ctl().SessionPath())
	if err != nil {
		t.Fatalf("load transcript after switch: %v", err)
	}
	msgs := loaded.Snapshot()
	if len(msgs) != 3 || msgs[0].Role != provider.RoleSystem {
		t.Fatalf("on-disk history after switch = %+v, want 3 messages with a leading system message", msgs)
	}
	if got, want := msgs[0].Content, "new system prompt"; got != want {
		t.Fatalf("on-disk leading system message = %q, want %q (a restart would revive the outgoing contract)", got, want)
	}
}

func TestSwitchModelSnapshotFailureKeepsOldController(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	invalidSessionDir := filepath.Join(t.TempDir(), "session-dir-is-a-file")
	if err := os.WriteFile(invalidSessionDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write invalid session dir: %v", err)
	}

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	bc := NewBroadcaster()
	old := control.New(control.Options{
		Executor: agent.New(nil, nil, oldSession, agent.Options{}, event.Discard),
		Label:    "old",
		Sink:     bc,
	})
	t.Cleanup(old.Close)
	s := &Server{ctrl: old, bc: bc}
	defer s.Close()
	s.buildController = func(_ context.Context, _ string) (*control.Controller, error) {
		return control.New(control.Options{
			Executor:   agent.New(nil, nil, agent.NewSession("new system prompt"), agent.Options{}, event.Discard),
			SessionDir: invalidSessionDir,
			Label:      "new",
			Sink:       bc,
		}), nil
	}

	err := s.switchModel(context.Background(), "next-model")
	if err == nil || !strings.Contains(err.Error(), "adopt history") {
		t.Fatalf("switchModel error = %v, want adopt history failure", err)
	}
	if got := s.ctl(); got != old {
		t.Fatalf("active controller changed after persistence failure: got %T %p, want outgoing %p", got, got, old)
	}
}
