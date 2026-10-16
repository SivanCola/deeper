package main

import (
	"deepzora/desktop/internal/workspacestate"
	"errors"
)

// Replay only current, explicitly requested session archive operations.
func (a *App) reconcileTopicRemovals(state workspacestate.State) error {
	var joined error
	for _, operation := range state.TopicRemovals {
		if operation.Phase != "prepared" {
			continue
		}
		if operation.Disposition != "archive_sessions" {
			joined = errors.Join(joined, workspacestate.ErrMutationConflict)
			continue
		}
		out, err := a.RemoveTopic(TopicRemovalRequest{OperationID: operation.ID, Target: TopicRemovalTarget{WorkspaceID: operation.WorkspaceID, TopicID: operation.TopicID}, ExpectedToken: operation.Token})
		if err == nil {
			err = topicRemovalError(out)
		}
		joined = errors.Join(joined, err)
	}
	return joined
}
