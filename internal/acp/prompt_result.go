package acp

import (
	"log/slog"

	"deepzora/internal/agent"
	"deepzora/internal/event"
)

// promptStopReason maps a finished controller run onto ACP v1. Controlled
// pauses remain successful prompt responses; genuine failures use JSON-RPC's
// error channel because ACP v1 has no error stop reason.
func promptStopReason(runErr error, cancelled bool, sessionID string) (StopReason, string, error) {
	if cancelled {
		return StopCancelled, "", nil
	}
	if runErr == nil {
		return StopEndTurn, "", nil
	}

	if pause, ok := agent.InspectRunPause(runErr); ok {
		stop := StopEndTurn
		if pause.Kind == "max_steps" {
			stop = StopMaxTurnRequests
		}
		return stop, clipStatusError(runErr, 2_048), nil
	}

	reason := clipStatusError(runErr, 2_048)
	slog.Error("acp: session/prompt failed", "session_id", sessionID, "err", reason)
	return "", "", &RPCError{Code: ErrInternal, Message: "session/prompt: " + reason}
}

func promptPauseNotice(runErr error, text string) event.Event {
	notice := event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: text}

	return notice
}
