package agent_test

import (
	"context"

	"deepzora/internal/agent"
	"deepzora/internal/session"
	"deepzora/internal/subagentpersist"
)

func init() {
	agent.CurrentPersistenceFactoryForTest = func(root string) (agent.CurrentPersistenceHarness, error) {
		service, err := session.NewService("current-test", session.NewFilesystemPersistence(root))
		if err != nil {
			return agent.CurrentPersistenceHarness{}, err
		}
		return agent.CurrentPersistenceHarness{Store: subagentpersist.New(service),
			Copy: func(ctx context.Context, source, child string) error {
				_, err := service.CopySession(ctx, session.CopyRequest{Source: session.SessionRef{HostID: service.HostID(), SessionID: source}, ChildID: child, OperationID: "intent-checkpoint:" + child})
				return err
			}, Close: func() error { return service.Shutdown(context.Background()) }}, nil
	}
}
