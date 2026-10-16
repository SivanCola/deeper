package main

import (
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/tool"
)

func TestCurrentFileCheckpointsSurviveColdReopen(t *testing.T) {
	fx := newCurrentRewindFixture(t)
	service := fx.ctrl.SessionService()
	before, err := fx.ctrl.CheckpointForkRequest(1)
	if err != nil {
		t.Fatal(err)
	}
	fx.ctrl.Close()
	<-fx.ctrl.Closed()
	if err := service.Close(t.Context(), fx.ref); err != nil {
		t.Fatal(err)
	}
	binding, err := service.Open(t.Context(), fx.ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Release(t.Context()) })
	exec := agent.New(testutil.NewMock("reopen"), tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Runner: exec, Executor: exec, Sink: event.Discard,
		WorkspaceRoot: filepath.Dir(fx.filePath), SessionService: service, SessionRuntime: binding.Runtime(), ExclusiveSession: true})
	t.Cleanup(func() { ctrl.Close(); <-ctrl.Closed() })
	checkpoints := ctrl.Checkpoints()
	if len(checkpoints) != 2 || len(checkpoints[1].Paths) != 1 {
		t.Fatalf("cold checkpoints: %+v", checkpoints)
	}
	after, err := ctrl.CheckpointForkRequest(1)
	if err != nil || before != after {
		t.Fatalf("cold fork boundary = %+v, %v; want %+v", after, err, before)
	}
	plan, err := ctrl.PrepareRewind(1, control.RewindCode)
	if err != nil || !plan.CanFiles {
		t.Fatalf("cold rewind plan = %+v, %v", plan, err)
	}
	result, err := ctrl.CommitRewind(plan.PlanID)
	if err != nil || !result.OK {
		t.Fatalf("cold rewind = %+v, %v", result, err)
	}
	got, err := os.ReadFile(fx.filePath)
	if err != nil || string(got) != "before" {
		t.Fatalf("restored file = %q, %v", got, err)
	}
}
