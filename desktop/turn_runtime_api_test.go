package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/sessioninbox"
)

type exactTurnRunner struct {
	once       sync.Once
	started    chan struct{}
	beforeWait func(context.Context) error
}

func (r *exactTurnRunner) Run(ctx context.Context, _ string) error {
	r.once.Do(func() { close(r.started) })
	if r.beforeWait != nil {
		if err := r.beforeWait(ctx); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

// waitTabSinkSettled blocks until the sink's turn reservation has been released
// by TurnDone, i.e. the turn reached its terminal state.
func waitTabSinkSettled(t *testing.T, sink *tabEventSink) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.RLock()
		idle := sink.turn.phase == turnSubmissionIdle
		sink.mu.RUnlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("turn did not reach terminal state")
}

func cleanupExactTurnController(t *testing.T, ctrl *control.Controller) {
	t.Helper()
	t.Cleanup(func() {
		ctrl.Close()
		select {
		case <-ctrl.Closed():
		case <-time.After(5 * time.Second):
			t.Error("Controller teardown did not finish before removing its storage")
		}
	})
}

func TestTurnRuntimeAPIRoutesStopAnswerAndReplayByExactTurn(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background(), runtimeEpoch: "runtime-current"}
	ctrl := control.New(control.Options{
		Runner: runner, Sink: sink, SessionDir: dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	cleanupExactTurnController(t, ctrl)
	answers := make(chan []event.AskAnswer, 1)
	runner.beforeWait = func(ctx context.Context) error {
		got, err := ctrl.Ask(ctx, []event.AskQuestion{{ID: "q1", Prompt: "Which environment?",
			Options: []event.AskOption{{Label: "staging"}, {Label: "production"}}}})
		if err == nil {
			answers <- got
		}
		return err
	}
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink,
		TopicID: "topic-current", SessionID: "session-current", SessionGeneration: 1}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-1")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	if !strings.HasPrefix(start.TurnID, "turn_") || start.SubmissionID != "submission-1" {
		t.Fatalf("start receipt = %+v, want stable turn and submission ids", start)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}

	var identity PromptIdentityView
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := app.PendingPromptIdentitiesForTab(tab.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 && pending[0].TurnID != "" {
			identity = pending[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if identity.PromptID == "" || identity.TurnID != start.TurnID || identity.RuntimeEpoch != sink.runtimeEpochSnapshot() || identity.Kind != "ask" {
		t.Fatalf("pending Ask identity = %+v, want current turn/runtime", identity)
	}
	answer := PromptAnswerView{Questions: []QuestionAnswer{{QuestionID: "q1", Selected: []string{"staging"}}}}
	resolveTab := func(target InteractionTargetView, answer PromptAnswerView) error {
		return app.ResolvePromptForTab(target.TabID, target.PromptID, target.TurnID, target.RuntimeEpoch, target.Kind, answer)
	}
	target := InteractionTargetView{TabID: tab.ID, HostID: localDesktopHostID,
		SessionID: tab.SessionID, SessionGeneration: tab.SessionGeneration,
		PromptID: identity.PromptID, TurnID: identity.TurnID, RuntimeEpoch: identity.RuntimeEpoch, Kind: identity.Kind}
	for _, resolve := range []struct {
		name string
		call func(InteractionTargetView, PromptAnswerView) error
	}{{"tab", resolveTab}, {"session", app.ResolvePromptForSession}} {
		t.Run(resolve.name+"/stale", func(t *testing.T) {
			stale := target
			stale.TurnID = "turn_stale"
			if err := resolve.call(stale, answer); !errors.Is(err, control.ErrPromptStaleTurn) {
				t.Fatalf("stale turn answer = %v, want ErrPromptStaleTurn", err)
			}
			stale = target
			stale.RuntimeEpoch = "runtime-old"
			if err := resolve.call(stale, answer); !errors.Is(err, control.ErrPromptStaleRuntime) {
				t.Fatalf("stale runtime answer = %v, want ErrPromptStaleRuntime", err)
			}
		})
	}
	for _, mutation := range []string{"session", "generation"} {
		stale := target
		if mutation == "session" {
			stale.SessionID = "session-old"
		} else {
			stale.SessionGeneration--
		}
		if err := app.ResolvePromptForSession(stale, answer); err == nil || !strings.Contains(err.Error(), "session binding is stale") {
			t.Fatalf("stale %s answer = %v, want stale session binding", mutation, err)
		}
	}
	if pending := ctrl.PendingPromptIdentities(); len(pending) != 1 {
		t.Fatalf("stale answers consumed Ask: %+v", pending)
	}
	// This fixture exercises the path-backed controller directly; route its
	// inbox assertion through that controller identity, then restore the exact
	// session identity used by the prompt assertions below.
	app.mu.Lock()
	sessionID := tab.SessionID
	tab.SessionID, tab.SessionPath = "", ctrl.SessionPath()
	app.mu.Unlock()
	inboxTarget, err := app.CaptureInboxTarget(tab.ID, ctrl.SessionPath())
	if err != nil {
		t.Fatal(err)
	}
	queueResult, err := app.InboxQueueForTarget(inboxTarget, control.InboxQueueRequest{Kind: "enqueue_steer", TurnID: "turn_stale", Display: "late steer", Text: "late steer", IdempotencyKey: "late-steer"})
	if err != nil || queueResult.Receipt == nil || queueResult.Receipt.Disposition != sessioninbox.DispositionQueuedFollowup {
		t.Fatalf("stale turn guidance was not preserved as a follow-up: %+v, %v", queueResult, err)
	}
	// Keep the follow-up queued while this test verifies the original turn's
	// terminal replay. Otherwise TurnDone legitimately dispatches a second
	// blocking turn, and observing an idle sink becomes a scheduling race.
	paused, err := app.InboxQueueForTarget(inboxTarget, control.InboxQueueRequest{Kind: "pause", Paused: true})
	if err != nil || !paused.Snapshot.Paused || len(paused.Snapshot.Items) != 1 {
		t.Fatalf("pause must preserve the queued follow-up: %+v, %v", paused, err)
	}
	app.mu.Lock()
	tab.SessionID, tab.SessionPath = sessionID, ""
	app.mu.Unlock()
	// Two exact transports may retry the same card concurrently. The provider
	// receives one answer and the durable ledger records one transition.
	gate := make(chan struct{})
	results := make(chan error, 2)
	for _, resolve := range []func(InteractionTargetView, PromptAnswerView) error{resolveTab, app.ResolvePromptForSession} {
		go func() { <-gate; results <- resolve(target, answer) }()
	}
	close(gate)
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("identical concurrent answer = %v, want idempotent success", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent answer did not finish")
		}
	}
	if err := app.ResolvePromptForSession(target, answer); err != nil {
		t.Fatalf("identical completed answer retry = %v, want idempotent success", err)
	}
	select {
	case got := <-answers:
		if !reflect.DeepEqual(got, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"staging"}}}) {
			t.Fatalf("Ask received %+v, want original selections", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("answer did not unblock Ask")
	}
	conflict := PromptAnswerView{Questions: []QuestionAnswer{{QuestionID: "q1", Selected: []string{"production"}}}}
	if err := resolveTab(target, conflict); !errors.Is(err, control.ErrPromptAlreadyResolved) {
		t.Fatalf("conflicting late answer = %v, want ErrPromptAlreadyResolved", err)
	}
	unknown := target
	unknown.PromptID = "never-published"
	if err := resolveTab(unknown, answer); !errors.Is(err, control.ErrPromptNotPending) {
		t.Fatalf("unknown answer = %v, want ErrPromptNotPending", err)
	}
	if pending := ctrl.PendingPromptIdentities(); len(pending) != 0 {
		t.Fatalf("answered Ask remains pending: %+v", pending)
	}
	records, err := ctrl.TurnEventsAfter(0)
	if err != nil {
		t.Fatal(err)
	}
	answered := 0
	for _, record := range records {
		if record.Kind == "prompt_answered" {
			answered++
		}
	}
	if answered != 1 {
		t.Fatalf("durable Ask answer count = %d, want one", answered)
	}

	before, err := app.TurnEventsForTab(tab.ID, 0)
	if err != nil {
		t.Fatalf("TurnEventsForTab before cancel: %v", err)
	}
	if len(before.Events) == 0 || before.Events[0].Status != event.TurnQueued {
		t.Fatalf("events before cancel = %+v, want durable queued prefix", before)
	}
	if err := app.InterruptTurnForTab(tab.ID, start.TurnID); err != nil {
		t.Fatalf("InterruptTurnForTab: %v", err)
	}
	waitTabSinkSettled(t, sink)
	after, err := app.TurnEventsForTab(tab.ID, before.Events[len(before.Events)-1].Sequence)
	if err != nil {
		t.Fatalf("TurnEventsForTab after cancel: %v", err)
	}
	if len(after.Events) == 0 || after.Events[len(after.Events)-1].Status != event.TurnInterrupted {
		t.Fatalf("events after cancel = %+v, want non-nil interrupted suffix", after)
	}
	empty, err := app.TurnEventsForTab(tab.ID, after.Events[len(after.Events)-1].Sequence)
	if err != nil {
		t.Fatalf("empty replay: %v", err)
	}
	if empty.Events == nil || len(empty.Events) != 0 {
		t.Fatalf("empty replay events = %#v, want []", empty.Events)
	}
}

// A Stop button rendered for an earlier turn must still stop the turn that is
// running now; only an idle tab is reported back, with a stable code.
func TestInterruptTurnForTabStopsActiveWorkDespiteStaleTurnID(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	ctrl := control.New(control.Options{
		Runner: runner, Sink: sink, SessionDir: dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	cleanupExactTurnController(t, ctrl)
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	if err := app.InterruptTurnForTab(tab.ID, "turn_none"); err != nil {
		t.Fatalf("idle stop = %v, want idempotent success", err)
	}
	if receipt, err := app.CancelSessionForTab(tab.ID); err != nil || !receipt.Accepted || !receipt.AlreadyIdle {
		t.Fatalf("idle cancel receipt = %+v, %v", receipt, err)
	}

	if _, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-1"); err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}
	if err := app.InterruptTurnForTab(tab.ID, "turn_stale"); err != nil {
		t.Fatalf("stale-id stop = %v, want the active turn interrupted", err)
	}
	waitTabSinkSettled(t, sink)
	if status := ctrl.RuntimeStatus(); status.Running || status.Status != event.TurnInterrupted {
		t.Fatalf("terminal status = %q (running=%v), want interrupted", status.Status, status.Running)
	}
}

func TestStartTurnForTabReturnsManagementDispositionWithoutTurnID(t *testing.T) {
	dir := t.TempDir()
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl"), Sink: sink})
	cleanupExactTurnController(t, ctrl)
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	start, err := app.StartTurnForTab(tab.ID, "/context", "submission-management")
	if err != nil {
		t.Fatalf("StartTurnForTab management command: %v", err)
	}
	if start.Disposition != control.SubmitManagementHandled || start.TurnID != "" {
		t.Fatalf("management receipt = %+v, want management_handled without turn id", start)
	}
	replay, err := app.TurnEventsForTab(tab.ID, 0)
	if err != nil {
		t.Fatalf("TurnEventsForTab: %v", err)
	}
	if len(replay.Events) != 0 {
		t.Fatalf("management command created durable turn events: %+v", replay.Events)
	}
}

func TestStartTurnForTabRejectsManagementDuringActiveTurn(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	ctrl := control.New(control.Options{
		Runner: runner, Sink: sink, SessionDir: dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	cleanupExactTurnController(t, ctrl)
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-active")
	if err != nil {
		t.Fatalf("StartTurnForTab active turn: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}
	if _, err := app.StartTurnForTab(tab.ID, "/context", "submission-management"); !errors.Is(err, control.ErrTurnRunning) {
		t.Fatalf("management command during active turn = %v, want ErrTurnRunning", err)
	}
	status := ctrl.RuntimeStatus()
	if !status.Running || status.TurnID != start.TurnID {
		t.Fatalf("active turn changed after rejected management command: %+v", status)
	}
	ctrl.Cancel()
	waitTabSinkSettled(t, sink)
}

// The turn id belongs to the controller that admitted the submission. A tab
// whose controller is replaced before the reply is built must still answer
// with that receipt instead of asking the replacement.
func TestStartTurnForTabAnswersWithAdmittingControllerReceipt(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	replacementDir := t.TempDir()
	replacement := control.New(control.Options{SessionDir: replacementDir, SessionPath: filepath.Join(replacementDir, "session.jsonl")})
	cleanupExactTurnController(t, replacement)
	var swap sync.Once
	ctrl := control.New(control.Options{
		Runner: runner, SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl"),
		Sink: event.FuncSink(func(e event.Event) {
			sink.Emit(e)
			if e.Kind == event.TurnStarted {
				swap.Do(func() {
					app.mu.Lock()
					tab.Ctrl = replacement
					app.mu.Unlock()
				})
			}
		}),
	})
	cleanupExactTurnController(t, ctrl)
	tab.Ctrl = ctrl

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-swapped")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	if want := ctrl.TurnIDForSubmission("submission-swapped"); want == "" || start.TurnID != want {
		t.Fatalf("turn id = %q, want the admitting controller's receipt %q", start.TurnID, want)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}
	ctrl.Cancel()
}
