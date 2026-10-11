package control

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	goaldomain "deepzora/internal/goal"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func TestGoalLifecycleProjectionRestoreIsDisarmed(t *testing.T) {
	raw := json.RawMessage(`{"version":1,"current":{"id":"goal-1","revision":2,"objective":"ship","phase":"active","maxGoalRounds":null,"roundsStarted":5,"createdAt":"2026-09-13T10:00:00Z","updatedAt":"2026-09-13T10:00:00Z"}}`)
	machine, err := goalLifecycleFromProjection(raw)
	if err != nil {
		t.Fatal(err)
	}
	view := machine.Get()
	if view == nil || view.ID != "goal-1" || view.RoundsStarted != 5 || view.Activation != goaldomain.ActivationDisarmed {
		t.Fatalf("view = %+v", view)
	}
}

func TestGoalLifecycleMalformedProjectionFailsClosed(t *testing.T) {
	if _, err := goalLifecycleFromProjection(json.RawMessage(`{"version":99,"current":null}`)); err == nil {
		t.Fatal("unknown goal state version was accepted")
	}
}

func TestExclusiveControllerLoadsGoalFromV3Projection(t *testing.T) {
	service, err := session.NewService("desktop", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v4")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "goal-session"})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"version":1,"current":{"id":"goal-v3","revision":3,"objective":"finish runtime","phase":"active","maxGoalRounds":null,"roundsStarted":2,"createdAt":"2026-09-13T10:00:00Z","updatedAt":"2026-09-13T10:00:00Z"}}`)
	if _, err := runtime.Session().AppendBatch(context.Background(), "goal-seed", []session.Event{{Kind: "goal/state", Payload: raw}}); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() { c.Close() })
	view, loadErr := c.goalLifecycleView()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if view == nil || view.ID != "goal-v3" || view.Activation != goaldomain.ActivationDisarmed {
		t.Fatalf("view = %+v", view)
	}
}

func TestColdRestoredGoalComposeIncludesRecoverableGoalContext(t *testing.T) {
	service, err := session.NewService("desktop", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v3")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "goal-recovery-context"})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"version":1,"current":{"id":"goal-v3","revision":3,"objective":"finish runtime","phase":"active","maxGoalRounds":null,"roundsStarted":2,"createdAt":"2026-09-13T10:00:00Z","updatedAt":"2026-09-13T10:00:00Z"}}`)
	if _, err := runtime.Session().AppendBatch(t.Context(), "goal-seed", []session.Event{{Kind: "goal/state", Payload: raw}}); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(c.Close)

	composed := c.Compose("continue")
	for _, want := range []string{"<goal-recovery>", `"goalId":"goal-v3"`, `"revision":3`, "finish runtime", "update_goal with action resume"} {
		if !strings.Contains(composed, want) {
			t.Fatalf("composed input missing %q:\n%s", want, composed)
		}
	}
}

func TestGoalLifecycleMutationAppendsToActiveV3Session(t *testing.T) {
	service, err := session.NewService("desktop", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v4")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "goal-mutation"})
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() { c.Close() })
	c.mu.Lock()
	c.turns.phase = session.RuntimeRunning
	c.noteExecutionLocked(session.RuntimeRunning, "turn")
	c.mu.Unlock()
	snapshot := runtime.Snapshot()
	authority := tool.GoalAuthority{
		Source: tool.GoalSourceDirectHuman, SessionID: runtime.Ref().SessionID,
		RuntimeEpoch: snapshot.Epoch, ActivityID: snapshot.ActivityRevision,
	}
	created, err := c.CreateGoal(t.Context(), goaldomain.CreateRequest{Objective: "ship"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if created.Activation != goaldomain.ActivationArmed {
		t.Fatalf("created = %+v", created)
	}
	projected := runtime.Session().Snapshot().Projection.GoalState
	if len(projected) == 0 {
		t.Fatal("goal mutation did not append goal/state")
	}
	loaded, err := goalLifecycleFromProjection(projected)
	if err != nil || loaded.Get() == nil || loaded.Get().ID != created.ID {
		t.Fatalf("projected goal = %+v, err = %v", loaded.Get(), err)
	}
	c.mu.Lock()
	c.turns.phase = session.RuntimeIdle
	c.noteExecutionLocked(session.RuntimeIdle, "")
	c.mu.Unlock()
}

func TestGoalLifecycleMutationRejectsStaleRuntimeAuthority(t *testing.T) {
	service, err := session.NewService("desktop", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v4")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "goal-stale"})
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() { c.Close() })
	_, err = c.CreateGoal(t.Context(), goaldomain.CreateRequest{Objective: "ship"}, tool.GoalAuthority{
		Source: tool.GoalSourceDirectHuman, SessionID: "another-session", RuntimeEpoch: "old", ActivityID: 1,
	})
	if goaldomain.ErrorCodeOf(err) != goaldomain.ErrUserAuthorityRequired {
		t.Fatalf("stale authority error = %v", err)
	}
	if len(runtime.Session().Snapshot().Projection.GoalState) != 0 {
		t.Fatal("rejected mutation changed projection")
	}
}

func TestModelCannotResumeUserPausedGoal(t *testing.T) {
	service, err := session.NewService("desktop", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v4")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "goal-paused"})
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(c.Close)
	c.mu.Lock()
	c.turns.phase = session.RuntimeRunning
	c.noteExecutionLocked(session.RuntimeRunning, "turn")
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.turns.phase = session.RuntimeIdle
		c.noteExecutionLocked(session.RuntimeIdle, "")
		c.mu.Unlock()
	}()
	snapshot := runtime.Snapshot()
	authority := tool.GoalAuthority{Source: tool.GoalSourceDirectHuman, SessionID: runtime.Ref().SessionID,
		RuntimeEpoch: snapshot.Epoch, ActivityID: snapshot.ActivityRevision}
	created, err := c.CreateGoal(t.Context(), goaldomain.CreateRequest{Objective: "stay paused"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := c.UpdateGoal(t.Context(), tool.GoalUpdateRequest{Ref: created.Ref(), Action: tool.GoalActionPause}, authority)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.UpdateGoal(t.Context(), tool.GoalUpdateRequest{Ref: paused.Ref(), Action: tool.GoalActionResume}, authority)
	if goaldomain.ErrorCodeOf(err) != goaldomain.ErrUserAuthorityRequired {
		t.Fatalf("model resume error = %v", err)
	}
}
