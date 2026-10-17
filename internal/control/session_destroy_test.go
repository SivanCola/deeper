package control

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"deepzora/internal/event"
	"deepzora/internal/jobs"
)

func TestSessionDestroyHandleJoinsExitAfterGraceExpires(t *testing.T) {
	manager := jobs.NewManager(event.Discard, jobs.WithTeardownGrace(time.Millisecond))
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() { releaseJob(); manager.Close() })
	started := make(chan struct{})
	job := manager.StartForSession("owned", "task", "slow cleanup", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	})
	<-started
	handle := (&Controller{jobs: manager}).BeginDestroySession("owned")
	if !handle.Async {
		t.Fatal("running job must require asynchronous teardown")
	}
	for _, grace := range []time.Duration{-1, 0} {
		result := handle.WaitFor(grace)
		if len(result.TimedOut) != 1 || result.TimedOut[0].ID != job.ID {
			t.Fatalf("WaitFor(%s) lost unfinished job: %+v", grace, result)
		}
	}
	joined := make(chan struct{})
	go func() { handle.WaitAll(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("WaitAll returned before the cancelled job exited")
	case <-time.After(20 * time.Millisecond):
	}
	releaseJob()
	select {
	case <-joined:
	case <-time.After(30 * time.Second):
		t.Fatal("WaitAll did not join actual exit")
	}
	if !manager.IsDestroying("owned") {
		t.Fatal("joining exit ended the destroy window before artifact cleanup")
	}
	handle.Finish()
	if manager.IsDestroying("owned") {
		t.Fatal("Finish retained the destroy window")
	}
	if _, _, ok := manager.OutputForSession("owned", job.ID); ok {
		t.Fatal("Finish retained the destroyed job")
	}
}

func TestSessionDestroyHandleWithoutJobsSupportsCurrentWaitContract(t *testing.T) {
	handle := (&Controller{}).BeginDestroySession("empty")
	if handle.Async || handle.WaitFor(-1).HasTimedOut() {
		t.Fatal("empty destroy should complete immediately")
	}
	handle.WaitAll()
	handle.Finish()
}
