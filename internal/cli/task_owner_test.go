package cli

import (
	"context"
	"io"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
)

func TestTaskKillerRoutesByCurrentOpaqueSessionIdentity(t *testing.T) {
	service := testCLISessionService(t, t.TempDir())
	manager := jobs.NewManager(event.Discard)
	ctrl := newOwnedTestController(t, control.Options{
		Executor: agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard),
		Jobs:     manager, SessionService: service, ExclusiveSession: true,
	})
	ref, err := ctrl.BindFreshSession(t.Context(), "task.with.dots")
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	job := manager.StartForSession(ref.SessionID, "task", "scoped job", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return "", ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not start")
	}
	killer := ctrlKillerAdapter{ctrl: ctrl}
	if killer.Kill("other-session", job.ID) {
		t.Fatal("another session could cancel this task")
	}
	if !killer.Kill(ref.SessionID, job.ID) {
		t.Fatal("current session could not cancel its own task")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not receive cancellation")
	}
}
