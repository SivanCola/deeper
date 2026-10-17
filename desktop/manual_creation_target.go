package main

import (
	"errors"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/agentpreset"
)

func manualCreationTargetError(err error) error {
	code, message, retryable := "", "", false
	switch {
	case errors.Is(err, workspacestate.ErrCreationWorkspaceUnavailable):
		code, message, retryable = "workspace_unavailable", "The project folder is unavailable. Creation will continue when it is accessible.", true
	case errors.Is(err, workspacestate.ErrWorkspaceNotFound):
		code, message = "workspace_removed", "The project is no longer registered. Open the project to create a new session."
	case errors.Is(err, workspacestate.ErrCreationWorkspaceChanged):
		code, message = "workspace_changed", "The project now points to a different folder. Open the intended project to create a new session."
	case errors.Is(err, workspacestate.ErrCreationSessionInactive):
		code, message = "creation_cancelled", "This session was archived or removed. Use New Session to start another conversation."
	case errors.Is(err, workspacestate.ErrMutationConflict):
		code, message = "creation_owner_conflict", "This creation request belongs to another session operation. Use New Session to start another conversation."
	default:
		return err
	}
	return &SessionOperationError{Code: code, Message: message, Retryable: retryable}
}

func (a *App) replayManualCreation(req ManualSessionCreationRequest, v ManualSessionCreationView) (ManualSessionCreationView, error) {
	if agentpreset.NormalizeID(req.AgentPreset) != agentpreset.NormalizeID(v.Settings.AgentPreset) {
		return v, errors.New("creation operation already belongs to another agent preset")
	}
	root := desktopWorkspaceRoot(v.Scope, v.WorkspaceRoot)
	requested := root
	if req.Scope != "" || req.WorkspaceRoot != "" {
		requested = desktopWorkspaceRoot(req.Scope, req.WorkspaceRoot)
	} else if req.WorkspaceID != "" && req.WorkspaceID != v.WorkspaceID {
		state, err := a.workspaceRegistry().Load(a.bootContext())
		if err != nil {
			return v, err
		}
		w, ok := state.Workspaces[req.WorkspaceID]
		if !ok {
			return v, manualCreationTargetError(workspacestate.ErrWorkspaceNotFound)
		}
		requested = w.Root
	}
	same, err := sameDesktopPathStrict(root, requested)
	if err != nil {
		return v, manualCreationTargetError(workspacestate.ErrCreationWorkspaceUnavailable)
	}
	if !same {
		return v, manualCreationTargetError(workspacestate.ErrCreationWorkspaceChanged)
	}
	if v.Phase == "reserved" || v.Phase == "starting" {
		a.creationManager().Ensure(v.OperationID, "begin", "")
	}
	return v, nil
}
