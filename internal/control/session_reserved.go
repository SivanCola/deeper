package control

import (
	"context"
	"deepzora/internal/agent"
	"deepzora/internal/session"
	"errors"
)

// InitializeEmptySession gives a current cold empty session the ordinary
// creation prompt and model configuration before its first runtime bind. The
// session writer serializes the idempotent seed with every other commit.
func (c *Controller) InitializeEmptySession(ctx context.Context, ref session.SessionRef) (result error) {
	service, _, _ := c.v3Binding()
	if service == nil {
		return errors.New("session service is unavailable")
	}
	binding, err := service.Open(ctx, ref)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, binding.Release(context.Background())) }()
	runtime := binding.Runtime()
	if len(runtime.Session().ExecutionSnapshot().Projection.ModelMessages) == 0 {
		fresh := agent.NewSession(c.basePrompt())
		if err := c.seedRuntimeSession(ctx, runtime, "session-create", fresh.Snapshot()); err != nil {
			return err
		}
	}
	_, err = runtime.Session().Flush(ctx)
	return err
}
