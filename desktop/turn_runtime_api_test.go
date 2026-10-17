package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/sessioninbox"
	"deepzora/internal/transcript"
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

// waitTabSinkSettled blocks until TurnDone releases the tab sink's reservation;
// controller finalization may still be in flight.
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

func TestTurnRuntimeAPIRoutesStopAnswerAndFollowByExactTurn(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background(), runtimeEpoch: "runtime-current"}
	ctrl := newCurrentRebuildController(t, app, control.Options{Runner: runner, Sink: sink})
	ref, _ := ctrl.SessionRef()
	runtime, ok := ctrl.SessionService().Runtime(ref)
	if !ok {
		t.Fatal("current runtime is missing")
	}
	sink.runtimeEpoch = runtime.StateSnapshot().Epoch
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
		TopicID: "topic-current", SessionID: ref.SessionID, SessionGeneration: 1}
	app.tabs, app.activeTabID = map[string]*WorkspaceTab{tab.ID: tab}, tab.ID
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
	inboxTarget, err := app.CaptureInboxTarget(tab.ID, sessionRoute(ref.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	queueResult, err := app.InboxQueueForTarget(inboxTarget, control.InboxQueueRequest{Kind: "enqueue_steer", TurnID: "turn_stale", Display: "late steer", Text: "late steer", IdempotencyKey: "late-steer"})
	if err != nil || queueResult.Receipt == nil || queueResult.Receipt.Disposition != sessioninbox.DispositionQueuedFollowup {
		t.Fatalf("stale turn guidance was not preserved as a follow-up: %+v, %v", queueResult, err)
	}
	// Keep the follow-up queued while this test verifies the original turn's
	// terminal Follow. Otherwise TurnDone legitimately dispatches a second
	// blocking turn, and observing an idle sink becomes a scheduling race.
	paused, err := app.InboxQueueForTarget(inboxTarget, control.InboxQueueRequest{Kind: "pause", Paused: true})
	if err != nil || !paused.Snapshot.Paused || len(paused.Snapshot.Items) != 1 {
		t.Fatalf("pause must preserve the queued follow-up: %+v, %v", paused, err)
	}
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
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	answered := 0
	for offset := uint64(0); ; {
		page, err := runtime.Session().Read(t.Context(), offset, 128)
		if err != nil {
			t.Fatal(err)
		}
		for _, commit := range page.Commits {
			for _, record := range commit.Events {
				if record.Kind == "interaction/resolved" {
					answered++
				}
			}
		}
		if !page.Truncated {
			break
		}
		if page.Next <= offset {
			t.Fatal("current event read did not advance")
		}
		offset = page.Next
	}
	if answered != 1 {
		t.Fatalf("durable Ask answer count = %d, want one", answered)
	}

	before, err := app.TranscriptFollowForTab(tab.ID, transcript.FollowRequest{})
	if err != nil || before.Snapshot == nil {
		t.Fatalf("Follow before cancel: %v", err)
	}
	defer app.TranscriptFollowForTab(tab.ID, transcript.FollowRequest{Subscription: before.Subscription, Close: true})
	if before.Snapshot.Runtime.TurnID != start.TurnID || before.Snapshot.Runtime.Status.Terminal() || len(before.Snapshot.Runtime.PendingEvents) != 0 {
		t.Fatalf("Follow lost the active turn or retained the answered prompt: %+v", before.Snapshot.Runtime)
	}
	if err := app.InterruptTurnForTab(tab.ID, start.TurnID); err != nil {
		t.Fatalf("InterruptTurnForTab: %v", err)
	}
	waitTabSinkSettled(t, sink)
	after, err := app.TranscriptFollowForTab(tab.ID, transcript.FollowRequest{Subscription: before.Subscription, AfterRevision: before.Snapshot.ProjectionRevision})
	if err != nil || after.ResetRequired {
		t.Fatalf("Follow after cancel: reset=%v err=%v", after.ResetRequired, err)
	}
	interrupted := false
	for _, change := range after.Changes {
		if change.Event != nil && change.Event.Kind == "turn_done" && change.Event.Status == string(event.TurnInterrupted) {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("Follow did not publish the interruption: %+v", after.Changes)
	}
	closed, err := app.TranscriptFollowForTab(tab.ID, transcript.FollowRequest{Subscription: before.Subscription, Close: true})
	if err != nil || closed.Changes == nil || len(closed.Changes) != 0 {
		t.Fatalf("closed Follow: %+v, %v", closed, err)
	}

}

// A Stop button rendered for an earlier turn must still stop the turn that is
// running now; only an idle tab is reported back, with a stable code.
func TestInterruptTurnForTabStopsActiveWorkDespiteStaleTurnID(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	terminal := make(chan event.Event, 1)
	release := make(chan struct{})
	releaseFanout := sync.OnceFunc(func() { close(release) })
	defer releaseFanout()
	ctrl := newCurrentRebuildController(t, app, control.Options{Runner: runner, Sink: event.FuncSink(func(e event.Event) {
		sink.Emit(e)
		if e.Kind == event.TurnDone {
			// The tab reservation is released before the controller finishes
			// terminal fan-out and opens its execution admission gate.
			terminal <- e
			<-release
		}
	})})
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app.tabs, app.activeTabID = map[string]*WorkspaceTab{tab.ID: tab}, tab.ID
	sink.app = app

	if err := app.InterruptTurnForTab(tab.ID, "turn_none"); err != nil {
		t.Fatalf("idle stop = %v, want idempotent success", err)
	}
	if receipt, err := app.CancelSessionForTab(tab.ID); err != nil || !receipt.Accepted || !receipt.AlreadyIdle {
		t.Fatalf("idle cancel receipt = %+v, %v", receipt, err)
	}

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-1")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(30 * time.Second):
		t.Fatal("turn runner did not start")
	}
	idle, active := ctrl.TurnIdleDone()
	if !active || idle == nil {
		t.Fatal("active turn did not expose its idle boundary")
	}
	if err := app.InterruptTurnForTab(tab.ID, "turn_stale"); err != nil {
		t.Fatalf("stale-id stop = %v, want the active turn interrupted", err)
	}
	select {
	case done := <-terminal:
		if done.TurnID != start.TurnID || done.Status != event.TurnInterrupted || !done.Cancelled {
			t.Fatalf("stale-id stop terminal event = %+v, want the admitted turn interrupted", done)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("stale-id stop did not publish TurnDone")
	}
	sink.mu.RLock()
	phase := sink.turn.phase
	sink.mu.RUnlock()
	if phase != turnSubmissionIdle {
		t.Fatalf("terminal sink phase = %v, want idle", phase)
	}
	if status := ctrl.RuntimeStateSnapshot(); !status.Running || status.Phase != "finishing" || status.TurnStatus != event.TurnInterrupted {
		t.Fatalf("runtime during terminal fan-out = %+v, want finishing with interrupted turn", status)
	}
	select {
	case <-idle:
		t.Fatal("controller idle boundary closed before terminal fan-out returned")
	default:
	}
	releaseFanout()
	select {
	case <-idle:
	case <-time.After(30 * time.Second):
		t.Fatal("interrupted turn did not release its idle boundary")
	}
	if status := ctrl.RuntimeStateSnapshot(); status.Running || status.Phase != "idle" || status.TurnStatus != event.TurnInterrupted {
		t.Fatalf("terminal runtime = %+v, want idle with interrupted turn", status)
	}
}

func TestStartTurnForTabReturnsManagementDispositionWithoutTurnID(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	ctrl := newCurrentRebuildController(t, app, control.Options{Sink: sink})
	ref, _ := ctrl.SessionRef()
	runtime, ok := ctrl.SessionService().Runtime(ref)
	if !ok {
		t.Fatal("current runtime is missing")
	}
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink, SessionID: ref.SessionID}
	app.tabs, app.activeTabID = map[string]*WorkspaceTab{tab.ID: tab}, tab.ID
	sink.app = app
	start, err := app.StartTurnForTab(tab.ID, "/context", "submission-management")
	if err != nil {
		t.Fatalf("StartTurnForTab management command: %v", err)
	}
	if start.Disposition != control.SubmitManagementHandled || start.TurnID != "" {
		t.Fatalf("management receipt = %+v, want management_handled without turn id", start)
	}
	projection := runtime.Session().StateSnapshot().Projection
	if projection.TurnID != "" || len(projection.Turns) != 0 {
		t.Fatal("management command created a durable turn")
	}
}

func TestStartTurnForTabRejectsManagementDuringActiveTurn(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	ctrl := newCurrentRebuildController(t, app, control.Options{Runner: runner, Sink: sink})
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app.tabs, app.activeTabID = map[string]*WorkspaceTab{tab.ID: tab}, tab.ID
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
	status := ctrl.RuntimeStateSnapshot()
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
	isolateDesktopUserDirs(t)
	app := NewApp()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, sink: sink}
	app.tabs, app.activeTabID = map[string]*WorkspaceTab{tab.ID: tab}, tab.ID
	sink.app = app

	replacement := newNamedCurrentRebuildController(t, app, "replacement", control.Options{})
	var swap sync.Once
	ctrl := newCurrentRebuildController(t, app, control.Options{
		Runner: runner,
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
