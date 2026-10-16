package control

import (
	"context"
	"errors"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
)

func TestReplacedCurrentExecutionBlocksAsyncAndSynchronousAdmission(t *testing.T) {
	first, service, runtime := exclusiveTestController(t, event.Discard)
	executor := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	replacement := newOwnedTestController(t, Options{Executor: executor, Sink: event.Discard,
		SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	oldGeneration := first.ExecutionGeneration()
	if err := ActivateControllerReplacement(first, replacement); err != nil {
		t.Fatal(err)
	}
	if runtime.OwnsExecution(oldGeneration) || !runtime.OwnsExecution(replacement.ExecutionGeneration()) {
		t.Fatal("runtime did not transfer execution ownership")
	}
	ran := false
	if got := first.runGuarded(func(context.Context) error { ran = true; return nil }); got != turnDroppedDraining {
		t.Fatalf("outgoing async admission = %v, want draining", got)
	}
	if ran {
		t.Fatal("outgoing execution ran after ownership transfer")
	}
	if err := first.Run(t.Context(), "must not run"); !errors.Is(err, ErrRuntimeDraining) {
		t.Fatalf("outgoing synchronous admission = %v, want draining", err)
	}
}
