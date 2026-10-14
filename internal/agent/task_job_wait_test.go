package agent

import (
	"context"
	"testing"
	"time"

	"deepzora/internal/jobs"
)

func awaitTaskJobs(t *testing.T, manager *jobs.Manager, sessionID string, ids []string) []jobs.Result {
	t.Helper()
	// Wait on the jobs' terminal channels; this deadline only detects a stuck
	// fixture, rather than requiring filesystem-backed work to finish quickly.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	results := manager.WaitForSession(ctx, sessionID, ids, 0)
	if err := ctx.Err(); err != nil {
		t.Fatalf("background job fixture did not terminate: %v; results: %+v", err, results)
	}
	return results
}
