package serve

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/boot"
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

func serveModelReplacement(opts boot.Options, configure func(*control.Options)) *control.Controller {
	options := control.Options{
		Executor: agent.New(nil, nil, agent.NewSession("fresh assembly prompt"), agent.Options{}, event.Discard),
		Sink:     opts.Sink, Label: opts.Model, ModelRef: opts.Model,
		WorkspaceRoot: opts.WorkspaceRoot, SessionDir: opts.SessionDir,
		SessionService: opts.SessionService, SessionRuntime: opts.SessionRuntime,
		ExclusiveSession: true, BackgroundScope: opts.BackgroundScope,
		SessionTemp: opts.SessionTemp, PersistentShell: opts.PersistentShell,
	}
	if configure != nil {
		configure(&options)
	}
	if options.Runner == nil {
		options.Runner = options.Executor
	}
	return control.New(options)
}

func TestSwitchModelKeepsAskInteractive(t *testing.T) {
	closeServeTestUsage(t)
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Cleanup(func() { closeServeTestUsage(t) })
	s, _, _, _ := newExclusiveSessionServe(t)
	defer s.Close()
	askCh := make(chan event.Ask, 1)
	s.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		reg := tool.NewRegistry()
		reg.Add(agent.NewAskTool())
		return serveModelReplacement(opts, func(o *control.Options) {
			o.Executor = agent.New(&switchAskProvider{}, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
			o.Sink = event.FuncSink(func(e event.Event) {
				if e.Kind == event.AskRequest {
					askCh <- e.Ask
				}
			})
		}), nil
	}

	if err := s.switchModel(context.Background(), "next-model"); err != nil {
		t.Fatalf("switchModel: %v", err)
	}

	newCtrl := s.ctl().(*control.Controller)
	t.Cleanup(func() { newCtrl.Close(); <-newCtrl.Closed() })
	// This integration crosses durable session and tool-receipt publication.
	// Bound the complete round trip, rather than asserting a two-second disk
	// latency. Cancellation also releases an unanswered ask on test failure.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	runDone := make(chan error, 1)
	finished := false
	defer func() {
		cancel()
		if !finished {
			select {
			case <-runDone:
			case <-time.After(30 * time.Second):
				t.Error("cancelled ask run did not finish")
			}
		}
	}()
	go func() { runDone <- newCtrl.RunTurn(ctx, "ask the user") }()

	select {
	case ask := <-askCh:
		if err := newCtrl.AnswerQuestionChecked(ask.ID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"A"}}}); err != nil {
			t.Fatalf("answer ask_request: %v", err)
		}
	case err := <-runDone:
		finished = true
		t.Fatalf("ask tool returned without an ask_request after model switch: %v", err)
	case <-ctx.Done():
		t.Fatal("ask tool did not emit ask_request after model switch")
	}

	select {
	case err := <-runDone:
		finished = true
		if err != nil {
			t.Fatalf("run after answering ask_request: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("run stayed blocked after answering ask_request")
	}
}

// The current session owns its model context. A fresh assembly prompt must
// not replace persisted context or cause a second identity to be allocated.
func TestSwitchModelPreservesCurrentRuntimeContextAndAuthorizations(t *testing.T) {
	closeServeTestUsage(t)
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Cleanup(func() { closeServeTestUsage(t) })
	s, old, service, ref := newExclusiveSessionServe(t)
	defer s.Close()
	if err := old.AdoptRebuiltModelContext([]provider.Message{
		{ID: "system", Role: provider.RoleSystem, Content: "persisted system prompt"},
		{ID: "user", Role: provider.RoleUser, Content: "hello", Origin: provider.MessageOriginUser},
		{ID: "assistant", Role: provider.RoleAssistant, Content: "hi"},
	}); err != nil {
		t.Fatal(err)
	}
	old.SetToolApprovalMode(control.ToolApprovalDangerFullAccess)
	old.SetPlanMode(true)
	old.RestoreSessionAuthorizations(control.SessionAuthorizations{Grants: []string{"bash|go test ./..."}})
	before := old.History()
	_, runtime, _ := old.SessionBinding()
	s.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		return serveModelReplacement(opts, nil), nil
	}
	if err := s.switchModel(t.Context(), "next-model"); err != nil {
		t.Fatal(err)
	}
	next := s.ctl().(*control.Controller)
	gotService, gotRuntime, bound := next.SessionBinding()
	if !bound || gotService != service || gotRuntime != runtime {
		t.Fatal("model switch replaced the session runtime")
	}
	if got, ok := next.SessionRef(); !ok || got != ref {
		t.Fatalf("session identity = %+v, want %+v", got, ref)
	}
	if !reflect.DeepEqual(next.History(), before) {
		t.Fatalf("model context changed: %+v", next.History())
	}
	if !next.PlanMode() || next.ToolApprovalMode() != control.ToolApprovalDangerFullAccess || !reflect.DeepEqual(next.SessionAuthorizations(), old.SessionAuthorizations()) {
		t.Fatal("model switch lost modes or session grants")
	}
	if err := next.Snapshot(); err != nil {
		t.Fatalf("retiring the old controller closed the writer: %v", err)
	}
	s.Close()
	stored, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Projection.ModelMessages, before) {
		t.Fatalf("persisted context changed after closing: %+v", stored.Projection.ModelMessages)
	}
	if stored.Projection.ModelRef != "next-model" {
		t.Fatalf("persisted model = %q, want next-model", stored.Projection.ModelRef)
	}
}

func TestSwitchModelRejectsCandidateWithAnotherRuntime(t *testing.T) {
	closeServeTestUsage(t)
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Cleanup(func() { closeServeTestUsage(t) })
	s, old, _, ref := newExclusiveSessionServe(t)
	defer s.Close()
	_, other, _, _ := newExclusiveSessionServe(t)
	s.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		opts.SessionService, opts.SessionRuntime, _ = other.SessionBinding()
		return serveModelReplacement(opts, nil), nil
	}
	if err := s.switchModel(t.Context(), "next-model"); err == nil || !strings.Contains(err.Error(), "changed the current session runtime") {
		t.Fatalf("invalid replacement error = %v", err)
	}
	if s.ctl() != old {
		t.Fatal("invalid candidate replaced current controller")
	}
	if got, _ := old.SessionRef(); got != ref {
		t.Fatal("invalid candidate changed current identity")
	}
	if err := old.Snapshot(); err != nil {
		t.Fatalf("invalid candidate closed source writer: %v", err)
	}
}

func TestSwitchModelSnapshotFailureKeepsOldController(t *testing.T) {
	closeServeTestUsage(t)
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Cleanup(func() { closeServeTestUsage(t) })
	s, old, _, _ := newExclusiveSessionServe(t)
	defer s.Close()
	if err := old.AdoptRebuiltModelContext([]provider.Message{
		{ID: "system", Role: provider.RoleSystem, Content: "system"},
		{ID: "user", Role: provider.RoleUser, Content: "persist this", Origin: provider.MessageOriginUser},
	}); err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := old.SessionBinding()
	if err := runtime.Session().Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.buildControllerWithOptions = func(context.Context, string, boot.Options) (*control.Controller, error) {
		t.Fatal("failed snapshot must stop replacement construction")
		return nil, nil
	}
	if err := s.switchModel(t.Context(), "next-model"); err == nil || !strings.Contains(err.Error(), "snapshot current session") {
		t.Fatalf("switchModel error = %v, want snapshot failure", err)
	}
	if s.ctl() != old {
		t.Fatal("snapshot failure published a replacement")
	}
}
