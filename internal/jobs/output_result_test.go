package jobs

import (
	"context"
	"io"
	"testing"

	"deepzora/internal/event"
)

// TestOutputSurfacesResultForBufferlessJob probes a task-style job: its run func
// returns a final answer and writes nothing to the streamed buffer (task ignores
// the io.Writer). job_output must expose the terminal answer even when the
// streaming buffer is empty, preserving task(run_in_background) results.
func TestOutputSurfacesResultForBufferlessJob(t *testing.T) {
	m := NewManager(event.Discard)
	j := m.StartForSession("", "task", "demo", func(ctx context.Context, _ io.Writer) (string, error) {
		return "THE-ANSWER", nil
	})
	<-j.done

	text, status, ok := m.OutputForSession(j.SessionID, j.ID)
	if !ok {
		t.Fatal("job not found")
	}
	if status != Done {
		t.Fatalf("status = %q, want done", status)
	}
	if text == "" {
		t.Errorf("job_output returned no output for a finished task job — its answer %q is invisible", "THE-ANSWER")
	}
}
