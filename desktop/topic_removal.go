package main

import (
	"deepzora/desktop/internal/workspacestate"
	"errors"
	"fmt"
	"strings"
)

type TopicRemovalTarget struct {
	WorkspaceID string `json:"workspaceId"`
	TopicID     string `json:"topicId"`
}
type TopicRemovalInspection struct {
	Target      TopicRemovalTarget `json:"target"`
	Disposition string             `json:"disposition"`
	Allowed     bool               `json:"allowed"`
	Reason      string             `json:"reason,omitempty"`
	Token       string             `json:"token"`
}
type TopicRemovalRequest struct {
	OperationID   string             `json:"operationId"`
	Target        TopicRemovalTarget `json:"target"`
	ExpectedToken string             `json:"expectedToken"`
}
type TopicRemovalResult struct {
	Committed    bool   `json:"committed"`
	Disposition  string `json:"disposition"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Retryable    bool   `json:"retryable"`
}

func (a *App) InspectTopicRemoval(target TopicRemovalTarget) (TopicRemovalInspection, error) {
	out, err := a.inspectTopicRemoval(target)
	return out, sessionOperationErrorForTarget(err, target.TopicID, "")
}

func (a *App) inspectTopicRemoval(target TopicRemovalTarget) (TopicRemovalInspection, error) {
	out := TopicRemovalInspection{Target: target, Disposition: "archive_sessions"}
	if strings.TrimSpace(target.TopicID) == "" {
		return out, workspacestate.ErrMutationConflict
	}
	state, err := a.workspaceRegistry().Load(a.bootContext())
	if err != nil {
		return out, err
	}
	token, owner, err := workspacestate.TopicSessionRemovalToken(state, target.WorkspaceID, target.TopicID)
	if err != nil {
		return out, err
	}
	if token == "" {
		return out, workspacestate.ErrSessionNotFound
	}
	out.Target.WorkspaceID, out.Token = owner, token
	out.Allowed = !a.topicHasActiveRuntimeWork(target.TopicID)
	if !out.Allowed {
		out.Reason = "busy"
	}
	return out, nil
}

func (a *App) RemoveTopic(req TopicRemovalRequest) (TopicRemovalResult, error) {
	out := TopicRemovalResult{}
	if req.OperationID == "" || len(req.OperationID) > 200 || req.ExpectedToken == "" || req.Target.WorkspaceID == "" {
		return out, errors.New("invalid topic removal request")
	}
	release, ok := a.tryLockRuntimeMutationBounded("remove topic")
	if !ok {
		out.ErrorCode, out.Retryable = "busy", true
		return out, nil
	}
	defer release()
	state, err := a.workspaceRegistry().Load(a.bootContext())
	if err == nil {
		if old, exists := state.TopicRemovals[req.OperationID]; exists && old.WorkspaceID == req.Target.WorkspaceID && old.TopicID == req.Target.TopicID && old.Token == req.ExpectedToken && old.Disposition == "archive_sessions" && old.Phase == "committed" {
			return TopicRemovalResult{Committed: true, Disposition: "archive_sessions"}, nil
		}
		out, err = a.removeTopicSessionsAdmissionHeld(req)
	}
	if err != nil {
		out.ErrorCode, out.Retryable, out.ErrorMessage = "operation_failed", true, err.Error()
		if errors.Is(err, workspacestate.ErrMutationConflict) || errors.Is(err, workspacestate.ErrSessionNotFound) {
			out.ErrorCode, out.Retryable = "state_conflict", false
		}
		if errors.Is(err, errTopicArchiveBusy) || errors.Is(err, errTopicHasActiveWork) {
			out.ErrorCode = "busy"
		}
		a.emitProjectTreeChanged()
	}
	return out, nil
}

func (a *App) removeTopicSessionsAdmissionHeld(req TopicRemovalRequest) (TopicRemovalResult, error) {
	// Match AI/manual rename: removal -> title. Cleanup must run after the
	// title unlock, while removal and runtime admission remain held.
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	cleanup := archivedRuntimeCleanup{app: a}
	defer cleanup.finish()
	// Serialize title/organization changes from this process across admission.
	a.topicTitleMutationMu.Lock()
	defer a.topicTitleMutationMu.Unlock()
	state, err := a.workspaceRegistry().Load(a.bootContext())
	if err != nil {
		return TopicRemovalResult{}, err
	}
	archiveID := "topic-sessions-" + req.OperationID
	finished := state.PendingOperations[archiveID].Phase == "committed"
	if !finished {
		inspection, err := a.inspectTopicRemoval(req.Target)
		if err != nil {
			return TopicRemovalResult{}, err
		}
		if !inspection.Allowed || inspection.Disposition != "archive_sessions" || inspection.Token != req.ExpectedToken {
			return TopicRemovalResult{}, workspacestate.ErrMutationConflict
		}
	}
	err = a.workspaceRegistry().TransitionTopicRemoval(a.bootContext(), req.OperationID, func(state workspacestate.State, item *workspacestate.TopicRemoval) error {
		if item.ID != "" {
			if item.WorkspaceID != req.Target.WorkspaceID || item.TopicID != req.Target.TopicID || item.Token != req.ExpectedToken || item.Disposition != "archive_sessions" {
				return workspacestate.ErrMutationConflict
			}
			return nil
		}
		token, _, err := workspacestate.TopicSessionRemovalToken(state, req.Target.WorkspaceID, req.Target.TopicID)
		if err != nil {
			return err
		}
		if token != "" && token != req.ExpectedToken {
			return workspacestate.ErrMutationConflict
		}
		*item = workspacestate.TopicRemoval{ID: req.OperationID, WorkspaceID: req.Target.WorkspaceID, TopicID: req.Target.TopicID, Token: req.ExpectedToken, SessionToken: token, Disposition: "archive_sessions", Phase: "prepared"}
		return nil
	})
	if err != nil {
		return TopicRemovalResult{}, err
	}
	if !finished {
		a.lifecycleCheckpoint("topic-sessions-before-archive")
		if err := a.archiveCompatibleTopicWithCleanupAdmissionHeld(req.Target.TopicID, archiveID, &cleanup); err != nil {
			return TopicRemovalResult{}, err
		}
	}
	err = a.workspaceRegistry().TransitionTopicRemoval(a.bootContext(), req.OperationID, func(_ workspacestate.State, item *workspacestate.TopicRemoval) error {
		item.Phase = "committed"
		return nil
	})
	return TopicRemovalResult{Committed: err == nil, Disposition: "archive_sessions"}, err
}

func topicRemovalError(result TopicRemovalResult) error {
	if result.Committed {
		return nil
	}
	return fmt.Errorf("topic removal incomplete: %s", result.ErrorCode)
}
