package control

import (
	"context"
	"time"

	"deepzora/internal/jobs"
)

// SessionDestroyHandle separates cancelled-job waiting from ending the destroy
// window, allowing persistent artifacts to move between WaitFor and Finish.
// WaitFor uses the manager's default grace for negative durations. WaitAll
// joins actual job exit without a grace deadline before deferred cleanup.
type SessionDestroyHandle struct {
	WaitFor func(time.Duration) jobs.TeardownResult
	WaitAll func()
	Finish  func()
	Async   bool
}

func (c *Controller) BeginDestroySession(parentSession string) SessionDestroyHandle {
	if c.jobs == nil || parentSession == "" {
		wait := func(time.Duration) jobs.TeardownResult { return jobs.TeardownResult{} }
		noop := func() {}
		return SessionDestroyHandle{WaitFor: wait, WaitAll: noop, Finish: noop}
	}
	teardown := c.jobs.BeginDestroySession(parentSession)
	return SessionDestroyHandle{
		WaitFor: func(requested time.Duration) jobs.TeardownResult {
			grace := c.jobs.TeardownGrace()
			if requested >= 0 && requested < grace {
				grace = requested
			}
			return c.jobs.WaitTeardown(context.Background(), teardown, grace)
		},
		WaitAll: func() {
			c.jobs.WaitTeardown(context.Background(), teardown, -1)
		},
		Finish: func() { c.jobs.FinishDestroySession(parentSession) },
		Async:  teardown.Async(),
	}
}
